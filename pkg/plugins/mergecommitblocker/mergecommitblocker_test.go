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

package mergecommitblocker

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/commentpruner"
	"sigs.k8s.io/prow/pkg/git/localgit"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/plugins"
)

var defaultBranch = localgit.DefaultBranch("")

type strSet map[string]struct{}

type fakeGHClient struct {
	labels   strSet
	comments map[int]string
}

func (f *fakeGHClient) AddLabel(org, repo string, number int, label string) error {
	f.labels[label] = struct{}{}
	return nil
}

func (f *fakeGHClient) RemoveLabel(org, repo string, number int, label string) error {
	delete(f.labels, label)
	return nil
}

func (f *fakeGHClient) GetIssueLabels(org, repo string, number int) ([]github.Label, error) {
	var labels []github.Label
	for l := range f.labels {
		labels = append(labels, github.Label{Name: l})
	}
	return labels, nil
}

func (f *fakeGHClient) CreateComment(org, repo string, number int, comment string) error {
	if _, ok := f.comments[number]; ok {
		return fmt.Errorf("comment id %d already exists", number)
	}
	f.comments[number] = comment
	return nil
}

func (f *fakeGHClient) DeleteComment(org, repo string, id int) error {
	delete(f.comments, id)
	return nil
}

func (f *fakeGHClient) BotUserChecker() (func(candidate string) bool, error) {
	return func(candidate string) bool {
		return candidate == "foo"
	}, nil
}

func (f *fakeGHClient) ListIssueComments(org, repo string, number int) ([]github.IssueComment, error) {
	var ghComments []github.IssueComment
	for id, c := range f.comments {
		ghComment := github.IssueComment{
			ID:   id,
			Body: c,
			User: github.User{Login: "foo"},
		}
		ghComments = append(ghComments, ghComment)
	}
	return ghComments, nil
}

func TestHandleV2(t *testing.T) {
	testHandle(localgit.NewV2, t)
}

func testHandle(clients localgit.Clients, t *testing.T) {
	lg, c, err := clients()
	if err != nil {
		t.Fatalf("Making localgit: %v", err)
	}
	defer func() {
		if err := lg.Clean(); err != nil {
			t.Errorf("Cleaning up localgit: %v", err)
		}
		if err := c.Clean(); err != nil {
			t.Errorf("Cleaning up client: %v", err)
		}
	}()
	if err := lg.MakeFakeRepo("foo", "bar"); err != nil {
		t.Fatalf("Making fake repo: %v", err)
	}
	var (
		checkoutPR = func(prNum int) {
			if err := lg.CheckoutNewBranch("foo", "bar", fmt.Sprintf("pull/%d/head", prNum)); err != nil {
				t.Fatalf("Creating & checking out pull branch pull/%d/head: %v", prNum, err)
			}
		}
		checkoutBranch = func(branch string) {
			if err := lg.Checkout("foo", "bar", branch); err != nil {
				t.Fatalf("Checking out branch %s: %v", branch, err)
			}
		}
		addCommit = func(file string) {
			if err := lg.AddCommit("foo", "bar", map[string][]byte{file: {}}); err != nil {
				t.Fatalf("Adding commit: %v", err)
			}
		}
		mergeMaster = func() {
			if _, err := lg.Merge("foo", "bar", defaultBranch); err != nil {
				t.Fatalf("Rebasing commit: %v", err)
			}
		}
		rebaseMaster = func() {
			if _, err := lg.Rebase("foo", "bar", defaultBranch); err != nil {
				t.Fatalf("Rebasing commit: %v", err)
			}
		}
	)

	type testCase struct {
		name          string
		fakeGHClient  *fakeGHClient
		prNum         int
		checkout      func()
		mergeOrRebase func()
		wantLabel     bool
		wantComment   bool
	}
	testcases := []testCase{
		{
			name: "merge commit label not exist, PR has merge commits",
			fakeGHClient: &fakeGHClient{
				labels:   strSet{},
				comments: make(map[int]string),
			},
			prNum:         11,
			checkout:      func() { checkoutBranch("pull/11/head") },
			mergeOrRebase: mergeMaster,
			wantLabel:     true,
			wantComment:   true,
		},
		{
			name: "merge commit label exists, PR has merge commits",
			fakeGHClient: &fakeGHClient{
				labels:   strSet{labels.MergeCommits: struct{}{}},
				comments: map[int]string{12: commentBody},
			},
			prNum:         12,
			checkout:      func() { checkoutBranch("pull/12/head") },
			mergeOrRebase: mergeMaster,
			wantLabel:     true,
			wantComment:   true,
		},
		{
			name: "merge commit label not exists, PR doesn't have merge commits",
			fakeGHClient: &fakeGHClient{
				labels:   strSet{},
				comments: make(map[int]string),
			},
			prNum:         13,
			checkout:      func() { checkoutBranch("pull/13/head") },
			mergeOrRebase: rebaseMaster,
			wantLabel:     false,
			wantComment:   false,
		},
		{
			name: "merge commit label exists, PR doesn't have merge commits",
			fakeGHClient: &fakeGHClient{
				labels:   strSet{labels.MergeCommits: struct{}{}},
				comments: map[int]string{14: commentBody},
			},
			prNum:         14,
			checkout:      func() { checkoutBranch("pull/14/head") },
			mergeOrRebase: rebaseMaster,
			wantLabel:     false,
			wantComment:   false,
		},
	}

	addCommit("wow")
	// preparation work: branch off all prs upon commit 'wow'
	for _, tt := range testcases {
		checkoutPR(tt.prNum)
	}
	// switch back to master and create a new commit 'ouch'
	checkoutBranch(defaultBranch)
	addCommit("ouch")
	masterSHA, err := lg.RevParse("foo", "bar", "HEAD")
	if err != nil {
		t.Fatalf("Fetching SHA: %v", err)
	}

	for _, tt := range testcases {
		tt.checkout()
		tt.mergeOrRebase()
		prSHA, err := lg.RevParse("foo", "bar", "HEAD")
		if err != nil {
			t.Fatalf("Fetching SHA: %v", err)
		}
		pre := &github.PullRequestEvent{
			Action: github.PullRequestActionOpened,
			PullRequest: github.PullRequest{
				Number: tt.prNum,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "foo"},
						Name:  "bar",
					},
					SHA: masterSHA,
				},
				Head: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "foo"},
						Name:  "bar",
					},
					SHA: prSHA,
				},
			},
		}
		log := logrus.NewEntry(logrus.New())
		fakePruner := commentpruner.NewEventClient(tt.fakeGHClient, log, "foo", "bar", tt.prNum)
		if err := handle(tt.fakeGHClient, c, fakePruner, log, pre, nil); err != nil {
			t.Errorf("Expect err is nil, but got %v", err)
		}
		// verify if MergeCommits label as expected
		if _, got := tt.fakeGHClient.labels[labels.MergeCommits]; got != tt.wantLabel {
			t.Errorf("Case: %v. Expect MergeCommits=%v, but got %v", tt.name, tt.wantLabel, got)
		}
		// verify if github comment is created/pruned as expected
		if _, got := tt.fakeGHClient.comments[tt.prNum]; got != tt.wantComment {
			t.Errorf("Case: %v. Expect wantComment=%v, but got %v", tt.name, tt.wantComment, got)
		}
	}
}

