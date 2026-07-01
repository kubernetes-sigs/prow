/*
Copyright 2017 The Kubernetes Authors.

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

package blunderbuss

import (
	"fmt"
	"slices"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/layeredsets"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/plugins/assign"
	"sigs.k8s.io/prow/pkg/reviewer"
)

const (
	PluginName = "blunderbuss"
)

func init() {
	plugins.RegisterPullRequestHandler(PluginName, handlePullRequestEvent, helpProvider)
	plugins.RegisterGenericCommentHandler(PluginName, handleGenericCommentEvent, helpProvider)
	plugins.RegisterStatusEventHandler(PluginName, handleStatusEvent, helpProvider)
}

func helpProvider(config *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	var reviewCount int
	if config.Blunderbuss.ReviewerCount != nil {
		reviewCount = *config.Blunderbuss.ReviewerCount
	}
	two := 2
	yamlSnippet, err := plugins.CommentMap.GenYaml(&plugins.Configuration{
		Blunderbuss: plugins.Blunderbuss{
			ReviewerCount:         &two,
			MaxReviewerCount:      3,
			ExcludeApprovers:      true,
			UseStatusAvailability: true,
			IgnoreAuthors:         []string{},
		},
	})
	if err != nil {
		logrus.WithError(err).Warnf("cannot generate comments for %s plugin", PluginName)
	}
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The blunderbuss plugin automatically requests reviews from reviewers when a new PR is created. The reviewers are selected randomly from the OWNERS files that apply to the files modified by the PR.",
		Config: map[string]string{
			"": reviewer.ConfigString(PluginName, reviewCount),
		},
		Snippet: yamlSnippet,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/auto-cc",
		Featured:    false,
		Description: "Manually request reviews from reviewers for a PR. Useful if OWNERS file were updated since the PR was opened.",
		Examples:    []string{"/auto-cc"},
		WhoCanUse:   "Anyone",
	})
	return pluginHelp, nil
}

func handlePullRequestEvent(pc plugins.Agent, pre github.PullRequestEvent) error {
	return handlePullRequest(
		pc.GitHubClient,
		pc.OwnersClient,
		pc.Logger,
		pc.PluginConfig.Blunderbuss,
		pre.Action,
		&pre.PullRequest,
		&pre.Repo,
	)
}

func handlePullRequest(ghc reviewer.GitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, config plugins.Blunderbuss, action github.PullRequestEventAction, pr *github.PullRequest, repo *github.Repo) error {
	if !(action == github.PullRequestActionOpened || action == github.PullRequestActionReadyForReview) || assign.CCRegexp.MatchString(pr.Body) || config.WaitForStatus != nil {
		return nil
	}
	if pr.Draft && config.IgnoreDrafts {
		return nil
	}
	if slices.Contains(config.IgnoreAuthors, pr.User.Login) {
		return nil
	}
	return handle(
		ghc,
		roc,
		log,
		config.ReviewerCount,
		config.MaxReviewerCount,
		config.ExcludeApprovers,
		config.UseStatusAvailability,
		repo,
		pr,
	)
}

func handleGenericCommentEvent(pc plugins.Agent, ce github.GenericCommentEvent) error {
	return handleGenericComment(
		pc.GitHubClient,
		pc.OwnersClient,
		pc.Logger,
		pc.PluginConfig.Blunderbuss,
		ce.Action,
		ce.IsPR,
		ce.Number,
		ce.IssueState,
		&ce.Repo,
		ce.Body,
	)
}

func handleGenericComment(ghc reviewer.GitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, config plugins.Blunderbuss, action github.GenericCommentEventAction, isPR bool, prNumber int, issueState string, repo *github.Repo, body string) error {
	if action != github.GenericCommentActionCreated || !isPR || issueState == "closed" {
		return nil
	}

	if !reviewer.AutoCCMatch.MatchString(body) {
		return nil
	}

	pr, err := ghc.GetPullRequest(repo.Owner.Login, repo.Name, prNumber)
	if err != nil {
		return fmt.Errorf("error loading PullRequest: %w", err)
	}

	return handle(
		ghc,
		roc,
		log,
		config.ReviewerCount,
		config.MaxReviewerCount,
		config.ExcludeApprovers,
		config.UseStatusAvailability,
		repo,
		pr,
	)
}

func handleStatusEvent(pc plugins.Agent, se github.StatusEvent) error {
	if se.State == "" || se.Context == "" || se.Description == "" {
		return fmt.Errorf("invalid status event delivered with empty state/context/description")
	}

	return handleStatus(
		pc.GitHubClient,
		pc.OwnersClient,
		pc.Logger,
		pc.PluginConfig.Blunderbuss,
		se.SHA,
		se.Context,
		se.State,
		se.Description,
		&se.Repo,
	)
}

func handleStatus(ghc reviewer.GitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, config plugins.Blunderbuss, sha string, context string, state string, description string, repo *github.Repo) error {
	wfs := config.WaitForStatus
	if wfs == nil {
		return nil
	}

	if context != wfs.Context {
		return nil
	}

	if state != wfs.State {
		return nil
	}

	if !wfs.DescriptionRe.MatchString(description) {
		return nil
	}

	org := repo.Owner.Login
	log.Info("Searching for PRs matching the commit.")

	issues, err := ghc.FindIssuesWithOrg(org, fmt.Sprintf("%s repo:%s/%s type:pr state:open", sha, org, repo.Name), "", false)
	if err != nil {
		return fmt.Errorf("error searching for issues matching commit: %w", err)
	}
	log.Infof("Found %d PRs matching commit.", len(issues))

	var errs []error
	for _, issue := range issues {
		l := log.WithField("pr", issue.Number)

		l.Info("Getting pull request info.")
		pr, err := ghc.GetPullRequest(org, repo.Name, issue.Number)
		if err != nil {
			l.WithError(err).Warning("Unable to fetch pull request.")
			continue
		}

		if pr.Head.SHA != sha {
			l.Info("Event is not for PR HEAD, skipping.")
			continue
		}

		if pr.Draft && config.IgnoreDrafts {
			continue
		}

		if slices.Contains(config.IgnoreAuthors, pr.User.Login) {
			continue
		}

		if len(pr.RequestedReviewers) > 0 {
			continue
		}

		if err := handle(
			ghc,
			roc,
			l,
			config.ReviewerCount,
			config.MaxReviewerCount,
			config.ExcludeApprovers,
			config.UseStatusAvailability,
			repo,
			pr); err != nil {
			l.WithError(err).Warning("Error processing event from commit status update")
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

func handle(ghc reviewer.GitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, reviewerCount *int, maxReviewers int, excludeApprovers bool, useStatusAvailability bool, repo *github.Repo, pr *github.PullRequest) error {
	oc, err := roc.LoadRepoOwners(repo.Owner.Login, repo.Name, pr.Base.Ref)
	if err != nil {
		return fmt.Errorf("error loading RepoOwners: %w", err)
	}

	changes, err := ghc.GetPullRequestChanges(repo.Owner.Login, repo.Name, pr.Number)
	if err != nil {
		return fmt.Errorf("error getting PR changes: %w", err)
	}

	busyReviewers := sets.New[string]()
	selector := func(candidates *layeredsets.String) string {
		return reviewer.FindReviewer(ghc, log, useStatusAvailability, &busyReviewers, candidates)
	}

	var reviewers []string
	var requiredReviewers []string
	if reviewerCount != nil {
		reviewers, requiredReviewers, err = reviewer.GetReviewers(oc, selector, log, pr.User.Login, changes, *reviewerCount)
		if err != nil {
			return err
		}
		if missing := *reviewerCount - len(reviewers); missing > 0 {
			if !excludeApprovers {
				frc := reviewer.FallbackReviewersClient{OwnersClient: oc}
				approvers, _, err := reviewer.GetReviewers(frc, selector, log, pr.User.Login, changes, *reviewerCount)
				if err != nil {
					return err
				}
				var added int
				combinedReviewers := sets.New[string](reviewers...)
				for _, approver := range approvers {
					if !combinedReviewers.Has(approver) {
						reviewers = append(reviewers, approver)
						combinedReviewers.Insert(approver)
						added++
					}
				}
				log.Infof("Added %d approvers as reviewers. %d/%d reviewers found.", added, combinedReviewers.Len(), *reviewerCount)
			}
		}
	}

	if maxReviewers > 0 && len(reviewers) > maxReviewers {
		log.Infof("Limiting request of %d reviewers to %d maxReviewers.", len(reviewers), maxReviewers)
		reviewers = reviewers[:maxReviewers]
	}

	reviewers = append(reviewers, requiredReviewers...)

	if len(reviewers) > 0 {
		log.Infof("Requesting reviews from users %s.", reviewers)
		return ghc.RequestReview(repo.Owner.Login, repo.Name, pr.Number, reviewers)
	}
	return nil
}
