package ocp

import (
	"fmt"
	"os"

	osappsv1client "github.com/openshift/client-go/apps/clientset/versioned"
	osbuildv1client "github.com/openshift/client-go/build/clientset/versioned"
	osimagev1client "github.com/openshift/client-go/image/clientset/versioned"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// IsOpenShift returns true if the cluster exposes the OpenShift apps API group.
func IsOpenShift(dc discovery.DiscoveryInterface) bool {
	groups, err := dc.ServerGroups()
	if err != nil {
		return false
	}
	for _, g := range groups.Groups {
		if g.Name == "apps.openshift.io" {
			return true
		}
	}
	return false
}

// NewAppsClient constructs an OpenShift apps clientset from a rest.Config.
func NewAppsClient(cfg *rest.Config) (osappsv1client.Interface, error) {
	c, err := osappsv1client.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating openshift apps client: %w", err)
	}
	return c, nil
}

// NewImageClient constructs an OpenShift image clientset from a rest.Config.
func NewImageClient(cfg *rest.Config) (osimagev1client.Interface, error) {
	c, err := osimagev1client.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating openshift image client: %w", err)
	}
	return c, nil
}

// NewBuildClient constructs an OpenShift build clientset from a rest.Config.
func NewBuildClient(cfg *rest.Config) (osbuildv1client.Interface, error) {
	c, err := osbuildv1client.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating openshift build client: %w", err)
	}
	return c, nil
}

// LogIfNotOpenShift prints a verbose note to stderr when OCP APIs are absent.
func LogIfNotOpenShift(verbose bool) {
	if verbose {
		fmt.Fprintln(os.Stderr, "note: OpenShift APIs not detected; skipping DC/Route/ImageStream scanners")
	}
}
