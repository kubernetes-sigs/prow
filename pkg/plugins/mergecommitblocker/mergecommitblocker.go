/*
Copyright 2019 The Kubernetes Authors.

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

package mergecommitblocker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/git/v2"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
)

const (
	// PluginName defines this plugin's registered name.
	PluginName = "mergecommitblocker"
)

var (
	commentBody = fmt.Sprintf("Adding label `%s` because PR contains merge commits, which are not allowed in this repository.\nUse `git rebase` to reapply your commits on top of the target branch. Detailed instructions for doing so can be found [here](https://git.k8s.io/community/contributors/guide/github-workflow.md#4-keep-your-branch-in-sync).", labels.MergeCommits)
)

// init registers out plugin as a pull request handler
func init() {
	plugins.RegisterPullRequestHandler(PluginName, handlePullRequest, helpProvider)
}

// helpProvider provides information on the plugin
func helpProvider(config *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: fmt.Sprintf("The merge commit blocker plugin adds the %s label to pull requests that contain merge commits. "+
			"Merge commits may be excluded from this check if they only modify files matching configured path patterns, "+
			"which is useful for workflows like Git subtrees.", labels.MergeCommits),
		Config: map[string]string{},
	}
	for _, mcb := range config.MergeCommitBlocker {
		for _, repo := range mcb.Repos {
			if len(mcb.ExcludedPaths) > 0 {
				pluginHelp.Config[repo] = fmt.Sprintf("Excluded paths: %v", mcb.ExcludedPaths)
			} else {
				pluginHelp.Config[repo] = "No excluded paths configured."
			}
		}
	}
	return pluginHelp, nil
}

type githubClient interface {
	AddLabel(org, repo string, number int, label string) error
	RemoveLabel(owner, repo string, number int, label string) error
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	CreateComment(org, repo string, number int, comment string) error
}

type pruneClient interface {
	PruneComments(func(ic github.IssueComment) bool)
}

func handlePullRequest(pc plugins.Agent, pre github.PullRequestEvent) error {
	if pre.Action != github.PullRequestActionOpened &&
		pre.Action != github.PullRequestActionReopened &&
		pre.Action != github.PullRequestActionSynchronize {
		return nil
	}
	cp, err := pc.CommentPruner()
	if err != nil {
		return err
	}
	org := pre.PullRequest.Base.Repo.Owner.Login
	repo := pre.PullRequest.Base.Repo.Name
	cfg := pc.PluginConfig.MergeCommitBlockerFor(org, repo)
	return handle(pc.GitHubClient, pc.GitClient, cp, pc.Logger, &pre, cfg)
}

func handle(ghc githubClient, gc git.ClientFactory, cp pruneClient, log *logrus.Entry, pre *github.PullRequestEvent, cfg *plugins.MergeCommitBlocker) error {
	var (
		org  = pre.PullRequest.Base.Repo.Owner.Login
		repo = pre.PullRequest.Base.Repo.Name
		num  = pre.PullRequest.Number
	)

	// Clone the repo, checkout the PR.
	r, err := gc.ClientFor(org, repo)
	if err != nil {
		return err
	}
	defer func() {
		if err := r.Clean(); err != nil {
			log.WithError(err).Error("Error cleaning up repo.")
		}
	}()
	if err := r.CheckoutPullRequest(num); err != nil {
		return err
	}
	// We are guaranteed to have both Base.SHA and Head.SHA
	target, head := pre.PullRequest.Base.SHA, pre.PullRequest.Head.SHA

	// Determine if there are any disallowed merge commits
	hasDisallowedMergeCommits, err := hasDisallowedMergeCommits(r, log, target, head, cfg)
	if err != nil {
		return err
	}

	issueLabels, err := ghc.GetIssueLabels(org, repo, num)
	if err != nil {
		return err
	}
	hasLabel := github.HasLabel(labels.MergeCommits, issueLabels)
	if hasLabel && !hasDisallowedMergeCommits {
		log.Infof("Removing %q Label for %s/%s#%d", labels.MergeCommits, org, repo, num)
		if err := ghc.RemoveLabel(org, repo, num, labels.MergeCommits); err != nil {
			return err
		}
		cp.PruneComments(func(ic github.IssueComment) bool {
			return strings.Contains(ic.Body, commentBody)
		})
	} else if !hasLabel && hasDisallowedMergeCommits {
		log.Infof("Adding %q Label for %s/%s#%d", labels.MergeCommits, org, repo, num)
		if err := ghc.AddLabel(org, repo, num, labels.MergeCommits); err != nil {
			return err
		}
		msg := plugins.FormatSimpleResponse(commentBody)
		return ghc.CreateComment(org, repo, num, msg)
	}
	return nil
}

// hasDisallowedMergeCommits checks if there are any merge commits in the PR that are not allowed
func hasDisallowedMergeCommits(r git.RepoClient, log *logrus.Entry, target, head string, cfg *plugins.MergeCommitBlocker) (bool, error) {
	if cfg == nil || len(cfg.CompiledExcludedPaths) == 0 {
		return r.MergeCommitsExistBetween(target, head)
	}

	shas, err := r.MergeCommitSHAsBetween(target, head)
	if err != nil {
		return false, err
	}

	if len(shas) == 0 {
		return false, nil
	}

	for _, sha := range shas {
		files, err := r.CommitChangedFiles(sha)
		if err != nil {
			return false, err
		}

		if !isMergeCommitAllowed(files, cfg.CompiledExcludedPaths) {
			log.Infof("Merge commit %s is not allowed because it modifies files outside excluded paths", sha)
			return true, nil
		}
		log.Debugf("Merge commit %s is allowed because changed files match excluded patterns", sha)
	}

	return false, nil
}

// isMergeCommitAllowed returns true if all files changed by the merge commit match at least one excluded pattern.
func isMergeCommitAllowed(files []string, excludedPatterns []*regexp.Regexp) bool {
	if len(files) == 0 {
		return true
	}

	for _, file := range files {
		if !slices.ContainsFunc(excludedPatterns, func(p *regexp.Regexp) bool {
			return p.MatchString(file)
		}) {
			return false
		}
	}
	return true
}
