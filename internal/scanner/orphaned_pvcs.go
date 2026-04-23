package scanner

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/mmahut/oc-find-waste/internal/pricing"
)

type orphanedPVCsScanner struct {
	client  kubernetes.Interface
	pricing *pricing.Profile // nil means no cost estimate
}

// NewOrphanedPVCs creates a scanner for Bound PVCs not mounted by any Pod.
// Pass nil for profile to omit cost estimates.
func NewOrphanedPVCs(client kubernetes.Interface, profile *pricing.Profile) Scanner {
	return &orphanedPVCsScanner{client: client, pricing: profile}
}

func (s *orphanedPVCsScanner) Name() string { return "orphaned-pvcs" }

func (s *orphanedPVCsScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	pvcs, err := s.client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pvcs: %w", err)
	}

	bound := make(map[string]corev1.PersistentVolumeClaim)
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		if pvc.Status.Phase == corev1.ClaimBound {
			bound[pvc.Name] = *pvc
		}
	}
	if len(bound) == 0 {
		return nil, nil
	}

	pods, err := s.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	mounted := make(map[string]bool)
	for i := range pods.Items {
		for _, vol := range pods.Items[i].Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				mounted[vol.PersistentVolumeClaim.ClaimName] = true
			}
		}
	}

	var findings []Finding
	for name, pvc := range bound {
		if mounted[name] {
			continue
		}
		storageQty := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		sizeGiB := float64(storageQty.Value()) / (1 << 30)

		var monthlyCost float64
		if s.pricing != nil {
			monthlyCost = s.pricing.PVCMonthlyUSD(sizeGiB)
		}

		findings = append(findings, Finding{
			Kind:        "PersistentVolumeClaim",
			Namespace:   namespace,
			Name:        name,
			Reason:      fmt.Sprintf("bound but unmounted (%s)", storageQty.String()),
			MonthlyCost: monthlyCost,
			Savings:     monthlyCost,
			Severity:    SeverityWarning,
			Suggestion:  "if data is no longer needed, delete the PVC to release storage",
		})
	}
	return findings, nil
}
