#!/usr/bin/env bash
# Copyright 2025 The Kubernetes Authors.
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

# Set up Tekton Pipeline in the KIND cluster.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_ROOT}"/lib.sh

# Tekton Pipeline version (LTS release)
readonly TEKTON_PIPELINE_VERSION="${TEKTON_PIPELINE_VERSION:-v1.6.0}"

function usage() {
  >&2 cat <<EOF
Install Tekton Pipeline components into the KIND cluster.

Usage: $0 [options]

Examples:
  # Install Tekton Pipeline v1.6.0 LTS (default).
  $0

  # Install a specific version of Tekton Pipeline.
  TEKTON_PIPELINE_VERSION=v1.7.0 $0

Options:
    -help:
        Display this help message.
EOF
}

function main() {
  for arg in "$@"; do
    case "${arg}" in
      -help)
        usage
        return
        ;;
      --*)
        echo >&2 "cannot use flags with two leading dashes ('--...'), use single dashes instead ('-...')"
        return 1
        ;;
    esac
  done

  install_tekton_pipeline
}

function install_tekton_pipeline() {
  log "Installing Tekton Pipeline ${TEKTON_PIPELINE_VERSION}"

  local tekton_release_url
  tekton_release_url="https://storage.googleapis.com/tekton-releases/pipeline/previous/${TEKTON_PIPELINE_VERSION}/release.yaml"

  log "Fetching Tekton Pipeline from ${tekton_release_url}"
  do_kubectl apply -f "${tekton_release_url}"

  log "Sleeping for 30s as pods may be in pending state before they get scheduled"
  sleep 30
  log "Waiting for Tekton Pipeline controller to be ready"
  if ! do_kubectl wait --for=condition=ready pod \
    -l app=tekton-pipelines-controller \
    -n tekton-pipelines \
    --timeout=600s; then
    log "ERROR: Tekton Pipeline controller failed to become ready"
    do_kubectl get pods -n tekton-pipelines
    return 1
  fi

  log "Waiting for Tekton Pipeline webhook to be ready"
  if ! do_kubectl wait --for=condition=ready pod \
    -l app=tekton-pipelines-webhook \
    -n tekton-pipelines \
    --timeout=600s; then
    log "ERROR: Tekton Pipeline webhook failed to become ready"
    do_kubectl get pods -n tekton-pipelines
    return 1
  fi

  log "Tekton Pipeline ${TEKTON_PIPELINE_VERSION} is ready"
  do_kubectl get pods -n tekton-pipelines

  # Create test-pods namespace if it doesn't exist (needed for test Task)
  log "Creating test-pods namespace"
  do_kubectl create namespace test-pods --dry-run=client -o yaml | do_kubectl apply -f -

  # Deploy test Task resource for integration tests
  log "Deploying test Task resources"
  do_kubectl apply -f "${SCRIPT_ROOT}/config/tekton/test-task.yaml"
}

main "$@"
