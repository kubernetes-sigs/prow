/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package git

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"strings"
	"time"
)

// Interactor knows how to operate on a git repository cloned from GitHub
// using a local cache.
type Interactor interface {
	// Directory exposes the directory in which the repository has been cloned
	Directory() string
	// Clean removes the repository. It is up to the user to call this once they are done
	Clean() error
	// ResetHard runs `git reset --hard`
	ResetHard(commitlike string) error
	// IsDirty checks whether the repo is dirty or not
	IsDirty() (bool, error)
	// Checkout runs `git checkout`
	Checkout(commitlike string) error
	// RevParse runs `git rev-parse`
	RevParse(commitlike string) (string, error)
	// RevParseN runs `git rev-parse`, but takes a slice of git revisions, and
	// returns a map of the git revisions as keys and the SHAs as values.
	RevParseN(rev []string) (map[string]string, error)
	// BranchExists determines if a branch with the name exists
	BranchExists(branch string) bool
	// ObjectExists determines if the Git object exists locally
	ObjectExists(sha string) (bool, error)
	// CheckoutNewBranch creates a new branch from HEAD and checks it out
	CheckoutNewBranch(branch string) error
	// Merge merges the commitlike into the current HEAD
	Merge(commitlike string) (bool, error)
	// MergeWithStrategy merges the commitlike into the current HEAD with the strategy
	MergeWithStrategy(commitlike, mergeStrategy string, opts ...MergeOpt) (bool, error)
	// MergeAndCheckout merges all commitlikes into the current HEAD with the appropriate strategy
	MergeAndCheckout(baseSHA string, mergeStrategy string, headSHAs ...string) error
	// Am calls `git am`
	Am(path string) error
	// Fetch calls `git fetch arg...`
	Fetch(arg ...string) error
	// FetchRef fetches the refspec
	FetchRef(refspec string) error
	// FetchFromRemote fetches the branch of the given remote
	FetchFromRemote(remote RemoteResolver, branch string) error
	// CheckoutPullRequest fetches and checks out the synthetic refspec from GitHub for a pull request HEAD
	CheckoutPullRequest(number int) error
	// Config runs `git config`
	Config(args ...string) error
	// Diff runs `git diff`
	Diff(head, sha string) (changes []string, err error)
	// MergeCommitsExistBetween determines if merge commits exist between target and HEAD
	MergeCommitsExistBetween(target, head string) (bool, error)
	// ShowRef returns the commit for a commitlike. Unlike rev-parse it does not require a checkout.
	ShowRef(commitlike string) (string, error)
}

// cacher knows how to cache and update repositories in a central cache
type cacher interface {
	// MirrorClone sets up a mirror of the source repository.
	MirrorClone() error
	// RemoteUpdate fetches all updates from the remote.
	RemoteUpdate() error
	// FetchCommits fetches only the given commits.
	FetchCommits([]string) error
	// RetargetBranch moves the given branch to an already-existing commit.
	RetargetBranch(string, string) error
}

// cloner knows how to clone repositories from a central cache
type cloner interface {
	// Clone clones the repository from a local path.
	Clone(from string) error
	CloneWithRepoOpts(from string, repoOpts RepoOpts) error
}

// MergeOpt holds options for git merge operations.
// Currently only commit message option is supported.
type MergeOpt struct {
	CommitMessage string
}

type interactor struct {
	executor executor
	remote   RemoteResolver
	dir      string
	logger   *logrus.Entry
}

// Directory exposes the directory in which this repository has been cloned
func (i *interactor) Directory() string {
	return i.dir
}

// Clean cleans up the repository from the on-disk cache
func (i *interactor) Clean() error {
	return os.RemoveAll(i.dir)
}

// ResetHard runs `git reset --hard`
func (i *interactor) ResetHard(commitlike string) error {
	// `git reset --hard` doesn't cleanup untracked file
	i.logger.Info("Clean untracked files and dirs.")
	if out, err := i.executor.Run("clean", "-df"); err != nil {
		return fmt.Errorf("error clean -df: %v. output: %s", err, string(out))
	}
	i.logger.WithField("commitlike", commitlike).Info("Reset hard.")
	if out, err := i.executor.Run("reset", "--hard", commitlike); err != nil {
		return fmt.Errorf("error reset hard %s: %v. output: %s", commitlike, err, string(out))
	}
	return nil
}

