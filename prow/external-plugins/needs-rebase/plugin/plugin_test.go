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

package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/labels"
	"k8s.io/test-infra/prow/plugins"
)

func testKey(org, repo string, num int) string {
	return fmt.Sprintf("%s/%s#%d", org, repo, num)
}

type fghc struct {
	allPRs []struct {
		PullRequest pullRequest `graphql:"... on PullRequest"`
	}
	pr *github.PullRequest

	initialLabels []github.Label
	mergeable     bool

	// The following are maps are keyed using 'testKey'
	commentCreated, commentDeleted       map[string]bool
	IssueLabelsAdded, IssueLabelsRemoved map[string][]string
}

func newFakeClient(prs []pullRequest, initialLabels []string, mergeable bool, pr *github.PullRequest) *fghc {
	f := &fghc{
		mergeable:          mergeable,
		commentCreated:     make(map[string]bool),
		commentDeleted:     make(map[string]bool),
		IssueLabelsAdded:   make(map[string][]string),
		IssueLabelsRemoved: make(map[string][]string),
		pr:                 pr,
	}
	for _, pr := range prs {
		s := struct {
			PullRequest pullRequest `graphql:"... on PullRequest"`
		}{pr}
		f.allPRs = append(f.allPRs, s)
	}
	for _, label := range initialLabels {
		f.initialLabels = append(f.initialLabels, github.Label{Name: label})
	}
	return f
}

func (f *fghc) GetIssueLabels(org, repo string, number int) ([]github.Label, error) {
	return f.initialLabels, nil
}

func (f *fghc) CreateCommentWithContext(_ context.Context, org, repo string, number int, comment string) error {
	f.commentCreated[testKey(org, repo, number)] = true
	return nil
}

func (f *fghc) BotUserChecker() (func(candidate string) bool, error) {
	return func(candidate string) bool { return candidate == "k8s-ci-robot" }, nil
}

func (f *fghc) AddLabelWithContext(_ context.Context, org, repo string, number int, label string) error {
	key := testKey(org, repo, number)
	f.IssueLabelsAdded[key] = append(f.IssueLabelsAdded[key], label)
	return nil
}

func (f *fghc) RemoveLabelWithContext(_ context.Context, org, repo string, number int, label string) error {
	key := testKey(org, repo, number)
	f.IssueLabelsRemoved[key] = append(f.IssueLabelsRemoved[key], label)
	return nil
}

func (f *fghc) IsMergeable(org, repo string, number int, sha string) (bool, error) {
	return f.mergeable, nil
}

func (f *fghc) DeleteStaleCommentsWithContext(_ context.Context, org, repo string, number int, comments []github.IssueComment, isStale func(github.IssueComment) bool) error {
	f.commentDeleted[testKey(org, repo, number)] = true
	return nil
}

func (f *fghc) QueryWithGitHubAppsSupport(_ context.Context, q interface{}, _ map[string]interface{}, _ string) error {
	query, ok := q.(*searchQuery)
	if !ok {
		return errors.New("invalid query format")
	}
	query.Search.Nodes = f.allPRs
	return nil
}

func (f *fghc) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	if f.pr != nil {
		return f.pr, nil
	}
	return nil, fmt.Errorf("didn't find pull request %s/%s#%d", org, repo, number)
}

func (f *fghc) compareExpected(t *testing.T, org, repo string, num int, expectedAdded []string, expectedRemoved []string, expectComment bool, expectDeletion bool) {
	key := testKey(org, repo, num)
	sort.Strings(expectedAdded)
	sort.Strings(expectedRemoved)
	sort.Strings(f.IssueLabelsAdded[key])
	sort.Strings(f.IssueLabelsRemoved[key])
	if !reflect.DeepEqual(expectedAdded, f.IssueLabelsAdded[key]) {
		t.Errorf("Expected the following labels to be added to %s: %q, but got %q.", key, expectedAdded, f.IssueLabelsAdded[key])
	}
	if !reflect.DeepEqual(expectedRemoved, f.IssueLabelsRemoved[key]) {
		t.Errorf("Expected the following labels to be removed from %s: %q, but got %q.", key, expectedRemoved, f.IssueLabelsRemoved[key])
	}
	if expectComment && !f.commentCreated[key] {
		t.Errorf("Expected a comment to be created on %s, but none was.", key)
	} else if !expectComment && f.commentCreated[key] {
		t.Errorf("Unexpected comment on %s.", key)
	}
	if expectDeletion && !f.commentDeleted[key] {
		t.Errorf("Expected a comment to be deleted from %s, but none was.", key)
	} else if !expectDeletion && f.commentDeleted[key] {
		t.Errorf("Unexpected comment deletion on %s.", key)
	}
}

func TestMain(m *testing.M) {
	sleep = func(time.Duration) {}
	code := m.Run()
	os.Exit(code)
}

