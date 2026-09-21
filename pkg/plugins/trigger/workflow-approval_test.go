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

package trigger

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/client/clientset/versioned/fake"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/plugins"
)

const (
	approvalOrg     = "org"
	approvalRepo    = "repo"
	approvalBranch  = "pr-branch"
	approvalHeadSHA = "abc123"
)

func pendingRun(id int) github.WorkflowRun {
	return github.WorkflowRun{
		ID:         id,
		Event:      "pull_request",
		Status:     "completed",
		Conclusion: "action_required",
	}
}

func startedRun(id int) github.WorkflowRun {
	return github.WorkflowRun{ID: id, Event: "pull_request", Status: "in_progress"}
}

// approvalTestClient counts the list calls and controls what each one returns,
// which is how the tests observe the poll.
type approvalTestClient struct {
	*fakegithub.FakeClient

	listCalls  int
	runs       func(call int) []github.WorkflowRun
	beforeList func(call int)
}

func (c *approvalTestClient) ListWorkflowRunsByHeadBranch(org, repo, branchName, headSHA string) ([]github.WorkflowRun, error) {
	c.listCalls++
	if c.beforeList != nil {
		c.beforeList(c.listCalls)
	}
	if c.runs == nil {
		return nil, nil
	}
	return c.runs(c.listCalls), nil
}

func approvalTestPullRequest(author string) github.PullRequest {
	return github.PullRequest{
		Number: 0,
		User:   github.User{Login: author},
		Base: github.PullRequestBranch{
			Ref: "master",
			Repo: github.Repo{
				Owner:    github.User{Login: approvalOrg},
				Name:     approvalRepo,
				FullName: approvalOrg + "/" + approvalRepo,
			},
		},
		Head: github.PullRequestBranch{Ref: approvalBranch, SHA: approvalHeadSHA},
	}
}

func TestApproveWorkflowRunsOnPullRequestEvent(t *testing.T) {
	const bot = "k8s-ci-robot"

	testCases := []struct {
		name           string
		action         github.PullRequestEventAction
		label          string
		sender         string
		author         string
		hasOkToTest    bool
		baseChange     bool
		noPresubmits   bool
		flagOff        bool
		runs           []github.WorkflowRun
		expectApproved []string
	}{
		{
			name:           "a push to a trusted pull request approves the pending runs",
			action:         github.PullRequestActionSynchronize,
			author:         "t",
			runs:           []github.WorkflowRun{pendingRun(1), pendingRun(2)},
			expectApproved: []string{"org/repo/1", "org/repo/2"},
		},
		{
			name:           "a repository without presubmits still approves",
			action:         github.PullRequestActionSynchronize,
			author:         "t",
			noPresubmits:   true,
			runs:           []github.WorkflowRun{pendingRun(1)},
			expectApproved: []string{"org/repo/1"},
		},
		{
			name:   "the flag off approves nothing",
			action: github.PullRequestActionSynchronize,
			author: "t",
			// The whole feature is behind trigger_github_workflows.
			flagOff: true,
			runs:    []github.WorkflowRun{pendingRun(1)},
		},
		{
			name:   "a push to an untrusted pull request approves nothing",
			action: github.PullRequestActionSynchronize,
			author: "u",
			runs:   []github.WorkflowRun{pendingRun(1)},
		},
		{
			name:           "a push to an untrusted pull request with ok-to-test approves",
			action:         github.PullRequestActionSynchronize,
			author:         "u",
			hasOkToTest:    true,
			runs:           []github.WorkflowRun{pendingRun(1)},
			expectApproved: []string{"org/repo/1"},
		},
		{
			name:        "an ok-to-test label from the bot approves nothing",
			action:      github.PullRequestActionLabeled,
			label:       labels.OkToTest,
			sender:      bot,
			author:      "u",
			hasOkToTest: true,
			// The comment handler approved the runs already.
			runs: []github.WorkflowRun{pendingRun(1)},
		},
		{
			name:           "an ok-to-test label from a person approves the pending runs",
			action:         github.PullRequestActionLabeled,
			label:          labels.OkToTest,
			sender:         "a-human",
			author:         "u",
			hasOkToTest:    true,
			runs:           []github.WorkflowRun{pendingRun(1)},
			expectApproved: []string{"org/repo/1"},
		},
		{
			name:        "an lgtm label approves nothing",
			action:      github.PullRequestActionLabeled,
			label:       labels.LGTM,
			sender:      "a-human",
			author:      "u",
			hasOkToTest: true,
			// The build-once of LGTM runs an untrusted pull request on
			// purpose. That rule must not reach GitHub Actions.
			runs: []github.WorkflowRun{pendingRun(1)},
		},
		{
			name:   "an opened pull request approves nothing",
			action: github.PullRequestActionOpened,
			author: "t",
			runs:   []github.WorkflowRun{pendingRun(1)},
		},
		{
			name:           "an edit that changes the base approves",
			action:         github.PullRequestActionEdited,
			author:         "t",
			baseChange:     true,
			runs:           []github.WorkflowRun{pendingRun(1)},
			expectApproved: []string{"org/repo/1"},
		},
		{
			name:   "an edit that keeps the base approves nothing",
			action: github.PullRequestActionEdited,
			author: "t",
			runs:   []github.WorkflowRun{pendingRun(1)},
		},
		{
			name:   "a run of another event is not approved",
			action: github.PullRequestActionSynchronize,
			author: "t",
			runs: []github.WorkflowRun{
				{ID: 1, Event: "schedule", Status: "completed", Conclusion: "action_required"},
			},
		},
		{
			name:   "a run that left the gate is not approved",
			action: github.PullRequestActionSynchronize,
			author: "t",
			runs:   []github.WorkflowRun{startedRun(1)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fakegithub.NewFakeClient()
			fakeClient.OrgMembers = map[string][]string{approvalOrg: {"t"}}
			prObject := approvalTestPullRequest(tc.author)
			fakeClient.PullRequests = map[int]*github.PullRequest{0: &prObject}
			if tc.hasOkToTest {
				fakeClient.IssueLabelsExisting = append(fakeClient.IssueLabelsExisting, issueLabels(labels.OkToTest)...)
			}

			g := &approvalTestClient{
				FakeClient: fakeClient,
				runs:       func(int) []github.WorkflowRun { return tc.runs },
			}

			cfg := &config.Config{}
			if !tc.noPresubmits {
				presubmits := map[string][]config.Presubmit{
					approvalOrg + "/" + approvalRepo: {
						{JobBase: config.JobBase{Name: "jib"}, AlwaysRun: true},
					},
				}
				if err := cfg.SetPresubmits(presubmits); err != nil {
					t.Fatalf("failed to set presubmits: %v", err)
				}
			}

			c := Client{
				GitHubClient:  g,
				ProwJobClient: fake.NewSimpleClientset().ProwV1().ProwJobs("namespace"),
				Config:        cfg,
				Logger:        logrus.WithField("plugin", PluginName),
			}

			sender := tc.sender
			if sender == "" {
				sender = tc.author
			}
			event := github.PullRequestEvent{
				Action:      tc.action,
				Label:       github.Label{Name: tc.label},
				PullRequest: approvalTestPullRequest(tc.author),
				Sender:      github.User{Login: sender},
			}
			if tc.baseChange {
				event.Changes = json.RawMessage(`{"base":{"ref":{"from":"REF"}, "sha":{"from":"SHA"}}}`)
			}

			trigger := plugins.Trigger{
				TrustedOrg:             approvalOrg,
				OnlyOrgMembers:         true,
				TriggerGitHubWorkflows: !tc.flagOff,
			}
			trigger.SetDefaults()

			if err := handlePR(c, trigger, event, time.Nanosecond); err != nil {
				t.Fatalf("Didn't expect error: %s", err)
			}

			if !reflect.DeepEqual(sets.New[string](g.ApprovedWorkflowRuns...), sets.New[string](tc.expectApproved...)) {
				t.Errorf("Expected approvals %v, got %v", tc.expectApproved, g.ApprovedWorkflowRuns)
			}
		})
	}
}