func TestHandleWithExcludedPathsV2(t *testing.T) {
	testHandleWithExcludedPaths(localgit.NewV2, t)
}

func testHandleWithExcludedPaths(clients localgit.Clients, t *testing.T) {
	lg, c, err := clients()
	if err != nil {
		t.Fatalf("Making localgit: %v", err)
	}
	defer func() {
		if err := lg.Clean(); err != nil {
			t.Errorf("Cleaning up localgit: %v", err)
		}
		if err := c.Clean(); err != nil {
			t.Errorf("Cleaning up client: %v", err)
		}
	}()
	if err := lg.MakeFakeRepo("foo", "bar"); err != nil {
		t.Fatalf("Making fake repo: %v", err)
	}
	var (
		checkoutPR = func(prNum int) {
			if err := lg.CheckoutNewBranch("foo", "bar", fmt.Sprintf("pull/%d/head", prNum)); err != nil {
				t.Fatalf("Creating & checking out pull branch pull/%d/head: %v", prNum, err)
			}
		}
		checkoutBranch = func(branch string) {
			if err := lg.Checkout("foo", "bar", branch); err != nil {
				t.Fatalf("Checking out branch %s: %v", branch, err)
			}
		}
		addCommit = func(files map[string][]byte) {
			if err := lg.AddCommit("foo", "bar", files); err != nil {
				t.Fatalf("Adding commit: %v", err)
			}
		}
		mergeMaster = func() {
			if _, err := lg.Merge("foo", "bar", defaultBranch); err != nil {
				t.Fatalf("Merging master: %v", err)
			}
		}
	)

	type testCase struct {
		name         string
		fakeGHClient *fakeGHClient
		prNum        int
		config       *plugins.MergeCommitBlocker
		setupPR      func() // function to set up the PR branch
		wantLabel    bool
		wantComment  bool
	}

	// Set up the initial repo state
	addCommit(map[string][]byte{"README.md": []byte("initial")})

	// Create PR branches for tests
	// PR 21: merge commit with files in excluded path only
	checkoutPR(21)
	// PR 22: merge commit with files outside excluded path
	checkoutPR(22)
	// PR 23: merge commit with files both inside and outside excluded path
	checkoutPR(23)

	// add another commit
	checkoutBranch(defaultBranch)
	addCommit(map[string][]byte{"vendor/lib/file.go": []byte("vendor content")})
	masterSHA, err := lg.RevParse("foo", "bar", "HEAD")
	if err != nil {
		t.Fatalf("Fetching SHA: %v", err)
	}

	testcases := []testCase{
		{
			name: "merge commit with files only in excluded path - no label",
			fakeGHClient: &fakeGHClient{
				labels:   strSet{},
				comments: make(map[int]string),
			},
			prNum:  21,
			config: newMergeCommitBlockerConfig([]string{`^vendor/`}),
			setupPR: func() {
				checkoutBranch("pull/21/head")
				// Add a commit with files in vendor/ before merge
				addCommit(map[string][]byte{"vendor/other/file.go": []byte("content")})
				mergeMaster()
			},
			wantLabel:   false,
			wantComment: false,
		},
		{
			name: "merge commit with files outside excluded path - add label",
			fakeGHClient: &fakeGHClient{
				labels:   strSet{},
				comments: make(map[int]string),
			},
			prNum:  22,
			config: newMergeCommitBlockerConfig([]string{`^vendor/`}),
			setupPR: func() {
				checkoutBranch("pull/22/head")
				// Add a commit with files outside vendor/
				addCommit(map[string][]byte{"src/main.go": []byte("content")})
				mergeMaster()
			},
			wantLabel:   true,
			wantComment: true,
		},
		{
			name: "merge commit with mixed files - add label",
			fakeGHClient: &fakeGHClient{
				labels:   strSet{},
				comments: make(map[int]string),
			},
			prNum:  23,
			config: newMergeCommitBlockerConfig([]string{`^vendor/`}),
			setupPR: func() {
				checkoutBranch("pull/23/head")
				// Add commits with files both inside and outside vendor/
				addCommit(map[string][]byte{
					"vendor/pkg/file.go": []byte("vendor"),
					"src/app.go":         []byte("app"),
				})
				mergeMaster()
			},
			wantLabel:   true,
			wantComment: true,
		},
	}

	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupPR()
			prSHA, err := lg.RevParse("foo", "bar", "HEAD")
			if err != nil {
				t.Fatalf("Fetching SHA: %v", err)
			}
			pre := &github.PullRequestEvent{
				Action: github.PullRequestActionOpened,
				PullRequest: github.PullRequest{
					Number: tt.prNum,
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "foo"},
							Name:  "bar",
						},
						SHA: masterSHA,
					},
					Head: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "foo"},
							Name:  "bar",
						},
						SHA: prSHA,
					},
				},
			}
			log := logrus.NewEntry(logrus.New())
			fakePruner := commentpruner.NewEventClient(tt.fakeGHClient, log, "foo", "bar", tt.prNum)
			if err := handle(tt.fakeGHClient, c, fakePruner, log, pre, tt.config); err != nil {
				t.Errorf("Expect err is nil, but got %v", err)
			}
			if _, got := tt.fakeGHClient.labels[labels.MergeCommits]; got != tt.wantLabel {
				t.Errorf("Case: %v. Expect MergeCommits=%v, but got %v", tt.name, tt.wantLabel, got)
			}
			if _, got := tt.fakeGHClient.comments[tt.prNum]; got != tt.wantComment {
				t.Errorf("Case: %v. Expect wantComment=%v, but got %v", tt.name, tt.wantComment, got)
			}
		})
	}
}

