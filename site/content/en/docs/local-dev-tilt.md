---
title: "Local Development with Tilt"
weight: 76
description: >
  Automatic rebuild and redeploy on file change using Tilt, for a faster
  inner development loop.
---

[Tilt](https://tilt.dev/) watches your source files and automatically rebuilds
and redeploys affected Prow components when you save a change. This eliminates
the manual `hack/dev-env.sh -rebuild=hook` step and gives you a live view of
component status and logs in one terminal.

This guide assumes you have already read
[Local Development Environment](/docs/local-dev/) and have the kind cluster
running.

## Prerequisites

Install [Tilt](https://docs.tilt.dev/install.html) in addition to the tools
listed in the [Local Development Environment prerequisites](/docs/local-dev/#prerequisites).

## Workflow

### First-time setup

```bash
# Create the kind cluster and deploy the core components (one-time).
make dev
```

This builds all core images and deploys them. Once `make dev` completes, start
Tilt:

```bash
tilt up
# or:
make dev-tilt
```

Tilt opens a browser UI at `http://localhost:10350/` showing each component's
build status and live logs.

### Day-to-day development

After the first-time setup, start the loop with `make dev-tilt`. Tilt detects
the running cluster and connects immediately - no rebuild needed unless source
has changed since the last run.

```bash
make dev-tilt
```

`make dev-tilt` checks that the kind cluster is running and that `tilt` is
installed before invoking `tilt up`, giving a clear error if either is missing.

**Edit → save → done.** Tilt detects the changed file, rebuilds only the
affected image (via `hack/tilt-build.sh`), and rolls out the new pod. For a
typical single-component change this takes the same time as a manual
`hack/dev-env.sh -rebuild=hook`, but happens automatically.

### Config file changes

Changes to Prow config files (`config.yaml`, `plugins.yaml`, or files under
`jobs/`) are applied automatically by the `prow-config` resource in Tilt. There
is no `kubectl apply` needed.

### Stopping Tilt

Press `Ctrl+C` in the terminal where `tilt up` is running, or run:

```bash
tilt down
```

`tilt down` deletes only the resources Tilt manages. Since the `Tiltfile` does
not apply any Kubernetes resources directly (it relies on `make dev` for initial
deployment), `tilt down` is effectively a no-op for cluster state - the
components remain running. Use `make dev-teardown` to remove the cluster
entirely.

## How it works

The `Tiltfile` at the repository root defines:

- **`prow-config`** - a `local_resource` that runs `hack/tilt-apply-config.sh`
  whenever `config.yaml`, `plugins.yaml`, or the `jobs/` directory changes.

- **`custom_build` per component** - tells Tilt which source directories to
  watch and which script to run for each image. When a watched file changes,
  Tilt calls `hack/tilt-build.sh <name> $EXPECTED_REF`, which builds the image
  via `prowimagebuilder` (wrapping `ko`) and pushes it to the local registry.
  Tilt then patches the running Deployment to use the new image tag, triggering
  a rolling update.

Tilt discovers which Deployments to update by watching the cluster for pods
whose image name matches `localhost:5001/<component>`. No `k8s_yaml` is needed
in the Tiltfile because `make dev` has already deployed the components.

## Customizing with tilt-settings.yaml

The `tilt-settings.yaml` file at the repository root is git-ignored and lets
you override defaults without touching the committed `Tiltfile`. Copy the
checked-in version as a starting point:

```bash
cp tilt-settings.yaml tilt-settings.yaml  # already there; just edit it
```

### Disable automatic rebuilds

If `pkg/` changes trigger too many simultaneous rebuilds, switch to manual
mode. Tilt will only rebuild when you press the ▶ button in its UI:

```yaml
trigger_mode: manual
```

### Watch additional components

To also watch non-core components (requires them to already be deployed, e.g.
via `make dev-full`):

```yaml
extra_components:
  - tide
  - gerrit
  - fakegerritserver
```

## Personal Tiltfile overrides (tilt.d/)

Drop any `*.tiltfile` file into the `tilt.d/` directory for local
customizations that are never committed. The `Tiltfile` loads all files from
that directory automatically. For example, to narrow the watch paths for hook:

```python
# tilt.d/narrow-hook-deps.tiltfile
prow_component('hook', ['cmd/hook/', 'pkg/github/', 'pkg/plugins/'])
```

Files in `tilt.d/` are git-ignored. The directory itself (containing only
`.gitkeep`) is committed so the path exists for everyone.
