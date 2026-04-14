---
title: "Local Development Environment"
weight: 75
description: >
  Run a complete Prow stack locally using kind, with fake replacements for
  all external services (GitHub, GCS, Gerrit, Pub/Sub).
---

This guide explains how to bring up a local Prow environment for development
and testing. No cloud account, real GitHub credentials, or external services
are required. All external dependencies are replaced by in-cluster fakes.

For deploying Prow to a real cluster, see
[Deploying Prow](/docs/getting-started-deploy/) instead.

## Prerequisites

Install the following tools before proceeding:

| Tool | Purpose | Install |
|---|---|---|
| [Docker](https://docs.docker.com/get-docker/) | Container runtime | Required |
| [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | Local Kubernetes cluster | Required |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | Cluster interaction | Required |
| [Go](https://go.dev/doc/install) | Build Prow images | Required |

## How it works

The local environment reuses the same kind cluster infrastructure that powers
Prow's integration test suite (`test/integration/`). It deploys the full set of
Prow components alongside in-cluster fakes for every external dependency:

| External service | Local replacement |
|---|---|
| GitHub API | `fakeghserver` |
| Git hosting | `fakegitserver` |
| Gerrit | `fakegerritserver` |
| GCS / blob storage | `fakegcsserver` |
| Pub/Sub | `fakepubsub` |

The cluster exposes HTTP at `localhost:8080` and HTTPS at `localhost:8443` (via
an nginx ingress), using unprivileged ports so root is not required on Linux.
A local Docker registry at `localhost:5001` holds the built images.

Note: the integration test suite (`make integration`) uses ports 80/443. The dev
environment intentionally uses different ports to avoid that root requirement.
Do not run both environments simultaneously on the same machine, as they share
the same kind cluster name.

## Component profiles

The dev environment supports two profiles to balance resource usage against
completeness:

| Profile | Components | Use when |
|---|---|---|
| `core` (default) | hook, deck, crier, horologium, prow-controller-manager, sinker + core fakes | Day-to-day development; fits on a laptop |
| `full` | All components, matching the integration-test environment | Testing Gerrit, Pub/Sub, Tide, Gangway, etc. |

`make dev` uses the `core` profile. Use `make dev-full` or
`hack/dev-env.sh -profile=full` for the full set.

## Quick start

```bash
# First-time setup (core profile): creates the cluster, builds core images, deploys.
# Takes a few minutes on a cold machine; subsequent runs are faster.
make dev

# Full profile: builds and deploys all components.
make dev-full
```

Once it completes, the script prints connection details, including the URL for
the Deck UI and kubectl commands for watching logs.

## Common workflows

### Bring up the environment without rebuilding images

If images are already in the local registry (e.g. after a machine restart), use
`-no-build` to skip the image build step:

```bash
make dev          # full setup including image build
# ... or ...
hack/dev-env.sh -no-build   # reuse existing images, just (re)deploy
```

### Rebuild a single component after a code change

After editing Go source for a component, rebuild and redeploy only that image:

```bash
# Rebuild hook and immediately redeploy it in the running cluster:
hack/dev-env.sh -rebuild=hook

# Rebuild multiple components at once:
hack/dev-env.sh -rebuild=hook,crier

# Rebuild all images for the active profile (core by default):
hack/dev-env.sh -rebuild=ALL

# Rebuild all images in the full profile:
hack/dev-env.sh -profile=full -rebuild=ALL
```

This is equivalent to calling `test/integration/setup-prow-components.sh
-build=hook` directly, but stays at the same entry point.

### Send a fake GitHub webhook

[`phony`](/docs/components/cli-tools/phony/) sends fake GitHub webhook payloads
to a running `hook` instance. Use it to test plugin behavior without a real
repository:

```bash
hack/phony.sh --event=issue_comment --payload=<path-to-payload.json>
```

`hack/phony.sh` reads the HMAC token from the cluster automatically and
defaults `--address` to `http://localhost:8080/hook`. Set `DEV_HTTP_PORT` to
override the port. Any additional flags are passed through to `phony`.

### Validate config files without deploying

```bash
go run ./cmd/checkconfig \
  --config-path=test/integration/config/prow/config.yaml \
  --plugin-config=test/integration/config/prow/plugins.yaml
```

### Watch component logs

```bash
kubectl --context=kind-kind-prow-integration logs -f -l app=hook
kubectl --context=kind-kind-prow-integration logs -f -l app=deck
```

### Access the Deck UI

Open [http://localhost:8080/](http://localhost:8080/) in a browser while the
cluster is running.

### Run the full integration test suite

The local dev environment uses the same cluster as the integration tests. To run
them against your running cluster:

```bash
# Run all integration tests (skips cluster setup if cluster already exists):
test/integration/integration-test.sh -no-setup-kind-cluster

# Run a single test:
test/integration/integration-test.sh -no-setup -run=TestHook
```

See [Integration tests](/docs/test/integration/) for full details.

### Tear down the environment

```bash
make dev-teardown
# or:
hack/dev-env.sh -teardown
```

This removes the kind cluster and the local Docker registry container.

## Developing hook plugins

The most common development target is a hook plugin. The minimal loop is:

1. Edit code in `pkg/plugins/<your-plugin>/`.
2. Rebuild and redeploy hook: `hack/dev-env.sh -rebuild=hook`.
3. Send a fake webhook with `hack/phony.sh` (see above).
4. Check hook logs: `kubectl --context=kind-kind-prow-integration logs -f -l app=hook`.

See [Adding new plugins](/docs/getting-started-develop/#how-to-add-new-plugins)
for a walkthrough of the plugin registration API.
