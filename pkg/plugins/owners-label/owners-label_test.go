/*
Copyright 2018 The Kubernetes Authors.

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

package ownerslabel

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
	"sigs.k8s.io/prow/pkg/labels"
)

func formatLabels(labels ...string) []string {
	r := []string{}
	for _, l := range labels {
		r = append(r, fmt.Sprintf("%s/%s#%d:%s", "org", "repo", 1, l))
	}
	if len(r) == 0 {
		return nil
	}
	return r
}

type fakeOwnersClient struct {
	labels map[string]sets.Set[string]
}

func (foc *fakeOwnersClient) FindLabelsForFile(path string) sets.Set[string] {
	return foc.labels[path]
}

// TestHandle tests that the handle function requests reviews from the correct number of unique users.
func TestHandle(t *testing.T) {
	foc := &fakeOwnersClient{
		labels: map[string]sets.Set[string]{
			"a.go": sets.New[string](labels.LGTM, labels.Approved, "kind/docs"),
			"b.go": sets.New[string](labels.LGTM),
			"c.go": sets.New[string](labels.LGTM, "dnm/frozen-docs"),
			"d.sh": sets.New[string]("dnm/bash"),
			"e.sh": sets.New[string]("dnm/bash"),
		},
	}

	type testCase struct {
		name              string
		filesChanged      []string
		expectedNewLabels []string
		repoLabels        []string
		issueLabels       []string
	}
	testcases := []testCase{
		{
			name:              "no labels",
			filesChanged:      []string{"other.go", "something.go"},
			expectedNewLabels: []string{},
			repoLabels:        []string{},
			issueLabels:       []string{},
		},
		{
			name:              "1 file 1 label",
			filesChanged:      []string{"b.go"},
			expectedNewLabels: formatLabels(labels.LGTM),
			repoLabels:        []string{labels.LGTM},
			issueLabels:       []string{},
		},
		{
			name:              "1 file 3 labels",
			filesChanged:      []string{"a.go"},
			expectedNewLabels: formatLabels(labels.LGTM, labels.Approved, "kind/docs"),
			repoLabels:        []string{labels.LGTM, labels.Approved, "kind/docs"},
			issueLabels:       []string{},
		},
		{
			name:              "2 files no overlap",
			filesChanged:      []string{"c.go", "d.sh"},
			expectedNewLabels: formatLabels(labels.LGTM, "dnm/frozen-docs", "dnm/bash"),
			repoLabels:        []string{labels.LGTM, "dnm/frozen-docs", "dnm/bash"},
			issueLabels:       []string{},
		},
		{
			name:              "2 files partial overlap",
			filesChanged:      []string{"a.go", "b.go"},
			expectedNewLabels: formatLabels(labels.LGTM, labels.Approved, "kind/docs"),
			repoLabels:        []string{labels.LGTM, labels.Approved, "kind/docs"},
			issueLabels:       []string{},
		},
		{
			name:              "2 files complete overlap",
			filesChanged:      []string{"d.sh", "e.sh"},
			expectedNewLabels: formatLabels("dnm/bash"),
			repoLabels:        []string{"dnm/bash"},
			issueLabels:       []string{},
		},
		{
			name:              "3 files partial overlap",
			filesChanged:      []string{"a.go", "b.go", "c.go"},
			expectedNewLabels: formatLabels(labels.LGTM, labels.Approved, "kind/docs", "dnm/frozen-docs"),
			repoLabels:        []string{labels.LGTM, labels.Approved, "kind/docs", "dnm/frozen-docs"},
			issueLabels:       []string{},
		},
		{
			name:              "no labels to add, initial unrelated label",
			filesChanged:      []string{"other.go", "something.go"},
			expectedNewLabels: []string{},
			repoLabels:        []string{labels.LGTM},
			issueLabels:       []string{labels.LGTM},
		},
		{
			name:              "1 file 1 label, already present",
			filesChanged:      []string{"b.go"},
			expectedNewLabels: []string{},
			repoLabels:        []string{labels.LGTM},
			issueLabels:       []string{labels.LGTM},
		},
		{
			name:              "1 file 1 label, doesn't exist on the repo",
			filesChanged:      []string{"b.go"},
			expectedNewLabels: []string{},
			repoLabels:        []string{labels.Approved},
			issueLabels:       []string{},
		},
		{
			name:              "2 files no overlap, 1 label already present",
			filesChanged:      []string{"c.go", "d.sh"},
			expectedNewLabels: formatLabels(labels.LGTM, "dnm/frozen-docs"),
			repoLabels:        []string{"dnm/bash", labels.Approved, labels.LGTM, "dnm/frozen-docs"},
			issueLabels:       []string{"dnm/bash", labels.Approved},
		},
		{
			name:              "2 files complete overlap, label already present",
			filesChanged:      []string{"d.sh", "e.sh"},
			expectedNewLabels: []string{},
			repoLabels:        []string{"dnm/bash"},
			issueLabels:       []string{"dnm/bash"},
		},
	}

	for _, tc := range testcases {
		basicPR := github.PullRequest{
			Number: 1,
			Base: github.PullRequestBranch{
				Repo: github.Repo{
					Owner: github.User{
						Login: "org",
					},
					Name: "repo",
				},
			},
			User: github.User{
				Login: "user",
			},
		}

		t.Logf("Running scenario %q", tc.name)
		sort.Strings(tc.expectedNewLabels)
		changes := make([]github.PullRequestChange, 0, len(tc.filesChanged))
		for _, name := range tc.filesChanged {
			changes = append(changes, github.PullRequestChange{Filename: name})
		}
		fghc := fakegithub.NewFakeClient()
		fghc.PullRequests = map[int]*github.PullRequest{
			basicPR.Number: &basicPR,
		}
		fghc.PullRequestChanges = map[int][]github.PullRequestChange{
			basicPR.Number: changes,
		}
		fghc.RepoLabelsExisting = tc.repoLabels
		fghc.IssueLabelsAdded = []string{}
		// Add initial labels
		for _, label := range tc.issueLabels {
			fghc.AddLabel(basicPR.Base.Repo.Owner.Login, basicPR.Base.Repo.Name, basicPR.Number, label)
		}
		pre := &github.PullRequestEvent{
			Action:      github.PullRequestActionOpened,
			Number:      basicPR.Number,
			PullRequest: basicPR,
			Repo:        basicPR.Base.Repo,
		}

		err := handle(fghc, foc, logrus.WithField("plugin", PluginName), pre, false)
		if err != nil {
			t.Errorf("[%s] unexpected error from handle: %v", tc.name, err)
			continue
		}

		// Check that all the correct labels (and only the correct labels) were added.
		expectLabels := append(formatLabels(tc.issueLabels...), tc.expectedNewLabels...)
		if expectLabels == nil {
			expectLabels = []string{}
		}
		sort.Strings(expectLabels)
		sort.Strings(fghc.IssueLabelsAdded)
		if !reflect.DeepEqual(expectLabels, fghc.IssueLabelsAdded) {
			t.Errorf("expected the labels %q to be added, but %q were added.", expectLabels, fghc.IssueLabelsAdded)
		}

	}
}

func TestHandleIgnoreMergeCommits(t *testing.T) {
	foc := &fakeOwnersClient{
		labels: map[string]sets.Set[string]{
			"a.go": sets.New[string](labels.LGTM, labels.Approved),
		},
	}

	basicPR := github.PullRequest{
		Number: 1,
		Base: github.PullRequestBranch{
			Repo: github.Repo{
				Owner: github.User{Login: "org"},
				Name:  "repo",
			},
		},
		User: github.User{Login: "user"},
	}
	pre := &github.PullRequestEvent{
		Action:      github.PullRequestActionSynchronize,
		Number:      basicPR.Number,
		PullRequest: basicPR,
		Repo:        basicPR.Base.Repo,
	}

	testcases := []struct {
		name              string
		commits           []github.RepositoryCommit
		expectedNewLabels []string
	}{
		{
			name: "no merge commits, labels added",
			commits: []github.RepositoryCommit{
				{SHA: "abc", Parents: []github.GitCommit{{SHA: "parent1"}}},
			},
			expectedNewLabels: formatLabels(labels.LGTM, labels.Approved),
		},
		{
			name: "merge commit present, labels skipped",
			commits: []github.RepositoryCommit{
				{SHA: "abc", Parents: []github.GitCommit{{SHA: "parent1"}}},
				{SHA: "def", Parents: []github.GitCommit{{SHA: "parent1"}, {SHA: "parent2"}}},
			},
			expectedNewLabels: []string{},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			fghc := fakegithub.NewFakeClient()
			fghc.PullRequests = map[int]*github.PullRequest{basicPR.Number: &basicPR}
			fghc.PullRequestChanges = map[int][]github.PullRequestChange{
				basicPR.Number: {{Filename: "a.go"}},
			}
			fghc.RepoLabelsExisting = []string{labels.LGTM, labels.Approved}
			fghc.IssueLabelsAdded = []string{}
			fghc.CommitMap = map[string][]github.RepositoryCommit{
				fmt.Sprintf("%s/%s#%d", "org", "repo", basicPR.Number): tc.commits,
			}

			err := handle(fghc, foc, logrus.WithField("plugin", PluginName), pre, true)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			sort.Strings(tc.expectedNewLabels)
			sort.Strings(fghc.IssueLabelsAdded)
			if !reflect.DeepEqual(tc.expectedNewLabels, fghc.IssueLabelsAdded) {
				t.Errorf("expected labels %q, got %q", tc.expectedNewLabels, fghc.IssueLabelsAdded)
			}
		})
	}
}
