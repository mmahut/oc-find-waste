# oc-find-waste

A read-only CLI that scans an OpenShift or Kubernetes namespace for wasted resources and reports an estimated monthly cost.

![demo](demo.gif)

## What it finds

- **Scaled-to-zero** workloads (Deployments, StatefulSets, DeploymentConfigs)
- **Completed jobs** older than 7 days
- **Orphaned PVCs** — bound but not mounted by any Pod
- **Unused ImageStreams** — no running Pod or BuildConfig references them
- **Over-provisioned Pods** — p95 CPU/memory well below requests *(requires Prometheus)*
- **Unused Routes** — zero HAProxy traffic over the window *(requires Prometheus)*

## Install

```sh
# requires Go 1.21+
go install github.com/mmahut/oc-find-waste/cmd/oc-find-waste@latest
```

Pre-built binaries for Linux and macOS (amd64/arm64) are on the [Releases page](https://github.com/mmahut/oc-find-waste/releases).

## Usage

```
oc-find-waste scan -n <namespace> [flags]

Flags:
  -n, --namespace string        namespace to scan (defaults to current context namespace)
  -A, --all-namespaces          scan every namespace the user can list
      --kubeconfig string       path to kubeconfig (defaults to $KUBECONFIG or ~/.kube/config)
      --pricing string          aws | azure | gcp | on-prem | path/to/profile.yaml (default "on-prem")
      --window string           lookback window for metrics-based scanners, e.g. 7d, 24h (default "7d")
      --timeout string          maximum time for all scanners to complete (default "2m")
      --prometheus-url string   override Prometheus endpoint (auto-detected by default)
  -o, --output string           text | json (default "text")
      --no-color                disable ANSI colors
      --rightsize               print suggested resource patch YAML (does not apply changes)
      --skip stringArray        scanner name to skip (repeatable)
      --only stringArray        scanner name to run exclusively (repeatable)
  -v, --verbose                 log per-scanner progress to stderr
```

## Example output

```
Scanning namespace: myapp
Window: 7d  │  Pricing: aws

Deployment
  legacy-worker                  scaled to 0 (resource age: 47d)
    → if no longer used, delete the Deployment

  web-api                        over-provisioned                          $47.20/mo
    requests: 2000m CPU, 4Gi RAM  │  p95 usage: 180m CPU, 600Mi RAM
    → suggest: 300m CPU, 1Gi RAM  ($5.80/mo, -88%)

PersistentVolumeClaim
  data-backup                    bound but unmounted (50Gi)                 $4.00/mo
    → if data is no longer needed, delete the PVC to release storage

─────────────────────────────────────────────
Findings: 3
Estimated monthly waste: $51.20
Potential savings:       $45.40  (89%)
```

## Pricing profiles

Built-in profiles: `aws`, `azure`, `gcp`, `on-prem` (zero cost). Pass a path to load a custom YAML profile:

```yaml
name: my-cloud
description: "Custom pricing"
cpu_core_hour: 0.05
memory_gb_hour: 0.007
pvc_gb_month: 0.09
```