// IsDirty checks whether the repo is dirty or not
func (i *interactor) IsDirty() (bool, error) {
	i.logger.Info("Checking is dirty.")
	b, err := i.executor.Run("status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("error add -A: %v. output: %s", err, string(b))
	}
	return len(b) > 0, nil
}

// Clone clones the repository from a local path.
func (i *interactor) Clone(from string) error {
	return i.CloneWithRepoOpts(from, RepoOpts{})
}

// CloneWithRepoOpts clones the repository from a local path, but additionally
// use any repository options (RepoOpts) to customize the clone behavior.
func (i *interactor) CloneWithRepoOpts(from string, repoOpts RepoOpts) error {
	i.logger.Infof("Creating a clone of the repo at %s from %s", i.dir, from)
	cloneArgs := []string{"clone"}

	if repoOpts.ShareObjectsWithPrimaryClone {
		cloneArgs = append(cloneArgs, "--shared")
	}

	// Handle sparse checkouts.
	if repoOpts.SparseCheckoutDirs != nil {
		cloneArgs = append(cloneArgs, "--sparse")
	}

	cloneArgs = append(cloneArgs, from, i.dir)

	if out, err := i.executor.Run(cloneArgs...); err != nil {
		return fmt.Errorf("error creating a clone: %w %v", err, string(out))
	}

	// For sparse checkouts, we have to do some additional housekeeping after
	// the clone is completed. We use Git's global "-C <directory>" flag to
	// switch to that directory before running the "sparse-checkout" command,
	// because otherwise the command will fail (because it will try to run the
	// command in the $PWD, which is not the same as the just-created clone
	// directory (i.dir)).
	if repoOpts.SparseCheckoutDirs != nil {
		if len(repoOpts.SparseCheckoutDirs) == 0 {
			return nil
		}
		sparseCheckoutArgs := []string{"-C", i.dir, "sparse-checkout", "set"}
		sparseCheckoutArgs = append(sparseCheckoutArgs, repoOpts.SparseCheckoutDirs...)

		timeBeforeSparseCheckout := time.Now()
		if out, err := i.executor.Run(sparseCheckoutArgs...); err != nil {
			return fmt.Errorf("error setting it to a sparse checkout: %w %v", err, string(out))
		}
		gitMetrics.sparseCheckoutDuration.Observe(time.Since(timeBeforeSparseCheckout).Seconds())
	}
	return nil
}

// MirrorClone sets up a mirror of the source repository.
func (i *interactor) MirrorClone() error {
	i.logger.Infof("Creating a mirror of the repo at %s", i.dir)
	remote, err := i.remote()
	if err != nil {
		return fmt.Errorf("could not resolve remote for cloning: %w", err)
	}
	if out, err := i.executor.Run("clone", "--mirror", remote, i.dir); err != nil {
		return fmt.Errorf("error creating a mirror clone: %w %v", err, string(out))
	}
	return nil
}

// Checkout runs git checkout.
func (i *interactor) Checkout(commitlike string) error {
	i.logger.Infof("Checking out %q", commitlike)
	if out, err := i.executor.Run("checkout", commitlike); err != nil {
		return fmt.Errorf("error checking out %q: %w %v", commitlike, err, string(out))
	}
	return nil
}

// RevParse runs git rev-parse.
func (i *interactor) RevParse(commitlike string) (string, error) {
	i.logger.Infof("Parsing revision %q", commitlike)
	out, err := i.executor.Run("rev-parse", commitlike)
	if err != nil {
		return "", fmt.Errorf("error parsing %q: %w %v", commitlike, err, string(out))
	}
	return string(out), nil
}

func (i *interactor) RevParseN(revs []string) (map[string]string, error) {
	if len(revs) == 0 {
		return nil, errors.New("input revs must have at least 1 element")
	}

	i.logger.Infof("Parsing revisions %q", revs)

	arg := append([]string{"rev-parse"}, revs...)

	out, err := i.executor.Run(arg...)
	if err != nil {
		return nil, fmt.Errorf("error parsing %q: %w %v", revs, err, string(out))
	}

	ret := make(map[string]string)
	got := strings.Split(string(out), "\n")

	// We expect the length to be at least 2. This is because if we have the
	// minimal number of elements (just 1), "got" should look like ["abcdef...",
	// "\n"] because the trailing newline should be its own element.
	if len(got) < 2 {
		return nil, fmt.Errorf("expected parsed output to be at least 2 elements, got %d", len(got))
	}
	got = got[:len(got)-1] // Drop last element "\n".

	for i, sha := range got {
		ret[revs[i]] = sha
	}

	return ret, nil
}

