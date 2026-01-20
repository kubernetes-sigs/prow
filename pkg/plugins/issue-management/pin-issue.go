/*
Copyright 2026 The Kubernetes Authors.

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

func handlePinIsuse(gc githubClient, oc ownersClient, log *logrus.Entry, e github.GenericCommentEvent) error {
	log.Info("Handling pin issue")
	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	number := e.Number
	user := e.User.Login

	if e.IsPR || e.Action != github.GenericCommentActionCreated {
		return nil
	}

	if err := isRepoOwner(gc, oc, org, repo, user); err != nil {
		return gc.CreateComment(
			org, repo, number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, err.Error()),
		)
	}

	issue, err := gc.GetIssue(org, repo, number)
	if err != nil {
		return fmt.Errorf("failed to get issue: %w", err)
	}

	if err := pinIssue(gc, org, issue.NodeID); err != nil {
		log.Error("Failed to pin issue", err)
		return gc.CreateComment(
			org, repo, number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Failed to pin issue #%d: %v", number, err)),
		)
	}

	return gc.CreateComment(
		org, repo, number,
		plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Issue #%d has been pinned to the repository.", number)),
	)

}

func handleUnpinIsuse(gc githubClient, oc ownersClient, log *logrus.Entry, e github.GenericCommentEvent) error {
	log.Info("Handling unpin issue")
	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	number := e.Number
	user := e.User.Login

	if e.IsPR || e.Action != github.GenericCommentActionCreated {
		return nil
	}

	if err := isRepoOwner(gc, oc, org, repo, user); err != nil {
		return gc.CreateComment(
			org, repo, number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, err.Error()),
		)
	}

	issue, err := gc.GetIssue(org, repo, number)
	if err != nil {
		return fmt.Errorf("failed to get issue: %w", err)
	}

	if err := unpinIssue(gc, org, issue.NodeID); err != nil {
		log.Error("Failed to unpin issue", err)
		return gc.CreateComment(
			org, repo, number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Failed to unpin issue #%d: %v", number, err)),
		)
	}

	return gc.CreateComment(
		org, repo, number,
		plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Issue #%d has been unpinned from the repository.", number)),
	)
}

func isRepoOwner(gc githubClient, oc ownersClient, org, repo, user string) error {
	repoDetails, err := gc.GetRepo(org, repo)
	if err != nil {
		return fmt.Errorf("Failed to get repository details of %s/%s: %w", org, repo, err)
	}
	owners, err := oc.LoadRepoOwners(org, repo, repoDetails.DefaultBranch)
	if err != nil {
		return fmt.Errorf("Unable to determine whether %s is an approver from the top-level OWNERS of %s/%s: %w.", user, org, repo, err)
	}

	if !owners.TopLevelApprovers().Has(github.NormLogin(user)) {
		return fmt.Errorf("You are not authorized to pin or unpin issues on this repository. Only approvers from the top-level OWNERS file can use this command.")
	}
	return nil
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
