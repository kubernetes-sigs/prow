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

# Apply Prow configmaps (config, plugins, job-config) to the local dev cluster.
# Called by Tilt when config files change; also safe to run manually.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC1091
source "${REPO_ROOT}/test/integration/lib.sh"

PROW_CFG="${REPO_ROOT}/test/integration/config/prow"

pushd "${PROW_CFG}" >/dev/null

do_kubectl create configmap config \
  --from-file=./config.yaml \
  --dry-run=client -oyaml \
  | do_kubectl apply -f - --namespace=default

do_kubectl create configmap plugins \
  --from-file=./plugins.yaml \
  --dry-run=client -oyaml \
  | do_kubectl apply -f - --namespace=default

do_kubectl create configmap job-config \
  --from-file=./jobs \
  --dry-run=client -oyaml \
  | do_kubectl apply -f - --namespace=default

popd >/dev/null
