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

package override

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/layeredsets"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/plugins/ownersconfig"
	"sigs.k8s.io/prow/pkg/repoowners"
)

const (
	fakeOrg     = "fake-org"
	fakeRepo    = "fake-repo"
	fakePR      = 33
	fakeSHA     = "deadbeef"
	fakeBaseRef = "fake-branch"
	fakeBaseSHA = "fffffffffffffffffffffffffffffffffffffff0"
	adminUser   = "admin-user"
)

// fakeProwApp is the GitHub App the fake client reports as Prow's own by default.
var fakeProwApp = github.App{ID: 4242, Slug: "prow"}

func statusDescription(user string) string {
	return config.ContextDescriptionWithBaseSha(description(user), fakeBaseSHA)
}

func stickyStatusDescription(user string) string {
	return config.ContextDescriptionWithBaseSha(stickyDescription(user), fakeBaseSHA)
}

type fakeRepoownersClient struct {
	foc *fakeOwnersClient
}

func (froc *fakeRepoownersClient) LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error) {
	return froc.foc, nil
}

type fakeOwnersClient struct {
	topLevelApprovers sets.Set[string]
}

func (foc *fakeOwnersClient) AllApprovers() sets.Set[string] {
	return sets.Set[string]{}
}

func (foc *fakeOwnersClient) AllOwners() sets.Set[string] {
	return sets.Set[string]{}
}

func (foc *fakeOwnersClient) AllReviewers() sets.Set[string] {
	return sets.Set[string]{}
}

func (foc *fakeOwnersClient) Filenames() ownersconfig.Filenames {
	return ownersconfig.FakeFilenames
}

func (foc *fakeOwnersClient) TopLevelApprovers() sets.Set[string] {
	return foc.topLevelApprovers
}

func (foc *fakeOwnersClient) Approvers(path string) layeredsets.String {
	return layeredsets.String{}
}

func (foc *fakeOwnersClient) LeafApprovers(path string) sets.Set[string] {
	return sets.Set[string]{}
}

func (foc *fakeOwnersClient) FindApproverOwnersForFile(path string) string {
	return ""
}

func (foc *fakeOwnersClient) Reviewers(path string) layeredsets.String {
	return layeredsets.String{}
}

func (foc *fakeOwnersClient) RequiredReviewers(path string) sets.Set[string] {
	return sets.Set[string]{}
}

func (foc *fakeOwnersClient) LeafReviewers(path string) sets.Set[string] {
	return sets.Set[string]{}
}

func (foc *fakeOwnersClient) FindReviewersOwnersForFile(path string) string {
	return ""
}

func (foc *fakeOwnersClient) FindLabelsForFile(path string) sets.Set[string] {
	return sets.Set[string]{}
}

func (foc *fakeOwnersClient) IsNoParentOwners(path string) bool {
	return false
}

func (foc *fakeOwnersClient) IsAutoApproveUnownedSubfolders(path string) bool {
	return false
}

func (foc *fakeOwnersClient) ParseSimpleConfig(path string) (repoowners.SimpleConfig, error) {
	return repoowners.SimpleConfig{}, nil
}

func (foc *fakeOwnersClient) ParseFullConfig(path string) (repoowners.FullConfig, error) {
	return repoowners.FullConfig{}, nil
}

type fakeClient struct {
	comments         []string
	statuses         []github.Status
	branchProtection *github.BranchProtection
	ps               []config.Presubmit
	jobs             sets.Set[string]
	prowJobs         []prowapi.ProwJob
	owners           ownersClient
	checkruns        *github.CheckRunList
	usesAppsAuth     bool
	nextCheckRunID   int64
	listCheckRunsErr error
	app              *github.App
	getAppErr        error

	updateCheckRunErrs map[int64]error
}

func (c *fakeClient) presubmits(_, _ string, _ config.RefGetter, _ string) ([]config.Presubmit, error) {
	var result []config.Presubmit
	result = append(result, c.ps...)
	return result, nil
}

func (c *fakeClient) CreateComment(org, repo string, number int, comment string) error {
	c.comments = append(c.comments, comment)
	return nil
}

func (c *fakeClient) CreateStatus(org, repo, ref string, s github.Status) error {
	switch {
	case s.Context == "fail-create":
		return errors.New("injected CreateStatus failure")
	case org != fakeOrg:
		return fmt.Errorf("bad org: %s", org)
	case repo != fakeRepo:
		return fmt.Errorf("bad repo: %s", repo)
	case ref != fakeSHA:
		return fmt.Errorf("bad ref: %s", ref)
	}
	for i, status := range c.statuses {
		if status.Context == s.Context {
			c.statuses[i] = s
			return nil
		}
	}
	c.statuses = append(c.statuses, s)
	return nil
}

func (c *fakeClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	switch {
	case number < 0:
		return nil, errors.New("injected GetPullRequest failure")
	case org != fakeOrg:
		return nil, fmt.Errorf("bad org: %s", org)
	case repo != fakeRepo:
		return nil, fmt.Errorf("bad repo: %s", repo)
	case number != fakePR:
		return nil, fmt.Errorf("bad number: %d", number)
	}
	var pr github.PullRequest
	pr.Head.SHA = fakeSHA
	pr.Base.Ref = fakeBaseRef
	return &pr, nil
}

func (c *fakeClient) ListStatuses(org, repo, ref string) ([]github.Status, error) {
	switch {
	case org != fakeOrg:
		return nil, fmt.Errorf("bad org: %s", org)
	case repo != fakeRepo:
		return nil, fmt.Errorf("bad repo: %s", repo)
	case ref != fakeSHA:
		return nil, fmt.Errorf("bad ref: %s", ref)
	}
	var out []github.Status
	for _, s := range c.statuses {
		if s.Context == "fail-list" {
			return nil, errors.New("injected ListStatuses failure")
		}
		out = append(out, s)
	}
	return out, nil
}

func (c *fakeClient) ListCheckRuns(org, repo, ref string) (*github.CheckRunList, error) {
	if c.listCheckRunsErr != nil {
		return nil, c.listCheckRunsErr
	}
	if c.checkruns != nil {
		return c.checkruns, nil
	}
	return &github.CheckRunList{}, nil
}

func (c *fakeClient) CreateCheckRun(org, repo string, checkRun github.CheckRun) (int64, error) {
	if c.checkruns == nil {
		c.checkruns = &github.CheckRunList{}
	}
	c.nextCheckRunID++
	checkRun.ID = c.nextCheckRunID
	// GitHub records the app that created the check run and sets completed_at itself.
	checkRun.App = *c.currentApp()
	if checkRun.Status == "completed" {
		checkRun.CompletedAt = "1800 BC"
	}
	c.checkruns.CheckRuns = append(c.checkruns.CheckRuns, checkRun)
	return checkRun.ID, nil
}

func (c *fakeClient) UpdateCheckRun(org, repo string, checkRunId int64, checkRun github.CheckRun) error {
	if err := c.updateCheckRunErrs[checkRunId]; err != nil {
		return err
	}
	if c.checkruns != nil {
		for i := range c.checkruns.CheckRuns {
			cr := &c.checkruns.CheckRuns[i]
			if cr.ID != checkRunId {
				continue
			}
			cr.Status = checkRun.Status
			if checkRun.Status == "completed" {
				cr.CompletedAt = "1800 BC"
			}
			cr.Conclusion = checkRun.Conclusion
			cr.Output = checkRun.Output
			return nil
		}
	}
	return fmt.Errorf("no check run with id %d", checkRunId)
}

func (c *fakeClient) GetBranchProtection(org, repo, branch string) (*github.BranchProtection, error) {
	switch {
	case org != fakeOrg:
		return nil, fmt.Errorf("bad org: %s", org)
	case repo != fakeRepo:
		return nil, fmt.Errorf("bad repo: %s", repo)
	case branch != fakeBaseRef:
		return nil, fmt.Errorf("bad branch: %s", branch)
	}

	if c.branchProtection != nil && c.branchProtection.RequiredStatusChecks != nil &&
		len(c.branchProtection.RequiredStatusChecks.Contexts) > 0 &&
		c.branchProtection.RequiredStatusChecks.Contexts[0] == "fail-protection" {
		return nil, errors.New("injected GetBranchProtection failure")
	}

	return c.branchProtection, nil
}

