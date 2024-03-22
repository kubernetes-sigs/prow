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

package blockers

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestParseBranches(t *testing.T) {
	tcs := []struct {
		text     string
		expected []string
	}{
		{
			text:     "",
			expected: nil,
		},
		{
			text:     "BAD THINGS (all branches blocked)",
			expected: nil,
		},
		{
			text:     "branch:foo",
			expected: []string{"foo"},
		},
		{
			text:     "branch: foo-bar",
			expected: []string{"foo-bar"},
		},
		{
			text:     "BAD THINGS (BLOCKING BRANCH:foo branch:bar) AHHH",
			expected: []string{"foo", "bar"},
		},
		{
			text:     "branch:\"FOO-bar\"",
			expected: []string{"FOO-bar"},
		},
		{
			text:     "branch: \"foo\" branch: \"bar\"",
			expected: []string{"foo", "bar"},
		},
	}

	for _, tc := range tcs {
		if got := parseBranches(tc.text); !reflect.DeepEqual(got, tc.expected) {
			t.Errorf("Expected parseBranches(%q)==%q, but got %q.", tc.text, tc.expected, got)
		}
	}
}

func TestBlockerQuery(t *testing.T) {
	tcs := []struct {
		orgRepoQuery string
		expected     sets.Set[string]
	}{
		{
			orgRepoQuery: "org:\"k8s\"",
			expected: sets.New[string](
				"is:issue",
				"state:open",
				"label:\"blocker\"",
				"org:\"k8s\"",
			),
		},
		{
			orgRepoQuery: "repo:\"k8s/t-i\"",
			expected: sets.New[string](
				"is:issue",
				"state:open",
				"label:\"blocker\"",
				"repo:\"k8s/t-i\"",
			),
		},
		{
			orgRepoQuery: "org:\"k8s\" org:\"kuber\"",
			expected: sets.New[string](
				"is:issue",
				"state:open",
				"label:\"blocker\"",
				"org:\"k8s\"",
				"org:\"kuber\"",
			),
		},
		{
			orgRepoQuery: "repo:\"k8s/t-i\" repo:\"k8s/k8s\"",
			expected: sets.New[string](
				"is:issue",
				"state:open",
				"label:\"blocker\"",
				"repo:\"k8s/t-i\"",
				"repo:\"k8s/k8s\"",
			),
		},
		{
			orgRepoQuery: "org:\"k8s\" org:\"kuber\" repo:\"k8s/t-i\" repo:\"k8s/k8s\"",
			expected: sets.New[string](
				"is:issue",
				"state:open",
				"label:\"blocker\"",
				"repo:\"k8s/t-i\"",
				"repo:\"k8s/k8s\"",
				"org:\"k8s\"",
				"org:\"kuber\"",
			),
		},
	}

	for _, tc := range tcs {
		got := sets.New[string](blockerQuery("blocker", tc.orgRepoQuery)...)
		if diff := cmp.Diff(got, tc.expected); diff != "" {
			t.Errorf("Actual result differs from expected: %s", diff)
		}
	}
}

func testIssue(number int, title, org, repo string) Issue {
	return Issue{
		Number: githubql.Int(number),
		Title:  githubql.String(title),
		URL:    githubql.String(strconv.Itoa(number)),
		Repository: struct {
			Name  githubql.String
			Owner struct {
				Login githubql.String
			}
		}{
			Name: githubql.String(repo),
			Owner: struct {
				Login githubql.String
			}{
				Login: githubql.String(org),
			},
		},
	}
}