// BranchExists returns true if branch exists in heads.
func (i *interactor) BranchExists(branch string) bool {
	i.logger.Infof("Checking if branch %q exists", branch)
	_, err := i.executor.Run("ls-remote", "--exit-code", "--heads", "origin", branch)
	return err == nil
}

func (i *interactor) ObjectExists(sha string) (bool, error) {
	i.logger.WithField("SHA", sha).Info("Checking if Git object exists")
	output, err := i.executor.Run("cat-file", "-e", sha)
	// If the object does not exist, cat-file will exit with a non-zero exit
	// code. This will make err non-nil. However this is a known behavior, so
	// we just log it.
	//
	// We still have the error type as a return value because the v1 git client
	// adapter needs to know that this operation is not supported there.
	if err != nil {
		i.logger.WithError(err).WithField("SHA", sha).Debugf("error from 'git cat-file -e': %s", string(output))
		return false, nil
	}
	return true, nil
}

// CheckoutNewBranch creates a new branch and checks it out.
func (i *interactor) CheckoutNewBranch(branch string) error {
	i.logger.Infof("Checking out new branch %q", branch)
	if out, err := i.executor.Run("checkout", "-b", branch); err != nil {
		return fmt.Errorf("error checking out new branch %q: %w %v", branch, err, string(out))
	}
	return nil
}

// Merge attempts to merge commitlike into the current branch. It returns true
// if the merge completes. It returns an error if the abort fails.
func (i *interactor) Merge(commitlike string) (bool, error) {
	return i.MergeWithStrategy(commitlike, "merge")
}

// MergeWithStrategy attempts to merge commitlike into the current branch given the merge strategy.
// It returns true if the merge completes. if the merge does not complete successfully, we try to
// abort it and return an error if the abort fails.
func (i *interactor) MergeWithStrategy(commitlike, mergeStrategy string, opts ...MergeOpt) (bool, error) {
	i.logger.Infof("Merging %q using the %q strategy", commitlike, mergeStrategy)
	switch mergeStrategy {
	case "merge":
		return i.mergeMerge(commitlike, opts...)
	case "squash":
		return i.squashMerge(commitlike)
	case "rebase":
		return i.mergeRebase(commitlike)
	case "ifNecessary":
		return i.mergeIfNecessary(commitlike, opts...)
	default:
		return false, fmt.Errorf("merge strategy %q is not supported", mergeStrategy)
	}
}

func (i *interactor) mergeHelper(args []string, commitlike string, opts ...MergeOpt) (bool, error) {
	if len(opts) == 0 {
		args = append(args, []string{"-m", "merge"}...)
	} else {
		for _, opt := range opts {
			args = append(args, []string{"-m", opt.CommitMessage}...)
		}
	}

	args = append(args, commitlike)

	out, err := i.executor.Run(args...)
	if err == nil {
		return true, nil
	}
	i.logger.WithError(err).Infof("Error merging %q: %s", commitlike, string(out))
	if out, err := i.executor.Run("merge", "--abort"); err != nil {
		return false, fmt.Errorf("error aborting merge of %q: %w %v", commitlike, err, string(out))
	}
	return false, nil
}

func (i *interactor) mergeMerge(commitlike string, opts ...MergeOpt) (bool, error) {
	args := []string{"merge", "--no-ff", "--no-stat"}
	return i.mergeHelper(args, commitlike, opts...)
}

func (i *interactor) mergeIfNecessary(commitlike string, opts ...MergeOpt) (bool, error) {
	args := []string{"merge", "--ff", "--no-stat"}
	return i.mergeHelper(args, commitlike, opts...)
}

func (i *interactor) squashMerge(commitlike string) (bool, error) {
	out, err := i.executor.Run("merge", "--squash", "--no-stat", commitlike)
	if err != nil {
		i.logger.WithError(err).Warnf("Error staging merge for %q: %s", commitlike, string(out))
		if out, err := i.executor.Run("reset", "--hard", "HEAD"); err != nil {
			return false, fmt.Errorf("error aborting merge of %q: %w %v", commitlike, err, string(out))
		}
		return false, nil
	}
	out, err = i.executor.Run("commit", "--no-stat", "-m", "merge")
	if err != nil {
		i.logger.WithError(err).Warnf("Error committing merge for %q: %s", commitlike, string(out))
		if out, err := i.executor.Run("reset", "--hard", "HEAD"); err != nil {
			return false, fmt.Errorf("error aborting merge of %q: %w %v", commitlike, err, string(out))
		}
		return false, nil
	}
	return true, nil
}