func (c *fakeClient) HasPermission(org, repo, user string, roles ...string) (bool, error) {
	switch {
	case org != fakeOrg:
		return false, fmt.Errorf("bad org: %s", org)
	case repo != fakeRepo:
		return false, fmt.Errorf("bad repo: %s", repo)
	case roles[0] != github.RoleAdmin:
		return false, fmt.Errorf("bad roles: %s", roles)
	case user == "fail":
		return true, errors.New("injected HasPermission error")
	}
	return user == adminUser, nil
}

func (c *fakeClient) GetRef(org, repo, ref string) (string, error) {
	if repo == "fail-ref" {
		return "", errors.New("injected GetRef error")
	}
	return fakeBaseSHA, nil
}

func (c *fakeClient) ListTeams(org string) ([]github.Team, error) {
	if org == fakeOrg {
		return []github.Team{
			{
				ID:   1,
				Name: "team foo",
				Slug: "team-foo",
			},
		}, nil
	}
	return []github.Team{}, nil
}

func (c *fakeClient) ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error) {
	if teamSlug == "team-foo" {
		return []github.TeamMember{
			{Login: "user1"},
			{Login: "user2"},
		}, nil
	}
	return []github.TeamMember{}, nil
}

func (c *fakeClient) Create(_ context.Context, pj *prowapi.ProwJob, _ metav1.CreateOptions) (*prowapi.ProwJob, error) {
	if s := pj.Status.State; s != prowapi.SuccessState {
		return pj, fmt.Errorf("bad status state: %s", s)
	}
	if pj.Spec.Context == "fail-create" {
		return pj, errors.New("injected CreateProwJob error")
	}
	c.jobs.Insert(pj.Spec.Context)
	return pj, nil
}

func (c *fakeClient) List(_ context.Context, opts metav1.ListOptions) (*prowapi.ProwJobList, error) {
	selector := klabels.Everything()
	if opts.LabelSelector != "" {
		var err error
		selector, err = klabels.Parse(opts.LabelSelector)
		if err != nil {
			return nil, err
		}
	}
	var items []prowapi.ProwJob
	for _, pj := range c.prowJobs {
		if selector.Matches(klabels.Set(pj.Labels)) {
			items = append(items, *pj.DeepCopy())
		}
	}
	return &prowapi.ProwJobList{Items: items}, nil
}

func (c *fakeClient) Update(_ context.Context, pj *prowapi.ProwJob, _ metav1.UpdateOptions) (*prowapi.ProwJob, error) {
	for i, existing := range c.prowJobs {
		if existing.Name == pj.Name {
			c.prowJobs[i] = *pj.DeepCopy()
			return pj, nil
		}
	}
	return nil, fmt.Errorf("prowjob %s not found", pj.Name)
}

func (c *fakeClient) LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error) {
	return c.owners.LoadRepoOwners(org, repo, base)
}

func (c *fakeClient) UsesAppAuth() bool {
	return c.usesAppsAuth
}

func (c *fakeClient) GetApp() (*github.App, error) {
	if c.getAppErr != nil {
		return nil, c.getAppErr
	}
	return c.currentApp(), nil
}

func (c *fakeClient) currentApp() *github.App {
	if c.app != nil {
		return c.app
	}
	return &fakeProwApp
}

func TestAuthorizedUser(t *testing.T) {
	cases := []struct {
		name     string
		user     string
		expected bool
	}{
		{
			name: "fail closed",
			user: "fail",
		},
		{
			name: "reject rando",
			user: "random",
		},
		{
			name:     "accept admin",
			user:     adminUser,
			expected: true,
		},
	}

	log := logrus.WithField("plugin", pluginName)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if actual := authorizedUser(&fakeClient{}, log, fakeOrg, fakeRepo, tc.user); actual != tc.expected {
				t.Errorf("actual %t != expected %t", actual, tc.expected)
			}
		})
	}
}

