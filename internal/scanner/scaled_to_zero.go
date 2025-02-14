package scanner

import (
	"context"
	"fmt"
	"math"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type scaledToZeroScanner struct {
	client kubernetes.Interface
}

func NewScaledToZero(client kubernetes.Interface) Scanner {
	return &scaledToZeroScanner{client: client}
}

func (s *scaledToZeroScanner) Name() string { return "scaled-to-zero" }

func (s *scaledToZeroScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	var findings []Finding

	deps, err := s.client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	for i := range deps.Items {
		d := &deps.Items[i]
		if d.Spec.Replicas != nil && *d.Spec.Replicas == 0 {
			findings = append(findings, scaledToZeroFinding("Deployment", d.Name, namespace, d.CreationTimestamp.Time))
		}
	}

	stss, err := s.client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing statefulsets: %w", err)
	}
	for i := range stss.Items {
		sts := &stss.Items[i]
		if sts.Spec.Replicas != nil && *sts.Spec.Replicas == 0 {
			findings = append(findings, scaledToZeroFinding("StatefulSet", sts.Name, namespace, sts.CreationTimestamp.Time))
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
