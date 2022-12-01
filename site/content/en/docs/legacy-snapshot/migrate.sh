#!/usr/bin/env bash
# Copyright 2022 The Kubernetes Authors.
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

# Import Markdown files from kubernetes/test-infra to kubernetes-sigs/prow.

set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "$0")" && pwd)"

declare -a FILES_TO_MIGRATE=()

# Find all files to migrate over. These are all *.md files under the "prow/"
# directory in kubernetes/test-infra.
function identify_files_to_migrate() {
    local test_infra_path
    test_infra_path="${1}"

    >/dev/null pushd "${test_infra_path}"
    find prow -type f -name \*.md | sort
    >/dev/null popd
}

function migrate() {
    local test_infra_path
    test_infra_path="${1}"

    for file in "${FILES_TO_MIGRATE[@]}"; do
        # Create child (nested) directories if necessary.
        dir="${file#"${test_infra_path}"/prow/}"
        file_dest="${dir}"
        if [[ "${dir}" =~ / ]]; then
            dir="${dir%/*}"
            mkdir -p "${dir}"
            file_dest="${dir}/${file##*/}"
        fi

        # Add Hugo front matter for each file.
        cat <<EOF > "${file_dest}"
---
title: "${file_dest}"
---

EOF
        # Append file contents.
        cat "${test_infra_path}/${file}" >> "${file_dest}"
    done

    echo "Finished migrating files."
}

function main() {
    if [[ -z "${1:-}" ]]; then
        echo >&2 "Usage: $0 [kubernetes/test-infra directory]"
    fi

    mapfile -t FILES_TO_MIGRATE < <(identify_files_to_migrate "${1}")
    migrate "${1}"
}

main "$@"