func TestHandle(t *testing.T) {
	cases := []struct {
		name               string
		action             github.GenericCommentEventAction
		issue              bool
		state              string
		comment            string
		contexts           []github.Status
		branchProtection   *github.BranchProtection
		presubmits         []config.Presubmit
		user               string
		number             int
		expected           []github.Status
		expectedCheckRuns  *github.CheckRunList
		jobs               sets.Set[string]
		checkComments      []string
		unexpectedComments []string
		options            plugins.Override
		approvers          []string
		err                bool
		checkruns          *github.CheckRunList
		usesAppsAuth       bool
	}{
		{
			name:    "successfully override failure",
			comment: "/override broken-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "broken-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			checkComments: []string{"on behalf of " + adminUser},
		},
		{
			name:    "successfully override unknown context derived from checkruns",
			comment: "/override failure-checkrun",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "failure-checkrun", CompletedAt: "1800 BC", Conclusion: "failure"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "failure-checkrun", CompletedAt: "1800 BC", Conclusion: "failure"},
					{ID: 1, Name: "failure-checkrun", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
						Title:   fmt.Sprintf("Prow override - %s", "failure-checkrun"),
						Summary: fmt.Sprintf("Prow has received override command for the %s checkrun.", "failure-checkrun"),
					}},
				},
			},
			usesAppsAuth: true,
		},
		{
			name:    "successfully override unknown context with special characters derived from checkruns",
			comment: `/override "test / Unit Tests"`,
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "test / Unit Tests", CompletedAt: "1800 BC", Conclusion: "failure"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "test / Unit Tests", CompletedAt: "1800 BC", Conclusion: "failure"},
					{ID: 1, Name: "test / Unit Tests", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
						Title:   fmt.Sprintf("Prow override - %s", "test / Unit Tests"),
						Summary: fmt.Sprintf("Prow has received override command for the %s checkrun.", "test / Unit Tests"),
					}},
				},
			},
			usesAppsAuth: true,
		},
		{
			name:    "successfully override a mix of checkruns and prowjobs",
			comment: `/override broken-test "test / Unit Tests" hung-test`,
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "test / Unit Tests", CompletedAt: "1800 BC", Conclusion: "failure"},
				},
			},
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context:     "broken-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
				{
					Context:     "hung-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "test / Unit Tests", CompletedAt: "1800 BC", Conclusion: "failure"},
					{ID: 1, Name: "test / Unit Tests", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
						Title:   fmt.Sprintf("Prow override - %s", "test / Unit Tests"),
						Summary: fmt.Sprintf("Prow has received override command for the %s checkrun.", "test / Unit Tests"),
					}},
				},
			},
			usesAppsAuth: true,
		},
		{
			name:    "override a successful unknown context derived from checkruns",
			comment: "/override success-checkrun",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "success-checkrun", CompletedAt: "1800 BC", Conclusion: "success"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "success-checkrun", CompletedAt: "1800 BC", Conclusion: "success"},
				},
			},
			usesAppsAuth:       true,
			checkComments:      []string{"`success-checkrun` is already passing (or already overridden); no action taken. Use `/override-cancel success-checkrun` to remove an existing override."},
			unexpectedComments: []string{"The following unknown contexts/checkruns were given:"},
		},
		{
			name:    "override failure-checkrun checkrun, usesAppsAuth is false",
			comment: "/override failure-checkrun",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "failure-checkrun", CompletedAt: "1800 BC", Conclusion: "failure"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "failure-checkrun", CompletedAt: "1800 BC", Conclusion: "failure"},
				},
			},
			usesAppsAuth: false,
		},
		{
			name:    "override nonexistent checkrun",
			comment: "/override foobar",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "failure-checkrun", CompletedAt: "1800 BC", Conclusion: "failure"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "incomplete-checkrun"},
					{Name: "failure-checkrun", CompletedAt: "1800 BC", Conclusion: "failure"},
				},
			},
			usesAppsAuth: true,
		},
		{
			name:    "successfully override in-progress checkrun",
			comment: "/override soak-gate",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress", StartedAt: "1800 BC"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress", StartedAt: "1800 BC"},
					{ID: 1, Name: "soak-gate", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
						Title:   "Prow override - soak-gate",
						Summary: "Prow has received override command for the soak-gate checkrun.",
					}},
				},
			},
			checkComments: []string{"Overrode contexts on behalf of " + adminUser + ": soak-gate"},
			usesAppsAuth:  true,
		},
		{
			name:    "override queued and in-progress checkruns in sorted order",
			comment: "/override gate-a gate-b",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "gate-b", Status: "queued"},
					{Name: "gate-a", Status: "in_progress"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "gate-b", Status: "queued"},
					{Name: "gate-a", Status: "in_progress"},
					{ID: 1, Name: "gate-a", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
						Title:   "Prow override - gate-a",
						Summary: "Prow has received override command for the gate-a checkrun.",
					}},
					{ID: 2, Name: "gate-b", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
						Title:   "Prow override - gate-b",
						Summary: "Prow has received override command for the gate-b checkrun.",
					}},
				},
			},
			checkComments: []string{"Overrode contexts on behalf of " + adminUser + ": gate-a, gate-b"},
			usesAppsAuth:  true,
		},
		{
			name:    "pending checkrun is listed as overridable when an unknown context is given",
			comment: "/override foobar",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
				},
			},
			checkComments: []string{"Only the following failed or pending contexts/checkruns were expected", "`soak-gate`"},
			usesAppsAuth:  true,
		},
		{
			name:    "override in-progress checkrun that is also required by branch protection",
			comment: "/override soak-gate",
			branchProtection: &github.BranchProtection{RequiredStatusChecks: &github.RequiredStatusChecks{
				Contexts: []string{"soak-gate"},
			}},
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
					{ID: 1, Name: "soak-gate", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
						Title:   "Prow override - soak-gate",
						Summary: "Prow has received override command for the soak-gate checkrun.",
					}},
				},
			},
			usesAppsAuth: true,
		},
		{
			name:    "already-overridden in-progress checkrun is not overridden again",
			comment: "/override soak-gate",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
					{ID: 7, Name: "soak-gate", Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", Output: github.CheckRunOutput{Title: "Prow override - soak-gate"}},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
					{ID: 7, Name: "soak-gate", Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", Output: github.CheckRunOutput{Title: "Prow override - soak-gate"}},
				},
			},
			checkComments:      []string{"`soak-gate` is already passing (or already overridden); no action taken. Use `/override-cancel soak-gate` to remove an existing override."},
			unexpectedComments: []string{"The following unknown contexts/checkruns were given:"},
			usesAppsAuth:       true,
		},
		{
			name:    "already-overridden checkrun required by branch protection gets no second override run",
			comment: "/override soak-gate",
			branchProtection: &github.BranchProtection{RequiredStatusChecks: &github.RequiredStatusChecks{
				Contexts: []string{"soak-gate"},
			}},
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
					{ID: 7, Name: "soak-gate", Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", Output: github.CheckRunOutput{Title: "Prow override - soak-gate"}},
				},
			},
			expected: []github.Status{
				{
					Context:     "soak-gate",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress"},
					{ID: 7, Name: "soak-gate", Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", Output: github.CheckRunOutput{Title: "Prow override - soak-gate"}},
				},
			},
			usesAppsAuth: true,
		},
		{
			name:    "neutral checkrun is reported as already passing",
			comment: "/override neutral-check",
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "neutral-check", Status: "completed", CompletedAt: "1800 BC", Conclusion: "neutral"},
				},
			},
			expected: []github.Status{},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "neutral-check", Status: "completed", CompletedAt: "1800 BC", Conclusion: "neutral"},
				},
			},
			checkComments:      []string{"`neutral-check` is already passing (or already overridden); no action taken. Use `/override-cancel neutral-check` to remove an existing override."},
			unexpectedComments: []string{"The following unknown contexts/checkruns were given:"},
			usesAppsAuth:       true,
		},

		{
			name:    "successfully override pending",
			comment: "/override hung-test",
			contexts: []github.Status{
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context:     "hung-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			usesAppsAuth: true,
		},
		{
			name:    "comment for incorrect context",
			comment: "/override whatever-you-want",
			contexts: []github.Status{
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "hung-prow-job",
					},
					Reporter: config.Reporter{
						Context: "hung-test",
					},
				},
			},
			expected: []github.Status{
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			checkComments: []string{
				"The following unknown contexts/checkruns were given", "whatever-you-want",
				"Only the following failed or pending contexts/checkruns were expected", "hung-test", "hung-prow-job",
			},
		},
		{
			name:    "refuse override from non-admin",
			comment: "/override broken-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
			user:          "rando",
			checkComments: []string{"unauthorized"},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
		},
		{
			name:    "comment for override with no target",
			comment: "/override",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
			user:          "rando",
			checkComments: []string{"but none was given"},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
		},
		{
			name:    "override multiple",
			comment: "/override broken-test\n/override hung-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context:     "broken-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
				{
					Context:     "hung-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			checkComments: []string{fmt.Sprintf("%s: broken-test, hung-test", adminUser)},
		},
		{
			name:    "override multiple contexts inline",
			comment: "/override broken-test hung-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context:     "broken-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
				{
					Context:     "hung-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			checkComments: []string{fmt.Sprintf("%s: broken-test, hung-test", adminUser)},
		},
		{
			name: "override with extra whitespace",
			// Note two spaces here to start, and trailing whitespace
			comment: "/override  broken-test \r\n", // github ends lines with \r\n
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "broken-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			checkComments: []string{fmt.Sprintf("%s: broken-test", adminUser)},
		},
		{
			name:    "ignore non-PRs",
			issue:   true,
			comment: "/override broken-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
		},
		{
			name:    "ignore closed issues",
			state:   "closed",
			comment: "/override broken-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
		},
		{
			name:    "ignore edits",
			action:  github.GenericCommentActionEdited,
			comment: "/override broken-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
		},
		{
			name:    "ignore random text",
			comment: "/test broken-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusPending,
				},
			},
		},
		{
			name:    "comment on get pr failure",
			number:  fakePR * 2,
			comment: "/override broken-test",
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
			},
			checkComments: []string{"Cannot get PR"},
		},
		{
			name:    "comment on list statuses failure",
			comment: "/override fail-list",
			contexts: []github.Status{
				{
					Context: "fail-list",
					State:   github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context: "fail-list",
					State:   github.StatusFailure,
				},
			},
			checkComments: []string{"Cannot get commit statuses"},
		},
		{
			name:    "comment on get branch protection failure",
			comment: "/override fail-list",
			branchProtection: &github.BranchProtection{RequiredStatusChecks: &github.RequiredStatusChecks{
				Contexts: []string{"fail-protection"},
			}},
			contexts: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context: "broken-test",
					State:   github.StatusFailure,
				},
			},
			checkComments: []string{"Cannot get branch protection"},
		},
		{
			name:    "do not override passing contexts",
			comment: "/override passing-test",
			contexts: []github.Status{
				{
					Context:     "passing-test",
					Description: "preserve description",
					State:       github.StatusSuccess,
				},
			},
			expected: []github.Status{
				{
					Context:     "passing-test",
					State:       github.StatusSuccess,
					Description: "preserve description",
				},
			},
			checkComments: []string{"`passing-test` is already passing (or already overridden); no action taken. Use `/override-cancel passing-test` to remove an existing override."},
		},
		{
			name:    "already passing presubmit is recognized by job name",
			comment: "/override passing-job",
			contexts: []github.Status{
				{
					Context: "passing-ctx",
					State:   github.StatusSuccess,
				},
			},
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "passing-job",
					},
					Reporter: config.Reporter{
						Context: "passing-ctx",
					},
				},
			},
			expected: []github.Status{
				{
					Context: "passing-ctx",
					State:   github.StatusSuccess,
				},
			},
			checkComments:      []string{"`passing-job` is already passing (or already overridden); no action taken. Use `/override-cancel passing-job` to remove an existing override."},
			unexpectedComments: []string{"The following unknown contexts/checkruns were given:"},
		},
		{
			name:    "comment separates already passing contexts from unknown ones",
			comment: "/override passing-test whatever-you-want",
			contexts: []github.Status{
				{
					Context: "passing-test",
					State:   github.StatusSuccess,
				},
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			expected: []github.Status{
				{
					Context: "passing-test",
					State:   github.StatusSuccess,
				},
				{
					Context: "hung-test",
					State:   github.StatusPending,
				},
			},
			checkComments: []string{
				"The following unknown contexts/checkruns were given:\n - `whatever-you-want`\n",
				"`passing-test` is already passing (or already overridden); no action taken. Use `/override-cancel passing-test` to remove an existing override.",
			},
		},
		{
			name:    "mixed override with already passing context applies nothing",
			comment: "/override soak-gate passing-test",
			contexts: []github.Status{
				{
					Context: "passing-test",
					State:   github.StatusSuccess,
				},
			},
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress", StartedAt: "1800 BC"},
				},
			},
			expected: []github.Status{
				{
					Context: "passing-test",
					State:   github.StatusSuccess,
				},
			},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress", StartedAt: "1800 BC"},
				},
			},
			checkComments: []string{
				"`passing-test` is already passing (or already overridden); no action taken. Use `/override-cancel passing-test` to remove an existing override.",
				"No overrides were applied. Re-run `/override soak-gate` with only the contexts that can be overridden.",
			},
			unexpectedComments: []string{"Overrode contexts on behalf of", "The following unknown contexts/checkruns were given:"},
			usesAppsAuth:       true,
		},
		{
			name:    "mixed override with overridable, already passing and unknown contexts applies nothing",
			comment: "/override soak-gate passing-test bogus",
			contexts: []github.Status{
				{
					Context: "passing-test",
					State:   github.StatusSuccess,
				},
			},
			checkruns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress", StartedAt: "1800 BC"},
				},
			},
			expected: []github.Status{
				{
					Context: "passing-test",
					State:   github.StatusSuccess,
				},
			},
			expectedCheckRuns: &github.CheckRunList{
				CheckRuns: []github.CheckRun{
					{Name: "soak-gate", Status: "in_progress", StartedAt: "1800 BC"},
				},
			},
			checkComments: []string{
				"The following unknown contexts/checkruns were given:\n - `bogus`\n",
				"`passing-test` is already passing (or already overridden); no action taken. Use `/override-cancel passing-test` to remove an existing override.",
				"No overrides were applied. Re-run `/override soak-gate` with only the contexts that can be overridden.",
			},
			unexpectedComments: []string{"Overrode contexts on behalf of"},
			usesAppsAuth:       true,
		},
		{
			name:    "create successful prow job",
			comment: "/override prow-job",
			contexts: []github.Status{
				{
					Context:     "prow-job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "prow-job",
					},
					Reporter: config.Reporter{
						Context: "prow-job",
					},
				},
			},
			jobs: sets.New[string]("prow-job"),
			expected: []github.Status{
				{
					Context:     "prow-job",
					State:       github.StatusSuccess,
					Description: statusDescription(adminUser),
				},
			},
		},
		{
			name:    "successfully override prow job name",
			comment: "/override prow-job",
			contexts: []github.Status{
				{
					Context:     "ci/prow/pkg-job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "prow-job",
					},
					Reporter: config.Reporter{
						Context: "ci/prow/pkg-job",
					},
				},
			},
			jobs: sets.New[string]("ci/prow/pkg-job"),
			expected: []github.Status{
				{
					Context:     "ci/prow/pkg-job",
					State:       github.StatusSuccess,
					Description: statusDescription(adminUser),
				},
			},
		},
		{
			name:    "override prow job and context",
			comment: "/override prow-job\n/override ci/prow/context",
			contexts: []github.Status{
				{
					Context:     "ci/prow/context",
					Description: "failed",
					State:       github.StatusFailure,
				},
				{
					Context:     "ci/prow/pkg-job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "prow-job",
					},
					Reporter: config.Reporter{
						Context: "ci/prow/pkg-job",
					},
				},
			},
			jobs: sets.New[string]("ci/prow/pkg-job"),
			expected: []github.Status{
				{
					Context:     "ci/prow/context",
					State:       github.StatusSuccess,
					Description: statusDescription(adminUser),
				},
				{
					Context:     "ci/prow/pkg-job",
					State:       github.StatusSuccess,
					Description: statusDescription(adminUser),
				},
			},
		},
		{
			name:    "override same context and prow job",
			comment: "/override ci/prow/pkg-job\n/override prow-job",
			contexts: []github.Status{
				{
					Context:     "ci/prow/pkg-job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "prow-job",
					},
					Reporter: config.Reporter{
						Context: "ci/prow/pkg-job",
					},
				},
			},
			jobs: sets.New[string]("ci/prow/pkg-job"),
			expected: []github.Status{
				{
					Context:     "ci/prow/pkg-job",
					State:       github.StatusSuccess,
					Description: statusDescription(adminUser),
				},
			},
		},
		{
			name:    "override with explanation works",
			comment: "/override job\r\nobnoxious flake", // github ends lines with \r\n
			contexts: []github.Status{
				{
					Context:     "job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "job",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
		},
		{
			name:      "override with allow_top_level_owners works",
			comment:   "/override job",
			user:      "code_owner",
			options:   plugins.Override{AllowTopLevelOwners: true},
			approvers: []string{"code_owner"},
			contexts: []github.Status{
				{
					Context:     "job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "job",
					Description: statusDescription("code_owner"),
					State:       github.StatusSuccess,
				},
			},
		},
		{
			name:      "override with allow_top_level_owners works for uppercase user",
			comment:   "/override job",
			user:      "Code_owner",
			options:   plugins.Override{AllowTopLevelOwners: true},
			approvers: []string{"code_owner"},
			contexts: []github.Status{
				{
					Context:     "job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "job",
					Description: statusDescription("Code_owner"),
					State:       github.StatusSuccess,
				},
			},
		},
		{
			name:    "override with allow_top_level_owners fails if user is not in OWNERS file",
			comment: "/override job",
			user:    "non_code_owner",
			options: plugins.Override{AllowTopLevelOwners: true},
			contexts: []github.Status{
				{
					Context:     "job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
		},
		{
			name:    "override with allowed_github_team allowed if user is in specified github team",
			comment: "/override job",
			user:    "user1",
			options: plugins.Override{
				AllowedGitHubTeams: map[string][]string{
					fmt.Sprintf("%s/%s", fakeOrg, fakeRepo): {"team-foo"},
				},
			},
			contexts: []github.Status{
				{
					Context:     "job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "job",
					Description: statusDescription("user1"),
					State:       github.StatusSuccess,
				},
			},
		},
		{
			name:    "override does not fail due to invalid github team slug",
			comment: "/override job",
			user:    "user1",
			options: plugins.Override{
				AllowedGitHubTeams: map[string][]string{
					fmt.Sprintf("%s/%s", fakeOrg, fakeRepo): {"team-foo", "invalid-team-slug"},
				},
			},
			contexts: []github.Status{
				{
					Context:     "job",
					Description: "failed",
					State:       github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "job",
					Description: statusDescription("user1"),
					State:       github.StatusSuccess,
				},
			},
		},
		{
			name:             "override with empty branch protection",
			comment:          "/override job",
			branchProtection: &github.BranchProtection{},
			expected:         []github.Status{},
			checkComments:    []string{},
		},
		{
			name:             "override with branch protection empty status checks",
			comment:          "/override job",
			branchProtection: &github.BranchProtection{RequiredStatusChecks: &github.RequiredStatusChecks{}},
			expected:         []github.Status{},
			checkComments:    []string{},
		},
		{
			name:    "override with branch protection status checks",
			comment: "/override job",
			branchProtection: &github.BranchProtection{RequiredStatusChecks: &github.RequiredStatusChecks{
				Contexts: []string{"job"},
			}},
			expected: []github.Status{
				{
					Context:     "job",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			checkComments: []string{"on behalf of " + adminUser},
		},
		{
			name:    "override with same branch protection status check and status",
			comment: "/override job",
			branchProtection: &github.BranchProtection{RequiredStatusChecks: &github.RequiredStatusChecks{
				Contexts: []string{"job"},
			}},
			contexts: []github.Status{
				{
					Context: "job",
					State:   github.StatusFailure,
				},
			},
			expected: []github.Status{
				{
					Context:     "job",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
			},
			checkComments: []string{"on behalf of " + adminUser},
		},
		{
			name:    "handle only one status when multiple statuses have the same context",
			comment: "/override problematic-test",
			contexts: []github.Status{
				{
					Context: "problematic-test",
					State:   github.StatusPending,
				},
				{
					Context: "problematic-test",
					State:   github.StatusFailure,
				},
				{
					Context: "problematic-test",
					State:   github.StatusPending,
				},
			},
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "problematic-test",
					},
					Reporter: config.Reporter{
						Context: "problematic-test",
					},
				},
			},
			jobs: sets.New[string]("problematic-test"),
			expected: []github.Status{
				{
					Context:     "problematic-test",
					Description: statusDescription(adminUser),
					State:       github.StatusSuccess,
				},
				{
					Context: "problematic-test",
					State:   github.StatusFailure,
				},
				{
					Context: "problematic-test",
					State:   github.StatusPending,
				},
			},
		},
	}

	log := logrus.WithField("plugin", pluginName)
	log.Logger.SetLevel(logrus.DebugLevel)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.number == 0 {
				tc.number = fakePR
			}
			if tc.user == "" {
				tc.user = adminUser
			}
			if tc.state == "" {
				tc.state = "open"
			}
			if tc.action == "" {
				tc.action = github.GenericCommentActionCreated
			}
			if tc.contexts == nil {
				tc.contexts = []github.Status{}
			}

			event := github.GenericCommentEvent{
				Repo: github.Repo{
					Owner: github.User{
						Login: fakeOrg,
					},
					Name: fakeRepo,
				},
				User: github.User{
					Login: tc.user,
				},
				Body:       tc.comment,
				Number:     tc.number,
				IsPR:       !tc.issue,
				IssueState: tc.state,
				Action:     tc.action,
			}

			froc := &fakeRepoownersClient{
				foc: &fakeOwnersClient{
					topLevelApprovers: sets.New[string](tc.approvers...),
				},
			}
			fc := fakeClient{
				statuses:         tc.contexts,
				branchProtection: tc.branchProtection,
				ps:               tc.presubmits,
				jobs:             sets.Set[string]{},
				owners:           froc,
				checkruns:        tc.checkruns,
				usesAppsAuth:     tc.usesAppsAuth,
			}

			if tc.jobs == nil {
				tc.jobs = sets.Set[string]{}
			}

			err := handle(&fc, log, &event, tc.options, false)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Error("failed to receive an error")
			case !reflect.DeepEqual(fc.statuses, tc.expected):
				t.Errorf("bad statuses: actual %#v != expected %#v", fc.statuses, tc.expected)
			case !reflect.DeepEqual(fc.jobs, tc.jobs):
				t.Errorf("bad jobs: actual %#v != expected %#v", fc.jobs, tc.jobs)
			case !reflect.DeepEqual(fc.checkruns, tc.expectedCheckRuns):
				t.Errorf("expected checkruns differs from actual: %s", cmp.Diff(fc.checkruns, tc.expectedCheckRuns))

			}
			for _, expectedComment := range tc.checkComments {
				if !strings.Contains(strings.Join(fc.comments, "\n"), expectedComment) {
					t.Errorf("bad comments: expected %#v to be in %#v", expectedComment, fc.comments)
				}
			}
			for _, unexpectedComment := range tc.unexpectedComments {
				if strings.Contains(strings.Join(fc.comments, "\n"), unexpectedComment) {
					t.Errorf("bad comments: expected %#v not to be in %#v", unexpectedComment, fc.comments)
				}
			}
		})
	}
}

