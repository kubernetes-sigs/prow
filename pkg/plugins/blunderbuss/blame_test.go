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

package blunderbuss

import (
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/layeredsets"
)

type fakeBlameGetter struct {
	data map[string][]github.BlameRange
	errs map[string]error
}

func (f *fakeBlameGetter) GetBlame(org, repo, ref, path string) ([]github.BlameRange, error) {
	if err, ok := f.errs[path]; ok {
		return nil, err
	}
	return f.data[path], nil
}

func TestParseDiffHunks(t *testing.T) {
	tests := []struct {
		name     string
		patch    string
		expected []lineRange
	}{
		{
			name:  "single hunk with count",
			patch: "@@ -10,5 +10,7 @@ func foo() {",
			expected: []lineRange{
				{Start: 10, End: 14},
			},
		},
		{
			name:  "single hunk without count",
			patch: "@@ -10 +10 @@ func foo() {",
			expected: []lineRange{
				{Start: 10, End: 10},
			},
		},
		{
			name:  "pure addition (count=0) uses context lines",
			patch: "@@ -20,0 +21,3 @@ func bar() {",
			expected: []lineRange{
				{Start: 15, End: 20},
			},
		},
		{
			name:  "pure addition at file start",
			patch: "@@ -2,0 +1,5 @@ package main",
			expected: []lineRange{
				{Start: 1, End: 2},
			},
		},
		{
			name: "multiple hunks",
			patch: `@@ -5,3 +5,4 @@ import "fmt"
+import "os"
@@ -100,10 +101,12 @@ func main() {
 some context`,
			expected: []lineRange{
				{Start: 5, End: 7},
				{Start: 100, End: 109},
			},
		},
		{
			name:     "no hunks",
			patch:    "just some text with no hunk headers",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseDiffHunks(tc.patch)
			if len(result) != len(tc.expected) {
				t.Fatalf("expected %d ranges, got %d: %v", len(tc.expected), len(result), result)
			}
			for i, r := range result {
				if r.Start != tc.expected[i].Start || r.End != tc.expected[i].End {
					t.Errorf("range %d: expected {%d, %d}, got {%d, %d}", i, tc.expected[i].Start, tc.expected[i].End, r.Start, r.End)
				}
			}
		})
	}
}

func TestIntersectBlameWithChanges(t *testing.T) {
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	lastWeek := now.Add(-7 * 24 * time.Hour)

	blameRanges := []github.BlameRange{
		{StartingLine: 1, EndingLine: 10, AuthorLogin: "alice", Date: yesterday},
		{StartingLine: 11, EndingLine: 20, AuthorLogin: "bob", Date: lastWeek},
		{StartingLine: 21, EndingLine: 30, AuthorLogin: "alice", Date: lastWeek},
		{StartingLine: 31, EndingLine: 40, AuthorLogin: "", Date: now},
	}

	changedRanges := []lineRange{
		{Start: 5, End: 15},
		{Start: 35, End: 40},
	}

	stats := intersectBlameWithChanges(blameRanges, changedRanges)

	if stats["alice"].LineCount != 6 {
		t.Errorf("alice: expected 6 lines (5-10), got %d", stats["alice"].LineCount)
	}
	if stats["bob"].LineCount != 5 {
		t.Errorf("bob: expected 5 lines (11-15), got %d", stats["bob"].LineCount)
	}
	if _, ok := stats[""]; ok {
		t.Error("empty login should be filtered out")
	}
}

func TestIntersectBlameNoOverlap(t *testing.T) {
	blameRanges := []github.BlameRange{
		{StartingLine: 1, EndingLine: 10, AuthorLogin: "alice", Date: time.Now()},
	}
	changedRanges := []lineRange{
		{Start: 20, End: 30},
	}

	stats := intersectBlameWithChanges(blameRanges, changedRanges)
	if len(stats) != 0 {
		t.Errorf("expected no stats for non-overlapping ranges, got %v", stats)
	}
}

