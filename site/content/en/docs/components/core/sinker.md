---
title: "Sinker"
weight: 60
description: >
  Garbage collector for old ProwJobs and Pods.
---

Sinker is Prow's garbage-collector controller.
It periodically removes old `ProwJob` resources and old/orphaned Pods created by Prow.

Source code: [`cmd/sinker`](https://github.com/kubernetes-sigs/prow/tree/main/cmd/sinker)

## What Sinker Cleans Up

Sinker reconciles on a loop (`sinker.resync_period`) and:

- Deletes completed non-periodic `ProwJob`s older than `sinker.max_prowjob_age`.
- Deletes completed periodic `ProwJob`s older than `sinker.max_prowjob_age`, while keeping the latest run for active periodic jobs.
- Deletes Prow Pods (label `created-by-prow=true`) when they are:
  - older than `sinker.max_pod_age`,
  - older than `sinker.terminated_pod_ttl` after their `ProwJob` completed,
  - orphaned (no matching `ProwJob` exists anymore).
- Skips Pod cleanup in clusters listed in `sinker.exclude_clusters`.

## Configuration (`config.yaml`)

Sinker behavior is controlled by the top-level `sinker` block in `config.yaml`:

```yaml
sinker:
  resync_period: 1h
  max_prowjob_age: 168h
  max_pod_age: 24h
  terminated_pod_ttl: 24h
  exclude_clusters: []
```

Defaults:

- `resync_period`: `1h`
- `max_prowjob_age`: `168h` (7 days)
- `max_pod_age`: `24h`
- `terminated_pod_ttl`: same as `max_pod_age`
- `exclude_clusters`: empty

## CLI Flags

Sinker-specific flags:

| Flag | Description | Default |
| --- | --- | --- |
| `--run-once` | Run one reconciliation and exit. | `false` |
| `--dry-run` | Do not perform mutating Kubernetes API calls. | `true` |

Prow config loading flags:

| Flag | Description | Default |
| --- | --- | --- |
| `--config-path` | Path to `config.yaml` (required). | `""` |
| `--job-config-path` | Path to job config file or directory. | `""` |
| `--supplemental-prow-config-dir` | Additional config directory (repeatable). | none |
| `--supplemental-prow-configs-filename` | Deprecated alias for suffix flag. | `"_prowconfig.yaml"` |
| `--supplemental-prow-configs-filename-suffix` | Filename suffix for supplemental configs. | `"_prowconfig.yaml"` |
| `--in-repo-config-cache-size` | Cache size for in-repo config loading. | `200` |
| `--cache-dir-base` | Base directory for repo cache. | `""` |
| `--moonraker-address` | Full HTTP address of Moonraker service. | `""` |

Kubernetes client flags:

| Flag | Description | Default |
| --- | --- | --- |
| `--kubeconfig` | Path to kubeconfig file. | `""` |
| `--kubeconfig-dir` | Directory of kubeconfig files. | `""` |
| `--kubeconfig-suffix` | Only load files with this suffix from `--kubeconfig-dir`. | `""` |
| `--projected-token-file` | Projected service account token file for in-cluster auth. | `""` |
| `--no-in-cluster-config` | Disable in-cluster config resolution. | `false` |

Instrumentation flags:

| Flag | Description | Default |
| --- | --- | --- |
| `--metrics-port` | Port for Prometheus metrics endpoint. | `9090` |
| `--pprof-port` | Port for pprof endpoints. | `6060` |
| `--health-port` | Port for readiness/liveness endpoints. | `8081` |
| `--profile-memory-usage` | Enable periodic memory profiling. | `false` |
| `--memory-profile-interval` | Interval between memory profiles. | `30s` |

## Notes

- In production deployments, set `--dry-run=false`.
- Sinker needs access to list/watch/get/delete/patch Pods in build clusters and manage `ProwJob` resources in the Prow namespace.