func TestHelpProvider(t *testing.T) {
	cases := []struct {
		name        string
		config      plugins.Configuration
		org         string
		repo        string
		expectedWho string
	}{
		{
			name:        "WhoCanUse restricted to Repo administrators if no other options specified",
			config:      plugins.Configuration{},
			expectedWho: "Repo administrators.",
		},
		{
			name: "WhoCanUse includes top level code OWNERS if allow_top_level_owners is set",
			config: plugins.Configuration{
				Override: plugins.Override{
					AllowTopLevelOwners: true,
				},
			},
			expectedWho: "Repo administrators, approvers in top level OWNERS file.",
		},
		{
			name: "WhoCanUse includes specified github teams",
			config: plugins.Configuration{
				Override: plugins.Override{
					AllowedGitHubTeams: map[string][]string{
						"org1/repo1": {"team-foo", "team-bar"},
					},
				},
			},
			expectedWho: "Repo administrators, and the following github teams:" +
				"org1/repo1: team-foo team-bar.",
		},
	}

	for _, tc := range cases {
		help, err := helpProvider(&tc.config, []config.OrgRepo{})
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
		switch {
		case help == nil:
			t.Errorf("%s: expected a valid plugin help object, got nil", tc.name)
		case len(help.Commands) != 3:
			t.Errorf("%s: expected 3 commands from plugin help, got %d: %v", tc.name, len(help.Commands), help.Commands)
		default:
			for _, cmd := range help.Commands {
				if cmd.WhoCanUse != tc.expectedWho {
					t.Errorf("%s: expected command %q with WhoCanUse set to %s, got %s instead", tc.name, cmd.Usage, tc.expectedWho, cmd.WhoCanUse)
				}
			}
		}
	}
}