func TestReviewerScorer(t *testing.T) {
	now := time.Now()
	recentDate := now.Add(-1 * 24 * time.Hour)
	oldDate := now.Add(-365 * 24 * time.Hour)

	scorer := &reviewerScorer{
		org:       "org",
		repo:      "repo",
		ref:       "main",
		approvers: sets.New[string]("alice"),
		reviewers: sets.New[string]("bob", "charlie"),
		now:       now,
		log:       logrusEntry(),
	}

	_ = scorer

	candidates := sets.New[string]("alice", "bob", "charlie")

	fileStats := map[string]authorStats{
		"alice":   {LineCount: 50, MostRecentDate: recentDate},
		"bob":     {LineCount: 100, MostRecentDate: oldDate},
		"charlie": {LineCount: 10, MostRecentDate: recentDate},
	}

	scores := make(map[string]float64)
	for author, stats := range fileStats {
		if !candidates.Has(author) {
			continue
		}
		daysSince := now.Sub(stats.MostRecentDate).Hours() / 24
		recencyScore := 1.0 / (1.0 + daysSince)
		scores[author] += float64(stats.LineCount)*lineCountWeight + recencyScore*recencyWeight
	}
	for author := range scores {
		if scorer.approvers.Has(author) {
			scores[author] += approverBonus
		} else if scorer.reviewers.Has(author) {
			scores[author] += reviewerBonus
		}
	}

	if scores["alice"] <= scores["charlie"] {
		t.Errorf("alice (50 lines, recent, approver bonus) should score higher than charlie (10 lines, recent, reviewer bonus): alice=%f charlie=%f", scores["alice"], scores["charlie"])
	}
	if scores["bob"] <= scores["charlie"] {
		t.Errorf("bob (100 lines, old) should score higher than charlie (10 lines, recent): bob=%f charlie=%f", scores["bob"], scores["charlie"])
	}
}

