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

# All files to migrate over. These are all *.md files under the "prow/"
# directory in kubernetes/test-infra, except for prow/README.md.
declare -ra FILES_TO_MIGRATE=(
    prow/ANNOUNCEMENTS.md
    prow/build_test_update.md
    prow/cmd/branchprotector/README.md
    prow/cmd/checkconfig/README.md
    prow/cmd/clonerefs/README.md
    prow/cmd/cm2kc/README.md
    prow/cmd/config-bootstrapper/README.md
    prow/cmd/crier/README.md
    prow/cmd/deck/README.md
    prow/cmd/deck/csrf.md
    prow/cmd/deck/github_oauth_setup.md
    prow/cmd/entrypoint/README.md
    prow/cmd/exporter/README.md
    prow/cmd/gcsupload/README.md
    prow/cmd/generic-autobumper/README.md
    prow/cmd/gerrit/README.md
    prow/cmd/hmac/README.md
    prow/cmd/initupload/README.md
    prow/cmd/invitations-accepter/README.md
    prow/cmd/jenkins-operator/README.md
    prow/cmd/peribolos/README.md
    prow/cmd/phaino/README.md
    prow/cmd/phony/README.md
    prow/cmd/prow-controller-manager/README.md
    prow/cmd/sidecar/README.md
    prow/cmd/status-reconciler/README.md
    prow/cmd/sub/README.md
    prow/cmd/tide/README.md
    prow/cmd/tide/config.md
    prow/cmd/tide/maintainers.md
    prow/cmd/tide/pr-authors.md
    prow/cmd/tot/fallbackcheck/README.md
    prow/config/README.md
    prow/external-plugins/cherrypicker/README.md
    prow/gerrit/README.md
    prow/getting_started_deploy.md
    prow/getting_started_develop.md
    prow/github/README.md
    prow/inrepoconfig.md
    prow/jobs.md
    prow/life_of_a_prow_job.md
    prow/metadata_artifacts.md
    prow/metrics/README.md
    prow/more_prow.md
    prow/plank/README.md
    prow/plugins/README.md
    prow/plugins/approve/approvers/README.md
    prow/plugins/branchcleaner/README.md
    prow/plugins/lgtm/README.md
    prow/plugins/updateconfig/README.md
    prow/pod-utilities.md
    prow/private_deck.md
    prow/prow_secrets.md
    prow/scaling.md
    prow/spyglass/README.md
    prow/spyglass/architecture.md
    prow/spyglass/lenses/restcoverage/README.md
    prow/spyglass/write-a-lens.md
    prow/test/integration/README.md
    prow/test/integration/cmd/fakegitserver/README.md
)

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

    migrate "${1}"
}

main "$@"