func (i *interactor) mergeRebase(commitlike string) (bool, error) {
	if commitlike == "" {
		return false, errors.New("branch must be set")
	}

	headRev, err := i.revParse("HEAD")
	if err != nil {
		i.logger.WithError(err).Infof("Failed to parse HEAD revision")
		return false, err
	}
	headRev = strings.TrimSuffix(headRev, "\n")

	b, err := i.executor.Run("rebase", "--no-stat", headRev, commitlike)
	if err != nil {
		i.logger.WithField("out", string(b)).WithError(err).Infof("Rebase failed.")
		if b, err := i.executor.Run("rebase", "--abort"); err != nil {
			return false, fmt.Errorf("error aborting after failed rebase for commitlike %s: %v. output: %s", commitlike, err, string(b))
		}
		return false, nil
	}
	return true, nil
}

func (i *interactor) revParse(args ...string) (string, error) {
	fullArgs := append([]string{"rev-parse"}, args...)
	b, err := i.executor.Run(fullArgs...)
	if err != nil {
		return "", errors.New(string(b))
	}
	return string(b), nil
}

// Only the `merge` and `squash` strategies are supported.
func (i *interactor) MergeAndCheckout(baseSHA string, mergeStrategy string, headSHAs ...string) error {
	if baseSHA == "" {
		return errors.New("baseSHA must be set")
	}
	if err := i.Checkout(baseSHA); err != nil {
		return err
	}
	for _, headSHA := range headSHAs {
		ok, err := i.MergeWithStrategy(headSHA, mergeStrategy)
		if err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("failed to merge %q", headSHA)
		}
	}
	return nil
}

// Am tries to apply the patch in the given path into the current branch
// by performing a three-way merge (similar to git cherry-pick). It returns
// an error if the patch cannot be applied.
func (i *interactor) Am(path string) error {
	i.logger.Infof("Applying patch at %s", path)
	out, err := i.executor.Run("am", "--3way", path)
	if err == nil {
		return nil
	}
	i.logger.WithError(err).Infof("Patch apply failed with output: %s", string(out))
	if abortOut, abortErr := i.executor.Run("am", "--abort"); err != nil {
		i.logger.WithError(abortErr).Warningf("Aborting patch apply failed with output: %s", string(abortOut))
	}
	return errors.New(string(bytes.TrimPrefix(out, []byte("The copy of the patch that failed is found in: .git/rebase-apply/patch"))))
}

// FetchCommits only fetches those commits which we want, and only if they are
// missing.
func (i *interactor) FetchCommits(commitSHAs []string) error {
	fetchArgs := []string{"--no-write-fetch-head", "--no-tags"}

	// For each commit SHA, check if it already exists. If so, don't bother
	// fetching it.
	var missingCommits bool
	for _, commitSHA := range commitSHAs {
		if exists, _ := i.ObjectExists(commitSHA); exists {
			continue
		}

		fetchArgs = append(fetchArgs, commitSHA)
		missingCommits = true
	}

	// Skip the fetch operation altogether if nothing is missing (we already
	// fetched everything previously at some point).
	if !missingCommits {
		return nil
	}

	if err := i.Fetch(fetchArgs...); err != nil {
		return fmt.Errorf("failed to fetch %s: %v", fetchArgs, err)
	}

	return nil
}

// RetargetBranch moves the given branch to an already-existing commit.
func (i *interactor) RetargetBranch(branch, sha string) error {
	args := []string{"branch", "-f", branch, sha}
	if out, err := i.executor.Run(args...); err != nil {
		return fmt.Errorf("error retargeting branch: %w %v", err, string(out))
	}

	return nil
}

// RemoteUpdate fetches all updates from the remote.
func (i *interactor) RemoteUpdate() error {
	// We might need to refresh the token for accessing remotes in case of GitHub App auth (ghs tokens are only valid for
	// 1 hour, see https://github.com/kubernetes/test-infra/issues/31182).
	// Therefore, we resolve the remote again and update the clone's remote URL with a fresh token.
	remote, err := i.remote()
	if err != nil {
		return fmt.Errorf("could not resolve remote for updating: %w", err)
	}

	i.logger.Info("Setting remote URL")
	if out, err := i.executor.Run("remote", "set-url", "origin", remote); err != nil {
		return fmt.Errorf("error setting remote URL: %w %v", err, string(out))
	}

	i.logger.Info("Updating from remote")
	if out, err := i.executor.Run("remote", "update", "--prune"); err != nil {
		return fmt.Errorf("error updating: %w %v", err, string(out))
	}
	return nil
}

