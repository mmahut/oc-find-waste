package ocp_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mmahut/oc-find-waste/internal/ocp"
)

func TestIsOpenShift(t *testing.T) {
	tests := []struct {
		name      string
		resources []*metav1.APIResourceList
		want      bool
	}{
		{
			name: "openshift cluster — apps.openshift.io present",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "apps.openshift.io/v1",
					APIResources: []metav1.APIResource{
						{Name: "deploymentconfigs", Kind: "DeploymentConfig"},
					},
				},
			},
			want: true,
		},
		{
			name: "vanilla kubernetes — no openshift groups",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "apps/v1",
					APIResources: []metav1.APIResource{
						{Name: "deployments", Kind: "Deployment"},
					},
				},
			},
			want: false,
		},
		{
			name:      "empty discovery — no groups",
			resources: nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			client.Resources = tt.resources
			got := ocp.IsOpenShift(client.Discovery())
			if got != tt.want {
				t.Errorf("IsOpenShift = %v, want %v", got, tt.want)
			}
		})
	}
}
