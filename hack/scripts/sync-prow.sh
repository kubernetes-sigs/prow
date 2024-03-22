#!/usr/bin/env bash

set -euo pipefail

check_deps() {
  if ! command -v git-filter-repo &>/dev/null; then
    echo "please install the git-filter-repo tool per https://github.com/newren/git-filter-repo"
    exit 1
  fi

  if [[ -z "${1}" ]]; then
    echo "please specify your github username as a positional parameter."
    echo "${1}"
    exit 1
  fi
}

# $1 must be your GitHub username.
check_deps "${1:-}"

gh_username="${1}"
old_prow="$PWD/old_prow"
new_prow="$PWD/prow"

rm -rf $old_prow $new_prow
# Clone the old prow repo.
git clone https://github.com/kubernetes/test-infra.git $old_prow || true

# Clone the new prow repo (this should be your fork).
git clone "https://github.com/${gh_username}/prow.git" $new_prow || true

# These are the --path arguments that define the set of paths we want to migrate:
path_args="
  --path prow \
  --path ghproxy \
  --path pkg/flagutil \
  --path pkg/genyaml \
  --path hack/prowimagebuilder \
  --path hack/ts-rollup
"

# Strip down the master branch of $old_prow to only have commits that touch the prow related directories.
cd "$old_prow"
git-filter-repo \
  ${path_args}

cd "$new_prow"
# Check out a new "sync" branch that we want to create a PR from.
git branch sync origin/main
git checkout sync

# Remove any existing history for the directories we care about to avoid duplicate commits.
git-filter-repo \
  --invert-paths \
  --force \
  ${path_args}

# Import commits from old_prow/master into "sync" inside $new_prow.
git remote add old_prow "$old_prow"
git fetch old_prow
git merge old_prow/master --no-edit --allow-unrelated-histories --strategy-option theirs

# Rename go module paths
find . -type f -exec sed -i 's,sigs.k8s.io/prow,sigs.k8s.io/prow,g' {} \;
git commit -a -m "Rename sigs.k8s.io/prow module to sigs.k8s.io/prow."

echo "Sync completed successfully! 'cd ${new_prow} && git push origin' when you're ready."
