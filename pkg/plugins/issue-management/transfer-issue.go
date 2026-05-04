/*
Copyright 2021 The Kubernetes Authors.

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

// Package transferissue implements the `/transfer-issue` command which allows members of the org
// to transfer issues between repos
package issuemanagement

import (
	"context"
	"fmt"
	"strings"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

func handleTransfer(gc githubClient, log *logrus.Entry, e github.GenericCommentEvent, dstRepoName string) error {
	org := e.Repo.Owner.Login
	srcRepoName := e.Repo.Name
	srcRepoPair := org + "/" + srcRepoName
	user := e.User.Login

	if e.IsPR || e.Action != github.GenericCommentActionCreated {
		return nil
	}

	dstRepoName = strings.TrimSpace(dstRepoName)
	dstRepoPair := org + "/" + dstRepoName

	dstRepo, err := gc.GetRepo(org, dstRepoName)
	if err != nil {
		log.WithError(err).WithField("dstRepo", dstRepoPair).Warning("could not fetch destination repo")
		// TODO: Might want to add another GetRepo type call that checks if a repo exists vs a bad request
		return gc.CreateComment(
			org, srcRepoName, e.Number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Something went wrong or the destination repo %s does not exist.", dstRepoPair)),
		)
	}

	isMember, err := gc.IsMember(org, user)
	if err != nil {
		return fmt.Errorf("unable to fetch if %s is an org member of %s: %w", user, org, err)
	}
	if !isMember {
		return gc.CreateComment(
			org, srcRepoName, e.Number,
			plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, "You must be an org member to transfer this issue."),
		)
	}

	m, err := transferIssue(gc, org, dstRepo.NodeID, e.NodeID)
	if err != nil {
		log.WithError(err).WithFields(logrus.Fields{
			"issueNumber": e.Number,
			"srcRepo":     srcRepoPair,
			"dstRepo":     dstRepoPair,
		}).Error("issue could not be transferred")
		return err
	}
	log.WithFields(logrus.Fields{
		"user":        user,
		"org":         org,
		"srcRepo":     srcRepoName,
		"issueNumber": e.Number,
		"dstURL":      m.TransferIssue.Issue.URL,
	}).Infof("successfully transferred issue")
	return nil
}

// TransferIssueMutation is a GraphQL mutation struct compatible with shurcooL/githubql's client
//
// See https://docs.github.com/en/graphql/reference/input-objects#transferissueinput
type transferIssueMutation struct {
	TransferIssue struct {
		Issue struct {
			URL githubql.URI
		}
	} `graphql:"transferIssue(input: $input)"`
}

// TransferIssue will move an issue from one repo to another in the same org.
//
// See https://docs.github.com/en/graphql/reference/mutations#transferissue
//
// In the future we may want to interact with the TransferredEvent on the issue IssueTimeline
// See https://docs.github.com/en/graphql/reference/objects#transferredevent
// https://docs.github.com/en/graphql/reference/unions#issuetimelineitem
func transferIssue(gc githubClient, org, dstRepoNodeID string, issueNodeID string) (*transferIssueMutation, error) {
	m := &transferIssueMutation{}
	input := githubql.TransferIssueInput{
		IssueID:      githubql.ID(issueNodeID),
		RepositoryID: githubql.ID(dstRepoNodeID),
	}
	err := gc.MutateWithGitHubAppsSupport(context.Background(), m, input, nil, org)
	return m, err
}
