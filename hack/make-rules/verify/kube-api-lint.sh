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

set -o nounset
set -o errexit
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
cd "${REPO_ROOT}"

TOOLS_DIR="${REPO_ROOT}/hack/tools"
KAL_CONFIG="${TOOLS_DIR}/.golangci-kal.yml"
KAL_BINARY="${TOOLS_DIR}/tmp/bin/golangci-kube-api-linter"

# Build binary if it doesn't exist
if [[ ! -f "${KAL_BINARY}" ]]; then
  echo "Building golangci-kube-api-linter..."
  "${TOOLS_DIR}/build-kal"
fi

echo "Running kube-api-linter..."
"${KAL_BINARY}" run \
  --config "${KAL_CONFIG}" \
  ./pkg/apis/...
