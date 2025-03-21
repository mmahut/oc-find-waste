package scanner

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type orphanedPVCsScanner struct {
	client kubernetes.Interface
}

func NewOrphanedPVCs(client kubernetes.Interface) Scanner {
	return &orphanedPVCsScanner{client: client}
}

func (s *orphanedPVCsScanner) Name() string { return "orphaned-pvcs" }

func (s *orphanedPVCsScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	pvcs, err := s.client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pvcs: %w", err)
	}

	// Only care about Bound PVCs.
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

	// Collect all PVC names referenced by any pod.
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
		findings = append(findings, Finding{
			Kind:       "PersistentVolumeClaim",
			Namespace:  namespace,
			Name:       name,
			Reason:     fmt.Sprintf("bound but unmounted (%s)", storageQty.String()),
			Severity:   SeverityWarning,
			Suggestion: "if data is no longer needed, delete the PVC to release storage",
		})
	}
	return findings, nil
}