// Fetch fetches all updates from the remote.
func (i *interactor) Fetch(arg ...string) error {
	remote, err := i.remote()
	if err != nil {
		return fmt.Errorf("could not resolve remote for fetching: %w", err)
	}
	arg = append([]string{"fetch", remote}, arg...)
	i.logger.Infof("Fetching from %s", remote)
	if out, err := i.executor.Run(arg...); err != nil {
		return fmt.Errorf("error fetching: %w %v", err, string(out))
	}
	return nil
}

// FetchRef fetches a refspec from the remote and leaves it as FETCH_HEAD.
func (i *interactor) FetchRef(refspec string) error {
	remote, err := i.remote()
	if err != nil {
		return fmt.Errorf("could not resolve remote for fetching: %w", err)
	}
	i.logger.Infof("Fetching %q from %s", refspec, remote)
	if out, err := i.executor.Run("fetch", remote, refspec); err != nil {
		return fmt.Errorf("error fetching %q: %w %v", refspec, err, string(out))
	}
	return nil
}

// FetchFromRemote fetches all update from a specific remote and branch and leaves it as FETCH_HEAD.
func (i *interactor) FetchFromRemote(remote RemoteResolver, branch string) error {
	r, err := remote()
	if err != nil {
		return fmt.Errorf("couldn't get remote: %w", err)
	}

	i.logger.Infof("Fetching %s from %s", branch, r)
	if out, err := i.executor.Run("fetch", r, branch); err != nil {
		return fmt.Errorf("error fetching %s from %s: %w %v", branch, r, err, string(out))
	}
	return nil
}

// CheckoutPullRequest fetches the HEAD of a pull request using a synthetic refspec
// available on GitHub remotes and creates a branch at that commit.
func (i *interactor) CheckoutPullRequest(number int) error {
	i.logger.Infof("Checking out pull request %d", number)
	if err := i.FetchRef(fmt.Sprintf("pull/%d/head", number)); err != nil {
		return err
	}
	if err := i.Checkout("FETCH_HEAD"); err != nil {
		return err
	}
	if err := i.CheckoutNewBranch(fmt.Sprintf("pull%d", number)); err != nil {
		return err
	}
	return nil
}

// Config runs git config.
func (i *interactor) Config(args ...string) error {
	i.logger.WithField("args", args).Info("Configuring.")
	if out, err := i.executor.Run(append([]string{"config"}, args...)...); err != nil {
		return fmt.Errorf("error configuring %v: %w %v", args, err, string(out))
	}
	return nil
}

// Diff lists the difference between the two references, returning the output
// line by line.
func (i *interactor) Diff(head, sha string) ([]string, error) {
	i.logger.Infof("Finding the differences between %q and %q", head, sha)
	out, err := i.executor.Run("diff", head, sha, "--name-only")
	if err != nil {
		return nil, err
	}
	var changes []string
	scan := bufio.NewScanner(bytes.NewReader(out))
	scan.Split(bufio.ScanLines)
	for scan.Scan() {
		changes = append(changes, scan.Text())
	}
	return changes, nil
}

// MergeCommitsExistBetween runs 'git log <target>..<head> --merged' to verify
// if merge commits exist between "target" and "head".
func (i *interactor) MergeCommitsExistBetween(target, head string) (bool, error) {
	i.logger.Infof("Determining if merge commits exist between %q and %q", target, head)
	out, err := i.executor.Run("log", fmt.Sprintf("%s..%s", target, head), "--oneline", "--merges")
	if err != nil {
		return false, fmt.Errorf("error verifying if merge commits exist between %q and %q: %v %s", target, head, err, string(out))
	}
	return len(out) != 0, nil
}

func (i *interactor) ShowRef(commitlike string) (string, error) {
	i.logger.Infof("Getting the commit sha for commitlike %s", commitlike)
	out, err := i.executor.Run("show-ref", "-s", commitlike)
	if err != nil {
		return "", fmt.Errorf("failed to get commit sha for commitlike %s: %w", commitlike, err)
	}
	return strings.TrimSpace(string(out)), nil
}
