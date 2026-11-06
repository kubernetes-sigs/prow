/*
Copyright The Kubernetes Authors.

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

package reviewer

import (
	"reflect"
	"sort"
	"testing"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/layeredsets"
)

type fakeOwners struct {
	owners            map[string]string
	reviewers         map[string]layeredsets.String
	approvers         map[string]layeredsets.String
	leafReviewers     map[string]sets.Set[string]
	requiredReviewers map[string]sets.Set[string]
}

func (f *fakeOwners) FindReviewersOwnersForFile(path string) string { return f.owners[path] }
func (f *fakeOwners) Reviewers(path string) layeredsets.String      { return f.reviewers[path] }
func (f *fakeOwners) RequiredReviewers(path string) sets.Set[string] {
	return f.requiredReviewers[path]
}
func (f *fakeOwners) LeafReviewers(path string) sets.Set[string]   { return f.leafReviewers[path] }
func (f *fakeOwners) FindApproverOwnersForFile(path string) string { return f.owners[path] }
func (f *fakeOwners) Approvers(path string) layeredsets.String     { return f.approvers[path] }
func (f *fakeOwners) LeafApprovers(path string) sets.Set[string]   { return nil }
func (f *fakeOwners) AllOwners() sets.Set[string]                  { return nil }

func changedFiles(files ...string) []github.PullRequestChange {
	changes := make([]github.PullRequestChange, 0, len(files))
	for _, f := range files {
		changes = append(changes, github.PullRequestChange{Filename: f})
	}
	return changes
}

func TestEligibleRequestedReviewers(t *testing.T) {
	oc := &fakeOwners{
		owners: map[string]string{
			"a.go": "a",
			"b.go": "b",
		},
		reviewers: map[string]layeredsets.String{
			"a.go": layeredsets.NewString("rev1", "rev2"),
			"b.go": layeredsets.NewString("rev3"),
		},
		approvers: map[string]layeredsets.String{
			"a.go": layeredsets.NewString("app1"),
		},
	}

	testcases := []struct {
		name             string
		requested        []string
		files            []string
		includeApprovers bool
		expected         sets.Set[string]
	}{
		{
			name:     "no requested reviewers",
			files:    []string{"a.go"},
			expected: sets.New[string](),
		},
		{
			name:      "requested reviewer in pool counts",
			requested: []string{"rev1"},
			files:     []string{"a.go"},
			expected:  sets.New[string]("rev1"),
		},
		{
			name:      "requested reviewer outside pool is ignored",
			requested: []string{"stranger"},
			files:     []string{"a.go"},
			expected:  sets.New[string](),
		},
		{
			name:      "only reviewers of changed files count",
			requested: []string{"rev3"},
			files:     []string{"a.go"},
			expected:  sets.New[string](),
		},
		{
			name:      "pool is the union over all changed files",
			requested: []string{"rev1", "rev3"},
			files:     []string{"a.go", "b.go"},
			expected:  sets.New[string]("rev1", "rev3"),
		},
		{
			name:             "approver counts when approvers are included",
			requested:        []string{"app1"},
			files:            []string{"a.go"},
			includeApprovers: true,
			expected:         sets.New[string]("app1"),
		},
		{
			name:      "approver does not count when approvers are excluded",
			requested: []string{"app1"},
			files:     []string{"a.go"},
			expected:  sets.New[string](),
		},
		{
			name:      "requested login case is normalized",
			requested: []string{"Rev1"},
			files:     []string{"a.go"},
			expected:  sets.New[string]("rev1"),
		},
		{
			name:      "file without OWNERS entries yields no pool",
			requested: []string{"rev1"},
			files:     []string{"unknown.go"},
			expected:  sets.New[string](),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			pr := &github.PullRequest{}
			for _, login := range tc.requested {
				pr.RequestedReviewers = append(pr.RequestedReviewers, github.User{Login: login})
			}
			actual := EligibleRequestedReviewers(oc, pr, changedFiles(tc.files...), tc.includeApprovers)
			if !actual.Equal(tc.expected) {
				t.Errorf("expected eligible reviewers %v, got %v", sets.List(tc.expected), sets.List(actual))
			}
		})
	}
}

func TestGetReviewersExcluded(t *testing.T) {
	oc := &fakeOwners{
		owners: map[string]string{
			"a.go": "a",
		},
		reviewers: map[string]layeredsets.String{
			"a.go": layeredsets.NewString("alice", "bob", "carl", "dave"),
		},
		leafReviewers: map[string]sets.Set[string]{
			"a.go": sets.New[string]("alice", "bob", "carl"),
		},
		requiredReviewers: map[string]sets.Set[string]{
			"a.go": sets.New[string]("req1"),
		},
	}
	selector := func(candidates *layeredsets.String) string { return candidates.PopRandom() }
	log := logrus.WithField("plugin", "test")

	testcases := []struct {
		name             string
		minReviewers     int
		excluded         sets.Set[string]
		expected         []string
		expectedRequired []string
	}{
		{
			name:             "excluded reviewers are never selected",
			minReviewers:     2,
			excluded:         sets.New[string]("bob"),
			expected:         []string{"carl", "dave"},
			expectedRequired: []string{"req1"},
		},
		{
			name:             "nil excluded set only filters the author",
			minReviewers:     3,
			excluded:         nil,
			expected:         []string{"bob", "carl", "dave"},
			expectedRequired: []string{"req1"},
		},
		{
			name:             "exclusion can exhaust the pool",
			minReviewers:     3,
			excluded:         sets.New[string]("bob", "carl", "dave"),
			expected:         nil,
			expectedRequired: []string{"req1"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			reviewers, required, err := GetReviewers(oc, selector, log, "alice", changedFiles("a.go"), tc.minReviewers, tc.excluded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			sort.Strings(reviewers)
			if !reflect.DeepEqual(reviewers, tc.expected) {
				t.Errorf("expected reviewers %v, got %v", tc.expected, reviewers)
			}
			if !reflect.DeepEqual(required, tc.expectedRequired) {
				t.Errorf("expected required reviewers %v, got %v", tc.expectedRequired, required)
			}
		})
	}
}
