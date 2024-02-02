#!/usr/bin/env bash

prow="$PWD/prow"
cd "$prow" || exit

# replace go module name
find . -type f -name "*.go" -exec sed -i.bak 's/k8s.io\/test-infra/sigs.k8s.io\/prow/g' {} \;
find . -type f -name "*.go.bak" -delete # Remove backup files

# restore imports using packages from the test-infra repo
strings=(
    "sigs.k8s.io/prow/ghproxy/ghcache"
    "sigs.k8s.io/prow/pkg/genyaml"
    "sigs.k8s.io/prow/pkg/flagutil"
    "sigs.k8s.io/prow/greenhouse/diskutil"
    "sigs.k8s.io/prow/robots/pr-creator/updater"
    "sigs.k8s.io/prow/maintenance/migratestatus/migrator"
    "sigs.k8s.io/prow/experiment/clustersecretbackup/secretmanager"
    "sigs.k8s.io/prow/experiment/image-bumper/bumper"
)

targets=(
    "k8s.io/test-infra/ghproxy/ghcache"
    "k8s.io/test-infra/pkg/genyaml"
    "k8s.io/test-infra/pkg/flagutil"
    "k8s.io/test-infra/greenhouse/diskutil"
    "k8s.io/test-infra/robots/pr-creator/updater"
    "k8s.io/test-infra/maintenance/migratestatus/migrator"
    "k8s.io/test-infra/experiment/clustersecretbackup/secretmanager"
    "k8s.io/test-infra/experiment/image-bumper/bumper"
)

for ((i = 0; i < ${#strings[@]}; i++)); do
    escaped_string=$(printf '%s\n' "${strings[i]}" | sed 's/[[\.*^$/]/\\&/g')
    escaped_target=$(printf '%s\n' "${targets[i]}" | sed 's/[[\.*^$/]/\\&/g')
    find . -type f -name "*.go" -exec sed -i.bak "s/$escaped_string/$escaped_target/g" {} \;
    find . -type f -name "*.go.bak" -delete # Remove backup files
done
