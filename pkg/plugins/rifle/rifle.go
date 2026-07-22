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

package rifle

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sort"
	"time"

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
	PluginName = "rifle"
)

func init() {
	plugins.RegisterPullRequestHandler(PluginName, handlePullRequestEvent, helpProvider)
	plugins.RegisterGenericCommentHandler(PluginName, handleGenericCommentEvent, helpProvider)
	plugins.RegisterStatusEventHandler(PluginName, handleStatusEvent, helpProvider)
}

type rifleGitHubClient interface {
	reviewer.GitHubClient
	GetBlame(org, repo, ref, path string) ([]github.BlameRange, error)
}

func helpProvider(config *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	var reviewCount int
	if config.Rifle.ReviewerCount != nil {
		reviewCount = *config.Rifle.ReviewerCount
	}
	two := 2
	yamlSnippet, err := plugins.CommentMap.GenYaml(&plugins.Configuration{
		Rifle: plugins.Rifle{
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
		Description: "The rifle plugin automatically requests reviews from reviewers when a new PR is created. Unlike blunderbuss (which selects randomly), rifle uses git blame data to identify reviewers most familiar with the changed code.",
		Config: map[string]string{
			"": reviewer.ConfigString(PluginName, reviewCount),
		},
		Snippet: yamlSnippet,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/auto-cc",
		Featured:    false,
		Description: "Manually request reviews from reviewers for a PR. Useful if OWNERS files were updated since the PR was opened, or PR was changed significantly.",
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
		pc.PluginConfig.Rifle,
		pre.Action,
		&pre.PullRequest,
		&pre.Repo,
	)
}

func handlePullRequest(ghc rifleGitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, cfg plugins.Rifle, action github.PullRequestEventAction, pr *github.PullRequest, repo *github.Repo) error {
	if !(action == github.PullRequestActionOpened || action == github.PullRequestActionReadyForReview) || assign.CCRegexp.MatchString(pr.Body) || cfg.WaitForStatus != nil {
		return nil
	}
	if pr.Draft && cfg.IgnoreDrafts {
		return nil
	}
	if slices.Contains(cfg.IgnoreAuthors, pr.User.Login) {
		return nil
	}
	return handle(
		ghc,
		roc,
		log,
		cfg.ReviewerCount,
		cfg.MaxReviewerCount,
		cfg.ExcludeApprovers,
		cfg.UseStatusAvailability,
		repo,
		pr,
	)
}

func handleGenericCommentEvent(pc plugins.Agent, ce github.GenericCommentEvent) error {
	return handleGenericComment(
		pc.GitHubClient,
		pc.OwnersClient,
		pc.Logger,
		pc.PluginConfig.Rifle,
		ce.Action,
		ce.IsPR,
		ce.Number,
		ce.IssueState,
		&ce.Repo,
		ce.Body,
	)
}

func handleGenericComment(ghc rifleGitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, cfg plugins.Rifle, action github.GenericCommentEventAction, isPR bool, prNumber int, issueState string, repo *github.Repo, body string) error {
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
		cfg.ReviewerCount,
		cfg.MaxReviewerCount,
		cfg.ExcludeApprovers,
		cfg.UseStatusAvailability,
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
		pc.PluginConfig.Rifle,
		se.SHA,
		se.Context,
		se.State,
		se.Description,
		&se.Repo,
	)
}

func handleStatus(ghc rifleGitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, cfg plugins.Rifle, sha string, context string, state string, description string, repo *github.Repo) error {
	wfs := cfg.WaitForStatus
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

		if pr.Draft && cfg.IgnoreDrafts {
			continue
		}

		if slices.Contains(cfg.IgnoreAuthors, pr.User.Login) {
			continue
		}

		if len(pr.RequestedReviewers) > 0 {
			continue
		}

		if err := handle(
			ghc,
			roc,
			l,
			cfg.ReviewerCount,
			cfg.MaxReviewerCount,
			cfg.ExcludeApprovers,
			cfg.UseStatusAvailability,
			repo,
			pr); err != nil {
			l.WithError(err).Warning("Error processing event from commit status update")
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

func handle(ghc rifleGitHubClient, roc reviewer.RepoOwnersClient, log *logrus.Entry, reviewerCount *int, maxReviewers int, excludeApprovers bool, useStatusAvailability bool, repo *github.Repo, pr *github.PullRequest) error {
	oc, err := roc.LoadRepoOwners(repo.Owner.Login, repo.Name, pr.Base.Ref)
	if err != nil {
		return fmt.Errorf("error loading RepoOwners: %w", err)
	}

	changes, err := ghc.GetPullRequestChanges(repo.Owner.Login, repo.Name, pr.Number)
	if err != nil {
		return fmt.Errorf("error getting PR changes: %w", err)
	}

	allReviewerCandidates := sets.New[string]()
	allApproverCandidates := sets.New[string]()
	for _, file := range changes {
		allReviewerCandidates = allReviewerCandidates.Union(oc.Reviewers(file.Filename).Set())
		allApproverCandidates = allApproverCandidates.Union(oc.Approvers(file.Filename).Set())
	}

	scorer := &reviewerScorer{
		ghc:       ghc,
		org:       repo.Owner.Login,
		repo:      repo.Name,
		ref:       pr.Base.Ref,
		approvers: allApproverCandidates,
		reviewers: allReviewerCandidates,
		now:       time.Now(),
		log:       log,
	}
	var blameScores map[string]float64
	scores, err := scorer.scoreReviewers(changes)
	if err != nil {
		log.WithError(err).Warn("Failed to compute blame scores, falling back to random selection")
	} else {
		blameScores = scores
	}

	busyReviewers := sets.New[string]()
	selector := func(candidates *layeredsets.String) string {
		if hasBlameScores(blameScores) {
			return selectBestReviewer(blameScores, candidates, &busyReviewers, ghc, log, useStatusAvailability)
		}
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
		if missing := *reviewerCount - len(reviewers); missing > 0 {
			log.Debugf("Not enough reviewers found in OWNERS files for files touched by this PR. %d/%d reviewers found.", len(reviewers), *reviewerCount)

			excludeFromFallback := sets.New[string](reviewers...)
			excludeFromFallback.Insert(github.NormLogin(pr.User.Login))
			fallbackReviewers := findFallbackReviewers(
				blameScores, oc, excludeFromFallback, missing,
			)
			if len(fallbackReviewers) > 0 {
				reviewers = append(reviewers, fallbackReviewers...)
				log.Infof("Fallback added %d reviewer(s) from broader OWNERS scope: %v", len(fallbackReviewers), fallbackReviewers)
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

func selectBestReviewer(
	scores map[string]float64,
	candidates *layeredsets.String,
	busyReviewers *sets.Set[string],
	ghc reviewer.GitHubClient,
	log *logrus.Entry,
	useStatusAvailability bool,
) string {
	type candidate struct {
		login string
		score float64
	}

	var ranked []candidate
	candidateSet := candidates.Set()
	for login := range candidateSet {
		if busyReviewers.Has(login) {
			continue
		}
		ranked = append(ranked, candidate{login: login, score: scores[login]})
	}

	rand.Shuffle(len(ranked), func(i, j int) {
		ranked[i], ranked[j] = ranked[j], ranked[i]
	})
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].score > ranked[j-1].score; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}

	for _, c := range ranked {
		if useStatusAvailability {
			busy, err := reviewer.IsUserBusy(ghc, c.login)
			if err != nil {
				log.WithError(err).WithField("user", c.login).Warn("Failed to check user availability")
			} else if busy {
				busyReviewers.Insert(c.login)
				continue
			}
		}
		candidates.Delete(c.login)
		return c.login
	}
	return ""
}

func hasBlameScores(scores map[string]float64) bool {
	for _, s := range scores {
		if !math.IsNaN(s) && s > 0 {
			return true
		}
	}
	return false
}

func findFallbackReviewers(
	blameScores map[string]float64,
	oc reviewer.OwnersClient,
	exclude sets.Set[string],
	needed int,
) []string {
	allOwners := oc.AllOwners()
	eligible := allOwners.Difference(exclude)
	if eligible.Len() == 0 {
		return nil
	}

	var result []string
	if hasBlameScores(blameScores) {
		type scored struct {
			login string
			score float64
		}
		var candidates []scored
		for login := range eligible {
			if s := blameScores[login]; s > 0 {
				candidates = append(candidates, scored{login, s})
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].score > candidates[j].score
		})
		for i := 0; i < len(candidates) && len(result) < needed; i++ {
			result = append(result, candidates[i].login)
		}
	}

	if remaining := needed - len(result); remaining > 0 {
		picked := sets.New[string](result...)
		var pool []string
		for login := range eligible {
			if !picked.Has(login) {
				pool = append(pool, login)
			}
		}
		rand.Shuffle(len(pool), func(i, j int) {
			pool[i], pool[j] = pool[j], pool[i]
		})
		for i := 0; i < len(pool) && len(result) < needed; i++ {
			result = append(result, pool[i])
		}
	}

	return result
}