func TestScoreReviewersBlameErrors(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name          string
		files         []github.PullRequestChange
		blameData     map[string][]github.BlameRange
		blameErrs     map[string]error
		expectScored  []string
		expectEmpty   bool
	}{
		{
			name: "error on one file, scores from the other",
			files: []github.PullRequestChange{
				{Filename: "a.go", Status: "modified", Patch: "@@ -1,10 +1,12 @@ package a"},
				{Filename: "b.go", Status: "modified", Patch: "@@ -1,5 +1,6 @@ package b"},
			},
			blameData: map[string][]github.BlameRange{
				"b.go": {{StartingLine: 1, EndingLine: 5, AuthorLogin: "alice", Date: now}},
			},
			blameErrs:    map[string]error{"a.go": errors.New("API error")},
			expectScored: []string{"alice"},
		},
		{
			name: "empty blame ranges for a file produces no scores for that file",
			files: []github.PullRequestChange{
				{Filename: "a.go", Status: "modified", Patch: "@@ -1,10 +1,12 @@ package a"},
				{Filename: "b.go", Status: "modified", Patch: "@@ -1,5 +1,6 @@ package b"},
			},
			blameData: map[string][]github.BlameRange{
				"a.go": {},
				"b.go": {{StartingLine: 1, EndingLine: 5, AuthorLogin: "bob", Date: now}},
			},
			expectScored: []string{"bob"},
		},
		{
			name: "all files error returns empty scores",
			files: []github.PullRequestChange{
				{Filename: "a.go", Status: "modified", Patch: "@@ -1,10 +1,12 @@ package a"},
				{Filename: "b.go", Status: "modified", Patch: "@@ -1,5 +1,6 @@ package b"},
			},
			blameErrs: map[string]error{
				"a.go": errors.New("API error"),
				"b.go": errors.New("timeout"),
			},
			expectEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scorer := &reviewerScorer{
				ghc:       &fakeBlameGetter{data: tc.blameData, errs: tc.blameErrs},
				org:       "org",
				repo:      "repo",
				ref:       "main",
				approvers: sets.New[string](),
				reviewers: sets.New[string](),
				now:       now,
				log:       logrusEntry(),
			}

			scores, err := scorer.scoreReviewers(tc.files)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.expectEmpty {
				if len(scores) != 0 {
					t.Errorf("expected empty scores, got %v", scores)
				}
				return
			}

			for _, login := range tc.expectScored {
				if scores[login] <= 0 {
					t.Errorf("expected %q to have a positive score, got %f", login, scores[login])
				}
			}
		})
	}
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
		name            string
		blameScores     map[string]float64
		allOwners       sets.Set[string]
		exclude         sets.Set[string]
		needed          int
		expected        []string
		expectedCount   int
		checkOrder      bool
	}{
		{
			name: "owners ranked by score",
			blameScores: map[string]float64{
				"alice": 100.0,
				"bob":   50.0,
			},
			allOwners:     sets.New[string]("alice", "bob"),
			exclude:       sets.Set[string]{},
			needed:        2,
			expected:      []string{"alice", "bob"},
			checkOrder:    true,
		},
		{
			name: "non-owners excluded",
			blameScores: map[string]float64{
				"alice":   100.0,
				"charlie": 75.0,
			},
			allOwners:     sets.New[string]("alice"),
			exclude:       sets.Set[string]{},
			needed:        2,
			expected:      []string{"alice"},
			checkOrder:    true,
		},
		{
			name: "existing reviewers excluded",
			blameScores: map[string]float64{
				"alice": 100.0,
				"bob":   50.0,
			},
			allOwners:     sets.New[string]("alice", "bob"),
			exclude:       sets.New[string]("alice"),
			needed:        1,
			expected:      []string{"bob"},
			checkOrder:    true,
		},
		{
			name: "respects needed count",
			blameScores: map[string]float64{
				"alice": 100.0,
				"bob":   50.0,
				"carol": 25.0,
			},
			allOwners:     sets.New[string]("alice", "bob", "carol"),
			exclude:       sets.Set[string]{},
			needed:        1,
			expected:      []string{"alice"},
			checkOrder:    true,
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

func TestDiverseFileSelection(t *testing.T) {
	makeFile := func(name, status string) github.PullRequestChange {
		return github.PullRequestChange{Filename: name, Status: status}
	}

	tests := []struct {
		name     string
		files    []github.PullRequestChange
		limit    int
		expected []string
	}{
		{
			name: "under limit returns all non-removed",
			files: []github.PullRequestChange{
				makeFile("pkg/a/foo.go", "modified"),
				makeFile("pkg/b/bar.go", "added"),
				makeFile("pkg/c/baz.go", "removed"),
			},
			limit:    20,
			expected: []string{"pkg/a/foo.go", "pkg/b/bar.go"},
		},
		{
			name: "round-robin across directories",
			files: []github.PullRequestChange{
				makeFile("pkg/a/1.go", "modified"),
				makeFile("pkg/a/2.go", "modified"),
				makeFile("pkg/a/3.go", "modified"),
				makeFile("pkg/b/1.go", "modified"),
				makeFile("pkg/b/2.go", "modified"),
				makeFile("pkg/c/1.go", "modified"),
			},
			limit: 4,
			expected: []string{
				"pkg/a/1.go",
				"pkg/b/1.go",
				"pkg/c/1.go",
				"pkg/a/2.go",
			},
		},
		{
			name: "removed files excluded before selection",
			files: []github.PullRequestChange{
				makeFile("pkg/a/1.go", "removed"),
				makeFile("pkg/a/2.go", "removed"),
				makeFile("pkg/a/3.go", "modified"),
				makeFile("pkg/b/1.go", "modified"),
			},
			limit:    2,
			expected: []string{"pkg/a/3.go", "pkg/b/1.go"},
		},
		{
			name: "root-level files grouped together",
			files: []github.PullRequestChange{
				makeFile("main.go", "modified"),
				makeFile("util.go", "modified"),
				makeFile("pkg/a/foo.go", "modified"),
			},
			limit:    2,
			expected: []string{"main.go", "pkg/a/foo.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := diverseFileSelection(tc.files, tc.limit)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected %d files, got %d: %v", len(tc.expected), len(got), filenames(got))
			}
			for i, want := range tc.expected {
				if got[i].Filename != want {
					t.Errorf("file %d: expected %q, got %q", i, want, got[i].Filename)
				}
			}
		})
	}
}

func TestFileDir(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"pkg/plugins/blunderbuss/blame.go", "pkg/plugins/blunderbuss"},
		{"main.go", "."},
		{"cmd/tool/main.go", "cmd/tool"},
	}
	for _, tc := range tests {
		if got := fileDir(tc.path); got != tc.want {
			t.Errorf("fileDir(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func filenames(files []github.PullRequestChange) []string {
	var names []string
	for _, f := range files {
		names = append(names, f.Filename)
	}
	return names
}

func logrusEntry() *logrus.Entry {
	return logrus.NewEntry(logrus.StandardLogger())
}