func TestHandleIssueCommentEvent(t *testing.T) {
	t.Parallel()
	pr := func() *github.PullRequest {
		pr := github.PullRequest{
			Base: github.PullRequestBranch{
				Repo: github.Repo{
					Name:  "repo",
					Owner: github.User{Login: "org"},
				},
			},
			Number: 5,
		}
		return &pr
	}

	testCases := []struct {
		name string
		pr   *github.PullRequest

		mergeable bool
		labels    []string
		state     string
		merged    bool

		expectedAdded   []string
		expectedRemoved []string
		expectComment   bool
		expectDeletion  bool
	}{
		{
			name: "No pull request, ignoring",
		},
		{
			name:      "mergeable no-op",
			pr:        pr(),
			mergeable: true,
			labels:    []string{labels.LGTM, labels.Approved},
			state:     github.PullRequestStateOpen,
		},
		{
			name:      "unmergeable no-op",
			pr:        pr(),
			mergeable: false,
			labels:    []string{labels.LGTM, labels.Approved, labels.NeedsRebase},
			state:     github.PullRequestStateOpen,
		},
		{
			name:      "mergeable -> unmergeable",
			pr:        pr(),
			mergeable: false,
			labels:    []string{labels.LGTM, labels.Approved},
			state:     github.PullRequestStateOpen,

			expectedAdded: []string{labels.NeedsRebase},
			expectComment: true,
		},
		{
			name:      "unmergeable -> mergeable",
			pr:        pr(),
			mergeable: true,
			labels:    []string{labels.LGTM, labels.Approved, labels.NeedsRebase},
			state:     github.PullRequestStateOpen,

			expectedRemoved: []string{labels.NeedsRebase},
			expectDeletion:  true,
		},
		{
			name:      "merged pr is ignored",
			pr:        pr(),
			mergeable: false,
			state:     github.PullRequestStateClosed,
			merged:    true,
		},
		{
			name:      "closed pr is ignored",
			pr:        pr(),
			mergeable: false,
			state:     github.PullRequestStateClosed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeClient(nil, tc.labels, tc.mergeable, tc.pr)
			ice := &github.IssueCommentEvent{}
			if tc.pr != nil {
				ice.Issue.PullRequest = &struct{}{}
				tc.pr.Merged = tc.merged
				tc.pr.State = tc.state
			}
			cache := NewCache(0)
			if err := HandleIssueCommentEvent(logrus.WithField("plugin", PluginName), fake, ice, cache); err != nil {
				t.Fatalf("error handling issue comment event: %v", err)
			}
			fake.compareExpected(t, "org", "repo", 5, tc.expectedAdded, tc.expectedRemoved, tc.expectComment, tc.expectDeletion)
		})
	}
}

func TestHandlePullRequestEvent(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string

		mergeable bool
		labels    []string
		state     string
		merged    bool

		expectedAdded   []string
		expectedRemoved []string
		expectComment   bool
		expectDeletion  bool
	}{
		{
			name:      "mergeable no-op",
			mergeable: true,
			labels:    []string{labels.LGTM, labels.Approved},
			state:     github.PullRequestStateOpen,
		},
		{
			name:      "unmergeable no-op",
			mergeable: false,
			labels:    []string{labels.LGTM, labels.Approved, labels.NeedsRebase},
			state:     github.PullRequestStateOpen,
		},
		{
			name:      "mergeable -> unmergeable",
			mergeable: false,
			labels:    []string{labels.LGTM, labels.Approved},
			state:     github.PullRequestStateOpen,

			expectedAdded: []string{labels.NeedsRebase},
			expectComment: true,
		},
		{
			name:      "unmergeable -> mergeable",
			mergeable: true,
			labels:    []string{labels.LGTM, labels.Approved, labels.NeedsRebase},
			state:     github.PullRequestStateOpen,

			expectedRemoved: []string{labels.NeedsRebase},
			expectDeletion:  true,
		},
		{
			name:   "merged pr is ignored",
			merged: true,
			state:  github.PullRequestStateClosed,
		},
		{
			name:  "closed pr is ignored",
			state: github.PullRequestStateClosed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeClient(nil, tc.labels, tc.mergeable, nil)
			pre := &github.PullRequestEvent{
				Action: github.PullRequestActionSynchronize,
				PullRequest: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Name:  "repo",
							Owner: github.User{Login: "org"},
						},
					},
					Merged: tc.merged,
					State:  tc.state,
					Number: 5,
				},
			}
			t.Logf("Running test scenario: %q", tc.name)
			if err := HandlePullRequestEvent(logrus.WithField("plugin", PluginName), fake, pre); err != nil {
				t.Fatalf("Unexpected error handling event: %v.", err)
			}
			fake.compareExpected(t, "org", "repo", 5, tc.expectedAdded, tc.expectedRemoved, tc.expectComment, tc.expectDeletion)
		})
	}
}

