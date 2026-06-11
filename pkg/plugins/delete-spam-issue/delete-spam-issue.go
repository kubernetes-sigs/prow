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

// Package deletespamissue implements the delete-spam-issue plugin.
package deletespamissue

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
)

const pluginName = "delete-spam-issue"

var deleteSpamIssueRe = regexp.MustCompile(`(?mi)^/delete-spam-issue\s*$`)

type githubClient interface {
	CreateComment(org, repo string, number int, comment string) error
	TeamBySlugHasMember(org string, teamSlug string, memberLogin string) (bool, error)
	MutateWithGitHubAppsSupport(context.Context, any, githubql.Input, map[string]any, string) error
}

func init() {
	plugins.RegisterGenericCommentHandler(pluginName, handleGenericCommentEvent, helpProvider)
}

func helpProvider(_ *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	yamlSnippet, err := plugins.CommentMap.GenYaml(&plugins.Configuration{
		DeleteSpamIssue: plugins.DeleteSpamIssue{
			AllowedTeams: []string{"spam-fighters"},
			AllowedUsers: []string{"alice", "bob"},
		},
	})
	if err != nil {
		logrus.WithError(err).Warnf("cannot generate comments for %s plugin", pluginName)
	}
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The delete-spam-issue plugin allows a configured list of users to delete issues that are deemed spam.",
		Snippet:     yamlSnippet,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/delete-spam-issue",
		Description: "Deletes the issue because it is deemed spam.",
		WhoCanUse:   "Users listed under the plugin's allowed_users configuration and members of teams listed under allowed_teams.",
		Examples:    []string{"/delete-spam-issue"},
	})
	return pluginHelp, nil
}

func handleGenericCommentEvent(pc plugins.Agent, e github.GenericCommentEvent) error {
	return handleGenericComment(pc.GitHubClient, pc.PluginConfig.DeleteSpamIssue, pc.Logger, e)
}

func handleGenericComment(gc githubClient, config plugins.DeleteSpamIssue, log *logrus.Entry, e github.GenericCommentEvent) error {
	if e.Action != github.GenericCommentActionCreated {
		return nil
	}

	if !deleteSpamIssueRe.MatchString(e.Body) {
		return nil
	}

	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	number := e.Number
	user := e.User.Login

	if e.IsPR {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, "`/delete-spam-issue` can only be used on GitHub issues."))
	}

	canDelete, canNotDeleteReason, err := canUserDeleteIssue(gc, org, user, config)
	if err != nil {
		return fmt.Errorf("failed to check if user can delete issue: %w", err)
	}
	if !canDelete {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, canNotDeleteReason))
	}

	// Although the issue is being deleted, still leave a comment for an email trail just in case.
	if err := gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, "This issue is being deleted because it has been deemed spam. If you believe this is a mistake, please reach out in the [#github-management](https://kubernetes.slack.com/messages/github-management) Slack channel.")); err != nil {
		return fmt.Errorf("failed to comment on issue: %w", err)
	}

	if err := deleteIssue(gc, org, e.NodeID); err != nil {
		log.WithError(err).Error("Failed to delete issue.")
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, fmt.Sprintf("Unable to delete issue #%d. Please try again later.", number)))
	}

	return nil
}

func canUserDeleteIssue(gc githubClient, org string, user string, config plugins.DeleteSpamIssue) (canDelete bool, canNotDeleteReason string, err error) {
	for _, allowedUser := range config.AllowedUsers {
		if strings.EqualFold(allowedUser, user) {
			return true, "", nil
		}
	}

	for _, team := range config.AllowedTeams {
		isMember, err := gc.TeamBySlugHasMember(org, team, user)
		if err != nil {
			return false, "", err
		}
		if isMember {
			return true, "", nil
		}
	}

	msg := "This issue cannot be deleted, because you are not in one of the allowed teams and are not an allowed user."
	if len(config.AllowedTeams) > 0 {
		msg += fmt.Sprintf(" Must be a member of one of these teams: %s", strings.Join(config.AllowedTeams, ", "))
	}
	return false, msg, nil
}

// deleteIssueMutation is a GraphQL mutation struct compatible with shurcooL/githubql's client
//
// See https://docs.github.com/en/graphql/reference/input-objects#deleteissueinput
type deleteIssueMutation struct {
	DeleteIssue struct {
		Repository struct {
			Name githubql.String
		}
	} `graphql:"deleteIssue(input: $input)"`
}

// deleteIssue deletes an issue. Deleting issues is only available via
// GitHub's GraphQL API.
//
// See https://docs.github.com/en/graphql/reference/mutations#deleteissue
func deleteIssue(gc githubClient, org, issueNodeID string) error {
	m := &deleteIssueMutation{}
	input := githubql.DeleteIssueInput{
		IssueID: githubql.ID(issueNodeID),
	}
	return gc.MutateWithGitHubAppsSupport(context.Background(), m, input, nil, org)
}
