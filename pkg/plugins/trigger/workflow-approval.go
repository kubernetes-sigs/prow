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
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/plugins"
)

// GitHub creates the workflow runs some seconds after it sends the webhook.
// These values give 5 attempts over approximately 30 seconds.
const (
	workflowRunPollInterval = 2000
	workflowRunPollSteps    = 5
)

// shouldApproveWorkflowRuns reports whether this pull request event can create
// workflow runs that wait for approval.
func shouldApproveWorkflowRuns(c Client, pr github.PullRequestEvent) bool {
	switch pr.Action {
	case github.PullRequestActionSynchronize,
		github.PullRequestActionReopened,
		github.PullRequestActionReadyForReview:
		return true
	case github.PullRequestActionEdited:
		return baseChanged(pr)
	case github.PullRequestActionLabeled:
		if pr.Label.Name != labels.OkToTest {
			return false
		}
		// The bot adds this label after an /ok-to-test comment, and the
		// comment handler approves the runs already.
		botUserChecker, err := c.GitHubClient.BotUserChecker()
		if err != nil {
			c.Logger.WithError(err).Warn("Could not get the bot user, skipping the workflow run approval.")
			return false
		}
		return !botUserChecker(pr.Sender.Login)
	}
	return false
}

// approvePendingWorkflowRunsIfTrusted approves the workflow runs if the pull
// request is trusted.
//
// The approval is best effort and it returns no error, because it must not
// stop the presubmits.
func approvePendingWorkflowRunsIfTrusted(c Client, trigger plugins.Trigger, pr github.PullRequestEvent, millisecondOverride ...time.Duration) {
	org, repo, a := orgRepoAuthor(pr.PullRequest)

	// Give nil labels. TrustedPullRequest then reads them again, because a
	// label that a handler added a moment ago is not in the snapshot of the
	// caller.
	if _, trusted, err := TrustedPullRequest(c.GitHubClient, trigger, string(a), org, repo, pr.PullRequest.Number, nil); err != nil {
		c.Logger.WithError(err).Warn("Could not check the trust of the pull request, skipping the workflow run approval.")
		return
	} else if !trusted {
		return
	}

	approvePendingWorkflowRuns(c, trigger, org, repo, pr.PullRequest, millisecondOverride...)
}

// approvePendingWorkflowRuns approves the workflow runs of the head commit
// that wait at the approval gate.
//
// The function polls with a backoff, because GitHub creates the runs after it
// sends the webhook. It stops when the runs exist and no run waits for
// approval.
// An empty result means that GitHub did not create the runs yet, which is not
// the same as a pull request that starts no workflow.
func approvePendingWorkflowRuns(c Client, trigger plugins.Trigger, org, repo string, pr github.PullRequest, millisecondOverride ...time.Duration) {
	millisecond := time.Millisecond
	if len(millisecondOverride) == 1 {
		millisecond = millisecondOverride[0]
	}

	log := c.Logger.WithFields(logrus.Fields{"org": org, "repo": repo, "sha": pr.Head.SHA})

	backoff := wait.Backoff{
		Duration: workflowRunPollInterval * millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    workflowRunPollSteps,
	}
	// Approve each run one time only. A run that stays at the gate after an
	// approval has a cause that a further attempt does not remove.
	attempted := sets.New[int]()

	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		runs, err := c.GitHubClient.ListWorkflowRunsByHeadBranch(org, repo, pr.Head.Ref, pr.Head.SHA)
		if err != nil {
			log.WithError(err).Warn("Could not list the workflow runs, will retry.")
			return false, nil
		}
		if len(runs) == 0 {
			return false, nil
		}

		var pending []github.WorkflowRun
		for _, run := range runs {
			if github.IsPendingApprovalRun(run) && !attempted.Has(run.ID) {
				pending = append(pending, run)
			}
		}
		if len(pending) == 0 {
			return true, nil
		}
		if !approvalStillValid(c, trigger, org, repo, pr) {
			return true, nil
		}
		for _, run := range pending {
			attempted.Insert(run.ID)
			approveWorkflowRun(c, log, org, repo, run)
		}
		// Do one more attempt, because GitHub can hold a further run at the
		// gate while this attempt approves the runs of the first batch.
		return false, nil
	})
	if wait.Interrupted(err) {
		log.Info("Gave up waiting for the workflow runs to leave the approval gate.")
	}
}

// approvalStillValid reads the pull request again and reports whether the
// approval is still correct. The poll waits some seconds, and a new commit or
// a revoked ok-to-test label can arrive in that time.
//
// This does not make the approval atomic. A push can still arrive between this
// check and the approval.
func approvalStillValid(c Client, trigger plugins.Trigger, org, repo string, pr github.PullRequest) bool {
	current, err := c.GitHubClient.GetPullRequest(org, repo, pr.Number)
	if err != nil {
		c.Logger.WithError(err).Warn("Could not read the pull request again, skipping the workflow run approval.")
		return false
	}
	if current.Head.SHA != pr.Head.SHA {
		c.Logger.Info("The head commit changed during the poll, skipping the workflow run approval.")
		return false
	}

	_, trusted, err := TrustedPullRequest(c.GitHubClient, trigger, current.User.Login, org, repo, pr.Number, nil)
	if err != nil {
		c.Logger.WithError(err).Warn("Could not check the trust of the pull request again, skipping the workflow run approval.")
		return false
	}
	if !trusted {
		c.Logger.Info("The pull request is no longer trusted, skipping the workflow run approval.")
	}
	return trusted
}

// approveWorkflowRun approves one workflow run.
func approveWorkflowRun(c Client, log *logrus.Entry, org, repo string, run github.WorkflowRun) {
	log = log.WithFields(logrus.Fields{"runID": run.ID, "runName": run.Name})

	err := c.GitHubClient.ApproveGitHubWorkflowRun(org, repo, run.ID)
	if err == nil {
		log.Info("Approved the workflow run.")
		return
	}

	// Per GitHub API docs (https://docs.github.com/en/rest/actions/workflow-runs#approve-a-workflow-run-for-a-fork-pull-request):
	// - 404: Workflow run doesn't exist or is not pending approval (already approved/completed)
	// - 403: Permission denied or non-fork PR
	// 404 is expected in race conditions where another actor approved the run.
	// 403 can mean the approve endpoint doesn't apply (it only works for
	// fork PRs). For same-repo PRs created by bots, fall back to
	// rerunning the workflow which changes the triggering_actor to the
	// API caller and bypasses the approval gate.
	switch {
	case github.IsNotFound(err):
		log.Infof("workflow run not pending approval (already approved or completed): %v", err)
	case github.IsForbidden(err):
		log.Infof("approve endpoint returned 403 (likely non-fork PR), falling back to rerun: %v", err)
		if rerunErr := c.GitHubClient.TriggerGitHubWorkflow(org, repo, run.ID); rerunErr != nil {
			log.Errorf("failed to rerun workflow as fallback for approval: %v", rerunErr)
		} else {
			log.Infof("successfully reran workflow run as fallback for approval")
		}
	default:
		log.Errorf("failed to approve workflow run: %v", err)
	}
}
