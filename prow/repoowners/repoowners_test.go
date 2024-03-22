/*
Copyright 2017 The Kubernetes Authors.

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

package repoowners

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	prowConf "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git/localgit"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins/ownersconfig"
)

var (
	defaultBranch = "master" // TODO(fejta): localgit.DefaultBranch()
	testFiles     = map[string][]byte{
		"foo": []byte(`approvers:
- bob`),
		"OWNERS": []byte(`approvers:
- cjwagner
reviewers:
- Alice
- bob
required_reviewers:
- chris
labels:
- EVERYTHING`),
		"src/OWNERS": []byte(`approvers:
- Best-Approvers`),
		"src/dir/OWNERS": []byte(`approvers:
- bob
reviewers:
- alice
- "@CJWagner"
- jakub
required_reviewers:
- ben
labels:
- src-code`),
		"src/dir/subdir/OWNERS": []byte(`approvers:
- bob
- alice
reviewers:
- bob
- alice`),
		"src/dir/conformance/OWNERS": []byte(`options:
  no_parent_owners: true
  auto_approve_unowned_subfolders: true
approvers:
- mml`),
		"docs/file.md": []byte(`---
approvers:
- ALICE

labels:
- docs
---`),
		"vendor/OWNERS": []byte(`approvers:
- alice`),
		"vendor/k8s.io/client-go/OWNERS": []byte(`approvers:
- bob`),
	}

	testFilesRe = map[string][]byte{
		// regexp filtered
		"re/OWNERS": []byte(`filters:
  ".*":
    labels:
    - re/all
  "\\.go$":
    labels:
    - re/go`),
		"re/a/OWNERS": []byte(`filters:
  "\\.md$":
    labels:
    - re/md-in-a
  "\\.go$":
    labels:
    - re/go-in-a`),
	}
)

// regexpAll is used to construct a default {regexp -> values} mapping for ".*"
func regexpAll(values ...string) map[*regexp.Regexp]sets.Set[string] {
	return map[*regexp.Regexp]sets.Set[string]{nil: sets.New[string](values...)}
}

// patternAll is used to construct a default {regexp string -> values} mapping for ".*"
func patternAll(values ...string) map[string]sets.Set[string] {
	// use "" to represent nil and distinguish it from a ".*" regexp (which shouldn't exist).
	return map[string]sets.Set[string]{"": sets.New[string](values...)}
}

type cacheOptions struct {
	hasAliases bool

	mdYaml                   bool
	commonFileChanged        bool
	mdFileChanged            bool
	ownersAliasesFileChanged bool
	ownersFileChanged        bool
}

type fakeGitHubClient struct {
	Collaborators []string
	ref           string
}

func (f *fakeGitHubClient) ListCollaborators(org, repo string) ([]github.User, error) {
	result := make([]github.User, 0, len(f.Collaborators))
	for _, login := range f.Collaborators {
		result = append(result, github.User{Login: login})
	}
	return result, nil
}

func (f *fakeGitHubClient) GetRef(org, repo, ref string) (string, error) {
	return f.ref, nil
}

func getTestClient(
	files map[string][]byte,
	enableMdYaml,
	skipCollab,
	includeAliases bool,
	ignorePreconfiguredDefaults bool,
	ownersDirDenylistDefault []string,
	ownersDirDenylistByRepo map[string][]string,
	extraBranchesAndFiles map[string]map[string][]byte,
	cacheOptions *cacheOptions,
	clients localgit.Clients,
) (*Client, func(), error) {
	testAliasesFile := map[string][]byte{
		"OWNERS_ALIASES": []byte("aliases:\n  Best-approvers:\n  - carl\n  - cjwagner\n  best-reviewers:\n  - Carl\n  - BOB"),
	}

	localGit, git, err := clients()
	if err != nil {
		return nil, nil, err
	}

	if localgit.DefaultBranch("") != defaultBranch {
		localGit.InitialBranch = defaultBranch
	}

	if err := localGit.MakeFakeRepo("org", "repo"); err != nil {
		return nil, nil, fmt.Errorf("cannot make fake repo: %w", err)
	}

	if err := localGit.AddCommit("org", "repo", files); err != nil {
		return nil, nil, fmt.Errorf("cannot add initial commit: %w", err)
	}
	if includeAliases {
		if err := localGit.AddCommit("org", "repo", testAliasesFile); err != nil {
			return nil, nil, fmt.Errorf("cannot add OWNERS_ALIASES commit: %w", err)
		}
	}
	if len(extraBranchesAndFiles) > 0 {
		for branch, extraFiles := range extraBranchesAndFiles {
			if err := localGit.CheckoutNewBranch("org", "repo", branch); err != nil {
				return nil, nil, err
			}
			if len(extraFiles) > 0 {
				if err := localGit.AddCommit("org", "repo", extraFiles); err != nil {
					return nil, nil, fmt.Errorf("cannot add commit: %w", err)
				}
			}
		}
		if err := localGit.Checkout("org", "repo", defaultBranch); err != nil {
			return nil, nil, err
		}
	}
	cache := newCache()
	if cacheOptions != nil {
		var entry cacheEntry
		entry.sha, err = localGit.RevParse("org", "repo", "HEAD")
		if err != nil {
			return nil, nil, fmt.Errorf("cannot get commit SHA: %w", err)
		}
		if cacheOptions.hasAliases {
			entry.aliases = make(map[string]sets.Set[string])
		}
		entry.owners = &RepoOwners{
			enableMDYAML: cacheOptions.mdYaml,
		}
		if cacheOptions.commonFileChanged {
			md := map[string][]byte{"common": []byte(`---
This file could be anything
---`)}
			if err := localGit.AddCommit("org", "repo", md); err != nil {
				return nil, nil, fmt.Errorf("cannot add commit: %w", err)
			}
		}
		if cacheOptions.mdFileChanged {
			md := map[string][]byte{"docs/file.md": []byte(`---
approvers:
- ALICE


labels:
- docs
---`)}
			if err := localGit.AddCommit("org", "repo", md); err != nil {
				return nil, nil, fmt.Errorf("cannot add commit: %w", err)
			}
		}
		if cacheOptions.ownersAliasesFileChanged {
			testAliasesFile = map[string][]byte{
				"OWNERS_ALIASES": []byte("aliases:\n  Best-approvers:\n\n  - carl\n  - cjwagner\n  best-reviewers:\n  - Carl\n  - BOB"),
			}
			if err := localGit.AddCommit("org", "repo", testAliasesFile); err != nil {
				return nil, nil, fmt.Errorf("cannot add commit: %w", err)
			}
		}
		if cacheOptions.ownersFileChanged {
			owners := map[string][]byte{
				"OWNERS": []byte(`approvers:
- cjwagner
reviewers:
- "@Alice"
- bob

required_reviewers:
- chris
labels:
- EVERYTHING`),
			}
			if err := localGit.AddCommit("org", "repo", owners); err != nil {
				return nil, nil, fmt.Errorf("cannot add commit: %w", err)
			}
		}
		cache.data["org"+"/"+"repo:master"] = entry
		// mark this entry is cache
		entry.owners.baseDir = "cache"
	}
	ghc := &fakeGitHubClient{Collaborators: []string{"cjwagner", "k8s-ci-robot", "alice", "bob", "carl", "mml", "maggie"}}
	ghc.ref, err = localGit.RevParse("org", "repo", "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("cannot get commit SHA: %w", err)
	}
	return &Client{
			logger: logrus.WithField("client", "repoowners"),
			ghc:    ghc,
			delegate: &delegate{
				git:   git,
				cache: cache,

				mdYAMLEnabled: func(org, repo string) bool {
					return enableMdYaml
				},
				skipCollaborators: func(org, repo string) bool {
					return skipCollab
				},
				ownersDirDenylist: func() *prowConf.OwnersDirDenylist {
					return &prowConf.OwnersDirDenylist{
						Repos:                       ownersDirDenylistByRepo,
						Default:                     ownersDirDenylistDefault,
						IgnorePreconfiguredDefaults: ignorePreconfiguredDefaults,
					}
				},
				filenames: ownersconfig.FakeResolver,
			},
		},
		// Clean up function
		func() {
			git.Clean()
			localGit.Clean()
		},
		nil
}

func TestOwnersDirDenylistV2(t *testing.T) {
	testOwnersDirDenylist(localgit.NewV2, t)
}

func testOwnersDirDenylist(clients localgit.Clients, t *testing.T) {
	getRepoOwnersWithDenylist := func(t *testing.T, defaults []string, byRepo map[string][]string, ignorePreconfiguredDefaults bool) *RepoOwners {
		client, cleanup, err := getTestClient(testFiles, true, false, true, ignorePreconfiguredDefaults, defaults, byRepo, nil, nil, clients)
		if err != nil {
			t.Fatalf("Error creating test client: %v.", err)
		}
		defer cleanup()

		ro, err := client.LoadRepoOwners("org", "repo", defaultBranch)
		if err != nil {
			t.Fatalf("Unexpected error loading RepoOwners: %v.", err)
		}

		return ro.(*RepoOwners)
	}

	type testConf struct {
		denylistDefault             []string
		denylistByRepo              map[string][]string
		ignorePreconfiguredDefaults bool
		includeDirs                 []string
		excludeDirs                 []string
	}

	tests := map[string]testConf{}

	tests["denylist by org"] = testConf{
		denylistByRepo: map[string][]string{
			"org": {"src"},
		},
		includeDirs: []string{""},
		excludeDirs: []string{"src", "src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["denylist by org/repo"] = testConf{
		denylistByRepo: map[string][]string{
			"org/repo": {"src"},
		},
		includeDirs: []string{""},
		excludeDirs: []string{"src", "src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["denylist by default"] = testConf{
		denylistDefault: []string{"src"},
		includeDirs:     []string{""},
		excludeDirs:     []string{"src", "src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["subdir denylist"] = testConf{
		denylistDefault: []string{"dir"},
		includeDirs:     []string{"", "src"},
		excludeDirs:     []string{"src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["no denylist setup"] = testConf{
		includeDirs: []string{"", "src", "src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["denylist setup but not matching this repo"] = testConf{
		denylistByRepo: map[string][]string{
			"not_org/not_repo": {"src"},
			"not_org":          {"src"},
		},
		includeDirs: []string{"", "src", "src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["non-matching denylist"] = testConf{
		denylistDefault: []string{"sr$"},
		includeDirs:     []string{"", "src", "src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["path denylist"] = testConf{
		denylistDefault: []string{"src/dir"},
		includeDirs:     []string{"", "src"},
		excludeDirs:     []string{"src/dir", "src/dir/conformance", "src/dir/subdir"},
	}
	tests["regexp denylist path"] = testConf{
		denylistDefault: []string{"src/dir/."},
		includeDirs:     []string{"", "src", "src/dir"},
		excludeDirs:     []string{"src/dir/conformance", "src/dir/subdir"},
	}
	tests["path substring"] = testConf{
		denylistDefault: []string{"/c"},
		includeDirs:     []string{"", "src", "src/dir", "src/dir/subdir"},
		excludeDirs:     []string{"src/dir/conformance"},
	}
	tests["exclude preconfigured defaults"] = testConf{
		includeDirs: []string{"", "src", "src/dir", "src/dir/subdir", "vendor"},
		excludeDirs: []string{"vendor/k8s.io/client-go"},
	}
	tests["ignore preconfigured defaults"] = testConf{
		includeDirs:                 []string{"", "src", "src/dir", "src/dir/subdir", "vendor", "vendor/k8s.io/client-go"},
		ignorePreconfiguredDefaults: true,
	}

	for name, conf := range tests {
		t.Run(name, func(t *testing.T) {
			ro := getRepoOwnersWithDenylist(t, conf.denylistDefault, conf.denylistByRepo, conf.ignorePreconfiguredDefaults)

			includeDirs := sets.New[string](conf.includeDirs...)
			excludeDirs := sets.New[string](conf.excludeDirs...)
			for dir := range ro.approvers {
				if excludeDirs.Has(dir) {
					t.Errorf("Expected directory %s to be excluded from the approvers map", dir)
				}
				includeDirs.Delete(dir)
			}
			for dir := range ro.reviewers {
				if excludeDirs.Has(dir) {
					t.Errorf("Expected directory %s to be excluded from the reviewers map", dir)
				}
				includeDirs.Delete(dir)
			}

			for _, dir := range sets.List(includeDirs) {
				t.Errorf("Expected to find approvers or reviewers for directory %s", dir)
			}
		})
	}
}

func TestOwnersRegexpFilteringV2(t *testing.T) {
	testOwnersRegexpFiltering(localgit.NewV2, t)
}

func testOwnersRegexpFiltering(clients localgit.Clients, t *testing.T) {
	tests := map[string]sets.Set[string]{
		"re/a/go.go":   sets.New[string]("re/all", "re/go", "re/go-in-a"),
		"re/a/md.md":   sets.New[string]("re/all", "re/md-in-a"),
		"re/a/txt.txt": sets.New[string]("re/all"),
		"re/go.go":     sets.New[string]("re/all", "re/go"),
		"re/txt.txt":   sets.New[string]("re/all"),
		"re/b/md.md":   sets.New[string]("re/all"),
	}

	client, cleanup, err := getTestClient(testFilesRe, true, false, true, false, nil, nil, nil, nil, clients)
	if err != nil {
		t.Fatalf("Error creating test client: %v.", err)
	}
	defer cleanup()

	r, err := client.LoadRepoOwners("org", "repo", defaultBranch)
	if err != nil {
		t.Fatalf("Unexpected error loading RepoOwners: %v.", err)
	}
	ro := r.(*RepoOwners)
	t.Logf("labels: %#v\n\n", ro.labels)
	for file, expected := range tests {
		if got := ro.FindLabelsForFile(file); !got.Equal(expected) {
			t.Errorf("For file %q expected labels %q, but got %q.", file, sets.List(expected), sets.List(got))
		}
	}
}

func strP(str string) *string {
	return &str
}

func TestLoadRepoOwnersV2(t *testing.T) {
	testLoadRepoOwners(localgit.NewV2, t)
}

func testLoadRepoOwners(clients localgit.Clients, t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		mdEnabled         bool
		aliasesFileExists bool
		skipCollaborators bool
		// used for testing OWNERS from a branch different from master
		branch                *string
		extraBranchesAndFiles map[string]map[string][]byte

		expectedApprovers, expectedReviewers, expectedRequiredReviewers, expectedLabels map[string]map[string]sets.Set[string]

		expectedOptions  map[string]dirOptions
		cacheOptions     *cacheOptions
		expectedReusable bool
	}{
		{
			name: "no alias, no md",
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll(),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":        patternAll("EVERYTHING"),
				"src/dir": patternAll("src-code"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
		},
		{
			name:              "alias, no md",
			aliasesFileExists: true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("carl", "cjwagner"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":        patternAll("EVERYTHING"),
				"src/dir": patternAll("src-code"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
		},
		{
			name:              "alias, md",
			aliasesFileExists: true,
			mdEnabled:         true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("carl", "cjwagner"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"docs/file.md":        patternAll("alice"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":             patternAll("EVERYTHING"),
				"src/dir":      patternAll("src-code"),
				"docs/file.md": patternAll("docs"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
		},
		{
			name:   "OWNERS from non-default branch",
			branch: strP("release-1.10"),
			extraBranchesAndFiles: map[string]map[string][]byte{
				"release-1.10": {
					"src/doc/OWNERS": []byte("approvers:\n - maggie\n"),
				},
			},
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll(),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"src/doc":             patternAll("maggie"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":        patternAll("EVERYTHING"),
				"src/dir": patternAll("src-code"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
		},
		{
			name:   "OWNERS from master branch while release branch diverges",
			branch: strP(defaultBranch),
			extraBranchesAndFiles: map[string]map[string][]byte{
				"release-1.10": {
					"src/doc/OWNERS": []byte("approvers:\n - maggie\n"),
				},
			},
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll(),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":        patternAll("EVERYTHING"),
				"src/dir": patternAll("src-code"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
		},
		{
			name:              "Skip collaborator checks, use only OWNERS files",
			skipCollaborators: true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("best-approvers"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner", "jakub"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":        patternAll("EVERYTHING"),
				"src/dir": patternAll("src-code"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
		},
		{
			name:              "cache reuses, base sha equals to cache sha",
			skipCollaborators: true,
			cacheOptions: &cacheOptions{
				hasAliases: true,
			},
			expectedReusable: true,
		},
		{
			name:              "cache reuses, only change common files",
			skipCollaborators: true,
			cacheOptions: &cacheOptions{
				hasAliases:        true,
				commonFileChanged: true,
			},
			expectedReusable: true,
		},
		{
			name:              "cache does not reuse, mdYaml changed",
			aliasesFileExists: true,
			mdEnabled:         true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("carl", "cjwagner"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"docs/file.md":        patternAll("alice"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":             patternAll("EVERYTHING"),
				"src/dir":      patternAll("src-code"),
				"docs/file.md": patternAll("docs"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
			cacheOptions: &cacheOptions{},
		},
		{
			name:              "cache does not reuse, aliases is nil",
			aliasesFileExists: true,
			mdEnabled:         true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("carl", "cjwagner"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"docs/file.md":        patternAll("alice"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":             patternAll("EVERYTHING"),
				"src/dir":      patternAll("src-code"),
				"docs/file.md": patternAll("docs"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
			cacheOptions: &cacheOptions{
				commonFileChanged: true,
			},
		},
		{
			name:              "cache does not reuse, changes files contains OWNERS",
			aliasesFileExists: true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("carl", "cjwagner"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":        patternAll("EVERYTHING"),
				"src/dir": patternAll("src-code"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
			cacheOptions: &cacheOptions{
				hasAliases:        true,
				ownersFileChanged: true,
			},
		},
		{
			name:              "cache does not reuse, changes files contains OWNERS_ALIASES",
			aliasesFileExists: true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("carl", "cjwagner"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":        patternAll("EVERYTHING"),
				"src/dir": patternAll("src-code"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
			cacheOptions: &cacheOptions{
				hasAliases:               true,
				ownersAliasesFileChanged: true,
			},
		},
		{
			name:              "cache reuses, changes files contains .md, but mdYaml is false",
			skipCollaborators: true,
			cacheOptions: &cacheOptions{
				hasAliases:    true,
				mdFileChanged: true,
			},
			expectedReusable: true,
		},
		{
			name:              "cache does not reuse, changes files contains .md, and mdYaml is true",
			aliasesFileExists: true,
			mdEnabled:         true,
			expectedApprovers: map[string]map[string]sets.Set[string]{
				"":                    patternAll("cjwagner"),
				"src":                 patternAll("carl", "cjwagner"),
				"src/dir":             patternAll("bob"),
				"src/dir/conformance": patternAll("mml"),
				"src/dir/subdir":      patternAll("alice", "bob"),
				"docs/file.md":        patternAll("alice"),
				"vendor":              patternAll("alice"),
			},
			expectedReviewers: map[string]map[string]sets.Set[string]{
				"":               patternAll("alice", "bob"),
				"src/dir":        patternAll("alice", "cjwagner"),
				"src/dir/subdir": patternAll("alice", "bob"),
			},
			expectedRequiredReviewers: map[string]map[string]sets.Set[string]{
				"":        patternAll("chris"),
				"src/dir": patternAll("ben"),
			},
			expectedLabels: map[string]map[string]sets.Set[string]{
				"":             patternAll("EVERYTHING"),
				"src/dir":      patternAll("src-code"),
				"docs/file.md": patternAll("docs"),
			},
			expectedOptions: map[string]dirOptions{
				"src/dir/conformance": {
					NoParentOwners:               true,
					AutoApproveUnownedSubfolders: true,
				},
			},
			cacheOptions: &cacheOptions{
				hasAliases:    true,
				mdYaml:        true,
				mdFileChanged: true,
			},
		},
	}

	for i := range tests {
		test := tests[i]
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("Running scenario %q", test.name)
			client, cleanup, err := getTestClient(testFiles, test.mdEnabled, test.skipCollaborators, test.aliasesFileExists, false, nil, nil, test.extraBranchesAndFiles, test.cacheOptions, clients)
			if err != nil {
				t.Fatalf("Error creating test client: %v.", err)
			}
			t.Cleanup(cleanup)

			base := defaultBranch
			defer cleanup()

			if test.branch != nil {
				base = *test.branch
			}
			r, err := client.LoadRepoOwners("org", "repo", base)
			if err != nil {
				t.Fatalf("Unexpected error loading RepoOwners: %v.", err)
			}
			ro := r.(*RepoOwners)
			if test.expectedReusable {
				if ro.baseDir != "cache" {
					t.Fatalf("expected cache must be reused, but got baseDir %q", ro.baseDir)
				}
				return
			} else {
				if ro.baseDir == "cache" {
					t.Fatal("expected cache should not be reused, but reused")
				}
			}
			if ro.baseDir == "" {
				t.Fatal("Expected 'baseDir' to be populated.")
			}
			if (ro.RepoAliases != nil) != test.aliasesFileExists {
				t.Fatalf("Expected 'RepoAliases' to be poplulated: %t, but got %t.", test.aliasesFileExists, ro.RepoAliases != nil)
			}
			if ro.enableMDYAML != test.mdEnabled {
				t.Fatalf("Expected 'enableMdYaml' to be: %t, but got %t.", test.mdEnabled, ro.enableMDYAML)
			}

			check := func(field string, expected map[string]map[string]sets.Set[string], got map[string]map[*regexp.Regexp]sets.Set[string]) {
				converted := map[string]map[string]sets.Set[string]{}
				for path, m := range got {
					converted[path] = map[string]sets.Set[string]{}
					for re, s := range m {
						var pattern string
						if re != nil {
							pattern = re.String()
						}
						converted[path][pattern] = s
					}
				}
				if !reflect.DeepEqual(expected, converted) {
					t.Errorf("Expected %s to be:\n%+v\ngot:\n%+v.", field, expected, converted)
				}
			}
			check("approvers", test.expectedApprovers, ro.approvers)
			check("reviewers", test.expectedReviewers, ro.reviewers)
			check("required_reviewers", test.expectedRequiredReviewers, ro.requiredReviewers)
			check("labels", test.expectedLabels, ro.labels)
			if !reflect.DeepEqual(test.expectedOptions, ro.options) {
				t.Errorf("Expected options to be:\n%#v\ngot:\n%#v.", test.expectedOptions, ro.options)
			}
		})
	}
}

const (
	baseDir            = ""
	leafDir            = "a/b/c"
	leafFilterDir      = "a/b/e"
	noParentsDir       = "d"
	noParentsFilterDir = "f"
	nonExistentDir     = "DELETED_DIR"
)

var (
	mdFileReg  = regexp.MustCompile(`.*\.md`)
	txtFileReg = regexp.MustCompile(`.*\.txt`)
)

func TestGetApprovers(t *testing.T) {
	ro := &RepoOwners{
		approvers: map[string]map[*regexp.Regexp]sets.Set[string]{
			baseDir: regexpAll("alice", "bob"),
			leafDir: regexpAll("carl", "dave"),
			leafFilterDir: {
				mdFileReg:  sets.New[string]("carl", "dave"),
				txtFileReg: sets.New[string]("elic"),
			},
			noParentsDir: regexpAll("mml"),
			noParentsFilterDir: {
				mdFileReg:  sets.New[string]("carl", "dave"),
				txtFileReg: sets.New[string]("flex"),
			},
		},
		options: map[string]dirOptions{
			noParentsDir: {
				NoParentOwners: true,
			},
			noParentsFilterDir: {
				NoParentOwners: true,
			},
		},
	}
	tests := []struct {
		name               string
		filePath           string
		expectedOwnersPath string
		expectedLeafOwners sets.Set[string]
		expectedAllOwners  sets.Set[string]
	}{
		{
			name:               "Modified Base Dir Only",
			filePath:           filepath.Join(baseDir, "testFile.md"),
			expectedOwnersPath: baseDir,
			expectedLeafOwners: ro.approvers[baseDir][nil],
			expectedAllOwners:  ro.approvers[baseDir][nil],
		},
		{
			name:               "Modified Leaf Dir Only",
			filePath:           filepath.Join(leafDir, "testFile.md"),
			expectedOwnersPath: leafDir,
			expectedLeafOwners: ro.approvers[leafDir][nil],
			expectedAllOwners:  ro.approvers[baseDir][nil].Union(ro.approvers[leafDir][nil]),
		},
		{
			name:               "Modified regexp matched file in Leaf Dir Only",
			filePath:           filepath.Join(leafFilterDir, "testFile.md"),
			expectedOwnersPath: leafFilterDir,
			expectedLeafOwners: ro.approvers[leafFilterDir][mdFileReg],
			expectedAllOwners:  ro.approvers[baseDir][nil].Union(ro.approvers[leafFilterDir][mdFileReg]),
		},
		{
			name:               "Modified not regexp matched file in Leaf Dir Only",
			filePath:           filepath.Join(leafFilterDir, "testFile.dat"),
			expectedOwnersPath: baseDir,
			expectedLeafOwners: ro.approvers[baseDir][nil],
			expectedAllOwners:  ro.approvers[baseDir][nil],
		},
		{
			name:               "Modified NoParentOwners Dir Only",
			filePath:           filepath.Join(noParentsDir, "testFile.go"),
			expectedOwnersPath: noParentsDir,
			expectedLeafOwners: ro.approvers[noParentsDir][nil],
			expectedAllOwners:  ro.approvers[noParentsDir][nil],
		},
		{
			name:               "Modified regexp matched file NoParentOwners Dir Only",
			filePath:           filepath.Join(noParentsFilterDir, "testFile.txt"),
			expectedOwnersPath: noParentsFilterDir,
			expectedLeafOwners: ro.approvers[noParentsFilterDir][txtFileReg],
			expectedAllOwners:  ro.approvers[noParentsFilterDir][txtFileReg],
		},
		{
			name:               "Modified regexp not matched file in NoParentOwners Dir Only",
			filePath:           filepath.Join(noParentsFilterDir, "testFile.go_to_parent"),
			expectedOwnersPath: baseDir,
			expectedLeafOwners: ro.approvers[baseDir][nil],
			expectedAllOwners:  ro.approvers[baseDir][nil],
		},
		{
			name:               "Modified Nonexistent Dir (Default to Base)",
			filePath:           filepath.Join(nonExistentDir, "testFile.md"),
			expectedOwnersPath: baseDir,
			expectedLeafOwners: ro.approvers[baseDir][nil],
			expectedAllOwners:  ro.approvers[baseDir][nil],
		},
	}
	for testNum, test := range tests {
		foundLeafApprovers := ro.LeafApprovers(test.filePath)
		foundApprovers := ro.Approvers(test.filePath).Set()
		foundOwnersPath := ro.FindApproverOwnersForFile(test.filePath)
		if !foundLeafApprovers.Equal(test.expectedLeafOwners) {
			t.Errorf("The Leaf Approvers Found Do Not Match Expected For Test %d: %s", testNum, test.name)
			t.Errorf("\tExpected Owners: %v\tFound Owners: %v ", test.expectedLeafOwners, foundLeafApprovers)
		}
		if !foundApprovers.Equal(test.expectedAllOwners) {
			t.Errorf("The Approvers Found Do Not Match Expected For Test %d: %s", testNum, test.name)
			t.Errorf("\tExpected Owners: %v\tFound Owners: %v ", test.expectedAllOwners, foundApprovers)
		}
		if foundOwnersPath != test.expectedOwnersPath {
			t.Errorf("The Owners Path Found Does Not Match Expected For Test %d: %s", testNum, test.name)
			t.Errorf("\tExpected Owners: %v\tFound Owners: %v ", test.expectedOwnersPath, foundOwnersPath)
		}
	}
}

func TestFindLabelsForPath(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		expectedLabels sets.Set[string]
	}{
		{
			name:           "base 1",
			path:           "foo.txt",
			expectedLabels: sets.New[string]("sig/godzilla"),
		}, {
			name:           "base 2",
			path:           "./foo.txt",
			expectedLabels: sets.New[string]("sig/godzilla"),
		}, {
			name:           "base 3",
			path:           "",
			expectedLabels: sets.New[string]("sig/godzilla"),
		}, {
			name:           "base 4",
			path:           ".",
			expectedLabels: sets.New[string]("sig/godzilla"),
		}, {
			name:           "leaf 1",
			path:           "a/b/c/foo.txt",
			expectedLabels: sets.New[string]("sig/godzilla", "wg/save-tokyo"),
		}, {
			name:           "leaf 2",
			path:           "a/b/foo.txt",
			expectedLabels: sets.New[string]("sig/godzilla"),
		},
	}

	testOwners := &RepoOwners{
		labels: map[string]map[*regexp.Regexp]sets.Set[string]{
			baseDir: regexpAll("sig/godzilla"),
			leafDir: regexpAll("wg/save-tokyo"),
		},
	}
	for _, test := range tests {
		got := testOwners.FindLabelsForFile(test.path)
		if !got.Equal(test.expectedLabels) {
			t.Errorf(
				"[%s] Expected labels %q for path %q, but got %q.",
				test.name,
				sets.List(test.expectedLabels),
				test.path,
				sets.List(got),
			)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		expectedPath string
	}{
		{
			name:         "Empty String",
			path:         "",
			expectedPath: "",
		},
		{
			name:         "Dot (.) as Path",
			path:         ".",
			expectedPath: "",
		},
		{
			name:         "GitHub Style Input (No Root)",
			path:         "a/b/c/d.txt",
			expectedPath: "a/b/c/d.txt",
		},
		{
			name:         "Preceding Slash and Trailing Slash",
			path:         "/a/b/",
			expectedPath: "/a/b",
		},
		{
			name:         "Trailing Slash",
			path:         "foo/bar/baz/",
			expectedPath: "foo/bar/baz",
		},
	}
	for _, test := range tests {
		if got := canonicalize(test.path); test.expectedPath != got {
			t.Errorf(
				"[%s] Expected the canonical path for %v to be %v.  Found %v instead",
				test.name,
				test.path,
				test.expectedPath,
				got,
			)
		}
	}
}

func TestExpandAliases(t *testing.T) {
	testAliases := RepoAliases{
		"team/t1": sets.New[string]("u1", "u2"),
		"team/t2": sets.New[string]("u1", "u3"),
		"team/t3": sets.New[string](),
	}
	tests := []struct {
		name             string
		unexpanded       sets.Set[string]
		expectedExpanded sets.Set[string]
	}{
		{
			name:             "No expansions.",
			unexpanded:       sets.New[string]("abc", "def"),
			expectedExpanded: sets.New[string]("abc", "def"),
		},
		{
			name:             "One alias to be expanded",
			unexpanded:       sets.New[string]("abc", "team/t1"),
			expectedExpanded: sets.New[string]("abc", "u1", "u2"),
		},
		{
			name:             "Duplicates inside and outside alias.",
			unexpanded:       sets.New[string]("u1", "team/t1"),
			expectedExpanded: sets.New[string]("u1", "u2"),
		},
		{
			name:             "Duplicates in multiple aliases.",
			unexpanded:       sets.New[string]("u1", "team/t1", "team/t2"),
			expectedExpanded: sets.New[string]("u1", "u2", "u3"),
		},
		{
			name:             "Mixed casing in aliases.",
			unexpanded:       sets.New[string]("Team/T1"),
			expectedExpanded: sets.New[string]("u1", "u2"),
		},
		{
			name:             "Empty team.",
			unexpanded:       sets.New[string]("Team/T3"),
			expectedExpanded: sets.New[string](),
		},
	}

	for _, test := range tests {
		if got := testAliases.ExpandAliases(test.unexpanded); !test.expectedExpanded.Equal(got) {
			t.Errorf(
				"[%s] Expected %q to expand to %q, but got %q.",
				test.name,
				sets.List(test.unexpanded),
				sets.List(test.expectedExpanded),
				sets.List(got),
			)
		}
	}
}

func TestSaveSimpleConfig(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name     string
		given    SimpleConfig
		expected string
	}{
		{
			name: "No expansions.",
			given: SimpleConfig{
				Config: Config{
					Approvers: []string{"david", "sig-alias", "Alice"},
					Reviewers: []string{"adam", "sig-alias"},
				},
			},
			expected: `approvers:
- david
- sig-alias
- Alice
options: {}
reviewers:
- adam
- sig-alias
`,
		},
	}

	for _, test := range tests {
		file := filepath.Join(dir, fmt.Sprintf("%s.yaml", test.name))
		err := SaveSimpleConfig(test.given, file)
		if err != nil {
			t.Errorf("unexpected error when writing simple config")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("unexpected error when reading file: %s", file)
		}
		s := string(b)
		if test.expected != s {
			t.Errorf("result '%s' is differ from expected: '%s'", s, test.expected)
		}
		simple, err := LoadSimpleConfig(b)
		if err != nil {
			t.Errorf("unexpected error when load simple config: %v", err)
		}
		if !reflect.DeepEqual(simple, test.given) {
			t.Errorf("unexpected error when loading simple config from: '%s'", diff.ObjectReflectDiff(simple, test.given))
		}
	}
}

func TestSaveFullConfig(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name     string
		given    FullConfig
		expected string
	}{
		{
			name: "No expansions.",
			given: FullConfig{
				Filters: map[string]Config{
					".*": {
						Approvers: []string{"alice", "bob", "carol", "david"},
						Reviewers: []string{"adam", "bob", "carol"},
					},
				},
			},
			expected: `filters:
  .*:
    approvers:
    - alice
    - bob
    - carol
    - david
    reviewers:
    - adam
    - bob
    - carol
options: {}
`,
		},
	}

	for _, test := range tests {
		file := filepath.Join(dir, fmt.Sprintf("%s.yaml", test.name))
		err := SaveFullConfig(test.given, file)
		if err != nil {
			t.Errorf("unexpected error when writing full config")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("unexpected error when reading file: %s", file)
		}
		s := string(b)
		if test.expected != s {
			t.Errorf("result '%s' is differ from expected: '%s'", s, test.expected)
		}
		full, err := LoadFullConfig(b)
		if err != nil {
			t.Errorf("unexpected error when load full config: %v", err)
		}
		if !reflect.DeepEqual(full, test.given) {
			t.Errorf("unexpected error when loading simple config from: '%s'", diff.ObjectReflectDiff(full, test.given))
		}
	}
}

func TestTopLevelApprovers(t *testing.T) {
	expectedApprovers := []string{"alice", "bob"}
	ro := &RepoOwners{
		approvers: map[string]map[*regexp.Regexp]sets.Set[string]{
			baseDir: regexpAll(expectedApprovers...),
			leafDir: regexpAll("carl", "dave"),
		},
	}

	foundApprovers := ro.TopLevelApprovers()
	if !foundApprovers.Equal(sets.New[string](expectedApprovers...)) {
		t.Errorf("Expected Owners: %v\tFound Owners: %v ", expectedApprovers, foundApprovers)
	}
}

func TestCacheDoesntRace(t *testing.T) {
	key := "key"
	cache := newCache()

	wg := &sync.WaitGroup{}
	wg.Add(2)

	go func() { cache.setEntry(key, cacheEntry{}); wg.Done() }()
	go func() { cache.getEntry(key); wg.Done() }()

	wg.Wait()
}

func TestRepoOwners_AllOwners(t *testing.T) {
	expectedOwners := []string{"alice", "bob", "cjwagner", "matthyx", "mml"}
	ro := &RepoOwners{
		approvers: map[string]map[*regexp.Regexp]sets.Set[string]{
			"":                    regexpAll("cjwagner"),
			"src":                 regexpAll(),
			"src/dir":             regexpAll("bob"),
			"src/dir/conformance": regexpAll("mml"),
			"src/dir/subdir":      regexpAll("alice", "bob"),
			"vendor":              regexpAll("alice"),
		},
		reviewers: map[string]map[*regexp.Regexp]sets.Set[string]{
			"":               regexpAll("alice", "bob"),
			"src/dir":        regexpAll("alice", "matthyx"),
			"src/dir/subdir": regexpAll("alice", "bob"),
		},
	}
	foundOwners := ro.AllOwners()
	if !foundOwners.Equal(sets.New[string](expectedOwners...)) {
		t.Errorf("Expected Owners: %v\tFound Owners: %v ", expectedOwners, sets.List(foundOwners))
	}
}

func TestRepoOwners_AllApprovers(t *testing.T) {
	expectedApprovers := []string{"alice", "bob", "cjwagner", "mml"}
	ro := &RepoOwners{
		approvers: map[string]map[*regexp.Regexp]sets.Set[string]{
			"":                    regexpAll("cjwagner"),
			"src":                 regexpAll(),
			"src/dir":             regexpAll("bob"),
			"src/dir/conformance": regexpAll("mml"),
			"src/dir/subdir":      regexpAll("alice", "bob"),
			"vendor":              regexpAll("alice"),
		},
		reviewers: map[string]map[*regexp.Regexp]sets.Set[string]{
			"":               regexpAll("alice", "bob"),
			"src/dir":        regexpAll("alice", "matthyx"),
			"src/dir/subdir": regexpAll("alice", "bob"),
		},
	}
	foundApprovers := ro.AllApprovers()
	if !foundApprovers.Equal(sets.New[string](expectedApprovers...)) {
		t.Errorf("Expected approvers: %v\tFound approvers: %v ", expectedApprovers, sets.List(foundApprovers))
	}
}

func TestRepoOwners_AllReviewers(t *testing.T) {
	expectedReviewers := []string{"alice", "bob", "matthyx"}
	ro := &RepoOwners{
		approvers: map[string]map[*regexp.Regexp]sets.Set[string]{
			"":                    regexpAll("cjwagner"),
			"src":                 regexpAll(),
			"src/dir":             regexpAll("bob"),
			"src/dir/conformance": regexpAll("mml"),
			"src/dir/subdir":      regexpAll("alice", "bob"),
			"vendor":              regexpAll("alice"),
		},
		reviewers: map[string]map[*regexp.Regexp]sets.Set[string]{
			"":               regexpAll("alice", "bob"),
			"src/dir":        regexpAll("alice", "matthyx"),
			"src/dir/subdir": regexpAll("alice", "bob"),
		},
	}
	foundReviewers := ro.AllReviewers()
	if !foundReviewers.Equal(sets.New[string](expectedReviewers...)) {
		t.Errorf("Expected reviewers: %v\tFound reviewers: %v ", expectedReviewers, sets.List(foundReviewers))
	}
}
