package scanner

import (
	"context"
	"fmt"
	"math"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const completedJobThreshold = 7 * 24 * time.Hour

type completedJobsScanner struct {
	client kubernetes.Interface
}

func NewCompletedJobs(client kubernetes.Interface) Scanner {
	return &completedJobsScanner{client: client}
}

func (s *completedJobsScanner) Name() string { return "completed-jobs" }

func (s *completedJobsScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	jobs, err := s.client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}

	var findings []Finding
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if job.Status.CompletionTime == nil {
			continue
		}
		age := time.Since(job.Status.CompletionTime.Time)
		if age < completedJobThreshold {
			continue
		}
		days := int(math.Round(age.Hours() / 24))
		findings = append(findings, Finding{
			Kind:       "Job",
			Namespace:  namespace,
			Name:       job.Name,
			Reason:     fmt.Sprintf("completed %dd ago", days),
			Severity:   SeverityInfo,
			Suggestion: "delete, or set .spec.ttlSecondsAfterFinished on future jobs",
		})
	}
	return findings, nil
}
