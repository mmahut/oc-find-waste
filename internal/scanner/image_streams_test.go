package scanner_test

import (
	"context"
	"testing"

	osbuildv1 "github.com/openshift/api/build/v1"
	osimagev1 "github.com/openshift/api/image/v1"
	osbuildfake "github.com/openshift/client-go/build/clientset/versioned/fake"
	osimagefake "github.com/openshift/client-go/image/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mmahut/oc-find-waste/internal/scanner"
)

const testNS = "test"

func imageStream(name string) *osimagev1.ImageStream {
	return &osimagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
	}
}

func podWithImage(image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNS},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: image}},
		},
	}
}

func buildConfig(outputISTag string) *osbuildv1.BuildConfig {
	return &osbuildv1.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "my-build", Namespace: testNS},
		Spec: osbuildv1.BuildConfigSpec{
			CommonSpec: osbuildv1.CommonSpec{
				Output: osbuildv1.BuildOutput{
					To: &corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: outputISTag,
					},
				},
			},
		},
	}
}

func TestUnusedImageStreams(t *testing.T) {
	internalImage := "image-registry.openshift-image-registry.svc:5000/" + testNS + "/used-app:latest"

	tests := []struct {
		name        string
		imageObjs   []runtime.Object
		k8sObjs     []runtime.Object
		buildObjs   []runtime.Object
		wantCount   int
		wantFinding string // ImageStream name expected in findings
	}{
		{
			name:      "empty namespace",
			wantCount: 0,
		},
		{
			name:        "imagestream with no pod reference — finding",
			imageObjs:   []runtime.Object{imageStream("orphaned-app")},
			wantCount:   1,
			wantFinding: "orphaned-app",
		},
		{
			name:      "imagestream referenced by pod — no finding",
			imageObjs: []runtime.Object{imageStream("used-app")},
			k8sObjs:   []runtime.Object{podWithImage(internalImage)},
			wantCount: 0,
		},
		{
			name:      "imagestream referenced by buildconfig output — no finding",
			imageObjs: []runtime.Object{imageStream("built-app")},
			buildObjs: []runtime.Object{buildConfig("built-app:latest")},
			wantCount: 0,
		},
		{
			name:        "buildconfig with cross-namespace embedded ref — IS still unused",
			imageObjs:   []runtime.Object{imageStream("shared-app")},
			buildObjs:   []runtime.Object{buildConfig("other-ns/shared-app:latest")},
			wantCount:   1,
			wantFinding: "shared-app",
		},
		{
			name:      "imagestream used as s2i builder base (SourceStrategy.From) — no finding",
			imageObjs: []runtime.Object{imageStream("python-311")},
			buildObjs: []runtime.Object{func() *osbuildv1.BuildConfig {
				bc := &osbuildv1.BuildConfig{
					ObjectMeta: metav1.ObjectMeta{Name: "my-app-build", Namespace: testNS},
				}
				bc.Spec.Strategy.SourceStrategy = &osbuildv1.SourceBuildStrategy{
					From: corev1.ObjectReference{Kind: "ImageStreamTag", Name: "python-311:latest"},
				}
				return bc
			}()},
			wantCount: 0,
		},
		{
			name:      "imagestream referenced via ImageStreamImage digest — no finding",
			imageObjs: []runtime.Object{imageStream("python-311")},
			buildObjs: []runtime.Object{func() *osbuildv1.BuildConfig {
				bc := &osbuildv1.BuildConfig{
					ObjectMeta: metav1.ObjectMeta{Name: "my-app-build", Namespace: testNS},
				}
				bc.Spec.Strategy.SourceStrategy = &osbuildv1.SourceBuildStrategy{
					From: corev1.ObjectReference{Kind: "ImageStreamImage", Name: "python-311@sha256:abc123"},
				}
				return bc
			}()},
			wantCount: 0,
		},
		{
			name:      "external registry image with matching namespace substring — no false positive",
			imageObjs: []runtime.Object{imageStream("myapp")},
			k8sObjs:   []runtime.Object{podWithImage("quay.io/bar/" + testNS + "/myapp:tag")},
			wantCount: 1, // quay.io is not the internal registry; IS should still be flagged
		},
		{
			name:      "nil imageClient — no-op on vanilla k8s",
			wantCount: 0, // tested separately below via nil client
		},
		{
			name: "two imagestreams, one used one not",
			imageObjs: []runtime.Object{
				imageStream("orphaned-app"),
				imageStream("active-app"),
			},
			k8sObjs:     []runtime.Object{podWithImage("image-registry.openshift-image-registry.svc:5000/" + testNS + "/active-app:v2")},
			wantCount:   1,
			wantFinding: "orphaned-app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imageClient := osimagefake.NewClientset(tt.imageObjs...)
			k8sClient := fake.NewClientset(tt.k8sObjs...)
			buildClient := osbuildfake.NewClientset(tt.buildObjs...)

			s := scanner.NewUnusedImageStreams(k8sClient, imageClient, buildClient)
			findings, err := s.Scan(context.Background(), testNS)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != tt.wantCount {
				t.Errorf("got %d findings, want %d: %+v", len(findings), tt.wantCount, findings)
			}
			if tt.wantFinding != "" {
				found := false
				for _, f := range findings {
					if f.Name == tt.wantFinding {
						found = true
					}
				}
				if !found {
					t.Errorf("expected finding for %q, got %+v", tt.wantFinding, findings)
				}
			}
		})
	}

	t.Run("nil imageClient no-ops", func(t *testing.T) {
		s := scanner.NewUnusedImageStreams(fake.NewClientset(), nil, nil)
		findings, err := s.Scan(context.Background(), testNS)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 0 {
			t.Errorf("expected 0 findings with nil client, got %d", len(findings))
		}
	})
}

func TestUnusedImageStreams_CrossNamespaceRef_NotMarked(t *testing.T) {
	// A BuildConfig in the scanned namespace references an IS from another namespace
	// via the explicit Namespace field of the ObjectReference.
	// The local IS with the same short name must still be reported as unused.
	bc := &osbuildv1.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "my-build", Namespace: testNS},
	}
	bc.Spec.Strategy.SourceStrategy = &osbuildv1.SourceBuildStrategy{
		From: corev1.ObjectReference{
			Kind:      "ImageStreamTag",
			Namespace: "other-ns", // explicit cross-namespace
			Name:      "shared-is:latest",
		},
	}

	imageClient := osimagefake.NewClientset(imageStream("shared-is"))
	k8sClient := fake.NewClientset()
	buildClient := osbuildfake.NewClientset(bc)

	s := scanner.NewUnusedImageStreams(k8sClient, imageClient, buildClient)
	findings, err := s.Scan(context.Background(), testNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1 (cross-namespace ref must not protect local IS)", len(findings))
	}
	if findings[0].Name != "shared-is" {
		t.Errorf("expected finding for shared-is, got %q", findings[0].Name)
	}
}
