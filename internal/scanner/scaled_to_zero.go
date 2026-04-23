package scanner

import (
	"context"
	"fmt"
	"math"
	"time"

	osappsv1client "github.com/openshift/client-go/apps/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type scaledToZeroScanner struct {
	client     kubernetes.Interface
	appsClient osappsv1client.Interface // nil on vanilla k8s
}

// NewScaledToZero creates a scanner for replicas=0 workloads.
// Pass nil for appsClient on non-OpenShift clusters.
func NewScaledToZero(client kubernetes.Interface, appsClient osappsv1client.Interface) Scanner {
	return &scaledToZeroScanner{client: client, appsClient: appsClient}
}

func (s *scaledToZeroScanner) Name() string { return "scaled-to-zero" }

func (s *scaledToZeroScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	// Build a set of HPA-managed workloads so we don't flag intentional scale-to-zero
	// targets (some HPA controllers legitimately drive replicas=0 on cold-start targets).
	hpaTargets := make(map[string]bool)
	hpas, hpaErr := s.client.AutoscalingV1().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{})
	if hpaErr == nil {
		for i := range hpas.Items {
			ref := hpas.Items[i].Spec.ScaleTargetRef
			hpaTargets[ref.Kind+"/"+ref.Name] = true
		}
	}

	var findings []Finding

	deps, err := s.client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	for i := range deps.Items {
		d := &deps.Items[i]
		if d.Spec.Replicas != nil && *d.Spec.Replicas == 0 && !hpaTargets["Deployment/"+d.Name] {
			findings = append(findings, scaledToZeroFinding("Deployment", d.Name, namespace, d.CreationTimestamp.Time))
		}
	}

	stss, err := s.client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing statefulsets: %w", err)
	}
	for i := range stss.Items {
		sts := &stss.Items[i]
		if sts.Spec.Replicas != nil && *sts.Spec.Replicas == 0 && !hpaTargets["StatefulSet/"+sts.Name] {
			findings = append(findings, scaledToZeroFinding("StatefulSet", sts.Name, namespace, sts.CreationTimestamp.Time))
		}
	}

	if s.appsClient != nil {
		dcs, err := s.appsClient.AppsV1().DeploymentConfigs(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing deploymentconfigs: %w", err)
		}
		for i := range dcs.Items {
			dc := &dcs.Items[i]
			if dc.Spec.Replicas == 0 {
				findings = append(findings, scaledToZeroFinding("DeploymentConfig", dc.Name, namespace, dc.CreationTimestamp.Time))
			}
		}
	}

	return findings, nil
}

func scaledToZeroFinding(kind, name, namespace string, created time.Time) Finding {
	ageDays := int(math.Round(time.Since(created).Hours() / 24))
	suggestion := fmt.Sprintf("if no longer used, delete the %s", kind)
	if kind == "StatefulSet" {
		suggestion += " (and associated PVCs)"
	}
	return Finding{
		Kind:       kind,
		Namespace:  namespace,
		Name:       name,
		Reason:     fmt.Sprintf("scaled to 0 (resource age: %dd)", ageDays),
		Severity:   SeverityWarning,
		Suggestion: suggestion,
	}
}
