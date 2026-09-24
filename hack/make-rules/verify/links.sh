#!/usr/bin/env bash
# Copyright 2026 The Kubernetes Authors.
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

if ! command -v hugo >/dev/null 2>&1; then
  echo "ERROR: hugo is required to build the site before checking links."
  echo "Install the extended edition, see https://gohugo.io/installation/"
  exit 1
fi

BIN_DIR="${REPO_ROOT}/_bin/htmltest"
mkdir -p "${BIN_DIR}"
HTMLTEST="${BIN_DIR}/htmltest"

echo "Ensuring go version."
source ./hack/build/setup-go.sh

echo "Install htmltest."
cd "hack/tools"
go build -o "${HTMLTEST}" github.com/wjdp/htmltest
cd "${REPO_ROOT}"

cd site

# The site build transforms SCSS through PostCSS, which hugo calls via npx.
echo "Installing site dependencies."
npm ci

echo "Building the site."
hugo --quiet --cleanDestinationDir

echo "Checking links..."
make check-broken-links HTMLTEST="${HTMLTEST}"

echo 'PASS: No broken internal links detected'
echo 'NOTE: broken external links are reported as warnings above, not failures.'
