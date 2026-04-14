#!/usr/bin/env bash
# Copyright The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# dev-env.sh sets up a local Prow development environment in a kind cluster.
# All external dependencies (GitHub, GCS, Gerrit, Pub/Sub) are replaced with
# in-cluster fakes, so no cloud accounts or real credentials are needed.
#
# The cluster maps HTTP to localhost:8080 and HTTPS to localhost:8443 so that
# root privileges are not required to bind to those ports locally. This differs
# from the integration test cluster (which uses ports 80/443 to match the
# hardcoded addresses in the integration test suite).
#
# Usage:
#   hack/dev-env.sh            # First run: build all images and bring up the cluster
#   hack/dev-env.sh -no-build  # Skip image builds (cluster already has images)
#   hack/dev-env.sh -rebuild=hook,crier  # Rebuild specific images, then redeploy
#   hack/dev-env.sh -teardown  # Destroy the cluster and local registry

# Host ports used for the kind cluster ingress. Using unprivileged ports
# (>=1024) avoids requiring root on Linux. Override via environment variables
# if these conflict with other local services.
readonly DEV_HTTP_PORT="${DEV_HTTP_PORT:-8080}"
readonly DEV_HTTPS_PORT="${DEV_HTTPS_PORT:-8443}"

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
INTEGRATION_DIR="${REPO_ROOT}/test/integration"

source "${INTEGRATION_DIR}/lib.sh"

function usage() {
  cat <<EOF
Start a local Prow development environment using kind.

All external services (GitHub, GCS, Gerrit, Pub/Sub) are replaced with
in-cluster fakes. No cloud credentials are required.

Usage: $0 [options]

Examples:
  # First-time setup: create the cluster and build+deploy core images (default).
  $0

  # First-time setup with all components (matches integration-test environment).
  $0 -profile=full

  # Bring up the cluster (or reuse existing) without rebuilding images.
  # Useful after a cluster restart or when images are already in the local registry.
  $0 -no-build

  # Rebuild only hook and crier, redeploy them, then print connection info.
  $0 -rebuild=hook,crier

  # Tear down the kind cluster and local Docker registry.
  $0 -teardown

Options:
    -profile=<core|full>
        Component profile to deploy. "core" (default) deploys a minimal set
        (hook, deck, crier, horologium, prow-controller-manager, sinker, and
        the core fakes). "full" deploys all components, matching the
        integration-test environment.

    -no-build
        Skip image builds. The cluster will be created (or reused) and
        components deployed using whatever images are already in the local
        registry. Fails if no images have been built yet.

    -rebuild=<images>
        Comma-separated list of images to rebuild before deploying. Does not
        recreate the cluster. Use "ALL" to rebuild every image in the active
        profile.
        Example: -rebuild=hook,crier

    -teardown
        Delete the kind cluster and local Docker registry, then exit.

    -help
        Display this message.
EOF
}

function check_prerequisites() {
  local missing=()

  for cmd in docker kind kubectl go; do
    if ! command -v "${cmd}" &>/dev/null; then
      missing+=("${cmd}")
    fi
  done

  if ((${#missing[@]})); then
    log "ERROR: missing required tools: ${missing[*]}"
    log "Install them and re-run this script."
    log "  docker:  https://docs.docker.com/get-docker/"
    log "  kind:    https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
    log "  kubectl: https://kubernetes.io/docs/tasks/tools/"
    log "  go:      https://go.dev/doc/install"
    return 1
  fi
}

function print_connection_info() {
  cat <<EOF

==> Local Prow environment is ready!

Cluster: ${_KIND_CLUSTER_NAME}
Kubeconfig context: ${_KIND_CONTEXT}

Switch kubectl to the dev cluster:
  kubectl config use-context ${_KIND_CONTEXT}

  Or prefix every kubectl command:
  kubectl --context=${_KIND_CONTEXT} get prowjobs

Access the Deck UI:
  http://localhost:${DEV_HTTP_PORT}/

Tail logs for a component (e.g. hook):
  kubectl --context=${_KIND_CONTEXT} logs -f -l app=hook

Send a fake GitHub webhook (requires go):
  go run ./cmd/phony --address=http://localhost:${DEV_HTTP_PORT}/hook \\
    --hmac=\$(kubectl --context=${_KIND_CONTEXT} get secret hmac-token -o jsonpath='{.data.hmac}' | base64 -d) \\
    --event=issue_comment --payload=<your-payload.json>

Validate config without deploying:
  go run ./cmd/checkconfig \\
    --config-path=test/integration/config/prow/config.yaml \\
    --plugin-config=test/integration/config/prow/plugins.yaml

Rebuild a single component after a code change:
  test/integration/setup-prow-components.sh -build=hook

Tear down the environment when done:
  hack/dev-env.sh -teardown
  # or: make dev-teardown

EOF
}

function main() {
  local no_build=false
  local rebuild_images=""
  local teardown=false
  local profile="core"

  for arg in "$@"; do
    case "${arg}" in
      -no-build)
        no_build=true
        ;;
      -rebuild=*)
        rebuild_images="${arg#-rebuild=}"
        ;;
      -profile=*)
        profile="${arg#-profile=}"
        if [[ "${profile}" != "core" && "${profile}" != "full" ]]; then
          echo >&2 "invalid -profile value '${profile}': must be 'core' or 'full'"
          return 1
        fi
        ;;
      -teardown)
        teardown=true
        ;;
      -help)
        usage
        return 0
        ;;
      --*)
        echo >&2 "Use single-dash flags (e.g. -no-build, not --no-build)"
        return 1
        ;;
      *)
        echo >&2 "Unknown argument: ${arg}"
        usage >&2
        return 1
        ;;
    esac
  done

  if "${teardown}"; then
    log "Tearing down local Prow dev environment"
    "${INTEGRATION_DIR}/teardown.sh" -all
    return 0
  fi

  check_prerequisites

  # If -rebuild is set, skip cluster creation and only rebuild the named images.
  if [[ -n "${rebuild_images}" ]]; then
    log "Rebuilding images: ${rebuild_images} (cluster not recreated)"
    "${INTEGRATION_DIR}/setup-prow-components.sh" \
      "-profile=${profile}" \
      "-build=${rebuild_images}"
    print_connection_info
    return 0
  fi

  log "Setting up kind cluster (HTTP on :${DEV_HTTP_PORT}, HTTPS on :${DEV_HTTPS_PORT})"
  "${INTEGRATION_DIR}/setup-kind-cluster.sh" \
    "-http-host-port=${DEV_HTTP_PORT}" \
    "-https-host-port=${DEV_HTTPS_PORT}"

  if "${no_build}"; then
    log "Deploying Prow components (skipping image builds, profile=${profile})"
    "${INTEGRATION_DIR}/setup-prow-components.sh" "-profile=${profile}"
  else
    log "Building images and deploying Prow components (profile=${profile}, this takes a few minutes on first run)"
    "${INTEGRATION_DIR}/setup-prow-components.sh" "-profile=${profile}" -build=ALL
  fi

  print_connection_info
}

main "$@"
