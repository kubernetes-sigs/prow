---
title: "Horologium"
weight: 40
description: >
  Triggers periodic ProwJobs when their schedule is due.
---

Horologium is the Prow component that creates ProwJobs for configured
`periodic` jobs. It watches existing ProwJobs, evaluates each periodic job's
schedule, and creates a new ProwJob when the previous run is complete and the
next run is due.

## How Horologium Schedules Jobs

Horologium supports two scheduling modes for periodic jobs:

* `interval`: run after the configured duration has elapsed since the previous
  run. When `minimum_interval` is set, the interval is measured from the
  previous run's completion time.
* `cron`: run when the configured cron schedule fires.

Horologium retains the latest ProwJob for each active periodic job so it can
determine whether the next run should start. Sinker uses the same latest-run
information to avoid garbage-collecting the latest active periodic ProwJob.

If a periodic job config includes retry settings, Horologium can also create
retry ProwJobs until the configured attempt limit is reached.

## Configuration

Periodic jobs are configured in the Prow job configuration:

```yaml
periodics:
- name: ci-example-periodic
  interval: 1h
  decorate: true
  spec:
    containers:
    - image: alpine
      command:
      - /bin/sh
      args:
      - -c
      - echo "hello from horologium"
```

Horologium itself reads the `horologium` section of the main Prow configuration.
The main component-specific option is `tick_interval`, which controls how often
Horologium checks whether new periodic jobs need to be created:

```yaml
horologium:
  tick_interval: 1m
```

If `tick_interval` is unset, Horologium checks every minute.

## Required Access

Horologium needs access to the infrastructure cluster to list, watch, and create
ProwJobs in the configured ProwJob namespace.

## CLI Flags

Horologium uses the standard Prow config, Kubernetes, controller-manager, and
instrumentation flags. The most commonly used flags are:

* `--config-path`: Path to the main Prow configuration.
* `--job-config-path`: Path to the job configuration.
* `--kubeconfig`: Path to the kubeconfig for the infrastructure cluster.
* `--dry-run`: Controls whether Horologium creates ProwJobs. The default is
  `true`; production deployments must set `--dry-run=false`.

Horologium exposes Prometheus metrics under the `horologium` component name.
