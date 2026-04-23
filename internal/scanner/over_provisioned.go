package scanner

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/mmahut/oc-find-waste/internal/pricing"
	"github.com/mmahut/oc-find-waste/internal/prom"
)

const (
	overProvisionedThreshold = 0.30 // p95 below 30% of request = over-provisioned
	minPodAge                = 24 * time.Hour
)

type containerReq struct {
	name    string
	reqCPUm int64 // millicores
	reqMemB int64 // bytes
}

type overProvisionedScanner struct {
	k8sClient kubernetes.Interface
	prom      prom.Client
	pricing   *pricing.Profile
	window    time.Duration
}

// NewOverProvisioned creates a scanner for pods whose p95 resource usage is
// well below their requests. Pass nil promClient to no-op gracefully.
func NewOverProvisioned(k8sClient kubernetes.Interface, promClient prom.Client, profile *pricing.Profile, window time.Duration) Scanner {
	return &overProvisionedScanner{
		k8sClient: k8sClient,
		prom:      promClient,
		pricing:   profile,
		window:    window,
	}
}

func (s *overProvisionedScanner) Name() string { return "over-provisioned" }

func (s *overProvisionedScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	if s.prom == nil {
		return nil, nil
	}

	wh := fmt.Sprintf("%dh", int(s.window.Hours()))

	cpuQuery := fmt.Sprintf(
		`quantile_over_time(0.95,sum by (pod)(rate(container_cpu_usage_seconds_total{namespace=%q,container!="",container!="POD"}[5m]))[%s:5m])`,
		namespace, wh)
	memQuery := fmt.Sprintf(
		`quantile_over_time(0.95,sum by (pod)(container_memory_working_set_bytes{namespace=%q,container!="",container!="POD"})[%s:5m])`,
		namespace, wh)

	cpuP95, err := s.prom.RangeP95(ctx, cpuQuery, s.window)
	if err != nil {
		return nil, fmt.Errorf("querying cpu p95: %w", err)
	}
	memP95, err := s.prom.RangeP95(ctx, memQuery, s.window)
	if err != nil {
		return nil, fmt.Errorf("querying memory p95: %w", err)
	}

	pods, err := s.k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	type podStats struct {
		reqCPU float64 // cores
		reqMem float64 // bytes
		p95CPU float64 // cores
		p95Mem float64 // bytes
	}
	type ownerEntry struct {
		kind       string
		name       string
		replicas   int32
		stats      podStats
		containers []containerReq
	}
	owners := make(map[string]*ownerEntry)

	for i := range pods.Items {
		pod := &pods.Items[i]
		if time.Since(pod.CreationTimestamp.Time) < minPodAge {
			continue
		}

		var reqCPUm int64 // millicores
		var reqMemB int64 // bytes
		var containers []containerReq
		for _, c := range pod.Spec.Containers {
			cr := containerReq{name: c.Name}
			if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				cr.reqCPUm = q.MilliValue()
				reqCPUm += cr.reqCPUm
			}
			if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				cr.reqMemB = q.Value()
				reqMemB += cr.reqMemB
			}
			containers = append(containers, cr)
		}
		if reqCPUm == 0 && reqMemB == 0 {
			continue
		}

		reqCPU := float64(reqCPUm) / 1000
		reqMem := float64(reqMemB)
		p95CPU, haveCPU := cpuP95[pod.Name]
		p95Mem, haveMem := memP95[pod.Name]

		cpuOver := haveCPU && reqCPUm > 0 && p95CPU < overProvisionedThreshold*reqCPU
		memOver := haveMem && reqMemB > 0 && p95Mem < overProvisionedThreshold*reqMem
		if !cpuOver && !memOver {
			continue
		}

		kind, name, replicas := s.resolveOwner(ctx, pod, namespace)
		key := kind + "/" + name
		if _, seen := owners[key]; !seen {
			owners[key] = &ownerEntry{
				kind: kind, name: name, replicas: replicas,
				stats:      podStats{reqCPU: reqCPU, reqMem: reqMem, p95CPU: p95CPU, p95Mem: p95Mem},
				containers: containers,
			}
		}
	}

	var findings []Finding
	for _, o := range owners {
		st := o.stats

		// Suggested = ceil(p95 * 1.5) with reasonable granularity.
		sugCPU := math.Ceil(st.p95CPU*1.5*1000) / 1000 // round to millicore
		sugMem := math.Ceil(st.p95Mem*1.5/(1<<20)) * (1 << 20)

		detail := fmt.Sprintf("requests: %s CPU, %s RAM  │  p95 usage: %s CPU, %s RAM",
			fmtCPU(st.reqCPU), fmtMem(st.reqMem),
			fmtCPU(st.p95CPU), fmtMem(st.p95Mem))

		suggestion := fmt.Sprintf("suggest: %s CPU, %s RAM", fmtCPU(sugCPU), fmtMem(sugMem))

		var monthlyCost float64
		if s.pricing != nil {
			wastedCPU := math.Max(0, st.reqCPU-sugCPU)
			wastedMemGiB := math.Max(0, (st.reqMem-sugMem)/(1<<30))
			costPerPod := s.pricing.WorkloadMonthlyUSD(wastedCPU, wastedMemGiB)
			monthlyCost = costPerPod * float64(o.replicas)

			if monthlyCost > 0 {
				reqCostPerPod := s.pricing.WorkloadMonthlyUSD(st.reqCPU, st.reqMem/(1<<30))
				sugCostPerPod := s.pricing.WorkloadMonthlyUSD(sugCPU, sugMem/(1<<30))
				var savingsPct float64
				if reqCostPerPod > 0 {
					savingsPct = 100 * (reqCostPerPod - sugCostPerPod) / reqCostPerPod
				}
				suggestion += fmt.Sprintf(" ($%.2f/mo, -%.0f%%)", monthlyCost, savingsPct)
			}
		}

		patch := buildRightsizePatch(o.kind, o.name, namespace, sugCPU, sugMem, o.containers, st.reqCPU, st.reqMem)

		findings = append(findings, Finding{
			Kind:        o.kind,
			Namespace:   namespace,
			Name:        o.name,
			Reason:      "over-provisioned",
			Detail:      detail,
			MonthlyCost: monthlyCost,
			Suggestion:  suggestion,
			Patch:       patch,
			Severity:    SeverityWarning,
		})
	}
	return findings, nil
}

