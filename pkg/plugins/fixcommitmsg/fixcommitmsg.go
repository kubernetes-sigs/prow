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

// Package fixcommitmsg provides the /fix-commit-messages command. When
// triggered, it rewrites commit messages on a pull request to remove
// close-issue keywords and fixup/amend/squash prefixes that are flagged
// as invalid by the invalidcommitmsg plugin. Only members of the configured
// maintainer team can trigger this command.
package fixcommitmsg

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	git "sigs.k8s.io/prow/pkg/git/v2"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/plugins/invalidcommitmsg"
)

const (
	pluginName            = "fixcommitmsg"
	invalidCommitMsgLabel = "do-not-merge/invalid-commit-message"
)

var commandRe = regexp.MustCompile(`(?mi)^/fix-commit-messages\s*$`)

func init() {
	plugins.RegisterGenericCommentHandler(pluginName, handleGenericCommentEvent, helpProvider)
}

func helpProvider(_ *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The fixcommitmsg plugin rewrites commit messages on a pull request to remove close-issue keywords and fixup/amend/squash prefixes that are flagged by the invalidcommitmsg plugin.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/fix-commit-messages",
		Description: "Rewrites commit messages in the PR to replace close-issue keywords (closes/fixes/resolves #NNN) with 'Related: NNN' and strip fixup!/amend!/squash! prefixes.",
		Featured:    false,
		WhoCanUse:   "Members of the configured maintainer team.",
		Examples:    []string{"/fix-commit-messages"},
	})
	return pluginHelp, nil
}

type githubClient interface {
	CreateComment(org, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	ListPullRequestCommits(org, repo string, number int) ([]github.RepositoryCommit, error)
	ListTeams(org string) ([]github.Team, error)
	ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error)
	RemoveLabel(org, repo string, number int, label string) error
}

func handleGenericCommentEvent(pc plugins.Agent, e github.GenericCommentEvent) error {
	return handle(pc.GitHubClient, pc.GitClient, pc.Logger, pc.PluginConfig.FixCommitMsg, e)
}

func handle(gc githubClient, gitFactory git.ClientFactory, log *logrus.Entry, cfg plugins.FixCommitMsg, e github.GenericCommentEvent) error {
	if e.Action != github.GenericCommentActionCreated {
		return nil
	}
	if !e.IsPR {
		return nil
	}
	if e.IssueState != "open" {
		return nil
	}
	if !commandRe.MatchString(e.Body) {
		return nil
	}

	var (
		org    = e.Repo.Owner.Login
		repo   = e.Repo.Name
		number = e.Number
		user   = e.User.Login
	)

	if cfg.MaintainerTeam != "" {
		member, err := isTeamMember(gc, org, cfg.MaintainerTeam, user)
		if err != nil {
			return err
		}
		if !member {
			return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user,
				fmt.Sprintf("only members of the `%s` team can trigger this command", cfg.MaintainerTeam)))
		}
	}

	allCommits, err := gc.ListPullRequestCommits(org, repo, number)
	if err != nil {
		return fmt.Errorf("failed to list PR commits: %w", err)
	}

	rewrites := computeRewrites(allCommits)
	if len(rewrites) == 0 {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, "no commit messages need fixing"))
	}

	pr, err := gc.GetPullRequest(org, repo, number)
	if err != nil {
		return fmt.Errorf("failed to get pull request: %w", err)
	}

	if err := rewriteCommits(log, gitFactory, pr, allCommits, rewrites); err != nil {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user,
			fmt.Sprintf("failed to rewrite commits: %v", err)))
	}

	if err := gc.RemoveLabel(org, repo, number, invalidCommitMsgLabel); err != nil {
		log.WithError(err).Warn("failed to remove label after fixing commits")
	}

	return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user,
		fmt.Sprintf("rewrote %d commit message(s)", len(rewrites))))
}

// computeRewrites returns a map from commit SHA to corrected message for each
// commit whose message contains an invalid pattern.
func computeRewrites(commits []github.RepositoryCommit) map[string]string {
	rewrites := make(map[string]string)
	for _, c := range commits {
		msg := c.Commit.Message
		fixed := fixMessage(msg)
		if fixed != msg {
			rewrites[c.SHA] = fixed
		}
	}
	return rewrites
}

// fixMessage returns a corrected version of msg with all invalid patterns
// removed. If the message is already valid it is returned unchanged.
func fixMessage(msg string) string {
	subject, body, _ := strings.Cut(msg, "\n")
	fixed := fixSubject(subject)
	if body != "" {
		fixed += "\n" + body
	}
	return fixCloseKeywords(fixed)
}