func TestApprovePendingWorkflowRunsPoll(t *testing.T) {
	testCases := []struct {
		name string
		// runs answers each list call, where the argument counts from 1.
		runs func(call int) []github.WorkflowRun
		// duringPoll runs before the given list call and changes the state of
		// the pull request.
		duringPoll     func(call int, f *fakegithub.FakeClient)
		expectApproved []string
		expectListCall int
	}{
		{
			name:           "no run waits for approval, one list call",
			runs:           func(int) []github.WorkflowRun { return []github.WorkflowRun{startedRun(1)} },
			expectListCall: 1,
		},
		{
			name: "two runs wait for approval, one further list confirms",
			runs: func(int) []github.WorkflowRun {
				return []github.WorkflowRun{pendingRun(1), pendingRun(2)}
			},
			expectApproved: []string{"org/repo/1", "org/repo/2"},
			expectListCall: 2,
		},
		{
			name: "a run that arrives on the third attempt is approved",
			runs: func(call int) []github.WorkflowRun {
				if call < 3 {
					return nil
				}
				return []github.WorkflowRun{pendingRun(1)}
			},
			expectApproved: []string{"org/repo/1"},
			expectListCall: 4,
		},
		{
			name: "a sibling run that arrives later is approved",
			runs: func(call int) []github.WorkflowRun {
				if call == 1 {
					return []github.WorkflowRun{pendingRun(1)}
				}
				return []github.WorkflowRun{pendingRun(1), pendingRun(2)}
			},
			expectApproved: []string{"org/repo/1", "org/repo/2"},
			expectListCall: 3,
		},
		{
			// A push that starts no workflow is the normal case for a
			// repository without Actions. It must not loop forever.
			name:           "a push without workflow runs uses every attempt",
			runs:           func(int) []github.WorkflowRun { return nil },
			expectListCall: workflowRunPollSteps,
		},
		{
			name: "the head commit changed during the poll, nothing is approved",
			runs: func(int) []github.WorkflowRun { return []github.WorkflowRun{pendingRun(1)} },
			duringPoll: func(call int, f *fakegithub.FakeClient) {
				if call == 1 {
					f.PullRequests[0].Head.SHA = "a-newer-commit"
				}
			},
			expectListCall: 1,
		},
		{
			name: "ok-to-test was revoked during the poll, nothing is approved",
			runs: func(int) []github.WorkflowRun { return []github.WorkflowRun{pendingRun(1)} },
			duringPoll: func(call int, f *fakegithub.FakeClient) {
				if call == 1 {
					f.IssueLabelsExisting = nil
				}
			},
			expectListCall: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fakegithub.NewFakeClient()
			// The author is not a member of the org. The trust comes from the
			// ok-to-test label, and a revocation is visible.
			fakeClient.OrgMembers = map[string][]string{approvalOrg: {}}
			fakeClient.IssueLabelsExisting = issueLabels(labels.OkToTest)
			prObject := approvalTestPullRequest("u")
			fakeClient.PullRequests = map[int]*github.PullRequest{0: &prObject}

			g := &approvalTestClient{
				FakeClient: fakeClient,
				runs:       tc.runs,
			}
			if tc.duringPoll != nil {
				g.beforeList = func(call int) { tc.duringPoll(call, fakeClient) }
			}

			c := Client{
				GitHubClient: g,
				Logger:       logrus.WithField("plugin", PluginName),
			}
			trigger := plugins.Trigger{TrustedOrg: approvalOrg, OnlyOrgMembers: true}
			trigger.SetDefaults()

			approvePendingWorkflowRuns(c, trigger, approvalOrg, approvalRepo, approvalTestPullRequest("u"), time.Nanosecond)

			if !reflect.DeepEqual(sets.New[string](g.ApprovedWorkflowRuns...), sets.New[string](tc.expectApproved...)) {
				t.Errorf("Expected approvals %v, got %v", tc.expectApproved, g.ApprovedWorkflowRuns)
			}
			if g.listCalls != tc.expectListCall {
				t.Errorf("Expected %d list calls, got %d", tc.expectListCall, g.listCalls)
			}
		})
	}
}

