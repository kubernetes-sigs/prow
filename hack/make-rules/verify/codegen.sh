#!/usr/bin/env bash
# Copyright 2021 The Kubernetes Authors.
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

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/verify-codegen.XXXXXXXX")"
trap 'rm -rf -- "${TMP_ROOT}"' EXIT
TMP_ROOT="$(cd "${TMP_ROOT}" && pwd -P)"
output_paths=(
  pkg/apis
  pkg/client
  pkg/config
  pkg/gangway
  pkg/pipeline
  pkg/plugins
  config/prow/cluster/prowjob-crd/prowjob_customresourcedefinition.yaml
)

# The updater resolves its own repository root and writes generated files there.
# Include untracked files that Git does not ignore so directory comparisons
# do not report unrelated local files as missing from the temporary copy.
source_files=()
while IFS= read -r -d '' source_file; do
  if [[ -e "${REPO_ROOT}/${source_file}" || -L "${REPO_ROOT}/${source_file}" ]]; then
    source_files+=("${source_file}")
  fi
done < <(git -C "${REPO_ROOT}" ls-files -z --cached --others --exclude-standard)
printf '%s\0' "${source_files[@]}" \
  | tar -C "${REPO_ROOT}" --null -T - -cf - \
  | tar -C "${TMP_ROOT}" -xf -
# The compared paths must also contain any local ignored files so they do not
# appear as missing outputs in whole-directory diffs.
existing_output_paths=()
for path in "${output_paths[@]}"; do
  if [[ -e "${REPO_ROOT}/${path}" || -L "${REPO_ROOT}/${path}" ]]; then
    existing_output_paths+=("${path}")
  fi
done
if [[ ${#existing_output_paths[@]} -gt 0 ]]; then
  tar -C "${REPO_ROOT}" -cf - "${existing_output_paths[@]}" | tar -C "${TMP_ROOT}" -xf -
fi

# Reuse downloaded protoc dependencies
if [[ -d "${REPO_ROOT}/_bin/protoc" ]]; then
  mkdir -p "${TMP_ROOT}/_bin"
  cp -aL "${REPO_ROOT}/_bin/protoc" "${TMP_ROOT}/_bin/"
fi

if ! "${TMP_ROOT}/hack/make-rules/update/codegen.sh"; then
  echo "ERROR: codegen generation failed" >&2
  exit 1
fi

echo "diffing ${REPO_ROOT} against freshly generated codegen"
ret=0
compare_output() {
  local path="$1"
  diff -Naupr "${REPO_ROOT}/${path}" "${TMP_ROOT}/${path}" || ret=1
}

# Match all destinations in update/codegen.sh
for path in "${output_paths[@]}"; do
  compare_output "${path}"
done

# gen-all-proto-stubs also handles proto files outside the directories above.
while IFS= read -r -d '' proto; do
  relative="${proto#"${TMP_ROOT}/"}"
  for path in "${output_paths[@]}"; do
    if [[ "${relative}" == "${path}/"* ]]; then
      # Leave both loops for the next proto; its directory is compared above.
      continue 2
    fi
  done
  stem="${relative%.proto}"
  for path in "${stem}.pb.go" "${stem}_grpc.pb.go"; do
    if [[ -e "${REPO_ROOT}/${path}" || -e "${TMP_ROOT}/${path}" ]]; then
      compare_output "${path}"
    fi
  done
done < <(find "${TMP_ROOT}" \
  -path "${TMP_ROOT}/vendor" -prune -o \
  -path "${TMP_ROOT}/hack/tools/vendor" -prune -o \
  -path "${TMP_ROOT}/node_modules" -prune -o \
  -path "${TMP_ROOT}/_bin" -prune -o \
  -name '*.proto' -print0)

if [[ ${ret} -eq 0 ]]; then
  echo "${REPO_ROOT} up to date."
  exit 0
fi
echo "ERROR: out of date codegen files. Fix with make update-codegen" >&2
exit 1
