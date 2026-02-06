/*
Copyright 2025 The Kubernetes Authors.

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

// Package issuemanagement implements issue management commands.
package issuemanagement

import (
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
)

const pluginName = "issue-management"

var (
	linkIssueRegex   = regexp.MustCompile(`(?mi)^/link-issue\s+(.+)$`)
	unlinkIssueRegex = regexp.MustCompile(`(?mi)^/unlink-issue\s+(.+)$`)
)

type githubClient interface {
	CreateComment(org, repo string, number int, comment string) error
	GetIssue(org, repo string, number int) (*github.Issue, error)
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetRepo(org, name string) (github.FullRepo, error)
	IsMember(org, user string) (bool, error)
	UpdatePullRequest(org, repo string, number int, title, body *string, open *bool, branch *string, canModify *bool) error
}

func helpProvider(_ *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The issue management plugin provides commands for linking and unlinking issues to a PR.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/link-issue <issue(s)>",
		Description: "Links issue(s) to a PR in the same or different repo.",
		WhoCanUse:   "Org members",
		Examples:    []string{"/link-issue 1234", "/link-issue org/repo#789"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/unlink-issue <issue(s)>",
		Description: "Unlinks issue(s) from a PR in the same or different repo.",
		WhoCanUse:   "Org members",
		Examples:    []string{"/unlink-issue 1234", "/unlink-issue org/repo#789"},
	})
	return pluginHelp, nil
}

func init() {
	plugins.RegisterGenericCommentHandler(pluginName, handleGenericComment, helpProvider)
}

func handleGenericComment(pc plugins.Agent, e github.GenericCommentEvent) error {
	return handleIssues(pc.GitHubClient, pc.Logger.WithFields(logrus.Fields{
		"org":    e.Repo.Owner.Login,
		"repo":   e.Repo.Name,
		"number": e.Number,
		"user":   e.User.Login,
	}), e)
}

func handleIssues(gc githubClient, log *logrus.Entry, e github.GenericCommentEvent) error {
	toLink, toUnlink, err := parseCommentForLinkCommands(e.Body)
	if err != nil {
		return err
	}
	if len(toLink) > 0 || len(toUnlink) > 0 {
		return handleLinkIssue(gc, log, e, toLink, toUnlink)
	}

	return nil
}

func parseCommentForLinkCommands(commentBody string) ([]string, []string, error) {
	extractIssues := func(re *regexp.Regexp) []string {
		var issues []string
		allMatches := re.FindAllStringSubmatch(commentBody, -1)

		for _, match := range allMatches {
			if len(match) > 1 {
				issues = append(issues, strings.Fields(match[1])...)
			}
		}
		return issues
	}

	return extractIssues(linkIssueRegex), extractIssues(unlinkIssueRegex), nil
}
