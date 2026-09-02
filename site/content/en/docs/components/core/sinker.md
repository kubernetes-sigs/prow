---
title: "Sinker"
weight: 60
description: >
  Garbage-collects old ProwJobs and test pods.
---

Sinker removes old ProwJobs from the ProwJob namespace and old test pods from
build clusters. It keeps the Kubernetes API from accumulating completed jobs and
pods after their results have already been reported.

## What Sinker Cleans Up

Sinker uses the `sinker` section of the main Prow configuration to decide when
objects are old enough to remove:

* Completed ProwJobs older than `max_prowjob_age`.
* Completed periodic ProwJobs older than `max_prowjob_age`, while retaining the
  latest ProwJob for each active periodic job so horologium can continue to
  schedule from the latest state.
* Test pods created by Prow that are older than `max_pod_age`.
* Test pods for completed ProwJobs after `terminated_pod_ttl`.

Build clusters listed in `exclude_clusters` are skipped.

## Configuration

The main configuration fields are:

```yaml
sinker:
  resync_period: 1h
  max_prowjob_age: 168h
  max_pod_age: 24h
  terminated_pod_ttl: 24h
  exclude_clusters:
  - build-cluster-name
```

If unset, Sinker defaults to a one hour resync period, a one week maximum
ProwJob age, a one day maximum pod age, and a terminated pod TTL matching the
maximum pod age.

## Required Access

Sinker needs access to the infrastructure cluster to list and delete ProwJobs in
the configured ProwJob namespace. It also needs access to every managed build
cluster to list, watch, get, patch, and delete test pods in the configured pod
namespace.

## CLI Flags

Sinker uses the standard Prow config, Kubernetes, and instrumentation flags. The
most commonly used flags are:

* `--config-path`: Path to the main Prow configuration.
* `--job-config-path`: Path to the job configuration, when using split job
  config.
* `--kubeconfig`: Path to the kubeconfig used for the infrastructure and build
  clusters.
* `--dry-run`: Controls whether Sinker makes mutating API calls. The default is
  `true`; production deployments must set `--dry-run=false` to delete objects.
* `--run-once`: Runs one reconciliation loop and exits. This is useful for
  validating configuration and permissions before running continuously.

Sinker also exposes Prometheus metrics under the `sinker_` prefix, including
existing object counts, reconciliation duration, cleaned ProwJobs, removed pods,
and cleanup errors.