func (s *overProvisionedScanner) resolveOwner(ctx context.Context, pod *corev1.Pod, namespace string) (kind, name string, replicas int32) {
	for _, ref := range pod.OwnerReferences {
		switch ref.Kind {
		case "ReplicaSet":
			rs, err := s.k8sClient.AppsV1().ReplicaSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				return "ReplicaSet", ref.Name, 1
			}
			for _, rRef := range rs.OwnerReferences {
				if rRef.Kind == "Deployment" {
					dep, err := s.k8sClient.AppsV1().Deployments(namespace).Get(ctx, rRef.Name, metav1.GetOptions{})
					if err != nil {
						return "Deployment", rRef.Name, 1
					}
					r := int32(1)
					if dep.Spec.Replicas != nil {
						r = *dep.Spec.Replicas
					}
					return "Deployment", rRef.Name, r
				}
			}
			return "ReplicaSet", ref.Name, 1
		case "StatefulSet":
			sts, err := s.k8sClient.AppsV1().StatefulSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				return "StatefulSet", ref.Name, 1
			}
			r := int32(1)
			if sts.Spec.Replicas != nil {
				r = *sts.Spec.Replicas
			}
			return "StatefulSet", ref.Name, r
		}
	}
	return "Pod", pod.Name, 1
}

// buildRightsizePatch generates a kubectl strategic-merge-patch YAML for the
// suggested resource requests, distributing them proportionally across containers.
func buildRightsizePatch(kind, name, namespace string, sugCPU, sugMem float64, containers []containerReq, totalReqCPU, totalReqMem float64) string {
	// Convert totals back to millicores/bytes for ratio math.
	totalReqCPUm := int64(math.Round(totalReqCPU * 1000))
	totalReqMemB := int64(totalReqMem)

	// Determine YAML path: Pod uses spec.containers, everything else spec.template.spec.containers.
	isPod := kind == "Pod"
	kindLower := strings.ToLower(kind)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# kubectl patch %s %s -n %s --type strategic --patch-file /dev/stdin\n", kindLower, name, namespace)
	fmt.Fprintln(&sb, "spec:")
	if !isPod {
		fmt.Fprintln(&sb, "  template:")
		fmt.Fprintln(&sb, "    spec:")
		fmt.Fprintln(&sb, "      containers:")
	} else {
		fmt.Fprintln(&sb, "  containers:")
	}

	indent := "      "
	if isPod {
		indent = "  "
	}

	for _, c := range containers {
		fmt.Fprintf(&sb, "%s- name: %s\n", indent, c.name)
		fmt.Fprintf(&sb, "%s  resources:\n", indent)
		fmt.Fprintf(&sb, "%s    requests:\n", indent)

		if c.reqCPUm > 0 && totalReqCPUm > 0 {
			share := float64(c.reqCPUm) / float64(totalReqCPUm)
			perContainer := math.Ceil(sugCPU*share*1000) / 1000
			fmt.Fprintf(&sb, "%s      cpu: %q\n", indent, fmtCPU(perContainer))
		}
		if c.reqMemB > 0 && totalReqMemB > 0 {
			share := float64(c.reqMemB) / float64(totalReqMemB)
			perContainer := math.Ceil(sugMem*share/(1<<20)) * (1 << 20)
			fmt.Fprintf(&sb, "%s      memory: %q\n", indent, fmtMem(perContainer))
		}
	}
	return sb.String()
}

// fmtCPU formats cores as millicores (e.g. 0.18 → "180m", 2.0 → "2000m").
func fmtCPU(cores float64) string {
	return fmt.Sprintf("%dm", int(math.Round(cores*1000)))
}

// fmtMem formats bytes in the largest whole unit (GiB or MiB).
func fmtMem(bytes float64) string {
	if gib := bytes / (1 << 30); gib >= 1 {
		return fmt.Sprintf("%.0fGi", math.Ceil(gib))
	}
	return fmt.Sprintf("%.0fMi", math.Ceil(bytes/(1<<20)))
}
