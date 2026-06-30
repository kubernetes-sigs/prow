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
	"context"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"slices"
	"sort"
	"time"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/layeredsets"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/plugins/assign"
	"sigs.k8s.io/prow/pkg/repoowners"
)

const (
	// PluginName defines this plugin's registered name.
	PluginName = "blunderbuss"
)

var match = regexp.MustCompile(`(?mi)^/auto-cc\s*$`)

func init() {
	plugins.RegisterPullRequestHandler(PluginName, handlePullRequestEvent, helpProvider)
	plugins.RegisterGenericCommentHandler(PluginName, handleGenericCommentEvent, helpProvider)
	plugins.RegisterStatusEventHandler(PluginName, handleStatusEvent, helpProvider)
}

func configString(reviewCount int) string {
	var pluralSuffix string
	if reviewCount > 1 {
		pluralSuffix = "s"
	}
	return fmt.Sprintf("Blunderbuss is currently configured to request reviews from %d reviewer%s.", reviewCount, pluralSuffix)
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
		Description: "The blunderbuss plugin automatically requests reviews from reviewers when a new PR is created. The reviewers are selected based on the reviewers specified in the OWNERS files that apply to the files modified by the PR.",
		Config: map[string]string{
			"": configString(reviewCount),
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

type reviewersClient interface {
	FindReviewersOwnersForFile(path string) string
	Reviewers(path string) layeredsets.String
	RequiredReviewers(path string) sets.Set[string]
	LeafReviewers(path string) sets.Set[string]
}

type ownersClient interface {
	reviewersClient
	FindApproverOwnersForFile(path string) string
	Approvers(path string) layeredsets.String
	LeafApprovers(path string) sets.Set[string]
	AllOwners() sets.Set[string]
}

type fallbackReviewersClient struct {
	ownersClient
}

func (foc fallbackReviewersClient) FindReviewersOwnersForFile(path string) string {
	return foc.ownersClient.FindApproverOwnersForFile(path)
}

func (foc fallbackReviewersClient) Reviewers(path string) layeredsets.String {
	return foc.ownersClient.Approvers(path)
}

func (foc fallbackReviewersClient) LeafReviewers(path string) sets.Set[string] {
	return foc.ownersClient.LeafApprovers(path)
}

type githubClient interface {
	RequestReview(org string, repo string, number int, logins []string) error
	FindIssuesWithOrg(org string, query string, sort string, asc bool) ([]github.Issue, error)
	GetPullRequestChanges(org string, repo string, number int) ([]github.PullRequestChange, error)
	GetPullRequest(org string, repo string, number int) (*github.PullRequest, error)
	GetBlame(org, repo, ref, path string) ([]github.BlameRange, error)
	Query(context.Context, any, map[string]any) error
}

type repoownersClient interface {
	LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error)
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

func handlePullRequest(ghc githubClient, roc repoownersClient, log *logrus.Entry, config plugins.Blunderbuss, action github.PullRequestEventAction, pr *github.PullRequest, repo *github.Repo) error {
	// Ignore pull request event if:
	// - not an opened or ready for review event
	// - if they have a /cc command in the description (will be handled elsewhere)
	// - if we are configured to wait for a specific commit status
	if !(action == github.PullRequestActionOpened || action == github.PullRequestActionReadyForReview) || assign.CCRegexp.MatchString(pr.Body) || config.WaitForStatus != nil {
		return nil
	}
	if pr.Draft && config.IgnoreDrafts {
		// ignore Draft PR when IgnoreDrafts is true
		return nil
	}
	// Ignore PRs submitted by users matching logins set in IgnoreAuthors
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

func handleGenericComment(ghc githubClient, roc repoownersClient, log *logrus.Entry, config plugins.Blunderbuss, action github.GenericCommentEventAction, isPR bool, prNumber int, issueState string, repo *github.Repo, body string) error {
	if action != github.GenericCommentActionCreated || !isPR || issueState == "closed" {
		return nil
	}

	if !match.MatchString(body) {
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

func handleStatus(ghc githubClient, roc repoownersClient, log *logrus.Entry, config plugins.Blunderbuss, sha string, context string, state string, description string, repo *github.Repo) error {
	wfs := config.WaitForStatus
	if wfs == nil {
		return nil
	}

	if context != wfs.Context {
		// Not the expected context, do not process this.
		return nil
	}

	if state != wfs.State {
		// do nothing and wait for state to be updated.
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

	for _, issue := range issues {
		l := log.WithField("pr", issue.Number)

		l.Info("Getting pull request info.")
		pr, err := ghc.GetPullRequest(org, repo.Name, issue.Number)
		if err != nil {
			l.WithError(err).Warning("Unable to fetch pull request.")
			continue
		}

		// Check if this is the latest commit in the PR.
		if pr.Head.SHA != sha {
			l.Info("Event is not for PR HEAD, skipping.")
			continue
		}

		// Ignore drafts if specified in config
		if pr.Draft && config.IgnoreDrafts {
			// ignore Draft PR when IgnoreDrafts is true
			return nil
		}

		// Ignore PRs submitted by users matching logins set in IgnoreAuthors
		if slices.Contains(config.IgnoreAuthors, pr.User.Login) {
			return nil
		}

		// Don't add reviewers if there are already requested reviewers
		if len(pr.RequestedReviewers) > 0 {
			return nil
		}

		err = handle(
			ghc,
			roc,
			l,
			config.ReviewerCount,
			config.MaxReviewerCount,
			config.ExcludeApprovers,
			config.UseStatusAvailability,
			repo,
			pr)

		if err != nil {
			l.WithError(err).Warning("Error processing event from commit status update")
		}
	}
	return err
}

func handle(ghc githubClient, roc repoownersClient, log *logrus.Entry, reviewerCount *int, maxReviewers int, excludeApprovers bool, useStatusAvailability bool, repo *github.Repo, pr *github.PullRequest) error {
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

	var reviewers []string
	var requiredReviewers []string
	if reviewerCount != nil {
		reviewers, requiredReviewers, err = getReviewers(oc, ghc, log, pr.User.Login, changes, *reviewerCount, useStatusAvailability, blameScores)
		if err != nil {
			return err
		}
		if missing := *reviewerCount - len(reviewers); missing > 0 {
			if !excludeApprovers {
				// Attempt to use approvers as additional reviewers. This must use
				// reviewerCount instead of missing because owners can be both reviewers
				// and approvers and the search might stop too early if it finds
				// duplicates.
				frc := fallbackReviewersClient{ownersClient: oc}
				approvers, _, err := getReviewers(frc, ghc, log, pr.User.Login, changes, *reviewerCount, useStatusAvailability, blameScores)
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

	// add required reviewers if any
	reviewers = append(reviewers, requiredReviewers...)

	if len(reviewers) > 0 {
		log.Infof("Requesting reviews from users %s.", reviewers)
		return ghc.RequestReview(repo.Owner.Login, repo.Name, pr.Number, reviewers)
	}
	return nil
}

func getReviewers(rc reviewersClient, ghc githubClient, log *logrus.Entry, author string, files []github.PullRequestChange, minReviewers int, useStatusAvailability bool, blameScores map[string]float64) ([]string, []string, error) {
	authorSet := sets.New[string](github.NormLogin(author))
	reviewers := layeredsets.NewString()
	requiredReviewers := sets.New[string]()
	leafReviewers := layeredsets.NewString()
	busyReviewers := sets.New[string]()
	ownersSeen := sets.New[string]()
	if minReviewers == 0 {
		return reviewers.List(), sets.List(requiredReviewers), nil
	}

	useSmartSelection := hasBlameScores(blameScores)

	selectReviewer := func(candidates *layeredsets.String) string {
		if useSmartSelection {
			return selectBestReviewer(blameScores, candidates, &busyReviewers, ghc, log, useStatusAvailability)
		}
		return findReviewer(ghc, log, useStatusAvailability, &busyReviewers, candidates)
	}

	// First build 'reviewers' by taking a unique reviewer from each OWNERS file.
	for _, file := range files {
		ownersFile := rc.FindReviewersOwnersForFile(file.Filename)
		if ownersSeen.Has(ownersFile) {
			continue
		}
		ownersSeen.Insert(ownersFile)

		requiredReviewers.Insert(rc.RequiredReviewers(file.Filename).UnsortedList()...)

		fileUnusedLeaves := layeredsets.NewString(sets.List(rc.LeafReviewers(file.Filename))...).Difference(reviewers.Set()).Difference(authorSet)
		if fileUnusedLeaves.Len() == 0 {
			continue
		}
		leafReviewers = leafReviewers.Union(fileUnusedLeaves)
		if r := selectReviewer(&fileUnusedLeaves); r != "" {
			reviewers.Insert(0, r)
		}
	}
	// Ensure that we request review from at least minReviewers reviewers. Favor leaf reviewers.
	unusedLeaves := leafReviewers.Difference(reviewers.Set())
	for reviewers.Len() < minReviewers && unusedLeaves.Len() > 0 {
		if r := selectReviewer(&unusedLeaves); r != "" {
			reviewers.Insert(1, r)
		}
	}
	for _, file := range files {
		if reviewers.Len() >= minReviewers {
			break
		}
		fileReviewers := rc.Reviewers(file.Filename).Difference(authorSet)
		for reviewers.Len() < minReviewers && fileReviewers.Len() > 0 {
			if r := selectReviewer(&fileReviewers); r != "" {
				reviewers.Insert(2, r)
			}
		}
	}
	return reviewers.List(), sets.List(requiredReviewers), nil
}

// findReviewer finds a reviewer from a set, potentially using status
// availability.
func findReviewer(ghc githubClient, log *logrus.Entry, useStatusAvailability bool, busyReviewers *sets.Set[string], targetSet *layeredsets.String) string {
	// if we don't care about status availability, just pop a target from the set
	if !useStatusAvailability {
		return targetSet.PopRandom()
	}

	// if we do care, start looping through the candidates
	for targetSet.Len() > 0 {
		candidate := targetSet.PopRandom()
		if busyReviewers.Has(candidate) {
			// we've already verified this reviewer is busy
			continue
		}
		busy, err := isUserBusy(ghc, candidate)
		if err != nil {
			log.WithField("user", candidate).WithError(err).Error("Error checking user availability")
		}
		if !busy {
			return candidate
		}
		// if we haven't returned the candidate, then they must be busy.
		log.WithField("user", candidate).Debug("User marked as a busy reviewer")
		busyReviewers.Insert(candidate)
	}
	return ""
}

func selectBestReviewer(
	scores map[string]float64,
	candidates *layeredsets.String,
	busyReviewers *sets.Set[string],
	ghc githubClient,
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

	// Sort by score descending, break ties randomly
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
			busy, err := isUserBusy(ghc, c.login)
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

// findFallbackReviewers finds additional reviewers from the broader OWNERS scope
// when not enough were found for the changed files. If blame scores are available,
// candidates are ranked by score. Remaining slots are filled randomly.
func findFallbackReviewers(
	blameScores map[string]float64,
	oc ownersClient,
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

	// Fill remaining slots randomly from eligible OWNERS members
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

type githubAvailabilityQuery struct {
	User struct {
		Login  githubql.String
		Status struct {
			IndicatesLimitedAvailability githubql.Boolean
		}
	} `graphql:"user(login: $user)"`
}

func isUserBusy(ghc githubClient, user string) (bool, error) {
	var query githubAvailabilityQuery
	vars := map[string]any{
		"user": githubql.String(user),
	}
	ctx := context.Background()
	err := ghc.Query(ctx, &query, vars)
	return bool(query.User.Status.IndicatesLimitedAvailability), err
}
