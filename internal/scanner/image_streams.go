package scanner

import (
	"context"
	"fmt"
	"os"
	"strings"

	osbuildv1client "github.com/openshift/client-go/build/clientset/versioned"
	osimagev1client "github.com/openshift/client-go/image/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type unusedImageStreamsScanner struct {
	k8sClient   kubernetes.Interface
	imageClient osimagev1client.Interface
	buildClient osbuildv1client.Interface // nil if build API absent
}

// NewUnusedImageStreams creates a scanner for ImageStreams not referenced by
// any running Pod or BuildConfig. Pass nil imageClient on vanilla k8s to no-op.
func NewUnusedImageStreams(k8sClient kubernetes.Interface, imageClient osimagev1client.Interface, buildClient osbuildv1client.Interface) Scanner {
	return &unusedImageStreamsScanner{
		k8sClient:   k8sClient,
		imageClient: imageClient,
		buildClient: buildClient,
	}
}

func (s *unusedImageStreamsScanner) Name() string { return "unused-imagestreams" }

func (s *unusedImageStreamsScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	if s.imageClient == nil {
		return nil, nil
	}

	iss, err := s.imageClient.ImageV1().ImageStreams(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing imagestreams: %w", err)
	}
	if len(iss.Items) == 0 {
		return nil, nil
	}

	referenced := make(map[string]bool)

	// Mark ImageStreams referenced by pod container images.
	pods, err := s.k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}
	for i := range pods.Items {
		for _, c := range pods.Items[i].Spec.Containers {
			if name := isNameFromImage(c.Image, namespace); name != "" {
				referenced[name] = true
			}
		}
		for _, c := range pods.Items[i].Spec.InitContainers {
			if name := isNameFromImage(c.Image, namespace); name != "" {
				referenced[name] = true
			}
		}
	}

	// Mark ImageStreams referenced by BuildConfig outputs and strategy inputs.
	if s.buildClient != nil {
		bcs, bcErr := s.buildClient.BuildV1().BuildConfigs(namespace).List(ctx, metav1.ListOptions{})
		if bcErr != nil {
			fmt.Fprintf(os.Stderr, "warning: listing buildconfigs: %v\n", bcErr)
		} else {
			for i := range bcs.Items {
				bc := &bcs.Items[i]
				markISRef(bc.Spec.Output.To, namespace, referenced)
				if bc.Spec.Strategy.SourceStrategy != nil {
					markISRef(&bc.Spec.Strategy.SourceStrategy.From, namespace, referenced)
				}
				if bc.Spec.Strategy.DockerStrategy != nil {
					markISRef(bc.Spec.Strategy.DockerStrategy.From, namespace, referenced)
				}
				if bc.Spec.Strategy.CustomStrategy != nil {
					markISRef(&bc.Spec.Strategy.CustomStrategy.From, namespace, referenced)
				}
				for j := range bc.Spec.Source.Images {
					markISRef(&bc.Spec.Source.Images[j].From, namespace, referenced)
				}
			}
		}
	}

	var findings []Finding
	for i := range iss.Items {
		is := &iss.Items[i]
		if !referenced[is.Name] {
			findings = append(findings, Finding{
				Kind:       "ImageStream",
				Namespace:  namespace,
				Name:       is.Name,
				Reason:     "no tags in use",
				Severity:   SeverityInfo,
				Suggestion: "delete unused ImageStream to free registry storage",
			})
		}
	}
	return findings, nil
}

// isNameFromImage extracts the ImageStream name when the image reference points
// to the given namespace in the internal OpenShift registry.
// Handles: REGISTRY/namespace/name:tag  and  REGISTRY/namespace/name@sha256:...
//
// To avoid false matches on external registries that happen to contain the
// namespace name as a path component, we only match when the host portion of
// the image begins with one of the known internal registry prefixes.
func isNameFromImage(image, namespace string) string {
	const (
		svcRegistry     = "image-registry.openshift-image-registry.svc"
		legacyRegistry1 = "docker-registry.default.svc"
		legacyRegistry2 = "172.30.1.1" // common CRC/minishift default
	)
	hasInternalHost := strings.HasPrefix(image, svcRegistry) ||
		strings.HasPrefix(image, legacyRegistry1) ||
		strings.HasPrefix(image, legacyRegistry2)
	if !hasInternalHost {
		return ""
	}
	prefix := "/" + namespace + "/"
	idx := strings.Index(image, prefix)
	if idx < 0 {
		return ""
	}
	rest := image[idx+len(prefix):]
	if i := strings.IndexAny(rest, ":@"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// markISRef marks an ImageStream referenced by an ObjectReference as used,
// but only if the reference is scoped to the scanned namespace.
// Cross-namespace refs (via ref.Namespace or an embedded "ns/name:tag" prefix)
// must not shield a same-named local ImageStream from being reported unused.
func markISRef(ref *corev1.ObjectReference, namespace string, referenced map[string]bool) {
	if ref == nil {
		return
	}
	if ref.Kind != "ImageStreamTag" && ref.Kind != "ImageStreamImage" {
		return
	}
	// Explicit cross-namespace reference via the Namespace field.
	if ref.Namespace != "" && ref.Namespace != namespace {
		return
	}
	if name := isNameFromISTag(ref.Name, namespace); name != "" {
		referenced[name] = true
	}
}

// isNameFromISRef extracts the ImageStream name from an ImageStreamTag or
// ImageStreamImage reference. Accepts "name:tag", "name@sha256:digest", and
// "namespace/name:tag" / "namespace/name@sha256:digest". When a namespace prefix
// is present it must match the scanned namespace; otherwise the reference is
// cross-namespace and an empty string is returned.
func isNameFromISTag(ref, namespace string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		if ref[:i] != namespace {
			return "" // embedded namespace doesn't match → cross-namespace
		}
		ref = ref[i+1:]
	}
	if i := strings.IndexAny(ref, ":@"); i >= 0 {
		ref = ref[:i]
	}
	return ref
}
