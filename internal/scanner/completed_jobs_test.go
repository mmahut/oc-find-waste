package scanner_test

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mmahut/oc-find-waste/internal/scanner"
)

func completionTime(d time.Duration) *metav1.Time {
	t := metav1.NewTime(time.Now().Add(-d))
	return &t
}

func TestCompletedJobs(t *testing.T) {
	tests := []struct {
		name      string
		objects   []runtime.Object
		wantCount int
	}{
		{
			name:      "empty namespace",
			wantCount: 0,
		},
		{
			name: "job completed 10 days ago — finding",
			objects: []runtime.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "old-migration", Namespace: "test"},
					Status:     batchv1.JobStatus{CompletionTime: completionTime(10 * 24 * time.Hour)},
				},
			},
			wantCount: 1,
		},
		{
			name: "job completed 1 day ago — no finding",
			objects: []runtime.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "recent-job", Namespace: "test"},
					Status:     batchv1.JobStatus{CompletionTime: completionTime(24 * time.Hour)},
				},
			},
			wantCount: 0,
		},
		{
			name: "job still running — no finding",
			objects: []runtime.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "running-job", Namespace: "test"},
					Status:     batchv1.JobStatus{},
				},
			},
			wantCount: 0,
		},
		{
			name: "cronjob-owned job completed 10 days ago — no finding",
			objects: []runtime.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "weekly-report-28",
						Namespace: "test",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "CronJob", Name: "weekly-report"},
						},
					},
					Status: batchv1.JobStatus{CompletionTime: completionTime(10 * 24 * time.Hour)},
				},
			},
			wantCount: 0,
		},
		{
			name: "cronjob-owned and standalone old jobs — only standalone flagged",
			objects: []runtime.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cron-child",
						Namespace: "test",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "CronJob", Name: "nightly-backup"},
						},
					},
					Status: batchv1.JobStatus{CompletionTime: completionTime(10 * 24 * time.Hour)},
				},
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "old-migration", Namespace: "test"},
					Status:     batchv1.JobStatus{CompletionTime: completionTime(10 * 24 * time.Hour)},
				},
			},
			wantCount: 1,
		},
		{
			name: "mix: one old, one recent",
			objects: []runtime.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "old", Namespace: "test"},
					Status:     batchv1.JobStatus{CompletionTime: completionTime(20 * 24 * time.Hour)},
				},
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "fresh", Namespace: "test"},
					Status:     batchv1.JobStatus{CompletionTime: completionTime(2 * 24 * time.Hour)},
				},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientset(tt.objects...)
			s := scanner.NewCompletedJobs(client)
			findings, err := s.Scan(context.Background(), "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != tt.wantCount {
				t.Errorf("got %d findings, want %d", len(findings), tt.wantCount)
			}
			if len(findings) > 0 && findings[0].Kind != "Job" {
				t.Errorf("expected kind Job, got %s", findings[0].Kind)
			}
		})
	}
}
