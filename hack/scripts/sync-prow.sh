#!/usr/bin/env bash

set -euo pipefail

check_deps() {
  if ! which git-filter-repo > /dev/null 2>&1; then
    echo "please install the git-filter-repo tool per https://github.com/newren/git-filter-repo"
    exit 1
  fi
}

check_deps

old_prow="$PWD/test-infra"
new_prow="$PWD/prow"

# Clone the old prow repo.
if [[ ! -d "$old_prow" ]]; then
    git clone https://github.com/kubernetes/test-infra.git
fi

# Strip down the master branch of $old_prow to only have commits that touch the the /prow directory.
pushd "$old_prow"
git-filter-repo --path prow
popd

# Clone the new prow repo (this should be your fork).
if [[ ! -d "$new_prow" ]]; then
    git clone https://github.com/kubernetes-sigs/prow.git
fi
cd "$new_prow"

# Check out a new "sync" branch that we want to create a PR from.
git branch sync origin/main
git checkout sync

# Import commits from old_prow/master into "sync" inside $new_prow.
git remote add old_prow "$old_prow"
git fetch old_prow
git merge old_prow/master --no-edit