func TestHandleAll(t *testing.T) {
	t.Parallel()
	testPRs := []struct {
		name string

		labels    []string
		mergeable bool
		state     githubql.PullRequestState

		expectedAdded, expectedRemoved []string
		expectComment, expectDeletion  bool
	}{
		{
			name:      "PR State Merged",
			mergeable: false,
			state:     githubql.PullRequestStateMerged,
			labels:    []string{labels.LGTM, labels.Approved},
		},
		{
			name:      "PR State Closed",
			mergeable: false,
			state:     githubql.PullRequestStateClosed,
			labels:    []string{labels.LGTM, labels.Approved},
		},
		{
			name:      "PR State Closed with need-rebase label",
			mergeable: false,
			state:     githubql.PullRequestStateClosed,
			labels:    []string{labels.LGTM, labels.Approved, labels.NeedsRebase},
		},
		{
			name:      "PR State Open with non-mergeable",
			mergeable: false,
			state:     githubql.PullRequestStateOpen,
			labels:    []string{labels.LGTM, labels.Approved},

			expectedAdded: []string{labels.NeedsRebase},
			expectComment: true,
		},
		{
			name:      "PR State Open with mergeable",
			mergeable: true,
			state:     githubql.PullRequestStateOpen,
			labels:    []string{labels.LGTM, labels.Approved, labels.NeedsRebase},

			expectedRemoved: []string{labels.NeedsRebase},
			expectDeletion:  true,
		},
	}

	prs := []pullRequest{}
	for i, testPR := range testPRs {
		pr := pullRequest{
			Number: githubql.Int(i),
			State:  testPR.state,
		}
		if testPR.mergeable {
			pr.Mergeable = githubql.MergeableStateMergeable
		} else {
			pr.Mergeable = githubql.MergeableStateConflicting
		}
		for _, label := range testPR.labels {
			s := struct {
				Name githubql.String
			}{
				Name: githubql.String(label),
			}
			pr.Labels.Nodes = append(pr.Labels.Nodes, s)
		}
		prs = append(prs, pr)
	}
	fake := newFakeClient(prs, nil, false, nil)
	config := &plugins.Configuration{
		Plugins: plugins.Plugins{"/": {Plugins: []string{labels.LGTM, PluginName}}},

		ExternalPlugins: map[string][]plugins.ExternalPlugin{"/": {{Name: PluginName}}},
	}
	issueCache := NewFakeCache(0)

	if err := HandleAll(logrus.WithField("plugin", PluginName), fake, config, false, issueCache); err != nil {
		t.Fatalf("Unexpected error handling all prs: %v.", err)
	}
	for i, pr := range testPRs {
		t.Run(pr.name, func(t *testing.T) {
			fake.compareExpected(t, "", "", i, pr.expectedAdded, pr.expectedRemoved, pr.expectComment, pr.expectDeletion)
		})
	}
}

