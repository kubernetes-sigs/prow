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

# Build a single Prow image for Tilt's custom_build.
#
# Usage: hack/tilt-build.sh <image-name> [expected-ref]
#
# When invoked by Tilt, $EXPECTED_REF is set to the specific image reference
# Tilt expects (e.g. localhost:5001/hook:tilt-abc123). The script builds the
# image, re-tags it as $EXPECTED_REF, and pushes it so Tilt can roll out the
# update.
#
# When invoked manually (without $EXPECTED_REF), the image is pushed to the
# local registry as localhost:<PORT>/<name>:latest only.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC1091
source "${REPO_ROOT}/test/integration/lib.sh"

name="${1:?image name required}"
expected_ref="${2:-${EXPECTED_REF:-}}"

if [[ ! -v "PROW_IMAGES[${name}]" ]]; then
  log "ERROR: unknown image '${name}'"
  log "Valid images: ${!PROW_IMAGES[*]}"
  exit 1
fi

src_dir="${PROW_IMAGES[${name}]}"
local_ref="localhost:${LOCAL_DOCKER_REGISTRY_PORT}/${name}:latest"

log "Building image: ${name} (source: ${src_dir})"

# Build and push to the local registry via prowimagebuilder (which wraps ko).
tmpfile="$(mktemp /tmp/prowimagebuilder.XXXXXX.yaml)"
trap "rm -f ${tmpfile}" EXIT

printf 'images:\n  - dir: %s\n' "${src_dir}" > "${tmpfile}"

go -C "${REPO_ROOT}/hack/tools" run ./prowimagebuilder \
  --ko-docker-repo="localhost:${LOCAL_DOCKER_REGISTRY_PORT}" \
  --prow-images-file="${tmpfile}" \
  --push

# If Tilt provided an expected ref, re-tag and push so Tilt can track this
# specific build by its content-addressed tag.
# ko pushes to the registry but does not load the image into the local Docker
# daemon cache, so pull it first before tagging.
if [[ -n "${expected_ref}" && "${expected_ref}" != "${local_ref}" ]]; then
  docker pull "${local_ref}"
  docker tag "${local_ref}" "${expected_ref}"
  docker push "${expected_ref}"
fi
