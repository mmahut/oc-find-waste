package scanner

import (
	"context"
	"fmt"
	"os"
	"strings"

	osbuildv1client "github.com/openshift/client-go/build/clientset/versioned"
	osimagev1client "github.com/openshift/client-go/image/clientset/versioned"
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

	// Mark ImageStreams referenced by BuildConfig outputs.
	if s.buildClient != nil {
		bcs, bcErr := s.buildClient.BuildV1().BuildConfigs(namespace).List(ctx, metav1.ListOptions{})
		if bcErr != nil {
			fmt.Fprintf(os.Stderr, "warning: listing buildconfigs: %v\n", bcErr)
		} else {
			for i := range bcs.Items {
				to := bcs.Items[i].Spec.Output.To
				if to == nil || to.Kind != "ImageStreamTag" {
					continue
				}
				if name := isNameFromISTag(to.Name); name != "" {
					referenced[name] = true
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
func isNameFromImage(image, namespace string) string {
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

// isNameFromISTag extracts the ImageStream name from an ImageStreamTag reference.
// Accepts "name:tag" and "namespace/name:tag".
func isNameFromISTag(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	return ref
}
