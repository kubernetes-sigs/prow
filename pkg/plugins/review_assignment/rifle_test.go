/*
Copyright 2025 The Kubernetes Authors.

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

package review_assignment

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/layeredsets"
)

type fakeRifleClient struct {
	*fakeGitHubClient
	blames map[string][]github.BlameRange
}

func (c *fakeRifleClient) GetBlame(org, repo, ref, path string) ([]github.BlameRange, error) {
	if c.blames != nil {
		return c.blames[path], nil
	}
	return nil, nil
}

func TestSelectBestReviewer(t *testing.T) {
	scores := map[string]float64{
		"alice":   100.0,
		"bob":     50.0,
		"charlie": 25.0,
	}

	candidates := layeredsets.NewString("alice", "bob", "charlie")
	busyReviewers := sets.New[string]()
	fghc := newFakeGitHubClient(&github.PullRequest{Number: 5, User: github.User{Login: "author"}, Head: github.PullRequestBranch{SHA: "abc"}}, nil)

	selected := selectBestReviewer(scores, &candidates, &busyReviewers, fghc, logrusEntry(), false)
	if selected != "alice" {
		t.Errorf("expected highest-scored 'alice', got %q", selected)
	}

	selected = selectBestReviewer(scores, &candidates, &busyReviewers, fghc, logrusEntry(), false)
	if selected != "bob" {
		t.Errorf("expected next highest 'bob', got %q", selected)
	}
}

func TestSelectBestReviewerSkipsBusy(t *testing.T) {
	scores := map[string]float64{
		"alice": 100.0,
		"bob":   50.0,
	}

	candidates := layeredsets.NewString("alice", "bob")
	busyReviewers := sets.New[string]("alice")
	fghc := newFakeGitHubClient(&github.PullRequest{Number: 5, User: github.User{Login: "author"}, Head: github.PullRequestBranch{SHA: "abc"}}, nil)

	selected := selectBestReviewer(scores, &candidates, &busyReviewers, fghc, logrusEntry(), false)
	if selected != "bob" {
		t.Errorf("expected 'bob' (alice is busy), got %q", selected)
	}
}

func TestSelectBestReviewerNoScores(t *testing.T) {
	scores := map[string]float64{}

	candidates := layeredsets.NewString("alice", "bob")
	busyReviewers := sets.New[string]()
	fghc := newFakeGitHubClient(&github.PullRequest{Number: 5, User: github.User{Login: "author"}, Head: github.PullRequestBranch{SHA: "abc"}}, nil)

	selected := selectBestReviewer(scores, &candidates, &busyReviewers, fghc, logrusEntry(), false)
	if selected == "" {
		t.Error("should still select someone even with zero scores")
	}
}

func TestHasBlameScores(t *testing.T) {
	if hasBlameScores(map[string]float64{}) {
		t.Error("empty scores should return false")
	}
	if !hasBlameScores(map[string]float64{"alice": 5.0}) {
		t.Error("non-empty scores should return true")
	}
	if hasBlameScores(map[string]float64{"alice": 0}) {
		t.Error("zero score should return false")
	}
}

func TestFindFallbackReviewers(t *testing.T) {
	tests := []struct {
		name          string
		blameScores   map[string]float64
		allOwners     sets.Set[string]
		exclude       sets.Set[string]
		needed        int
		expected      []string
		expectedCount int
		checkOrder    bool
	}{
		{
			name: "owners ranked by score",
			blameScores: map[string]float64{
				"alice": 100.0,
				"bob":   50.0,
			},
			allOwners:  sets.New[string]("alice", "bob"),
			exclude:    sets.Set[string]{},
			needed:     2,
			expected:   []string{"alice", "bob"},
			checkOrder: true,
		},
		{
			name: "non-owners excluded",
			blameScores: map[string]float64{
				"alice":   100.0,
				"charlie": 75.0,
			},
			allOwners:  sets.New[string]("alice"),
			exclude:    sets.Set[string]{},
			needed:     2,
			expected:   []string{"alice"},
			checkOrder: true,
		},
		{
			name: "existing reviewers excluded",
			blameScores: map[string]float64{
				"alice": 100.0,
				"bob":   50.0,
			},
			allOwners:  sets.New[string]("alice", "bob"),
			exclude:    sets.New[string]("alice"),
			needed:     1,
			expected:   []string{"bob"},
			checkOrder: true,
		},
		{
			name: "respects needed count",
			blameScores: map[string]float64{
				"alice": 100.0,
				"bob":   50.0,
				"carol": 25.0,
			},
			allOwners:  sets.New[string]("alice", "bob", "carol"),
			exclude:    sets.Set[string]{},
			needed:     1,
			expected:   []string{"alice"},
			checkOrder: true,
		},
		{
			name: "zero blame scores fall back to random",
			blameScores: map[string]float64{
				"alice": 0,
				"bob":   50.0,
			},
			allOwners:     sets.New[string]("alice", "bob"),
			exclude:       sets.Set[string]{},
			needed:        2,
			expectedCount: 2,
		},
		{
			name:          "no blame scores falls back to random",
			blameScores:   map[string]float64{},
			allOwners:     sets.New[string]("alice", "bob"),
			exclude:       sets.Set[string]{},
			needed:        1,
			expectedCount: 1,
		},
		{
			name:          "nil blame scores falls back to random",
			blameScores:   nil,
			allOwners:     sets.New[string]("alice"),
			exclude:       sets.Set[string]{},
			needed:        1,
			expectedCount: 1,
		},
		{
			name:          "no eligible owners",
			blameScores:   nil,
			allOwners:     sets.New[string]("alice"),
			exclude:       sets.New[string]("alice"),
			needed:        1,
			expectedCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			foc := &fakeOwnersClient{}
			foc.allOwners = tc.allOwners

			got := findFallbackReviewers(
				tc.blameScores, foc, tc.exclude, tc.needed,
			)

			if tc.checkOrder {
				if len(got) != len(tc.expected) {
					t.Fatalf("expected %d reviewers, got %d: %v", len(tc.expected), len(got), got)
				}
				for i, expected := range tc.expected {
					if got[i] != expected {
						t.Errorf("reviewer %d: expected %q, got %q", i, expected, got[i])
					}
				}
			} else {
				if len(got) != tc.expectedCount {
					t.Fatalf("expected %d reviewers, got %d: %v", tc.expectedCount, len(got), got)
				}
				gotSet := sets.New[string](got...)
				eligible := tc.allOwners.Difference(tc.exclude)
				if !gotSet.IsSuperset(sets.New[string]()) && !eligible.IsSuperset(gotSet) {
					t.Errorf("got reviewers %v not all in eligible set %v", got, eligible)
				}
			}
		})
	}
}

func TestHandleRifleWithBlameScoring(t *testing.T) {
	froc := &fakeRepoownersClient{
		foc: &fakeOwnersClient{
			owners: map[string]string{
				"pkg/foo/a.go": "1",
			},
			approvers: map[string]layeredsets.String{},
			leafReviewers: map[string]sets.Set[string]{
				"pkg/foo/a.go": sets.New[string]("alice", "bob", "carol"),
			},
			reviewers: map[string]layeredsets.String{
				"pkg/foo/a.go": layeredsets.NewString("alice", "bob", "carol"),
			},
		},
	}

	now := time.Now()
	pr := github.PullRequest{
		Number: 5,
		User:   github.User{Login: "author"},
		Base:   github.PullRequestBranch{Ref: "main"},
		Head:   github.PullRequestBranch{SHA: "abc123"},
	}
	repo := github.Repo{Owner: github.User{Login: "org"}, Name: "repo"}

	tests := []struct {
		name              string
		reviewerCount     int
		blames            map[string][]github.BlameRange
		expectedRequested []string
	}{
		{
			name:          "selects highest-scored reviewer",
			reviewerCount: 1,
			blames: map[string][]github.BlameRange{
				"pkg/foo/a.go": {
					{StartingLine: 1, EndingLine: 10, AuthorLogin: "alice", Date: now.Add(-24 * time.Hour)},
					{StartingLine: 1, EndingLine: 2, AuthorLogin: "bob", Date: now.Add(-30 * 24 * time.Hour)},
				},
			},
			expectedRequested: []string{"alice"},
		},
		{
			name:          "ranks multiple reviewers by score",
			reviewerCount: 2,
			blames: map[string][]github.BlameRange{
				"pkg/foo/a.go": {
					{StartingLine: 1, EndingLine: 10, AuthorLogin: "alice", Date: now.Add(-24 * time.Hour)},
					{StartingLine: 1, EndingLine: 5, AuthorLogin: "bob", Date: now.Add(-24 * time.Hour)},
					{StartingLine: 1, EndingLine: 1, AuthorLogin: "carol", Date: now.Add(-365 * 24 * time.Hour)},
				},
			},
			expectedRequested: []string{"alice", "bob"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fghc := &fakeRifleClient{
				fakeGitHubClient: &fakeGitHubClient{
					pr: &pr,
					changes: []github.PullRequestChange{
						{
							Filename: "pkg/foo/a.go",
							Status:   "modified",
							Patch:    "@@ -1,10 +1,12 @@ package foo",
							SHA:      "abc123",
						},
					},
				},
				blames: tc.blames,
			}

			if err := handleRifle(
				fghc, froc, logrus.WithField("plugin", RiflePluginName),
				&tc.reviewerCount, 0, true, false, &repo, &pr,
			); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			sort.Strings(fghc.requested)
			sort.Strings(tc.expectedRequested)
			if !reflect.DeepEqual(fghc.requested, tc.expectedRequested) {
				t.Errorf("expected %v, got %v", tc.expectedRequested, fghc.requested)
			}
		})
	}
}