func TestBlockers(t *testing.T) {
	type check struct {
		org, repo, branch string
		blockers          sets.Set[int]
	}

	tcs := []struct {
		name   string
		issues []Issue
		checks []check
	}{
		{
			name:   "No blocker issues",
			issues: []Issue{},
			checks: []check{
				{
					org:      "org",
					repo:     "repo",
					branch:   "branch",
					blockers: sets.New[int](),
				},
			},
		},
		{
			name: "1 repo blocker",
			issues: []Issue{
				testIssue(5, "BLOCK THE WHOLE REPO!", "k", "t-i"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "feature",
					blockers: sets.New[int](5),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "master",
					blockers: sets.New[int](5),
				},
				{
					org:      "k",
					repo:     "k",
					branch:   "master",
					blockers: sets.New[int](),
				},
			},
		},
		{
			name: "1 repo blocker for a branch",
			issues: []Issue{
				testIssue(6, "BLOCK THE release-1.11 BRANCH! branch:release-1.11", "k", "t-i"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "release-1.11",
					blockers: sets.New[int](6),
				},
			},
		},
		{
			name: "1 repo blocker for a branch",
			issues: []Issue{
				testIssue(6, "BLOCK THE slash/in/name BRANCH! branch:slash/in/name", "k", "t-i"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "slash/in/name",
					blockers: sets.New[int](6),
				},
			},
		},
		{
			name: "2 repo blockers for same repo",
			issues: []Issue{
				testIssue(5, "BLOCK THE WHOLE REPO!", "k", "t-i"),
				testIssue(6, "BLOCK THE WHOLE REPO AGAIN!", "k", "t-i"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "feature",
					blockers: sets.New[int](5, 6),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "master",
					blockers: sets.New[int](5, 6),
				},
				{
					org:      "k",
					repo:     "k",
					branch:   "master",
					blockers: sets.New[int](),
				},
			},
		},
		{
			name: "2 repo blockers for different repos",
			issues: []Issue{
				testIssue(5, "BLOCK THE WHOLE REPO!", "k", "t-i"),
				testIssue(6, "BLOCK THE WHOLE (different) REPO!", "k", "community"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "feature",
					blockers: sets.New[int](5),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "master",
					blockers: sets.New[int](5),
				},
				{
					org:      "k",
					repo:     "community",
					branch:   "feature",
					blockers: sets.New[int](6),
				},
				{
					org:      "k",
					repo:     "community",
					branch:   "master",
					blockers: sets.New[int](6),
				},
				{
					org:      "k",
					repo:     "k",
					branch:   "master",
					blockers: sets.New[int](),
				},
			},
		},
		{
			name: "1 repo blocker, 1 branch blocker for different repos",
			issues: []Issue{
				testIssue(5, "BLOCK THE WHOLE REPO!", "k", "t-i"),
				testIssue(6, "BLOCK THE feature BRANCH! branch:feature", "k", "community"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "feature",
					blockers: sets.New[int](5),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "master",
					blockers: sets.New[int](5),
				},
				{
					org:      "k",
					repo:     "community",
					branch:   "feature",
					blockers: sets.New[int](6),
				},
				{
					org:      "k",
					repo:     "community",
					branch:   "master",
					blockers: sets.New[int](),
				},
				{
					org:      "k",
					repo:     "k",
					branch:   "master",
					blockers: sets.New[int](),
				},
			},
		},
		{
			name: "1 repo blocker, 1 branch blocker for same repo",
			issues: []Issue{
				testIssue(5, "BLOCK THE WHOLE REPO!", "k", "t-i"),
				testIssue(6, "BLOCK THE feature BRANCH! branch:feature", "k", "t-i"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "feature",
					blockers: sets.New[int](5, 6),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "master",
					blockers: sets.New[int](5),
				},
				{
					org:      "k",
					repo:     "k",
					branch:   "master",
					blockers: sets.New[int](),
				},
			},
		},
		{
			name: "2 repo blockers, 3 branch blockers (with overlap) for same repo",
			issues: []Issue{
				testIssue(5, "BLOCK THE WHOLE REPO!", "k", "t-i"),
				testIssue(6, "BLOCK THE WHOLE REPO AGAIN!", "k", "t-i"),
				testIssue(7, "BLOCK THE feature BRANCH! branch:feature", "k", "t-i"),
				testIssue(8, "BLOCK THE feature BRANCH! branch:master", "k", "t-i"),
				testIssue(9, "BLOCK THE feature BRANCH! branch:feature branch: master branch:foo", "k", "t-i"),
			},
			checks: []check{
				{
					org:      "k",
					repo:     "t-i",
					branch:   "feature",
					blockers: sets.New[int](5, 6, 7, 9),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "master",
					blockers: sets.New[int](5, 6, 8, 9),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "foo",
					blockers: sets.New[int](5, 6, 9),
				},
				{
					org:      "k",
					repo:     "t-i",
					branch:   "bar",
					blockers: sets.New[int](5, 6),
				},
				{
					org:      "k",
					repo:     "k",
					branch:   "master",
					blockers: sets.New[int](),
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Logf("Running test case %q.", tc.name)
		b := fromIssues(tc.issues, logrus.WithField("test", tc.name))
		for _, c := range tc.checks {
			actuals := b.GetApplicable(c.org, c.repo, c.branch)
			nums := sets.New[int]()
			for _, actual := range actuals {
				// Check blocker URLs:
				if actual.URL != strconv.Itoa(actual.Number) {
					t.Errorf("blocker %d has URL %q, expected %q", actual.Number, actual.URL, strconv.Itoa(actual.Number))
				}
				nums.Insert(actual.Number)
			}
			// Check that correct blockers were selected:
			if !reflect.DeepEqual(nums, c.blockers) {
				t.Errorf("expected blockers %v, but got %v", c.blockers, nums)
			}
		}
	}
}

type fakeGitHubClient struct {
	lock    sync.Mutex
	queries map[string][]string
}

func (fghc *fakeGitHubClient) QueryWithGitHubAppsSupport(ctx context.Context, q interface{}, vars map[string]interface{}, org string) error {
	if query := vars["query"]; query == nil || string(query.(githubql.String)) == "" {
		return fmt.Errorf("query variable was unset, variables: %+v", vars)
	}

	fghc.lock.Lock()
	defer fghc.lock.Unlock()

	if fghc.queries == nil {
		fghc.queries = map[string][]string{}
	}
	fghc.queries[org] = append(fghc.queries[org], string(vars["query"].(githubql.String)))

	return nil
}

func TestBlockersFindAll(t *testing.T) {
	t.Parallel()

	orgRepoTokensByOrg := map[string]string{
		"org-a": `org:"org-a" -repo:"org-a/repo-b"`,
		"org-b": `org:"org-b" -repo:"org-b/repo-b"`,
	}
	const blockerLabel = "tide/merge-blocker"
	testCases := []struct {
		name         string
		usesAppsAuth bool

		expectedQueries map[string][]string
	}{
		{
			name:         "Apps auth, query is split by org",
			usesAppsAuth: true,
			expectedQueries: map[string][]string{
				"org-a": {`-repo:"org-a/repo-b" is:issue label:"tide/merge-blocker" org:"org-a" state:open`},
				"org-b": {`-repo:"org-b/repo-b" is:issue label:"tide/merge-blocker" org:"org-b" state:open`},
			},
		},
		{
			name:         "No apps auth, one query",
			usesAppsAuth: false,
			expectedQueries: map[string][]string{
				"": {`-repo:"org-a/repo-b" -repo:"org-b/repo-b" is:issue label:"tide/merge-blocker" org:"org-a" org:"org-b" state:open`},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGitHubClient{}

			if _, err := FindAll(ghc, logrus.WithField("tc", tc.name), blockerLabel, orgRepoTokensByOrg, tc.usesAppsAuth); err != nil {
				t.Fatalf("FindAll: %v", err)
			}

			if diff := cmp.Diff(ghc.queries, tc.expectedQueries, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("actual queries differ from expected: %v", diff)
			}
		})
	}
}