// TestApprovalDoesNotDelayTheAbort makes sure that a push aborts the old jobs
// before the poll starts. The approval is deferred exactly for this reason.
func TestApprovalDoesNotDelayTheAbort(t *testing.T) {
	jobToAbort := &prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-to-abort",
			Namespace: "namespace",
			Labels: map[string]string{
				kube.OrgLabel:         approvalOrg,
				kube.RepoLabel:        approvalRepo,
				kube.PullLabel:        "0",
				kube.ProwJobTypeLabel: string(prowapi.PresubmitJob),
			},
		},
	}

	fakeClient := fakegithub.NewFakeClient()
	fakeClient.OrgMembers = map[string][]string{approvalOrg: {"t"}}
	prObject := approvalTestPullRequest("t")
	fakeClient.PullRequests = map[int]*github.PullRequest{0: &prObject}

	fakeProwJobClient := fake.NewSimpleClientset(jobToAbort)

	var abortedBeforeList bool
	g := &approvalTestClient{
		FakeClient: fakeClient,
		runs:       func(int) []github.WorkflowRun { return []github.WorkflowRun{pendingRun(1)} },
		beforeList: func(call int) {
			if call != 1 {
				return
			}
			pj, err := fakeProwJobClient.ProwV1().ProwJobs("namespace").Get(t.Context(), jobToAbort.Name, metav1.GetOptions{})
			if err != nil {
				t.Errorf("failed to get prowjob: %v", err)
				return
			}
			abortedBeforeList = pj.Status.State == prowapi.AbortedState
		},
	}

	cfg := &config.Config{}
	presubmits := map[string][]config.Presubmit{
		approvalOrg + "/" + approvalRepo: {
			{JobBase: config.JobBase{Name: "jib"}, AlwaysRun: true},
		},
	}
	if err := cfg.SetPresubmits(presubmits); err != nil {
		t.Fatalf("failed to set presubmits: %v", err)
	}

	c := Client{
		GitHubClient:  g,
		ProwJobClient: fakeProwJobClient.ProwV1().ProwJobs("namespace"),
		Config:        cfg,
		Logger:        logrus.WithField("plugin", PluginName),
	}

	event := github.PullRequestEvent{
		Action:      github.PullRequestActionSynchronize,
		PullRequest: approvalTestPullRequest("t"),
		Sender:      github.User{Login: "t"},
	}
	trigger := plugins.Trigger{
		TrustedOrg:             approvalOrg,
		OnlyOrgMembers:         true,
		TriggerGitHubWorkflows: true,
	}
	trigger.SetDefaults()

	if err := handlePR(c, trigger, event, time.Nanosecond); err != nil {
		t.Fatalf("Didn't expect error: %s", err)
	}

	if !abortedBeforeList {
		t.Error("The poll started before the abort of the old jobs.")
	}
	if len(g.ApprovedWorkflowRuns) != 1 {
		t.Errorf("Expected one approval, got %v", g.ApprovedWorkflowRuns)
	}
}