func TestWhoCanUse(t *testing.T) {
	override := plugins.Override{
		AllowedGitHubTeams: map[string][]string{
			"org1/repo1": {"team-foo", "team-bar"},
			"org2/repo2": {"team-bar"},
			"org1":       {"team-foo-bar"},
		},
	}
	expectedWho := "Repo administrators, and the following github teams:" +
		"org1/repo1: team-foo team-bar, org1: team-foo-bar."

	who := whoCanUse(override, "org1", "repo1")
	if who != expectedWho {
		t.Errorf("expected %q, got %q", expectedWho, who)
	}
}

func TestAuthorizedGitHubTeamMember(t *testing.T) {
	repoRef := fmt.Sprintf("%s/%s", fakeOrg, fakeRepo)
	cases := []struct {
		name     string
		slugs    map[string][]string
		org      string
		repo     string
		user     string
		expected bool
	}{
		{
			name: "members of specified teams are authorized",
			slugs: map[string][]string{
				repoRef: {"team-foo"},
			},
			user:     "user1",
			expected: true,
		},
		{
			name: "non-members of specified teams are not authorized",
			slugs: map[string][]string{
				repoRef: {"team-foo"},
			},
			user: "non-member",
		},
		{
			name: "only teams corresponding to the org/repo are considered",
			slugs: map[string][]string{
				"org/repo": {"team-foo"},
			},
			user: "member",
		},
		{
			name: "members of specified teams are authorized to org",
			slugs: map[string][]string{
				fakeOrg: {"team-foo"},
			},
			user:     "user1",
			expected: true,
		},
	}
	log := logrus.WithField("plugin", pluginName)
	log.Logger.SetLevel(logrus.DebugLevel)
	for _, tc := range cases {
		authorized := authorizedGitHubTeamMember(&fakeClient{}, log, tc.slugs, fakeOrg, fakeRepo, tc.user)
		if authorized != tc.expected {
			t.Errorf("%s: actual: %v != expected %v", tc.name, authorized, tc.expected)
		}
	}
}

func TestValidateGitHubTeamSlugs(t *testing.T) {
	githubTeams := []github.Team{
		{
			ID:   2,
			Slug: "team-bar",
		},
		{
			ID:   3,
			Slug: "team-baz",
		},
	}

	repoRef := fmt.Sprintf("%s/%s", fakeOrg, fakeRepo)
	cases := []struct {
		name      string
		teamSlugs map[string][]string
		err       error
	}{
		{
			name: "validation failure for invalid team slug",
			teamSlugs: map[string][]string{
				repoRef: {"foo"},
			},
			err: fmt.Errorf("invalid team slug(s): foo"),
		},
		{
			name: "no errors for valid team slugs",
			teamSlugs: map[string][]string{
				repoRef: {"team-bar", "team-baz"},
			},
		},
	}

	for _, tc := range cases {
		err := validateGitHubTeamSlugs(tc.teamSlugs, fakeOrg, fakeRepo, githubTeams)
		if !reflect.DeepEqual(err, tc.err) {
			t.Errorf("%s: actual: %v != expected %v", tc.name, err, tc.err)
		}
	}
}

