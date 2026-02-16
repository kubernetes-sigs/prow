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

// Package issuemanagement implements issue management commands.
package issuemanagement

import (
	"context"
	"regexp"

	githubql "github.com/shurcooL/githubv4"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/repoowners"
)

const pluginName = "issue-management"

var (
	pinRegex   = regexp.MustCompile(`(?mi)^/pin-issue\s*$`)
	unpinRegex = regexp.MustCompile(`(?mi)^/unpin-issue\s*$`)
)

type githubClient interface {
	CreateComment(org, repo string, number int, comment string) error
	GetRepo(org, name string) (github.FullRepo, error)
	GetIssue(org, repo string, number int) (*github.Issue, error)
	MutateWithGitHubAppsSupport(context.Context, interface{}, githubql.Input, map[string]interface{}, string) error
}

type ownersClient interface {
	LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error)
}

func helpProvider(_ *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The issue management plugin provides commands for pinning and unpinning issues in. a repository.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/pin-issue",
		Description: "Pins an issue to the repository",
		WhoCanUse:   "Approvers from the top-level OWNERS file",
		Examples:    []string{"/pin-issue"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/unpin-issue",
		Description: "Unpin an issue from the repository",
		WhoCanUse:   "Approvers from the top-level OWNERS file",
		Examples:    []string{"/unpin-issue"},
	})
	return pluginHelp, nil
}

func init() {
	plugins.RegisterGenericCommentHandler(pluginName, handleGenericComment, helpProvider)
}

func handleGenericComment(pc plugins.Agent, e github.GenericCommentEvent) error {
	return handleIssues(pc.GitHubClient, pc.OwnersClient, pc.Logger.WithFields(logrus.Fields{
		"org":    e.Repo.Owner.Login,
		"repo":   e.Repo.Name,
		"number": e.Number,
		"user":   e.User.Login,
	}), e)
}

func handleIssues(gc githubClient, oc ownersClient, log *logrus.Entry, e github.GenericCommentEvent) error {
	if pinRegex.MatchString(e.Body) {
		return handlePinIsuse(gc, oc, log, e)
	}

	if unpinRegex.MatchString(e.Body) {
		return handleUnpinIsuse(gc, oc, log, e)
	}

	return nil
}
