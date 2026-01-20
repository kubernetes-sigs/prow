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

package issuemanagement

import (
	"context"
	"fmt"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

func handlePinOrUnpinIssue(gc githubClient, oc ownersClient, log *logrus.Entry, e github.GenericCommentEvent, pin bool) error {

	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	number := e.Number
	user := e.User.Login

	if e.IsPR {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, "`/pin-issue` and `/unpin-issue` can only be triggered on GitHub issues."))
	}

	if e.Action != github.GenericCommentActionCreated {
		return nil
	}

	// Set action-specific variables based on pin/unpin operation
	action := "pin"
	mutateFn := pinIssue
	pastTense := "pinned to"
	if !pin {
		action = "unpin"
		mutateFn = unpinIssue
		pastTense = "unpinned from"
	}
	log.Infof("Handling %s issue", action)

	if message, err := isRepoOwner(oc, org, repo, user, e.Repo.DefaultBranch); err != nil {
		log.WithError(err).Warn("Authorization check failed")
		return gc.CreateComment(
			org, repo, number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, message),
		)
	}

	issue, err := gc.GetIssue(org, repo, number)
	if err != nil {
		return fmt.Errorf("failed to get issue: %w", err)
	}

	if err := mutateFn(gc, org, issue.NodeID); err != nil {
		log.WithError(err).Errorf("failed to %s issue", action)
		return gc.CreateComment(
			org, repo, number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Unable to %s issue #%d. Please try again later.", action, number)),
		)
	}

	return gc.CreateComment(
		org, repo, number,
		plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Issue #%d has been %s the repository.", number, pastTense)),
	)
}

func isRepoOwner(oc ownersClient, org, repo, user, defaultBranch string) (string, error) {
	owners, err := oc.LoadRepoOwners(org, repo, defaultBranch)
	if err != nil {
		return fmt.Sprintf("Unable to determine whether %s is an approver from the top-level OWNERS of %s/%s.", user, org, repo), err
	}

	if !owners.TopLevelApprovers().Has(github.NormLogin(user)) {
		return "You are not authorized to pin or unpin issues on this repository. Only approvers from the top-level OWNERS file can use this command.", fmt.Errorf("user %s is not a top-level approver", user)
	}
	return "", nil
}

// pinIssueMutation is a GraphQL mutation struct compatible with shurcooL/githubql's client
type pinIssueMutation struct {
	PinIssue struct {
		Issue struct {
			ID githubql.ID
		}
	} `graphql:"pinIssue(input: $input)"`
}

// unpinIssueMutation is a GraphQL mutation struct compatible with shurcooL/githubql's client
type unpinIssueMutation struct {
	UnpinIssue struct {
		Issue struct {
			ID githubql.ID
		}
	} `graphql:"unpinIssue(input: $input)"`
}

// pinIssue pins an issue to a repository
func pinIssue(gc githubClient, org, issueNodeID string) error {
	m := &pinIssueMutation{}
	input := githubql.PinIssueInput{
		IssueID: githubql.ID(issueNodeID),
	}
	return gc.MutateWithGitHubAppsSupport(context.Background(), m, input, nil, org)
}

// unpinIssue unpins an issue from a repository
func unpinIssue(gc githubClient, org, issueNodeID string) error {
	m := &unpinIssueMutation{}
	input := githubql.UnpinIssueInput{
		IssueID: githubql.ID(issueNodeID),
	}
	return gc.MutateWithGitHubAppsSupport(context.Background(), m, input, nil, org)
}