func TestIsSkipRetest(t *testing.T) {
	cases := []struct {
		description string
		expected    bool
	}{
		{"Overridden by admin-user [prow:skip-retest]", true},
		{"Overridden by bot [prow:skip-retest]", true},
		{"something [prow:skip-retest] else", true},
		{stickyStatusDescription("admin-user"), true},
		{"Overridden by admin-user", false},
		{"Build succeeded", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := config.IsSkipRetest(tc.description); got != tc.expected {
			t.Errorf("IsSkipRetest(%q) = %v, want %v", tc.description, got, tc.expected)
		}
	}
}

func TestHandleStickyOverride(t *testing.T) {
	log := logrus.WithField("plugin", pluginName)

	cases := []struct {
		name     string
		body     string
		sticky   bool
		statuses []github.Status
		expected []github.Status
	}{
		{
			name:   "/override-sticky sets sticky description",
			body:   "/override-sticky job-a",
			sticky: true,
			statuses: []github.Status{
				{Context: "job-a", State: github.StatusFailure, Description: "Build failed"},
			},
			expected: []github.Status{
				{Context: "job-a", State: github.StatusSuccess, Description: stickyStatusDescription(adminUser)},
			},
		},
		{
			name:   "/override sets regular description",
			body:   "/override job-a",
			sticky: false,
			statuses: []github.Status{
				{Context: "job-a", State: github.StatusFailure, Description: "Build failed"},
			},
			expected: []github.Status{
				{Context: "job-a", State: github.StatusSuccess, Description: statusDescription(adminUser)},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{
				statuses: tc.statuses,
				jobs:     sets.New[string](),
			}
			event := github.GenericCommentEvent{
				IsPR:       true,
				IssueState: "open",
				Action:     github.GenericCommentActionCreated,
				Body:       tc.body,
				Number:     fakePR,
				User:       github.User{Login: adminUser},
				Repo:       github.Repo{Owner: github.User{Login: fakeOrg}, Name: fakeRepo},
			}

			err := handle(fc, log, &event, plugins.Override{}, tc.sticky)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.expected, fc.statuses); diff != "" {
				t.Errorf("statuses mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHandleStickyCancel(t *testing.T) {
	log := logrus.WithField("plugin", pluginName)

	cases := []struct {
		name             string
		body             string
		statuses         []github.Status
		expectedStatuses []github.Status
		expectedComment  string
	}{
		{
			name: "cancel specific override",
			body: "/override-cancel job-a",
			statuses: []github.Status{
				{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user [prow:skip-retest]"},
				{Context: "job-b", State: github.StatusSuccess, Description: "Overridden by admin-user [prow:skip-retest]"},
			},
			expectedStatuses: []github.Status{
				{Context: "job-a", State: github.StatusFailure, Description: "Override cancelled by admin-user"},
				{Context: "job-b", State: github.StatusSuccess, Description: "Overridden by admin-user [prow:skip-retest]"},
			},
			expectedComment: "Cancelled overrides",
		},
		{
			name: "cancel all overrides",
			body: "/override-cancel",
			statuses: []github.Status{
				{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user [prow:skip-retest]"},
				{Context: "job-b", State: github.StatusSuccess, Description: "Overridden by admin-user [prow:skip-retest]"},
			},
			expectedStatuses: []github.Status{
				{Context: "job-a", State: github.StatusFailure, Description: "Override cancelled by admin-user"},
				{Context: "job-b", State: github.StatusFailure, Description: "Override cancelled by admin-user"},
			},
			expectedComment: "Cancelled overrides",
		},
		{
			name: "cancel also affects regular overrides",
			body: "/override-cancel",
			statuses: []github.Status{
				{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user"},
			},
			expectedStatuses: []github.Status{
				{Context: "job-a", State: github.StatusFailure, Description: "Override cancelled by admin-user"},
			},
			expectedComment: "Cancelled overrides",
		},
		{
			name: "cancel does not affect non-override statuses",
			body: "/override-cancel",
			statuses: []github.Status{
				{Context: "job-a", State: github.StatusSuccess, Description: "Build succeeded"},
			},
			expectedStatuses: []github.Status{
				{Context: "job-a", State: github.StatusSuccess, Description: "Build succeeded"},
			},
			expectedComment: "No overrides found to cancel",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{
				statuses: tc.statuses,
				jobs:     sets.New[string](),
			}
			event := github.GenericCommentEvent{
				IsPR:       true,
				IssueState: "open",
				Action:     github.GenericCommentActionCreated,
				Body:       tc.body,
				Number:     fakePR,
				User:       github.User{Login: adminUser},
				Repo:       github.Repo{Owner: github.User{Login: fakeOrg}, Name: fakeRepo},
			}

			err := handleOverrideCancel(fc, log, &event, plugins.Override{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.expectedStatuses, fc.statuses); diff != "" {
				t.Errorf("statuses mismatch (-want +got):\n%s", diff)
			}
			if len(fc.comments) == 0 {
				t.Fatal("expected a comment")
			}
			if !strings.Contains(fc.comments[len(fc.comments)-1], tc.expectedComment) {
				t.Errorf("expected comment containing %q, got %q", tc.expectedComment, fc.comments[len(fc.comments)-1])
			}
		})
	}
}

func TestHandleCheckRunCancel(t *testing.T) {
	log := logrus.WithField("plugin", pluginName)

	overrideRun := func(id int64, name string) github.CheckRun {
		return github.CheckRun{ID: id, Name: name, HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
			Title:   "Prow override - " + name,
			Summary: "Prow has received override command for the " + name + " checkrun.",
		}}
	}
	cancelledRun := func(id int64, name string) github.CheckRun {
		return github.CheckRun{ID: id, Name: name, HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "cancelled", App: fakeProwApp, Output: github.CheckRunOutput{
			Title:   "Prow override cancelled - " + name,
			Summary: "Override cancelled by " + adminUser,
		}}
	}
	pendingRun := func() github.CheckRun {
		return github.CheckRun{ID: 1, Name: "soak-gate", Status: "in_progress"}
	}
	foreignRun := func() github.CheckRun {
		return github.CheckRun{ID: 2, Name: "soak-gate", Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: github.App{ID: 999, Slug: "other-app"}, Output: github.CheckRunOutput{
			Title: "Prow override - soak-gate",
		}}
	}
	slugApp := github.App{Slug: fakeProwApp.Slug}
	withApp := func(cr github.CheckRun, app github.App) github.CheckRun {
		cr.App = app
		return cr
	}

	cases := []struct {
		name               string
		body               string
		statuses           []github.Status
		checkruns          []github.CheckRun
		usesAppsAuth       bool
		listCheckRunsErr   error
		app                *github.App
		getAppErr          error
		updateCheckRunErrs map[int64]error
		expectedStatuses   []github.Status
		expectedCheckRuns  []github.CheckRun
		expectedComments   []string
		unexpectedComments []string
	}{
		{
			name:              "cancel specific check run override",
			body:              "/override-cancel soak-gate",
			checkruns:         []github.CheckRun{pendingRun(), overrideRun(2, "soak-gate"), overrideRun(3, "other-gate")},
			usesAppsAuth:      true,
			expectedCheckRuns: []github.CheckRun{pendingRun(), cancelledRun(2, "soak-gate"), overrideRun(3, "other-gate")},
			expectedComments:  []string{"Cancelled overrides on behalf of admin-user: soak-gate"},
		},
		{
			name:              "cancel all cancels status and check run overrides",
			body:              "/override-cancel",
			statuses:          []github.Status{{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user"}},
			checkruns:         []github.CheckRun{pendingRun(), overrideRun(2, "soak-gate")},
			usesAppsAuth:      true,
			expectedStatuses:  []github.Status{{Context: "job-a", State: github.StatusFailure, Description: "Override cancelled by admin-user"}},
			expectedCheckRuns: []github.CheckRun{pendingRun(), cancelledRun(2, "soak-gate")},
			expectedComments:  []string{"Cancelled overrides on behalf of admin-user: job-a, soak-gate"},
		},
		{
			name:              "override-titled check run from another app is left alone",
			body:              "/override-cancel soak-gate",
			checkruns:         []github.CheckRun{pendingRun(), foreignRun(), overrideRun(3, "soak-gate")},
			usesAppsAuth:      true,
			expectedCheckRuns: []github.CheckRun{pendingRun(), foreignRun(), cancelledRun(3, "soak-gate")},
			expectedComments:  []string{"Cancelled overrides on behalf of admin-user: soak-gate", "`soak-gate`: check run titled as a Prow override was created by a different GitHub App and cannot be cancelled by this Prow instance"},
		},
		{
			name:               "comment when only another app's override-titled check run matches",
			body:               "/override-cancel",
			checkruns:          []github.CheckRun{foreignRun()},
			usesAppsAuth:       true,
			expectedCheckRuns:  []github.CheckRun{foreignRun()},
			expectedComments:   []string{"`soak-gate`: check run titled as a Prow override was created by a different GitHub App and cannot be cancelled by this Prow instance"},
			unexpectedComments: []string{"No overrides found to cancel", "Cancelled overrides"},
		},
		{
			name:              "ownership falls back to app slug when the app ID is unavailable",
			body:              "/override-cancel soak-gate",
			checkruns:         []github.CheckRun{withApp(overrideRun(2, "soak-gate"), slugApp)},
			usesAppsAuth:      true,
			app:               &slugApp,
			expectedCheckRuns: []github.CheckRun{withApp(cancelledRun(2, "soak-gate"), slugApp)},
			expectedComments:  []string{"Cancelled overrides on behalf of admin-user: soak-gate"},
		},
		{
			name:               "slug fallback leaves a check run from a differently named app alone",
			body:               "/override-cancel soak-gate",
			checkruns:          []github.CheckRun{withApp(overrideRun(2, "soak-gate"), github.App{Slug: "other-app"})},
			usesAppsAuth:       true,
			app:                &slugApp,
			expectedCheckRuns:  []github.CheckRun{withApp(overrideRun(2, "soak-gate"), github.App{Slug: "other-app"})},
			expectedComments:   []string{"`soak-gate`: check run titled as a Prow override was created by a different GitHub App and cannot be cancelled by this Prow instance"},
			unexpectedComments: []string{"Cancelled overrides"},
		},
		{
			name:              "check run titled as a cancelled override is not cancelled again",
			body:              "/override-cancel soak-gate",
			checkruns:         []github.CheckRun{pendingRun(), cancelledRun(2, "soak-gate")},
			usesAppsAuth:      true,
			expectedCheckRuns: []github.CheckRun{pendingRun(), cancelledRun(2, "soak-gate")},
			expectedComments:  []string{"No overrides found to cancel"},
		},
		{
			name: "override-titled check run that did not succeed is not cancelled again",
			body: "/override-cancel soak-gate",
			checkruns: []github.CheckRun{{ID: 2, Name: "soak-gate", Status: "completed", CompletedAt: "1800 BC", Conclusion: "cancelled", App: fakeProwApp, Output: github.CheckRunOutput{
				Title: "Prow override - soak-gate",
			}}},
			usesAppsAuth: true,
			expectedCheckRuns: []github.CheckRun{{ID: 2, Name: "soak-gate", Status: "completed", CompletedAt: "1800 BC", Conclusion: "cancelled", App: fakeProwApp, Output: github.CheckRunOutput{
				Title: "Prow override - soak-gate",
			}}},
			expectedComments: []string{"No overrides found to cancel"},
		},
		{
			name:              "check runs are skipped without app auth",
			body:              "/override-cancel",
			checkruns:         []github.CheckRun{overrideRun(2, "soak-gate")},
			usesAppsAuth:      false,
			expectedCheckRuns: []github.CheckRun{overrideRun(2, "soak-gate")},
			expectedComments:  []string{"No overrides found to cancel"},
		},
		{
			name:             "listing check runs fails before any status is cancelled",
			body:             "/override-cancel",
			statuses:         []github.Status{{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user"}},
			usesAppsAuth:     true,
			listCheckRunsErr: errors.New("injected ListCheckRuns failure"),
			expectedStatuses: []github.Status{{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user"}},
			expectedComments: []string{"Cannot list check runs for PR #", "no overrides were cancelled. Please retry /override-cancel."},
		},
		{
			name:               "partial check run failure reports both cancelled and failed overrides",
			body:               "/override-cancel",
			statuses:           []github.Status{{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user"}},
			checkruns:          []github.CheckRun{overrideRun(2, "gate-a"), overrideRun(3, "gate-b")},
			usesAppsAuth:       true,
			updateCheckRunErrs: map[int64]error{3: errors.New("injected UpdateCheckRun failure")},
			expectedStatuses:   []github.Status{{Context: "job-a", State: github.StatusFailure, Description: "Override cancelled by admin-user"}},
			expectedCheckRuns:  []github.CheckRun{cancelledRun(2, "gate-a"), overrideRun(3, "gate-b")},
			expectedComments: []string{
				"Cancelled overrides on behalf of admin-user: gate-a, job-a",
				"Cannot cancel override check run gate-b",
			},
		},
		{
			name:              "comment when Prow's app cannot be determined",
			body:              "/override-cancel",
			statuses:          []github.Status{{Context: "job-a", State: github.StatusSuccess, Description: "Overridden by admin-user"}},
			checkruns:         []github.CheckRun{overrideRun(2, "soak-gate")},
			usesAppsAuth:      true,
			getAppErr:         errors.New("injected GetApp failure"),
			expectedStatuses:  []github.Status{{Context: "job-a", State: github.StatusFailure, Description: "Override cancelled by admin-user"}},
			expectedCheckRuns: []github.CheckRun{overrideRun(2, "soak-gate")},
			expectedComments: []string{
				"Cancelled overrides on behalf of admin-user: job-a",
				"Cannot determine Prow's GitHub App, so check runs titled as overrides were not cancelled: soak-gate",
			},
		},
		{
			name:               "an app without an ID or slug is treated as undetermined",
			body:               "/override-cancel",
			checkruns:          []github.CheckRun{overrideRun(2, "soak-gate")},
			usesAppsAuth:       true,
			app:                &github.App{},
			expectedCheckRuns:  []github.CheckRun{overrideRun(2, "soak-gate")},
			expectedComments:   []string{"Cannot determine Prow's GitHub App, so check runs titled as overrides were not cancelled: soak-gate"},
			unexpectedComments: []string{"different GitHub App"},
		},
		{
			name:               "check runs not created by override are left alone without looking up the app",
			body:               "/override-cancel",
			checkruns:          []github.CheckRun{{ID: 4, Name: "lint", Status: "completed", Conclusion: "success", Output: github.CheckRunOutput{Title: "Lint passed"}}},
			usesAppsAuth:       true,
			getAppErr:          errors.New("injected GetApp failure"),
			expectedCheckRuns:  []github.CheckRun{{ID: 4, Name: "lint", Status: "completed", Conclusion: "success", Output: github.CheckRunOutput{Title: "Lint passed"}}},
			expectedComments:   []string{"No overrides found to cancel"},
			unexpectedComments: []string{"Cannot determine Prow's GitHub App"},
		},
		{
			name:              "a failed status write does not stop check run cancellation",
			body:              "/override-cancel",
			statuses:          []github.Status{{Context: "fail-create", State: github.StatusSuccess, Description: "Overridden by admin-user"}},
			checkruns:         []github.CheckRun{overrideRun(2, "soak-gate")},
			usesAppsAuth:      true,
			expectedStatuses:  []github.Status{{Context: "fail-create", State: github.StatusSuccess, Description: "Overridden by admin-user"}},
			expectedCheckRuns: []github.CheckRun{cancelledRun(2, "soak-gate")},
			expectedComments: []string{
				"Cancelled overrides on behalf of admin-user: soak-gate",
				"Cannot update PR status for context fail-create",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{
				statuses:           tc.statuses,
				checkruns:          &github.CheckRunList{CheckRuns: tc.checkruns},
				usesAppsAuth:       tc.usesAppsAuth,
				listCheckRunsErr:   tc.listCheckRunsErr,
				app:                tc.app,
				getAppErr:          tc.getAppErr,
				updateCheckRunErrs: tc.updateCheckRunErrs,
				jobs:               sets.New[string](),
			}
			event := github.GenericCommentEvent{
				IsPR:       true,
				IssueState: "open",
				Action:     github.GenericCommentActionCreated,
				Body:       tc.body,
				Number:     fakePR,
				User:       github.User{Login: adminUser},
				Repo:       github.Repo{Owner: github.User{Login: fakeOrg}, Name: fakeRepo},
			}

			if err := handleOverrideCancel(fc, log, &event, plugins.Override{}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.expectedStatuses, fc.statuses); diff != "" {
				t.Errorf("statuses mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedCheckRuns, fc.checkruns.CheckRuns); diff != "" {
				t.Errorf("check runs mismatch (-want +got):\n%s", diff)
			}
			if len(fc.comments) != 1 {
				t.Fatalf("expected exactly one comment, got %q", fc.comments)
			}
			for _, expected := range tc.expectedComments {
				if !strings.Contains(fc.comments[0], expected) {
					t.Errorf("expected comment containing %q, got %q", expected, fc.comments[0])
				}
			}
			for _, unexpected := range tc.unexpectedComments {
				if strings.Contains(fc.comments[0], unexpected) {
					t.Errorf("expected comment not containing %q, got %q", unexpected, fc.comments[0])
				}
			}
		})
	}
}

func TestOverrideCheckRunLifecycle(t *testing.T) {
	log := logrus.WithField("plugin", pluginName)

	originalRun := github.CheckRun{ID: 100, Name: "soak-gate", Status: "in_progress"}
	overrideRun := func(id int64) github.CheckRun {
		return github.CheckRun{ID: id, Name: "soak-gate", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "success", App: fakeProwApp, Output: github.CheckRunOutput{
			Title:   "Prow override - soak-gate",
			Summary: "Prow has received override command for the soak-gate checkrun.",
		}}
	}
	cancelledRun := func(id int64) github.CheckRun {
		return github.CheckRun{ID: id, Name: "soak-gate", HeadSHA: fakeSHA, Status: "completed", CompletedAt: "1800 BC", Conclusion: "cancelled", App: fakeProwApp, Output: github.CheckRunOutput{
			Title:   "Prow override cancelled - soak-gate",
			Summary: "Override cancelled by " + adminUser,
		}}
	}

	fc := &fakeClient{
		checkruns:    &github.CheckRunList{CheckRuns: []github.CheckRun{originalRun}},
		usesAppsAuth: true,
		jobs:         sets.New[string](),
		owners:       &fakeRepoownersClient{foc: &fakeOwnersClient{}},
	}
	event := func(body string) *github.GenericCommentEvent {
		return &github.GenericCommentEvent{
			IsPR:       true,
			IssueState: "open",
			Action:     github.GenericCommentActionCreated,
			Body:       body,
			Number:     fakePR,
			User:       github.User{Login: adminUser},
			Repo:       github.Repo{Owner: github.User{Login: fakeOrg}, Name: fakeRepo},
		}
	}
	override := func() {
		t.Helper()
		if err := handle(fc, log, event("/override soak-gate"), plugins.Override{}, false); err != nil {
			t.Fatalf("unexpected error overriding: %v", err)
		}
	}
	cancel := func() {
		t.Helper()
		if err := handleOverrideCancel(fc, log, event("/override-cancel soak-gate"), plugins.Override{}); err != nil {
			t.Fatalf("unexpected error cancelling: %v", err)
		}
	}

	override()
	cancel()
	override()
	expected := []github.CheckRun{originalRun, cancelledRun(1), overrideRun(2)}
	if diff := cmp.Diff(expected, fc.checkruns.CheckRuns); diff != "" {
		t.Fatalf("check runs after override, cancel, override mismatch (-want +got):\n%s", diff)
	}

	cancel()
	expected = []github.CheckRun{originalRun, cancelledRun(1), cancelledRun(2)}
	if diff := cmp.Diff(expected, fc.checkruns.CheckRuns); diff != "" {
		t.Errorf("check runs after second cancel mismatch (-want +got):\n%s", diff)
	}
	if last := fc.comments[len(fc.comments)-1]; !strings.Contains(last, "Cancelled overrides on behalf of admin-user: soak-gate") {
		t.Errorf("unexpected final comment: %q", last)
	}
}

func TestStickyDescriptionFitsGitHubLimit(t *testing.T) {
	maxUsername := strings.Repeat("x", 39)
	desc := stickyDescription(maxUsername)
	full := config.ContextDescriptionWithBaseSha(desc, strings.Repeat("f", 40))
	if len(full) > 140 {
		t.Errorf("sticky description with max-length username exceeds 140 chars: got %d (%q)", len(full), full)
	}
	if !strings.Contains(full, config.SkipRetestSentinel) {
		t.Errorf("sticky description lost sentinel after ContextDescriptionWithBaseSha: %q", full)
	}
}

func testPresubmitJob(name, context, sha string, state prowapi.ProwJobState, complete, report bool, pull int) prowapi.ProwJob {
	pj := prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "-pj",
			Labels: map[string]string{
				kube.OrgLabel:         fakeOrg,
				kube.RepoLabel:        fakeRepo,
				kube.PullLabel:        strconv.Itoa(pull),
				kube.ProwJobTypeLabel: string(prowapi.PresubmitJob),
			},
		},
		Spec: prowapi.ProwJobSpec{
			Type:    prowapi.PresubmitJob,
			Job:     name,
			Context: context,
			Report:  report,
			Refs: &prowapi.Refs{
				Org:  fakeOrg,
				Repo: fakeRepo,
				Pulls: []prowapi.Pull{{
					Number: pull,
					SHA:    sha,
				}},
			},
		},
		Status: prowapi.ProwJobStatus{
			State: state,
		},
	}
	if complete {
		now := metav1.Now()
		pj.Status.CompletionTime = &now
	}
	return pj
}

func aborted(pj prowapi.ProwJob) prowapi.ProwJob {
	pj.Spec.Report = false
	pj.Status.State = prowapi.AbortedState
	pj.Status.Description = abortedByOverrideDescription
	return pj
}

func TestAbortJobsOnOverride(t *testing.T) {
	log := logrus.WithField("plugin", pluginName)
	jobPresubmit := config.Presubmit{
		JobBase: config.JobBase{
			Name: "job-a",
		},
		Reporter: config.Reporter{
			Context: "job-a",
		},
	}

	jobA := testPresubmitJob("job-a", "job-a", fakeSHA, prowapi.PendingState, false, true, fakePR)
	jobB := testPresubmitJob("job-b", "job-b", fakeSHA, prowapi.PendingState, false, true, fakePR)
	completeA := testPresubmitJob("job-a", "job-a", fakeSHA, prowapi.FailureState, true, true, fakePR)

	cases := []struct {
		name         string
		body         string
		prowJobs     []prowapi.ProwJob
		wantProwJobs []prowapi.ProwJob
	}{
		{
			name:         "aborts running job on /override",
			body:         "/override job-a",
			prowJobs:     []prowapi.ProwJob{jobA},
			wantProwJobs: []prowapi.ProwJob{aborted(jobA)},
		},
		{
			name:         "aborts running job on /override-sticky",
			body:         "/override-sticky job-a",
			prowJobs:     []prowapi.ProwJob{jobA},
			wantProwJobs: []prowapi.ProwJob{aborted(jobA)},
		},
		{
			name:         "leaves running job for a different context alone",
			body:         "/override job-a",
			prowJobs:     []prowapi.ProwJob{jobA, jobB},
			wantProwJobs: []prowapi.ProwJob{aborted(jobA), jobB},
		},
		{
			name:         "leaves already-complete matching job alone",
			body:         "/override job-a",
			prowJobs:     []prowapi.ProwJob{completeA},
			wantProwJobs: []prowapi.ProwJob{completeA},
		},
		{
			name: "no matching prowjob still overrides",
			body: "/override job-a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var prowJobs []prowapi.ProwJob
			for _, pj := range tc.prowJobs {
				prowJobs = append(prowJobs, *pj.DeepCopy())
			}
			fc := &fakeClient{
				statuses: []github.Status{
					{Context: "job-a", State: github.StatusFailure, Description: "Build failed"},
				},
				ps:       []config.Presubmit{jobPresubmit},
				jobs:     sets.New[string](),
				prowJobs: prowJobs,
			}
			event := github.GenericCommentEvent{
				IsPR:       true,
				IssueState: "open",
				Action:     github.GenericCommentActionCreated,
				Body:       tc.body,
				Number:     fakePR,
				User:       github.User{Login: adminUser},
				Repo:       github.Repo{Owner: github.User{Login: fakeOrg}, Name: fakeRepo},
			}

			sticky := strings.Contains(tc.body, "/override-sticky")
			if err := handle(fc, log, &event, plugins.Override{}, sticky); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			wantDesc := statusDescription(adminUser)
			if sticky {
				wantDesc = stickyStatusDescription(adminUser)
			}
			if diff := cmp.Diff([]github.Status{{
				Context:     "job-a",
				State:       github.StatusSuccess,
				Description: wantDesc,
			}}, fc.statuses); diff != "" {
				t.Errorf("statuses mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(sets.New("job-a"), fc.jobs); diff != "" {
				t.Errorf("created jobs mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantProwJobs, fc.prowJobs); diff != "" {
				t.Errorf("prowjobs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