// fixSubject removes a leading fixup!/amend!/squash! prefix from the commit
// subject line, leaving the remainder trimmed.
func fixSubject(subject string) string {
	return strings.TrimSpace(invalidcommitmsg.FixupCommitRegex.ReplaceAllString(subject, ""))
}

// fixCloseKeywords replaces close-issue patterns such as "closes #NNN" or
// "fixes org/repo#NNN" with "Related: NNN" or "Related: org/repo NNN"
// respectively, removing the "#" that GitHub would otherwise turn into a link.
func fixCloseKeywords(msg string) string {
	return invalidcommitmsg.CloseIssueRegex.ReplaceAllStringFunc(msg, func(match string) string {
		sub := invalidcommitmsg.CloseIssueRegex.FindStringSubmatch(match)
		// sub[6] is the optional "org/repo" cross-repo prefix; sub[7] is the issue number.
		repoRef, issueNum := sub[6], sub[7]
		if repoRef != "" {
			return "Related: " + repoRef + " " + issueNum
		}
		return "Related: " + issueNum
	})
}

// rewriteCommits clones the repository that hosts the PR branch, rewrites the
// commit messages identified in rewrites by cherry-picking each commit and
// amending where necessary, then force-pushes the result back to the PR branch.
func rewriteCommits(log *logrus.Entry, gitFactory git.ClientFactory, pr *github.PullRequest, commits []github.RepositoryCommit, rewrites map[string]string) error {
	if len(commits) == 0 {
		return nil
	}

	var (
		headOrg  = pr.Head.Repo.Owner.Login
		headRepo = pr.Head.Repo.Name
		prBranch = pr.Head.Ref
	)

	r, err := gitFactory.ClientFor(headOrg, headRepo)
	if err != nil {
		return fmt.Errorf("failed to clone %s/%s: %w", headOrg, headRepo, err)
	}
	defer func() {
		if cerr := r.Clean(); cerr != nil {
			log.WithError(cerr).Error("failed to clean up git checkout")
		}
	}()

	if err := r.Checkout(prBranch); err != nil {
		return fmt.Errorf("failed to checkout %s: %w", prBranch, err)
	}

	// The parent of the oldest PR commit serves as the base for the rewrite.
	baseSHA, err := r.RevParse(commits[0].SHA + "^")
	if err != nil {
		return fmt.Errorf("failed to resolve base commit: %w", err)
	}
	baseSHA = strings.TrimSpace(baseSHA)

	// Create a temporary branch at the base commit, cherry-pick all PR commits
	// onto it in order, and amend the message for each flagged commit.
	tempBranch := "fix-commit-messages-temp"
	if err := r.Checkout(baseSHA); err != nil {
		return fmt.Errorf("failed to checkout base %s: %w", baseSHA, err)
	}
	if err := r.CheckoutNewBranch(tempBranch); err != nil {
		return fmt.Errorf("failed to create temp branch: %w", err)
	}

	for _, commit := range commits {
		if err := r.CherryPick(commit.SHA); err != nil {
			return fmt.Errorf("cherry-pick of %s failed: %w", commit.SHA, err)
		}
		if newMsg, ok := rewrites[commit.SHA]; ok {
			if err := r.Amend(newMsg); err != nil {
				return fmt.Errorf("amend of %s failed: %w", commit.SHA, err)
			}
		}
	}

	newHEAD, err := r.RevParse("HEAD")
	if err != nil {
		return fmt.Errorf("failed to resolve new HEAD: %w", err)
	}
	newHEAD = strings.TrimSpace(newHEAD)

	// Reset the PR branch to the rewritten tip and force-push.
	if err := r.Checkout(prBranch); err != nil {
		return fmt.Errorf("failed to re-checkout %s: %w", prBranch, err)
	}
	if err := r.ResetHard(newHEAD); err != nil {
		return fmt.Errorf("failed to reset %s: %w", prBranch, err)
	}
	if err := r.PushToCentral(prBranch, true); err != nil {
		return fmt.Errorf("failed to push: %v — the author may need to enable 'Allow edits from maintainers' on this PR", err)
	}

	return nil
}

// isTeamMember reports whether user belongs to the GitHub team identified by
// team (matched against both slug and display name) in the given org.
func isTeamMember(gc githubClient, org, team, user string) (bool, error) {
	teams, err := gc.ListTeams(org)
	if err != nil {
		return false, fmt.Errorf("failed to list teams in org %s: %w", org, err)
	}
	for _, t := range teams {
		if t.Slug != team && t.Name != team {
			continue
		}
		members, err := gc.ListTeamMembersBySlug(org, t.Slug, github.RoleAll)
		if err != nil {
			return false, fmt.Errorf("failed to list members of team %s: %w", team, err)
		}
		for _, m := range members {
			if m.Login == user {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}