func TestIsMergeCommitAllowed(t *testing.T) {
	testcases := []struct {
		name     string
		files    []string
		patterns []string
		expected bool
	}{
		{
			name:     "all files match pattern",
			files:    []string{"vendor/lib/a.go", "vendor/lib/b.go"},
			patterns: []string{`^vendor/`},
			expected: true,
		},
		{
			name:     "no files match pattern",
			files:    []string{"src/main.go", "pkg/utils.go"},
			patterns: []string{`^vendor/`},
			expected: false,
		},
		{
			name:     "some files match pattern",
			files:    []string{"vendor/lib/a.go", "src/main.go"},
			patterns: []string{`^vendor/`},
			expected: false,
		},
		{
			name:     "empty files list",
			files:    []string{},
			patterns: []string{`^vendor/`},
			expected: true,
		},
		{
			name:     "empty patterns list",
			files:    []string{"src/main.go"},
			patterns: []string{},
			expected: false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			patterns := compilePatterns(tc.patterns)
			if got := isMergeCommitAllowed(tc.files, patterns); got != tc.expected {
				t.Errorf("isMergeCommitAllowed(%v, %v) = %v, want %v", tc.files, tc.patterns, got, tc.expected)
			}
		})
	}
}

// compilePatterns is a helper function to compile regex patterns for testing
func compilePatterns(patterns []string) []*regexp.Regexp {
	var result []*regexp.Regexp
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		result = append(result, re)
	}
	return result
}

// newMergeCommitBlockerConfig creates a MergeCommitBlocker with both
// ExcludedPaths and CompiledExcludedPaths properly set.
func newMergeCommitBlockerConfig(excludedPaths []string) *plugins.MergeCommitBlocker {
	return &plugins.MergeCommitBlocker{
		ExcludedPaths:         excludedPaths,
		CompiledExcludedPaths: compilePatterns(excludedPaths),
	}
}