func TestConstructQueries(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		orgs         []string
		repos        []string
		usesAppsAuth bool

		expected map[string][]string
	}{
		{
			name: "Kubernetes and Kubernetes-Sigs org, no repos",
			orgs: []string{"kubernetes", "kubernetes-sigs"},

			expected: map[string][]string{
				"kubernetes": {
					`archived:false is:pr is:open org:"kubernetes" -repo:"kubernetes/kubernetes"`,
					`archived:false is:pr is:open repo:"kubernetes/kubernetes" created:>=0000-11-02`,
					`archived:false is:pr is:open repo:"kubernetes/kubernetes" created:<0000-11-02`,
				},
				"kubernetes-sigs": {`archived:false is:pr is:open org:"kubernetes-sigs"`},
			},
		},
		{
			name:         "Kubernetes and Kubernetes-Sigs org, no repos, apps auth",
			orgs:         []string{"kubernetes", "kubernetes-sigs"},
			usesAppsAuth: true,

			expected: map[string][]string{
				"kubernetes": {
					`archived:false is:pr is:open org:"kubernetes" -repo:"kubernetes/kubernetes"`,
					`archived:false is:pr is:open repo:"kubernetes/kubernetes" created:>=0000-11-02`,
					`archived:false is:pr is:open repo:"kubernetes/kubernetes" created:<0000-11-02`,
				},
				"kubernetes-sigs": {`archived:false is:pr is:open org:"kubernetes-sigs"`},
			},
		},
		{
			name: "Other orgs, no repos",
			orgs: []string{"other", "other-sigs"},

			expected: map[string][]string{
				"other":      {`archived:false is:pr is:open org:"other"`},
				"other-sigs": {`archived:false is:pr is:open org:"other-sigs"`},
			},
		},
		{
			name:         "Other org, no repos, apps auth",
			orgs:         []string{"other", "other-sigs"},
			usesAppsAuth: true,

			expected: map[string][]string{
				"other":      {`archived:false is:pr is:open org:"other"`},
				"other-sigs": {`archived:false is:pr is:open org:"other-sigs"`},
			},
		},
		{
			name:  "Some repos, no orgs",
			repos: []string{"org/repo", "other/repo"},

			expected: map[string][]string{"": {`archived:false is:pr is:open repo:"org/repo" repo:"other/repo"`}},
		},
		{
			name:         "Some repos, no orgs, apps auth",
			repos:        []string{"org/repo", "other/repo"},
			usesAppsAuth: true,

			expected: map[string][]string{
				"org":   {`archived:false is:pr is:open repo:"org/repo"`},
				"other": {`archived:false is:pr is:open repo:"other/repo"`},
			},
		},
		{
			name:  "Invalid repo is ignored",
			repos: []string{"repo"},
		},
		{
			name:  "Org and repo in that org, repo is ignored",
			orgs:  []string{"org"},
			repos: []string{"org/repo"},

			expected: map[string][]string{"org": {`archived:false is:pr is:open org:"org"`}},
		},
		{
			name:  "Org and repo in that org, repo is ignored, apps auth",
			orgs:  []string{"org"},
			repos: []string{"org/repo"},

			expected: map[string][]string{"org": {`archived:false is:pr is:open org:"org"`}},
		},
		{
			name:  "Some orgs and some repos",
			orgs:  []string{"org", "other"},
			repos: []string{"repoorg/repo", "otherrepoorg/repo"},

			expected: map[string][]string{
				"":      {`archived:false is:pr is:open repo:"repoorg/repo" repo:"otherrepoorg/repo"`},
				"org":   {`archived:false is:pr is:open org:"org"`},
				"other": {`archived:false is:pr is:open org:"other"`},
			},
		},
		{
			name:         "Some orgs and some repos, apps auth",
			orgs:         []string{"org", "other"},
			repos:        []string{"repoorg/repo", "otherrepoorg/repo"},
			usesAppsAuth: true,

			expected: map[string][]string{
				"org":          {`archived:false is:pr is:open org:"org"`},
				"other":        {`archived:false is:pr is:open org:"other"`},
				"otherrepoorg": {`archived:false is:pr is:open repo:"otherrepoorg/repo"`},
				"repoorg":      {`archived:false is:pr is:open repo:"repoorg/repo"`},
			},
		},
		{
			name:  "Multiple repos in the same org",
			repos: []string{"org/a", "org/b"},

			expected: map[string][]string{"": {`archived:false is:pr is:open repo:"org/a" repo:"org/b"`}},
		},
		{
			name:         "Multiple repos in the same org, apps auth",
			repos:        []string{"org/a", "org/b"},
			usesAppsAuth: true,

			expected: map[string][]string{"org": {`archived:false is:pr is:open repo:"org/a" repo:"org/b"`}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := constructQueries(logrus.WithField("test", tc.name), time.Time{}, tc.orgs, tc.repos, tc.usesAppsAuth)
			if diff := cmp.Diff(tc.expected, result, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("expected result differs from actual: %s", diff)
			}
		})
	}
}

func NewFakeCache(validTime time.Duration) *Cache {
	return &Cache{
		cache:       make(map[int]time.Time),
		validTime:   time.Second * validTime,
		currentTime: getFakeTime(),
	}
}

func getFakeTime() timeNow {
	var i = 0
	now := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		i = i + 1
		return now.Add(time.Duration(i) * time.Second)
	}
}

func TestCache(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		validTime time.Duration
		keys      []int

		expected []bool
	}{
		{
			name:      "Test cache - disabled",
			validTime: 0,

			keys:     []int{11, 22, 22, 11},
			expected: []bool{false, false, false, false},
		},
		{
			name:      "Test cache - all miss",
			validTime: 100,

			keys:     []int{11, 22, 33, 44},
			expected: []bool{false, false, false, false},
		},
		{
			name:      "Test cache - one key hits, other missed",
			validTime: 100,

			keys:     []int{11, 22, 33, 11},
			expected: []bool{false, false, false, true},
		},
		{
			name:      "Test cache - repeated requested same key",
			validTime: 100,

			keys:     []int{11, 11, 11, 11},
			expected: []bool{false, true, true, true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fake := NewFakeCache(tc.validTime)
			t.Logf("Running test scenario: %q", tc.name)
			for idx, key := range tc.keys {
				age := fake.Get(key)
				if age != tc.expected[idx] {
					t.Errorf("Unexpected cache age %t, expected %t.", age, tc.expected[idx])
				}
				fake.Set(key)
			}
		})
	}
}
