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

package reviewer

import (
	"context"
	"fmt"
	"regexp"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/layeredsets"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/repoowners"
)

var AutoCCMatch = regexp.MustCompile(`(?mi)^/auto-cc\s*$`)

type ReviewersClient interface {
	FindReviewersOwnersForFile(path string) string
	Reviewers(path string) layeredsets.String
	RequiredReviewers(path string) sets.Set[string]
	LeafReviewers(path string) sets.Set[string]
}

type OwnersClient interface {
	ReviewersClient
	FindApproverOwnersForFile(path string) string
	Approvers(path string) layeredsets.String
	LeafApprovers(path string) sets.Set[string]
	AllOwners() sets.Set[string]
}

type FallbackReviewersClient struct {
	OwnersClient OwnersClient
}

func (foc FallbackReviewersClient) FindReviewersOwnersForFile(path string) string {
	return foc.OwnersClient.FindApproverOwnersForFile(path)
}

func (foc FallbackReviewersClient) Reviewers(path string) layeredsets.String {
	return foc.OwnersClient.Approvers(path)
}

func (foc FallbackReviewersClient) LeafReviewers(path string) sets.Set[string] {
	return foc.OwnersClient.LeafApprovers(path)
}

func (foc FallbackReviewersClient) RequiredReviewers(path string) sets.Set[string] {
	return foc.OwnersClient.RequiredReviewers(path)
}

type GitHubClient interface {
	RequestReview(org string, repo string, number int, logins []string) error
	FindIssuesWithOrg(org string, query string, sort string, asc bool) ([]github.Issue, error)
	GetPullRequestChanges(org string, repo string, number int) ([]github.PullRequestChange, error)
	GetPullRequest(org string, repo string, number int) (*github.PullRequest, error)
	Query(context.Context, any, map[string]any) error
}

type RepoOwnersClient interface {
	LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error)
}

type ReviewerSelector func(candidates *layeredsets.String) string

func ConfigString(pluginName string, reviewCount int) string {
	var pluralSuffix string
	if reviewCount > 1 {
		pluralSuffix = "s"
	}
	return fmt.Sprintf("%s is currently configured to request reviews from %d reviewer%s.", pluginName, reviewCount, pluralSuffix)
}

func GetReviewers(rc ReviewersClient, selector ReviewerSelector, log *logrus.Entry, author string, files []github.PullRequestChange, minReviewers int) ([]string, []string, error) {
	authorSet := sets.New[string](github.NormLogin(author))
	reviewers := layeredsets.NewString()
	requiredReviewers := sets.New[string]()
	leafReviewers := layeredsets.NewString()
	ownersSeen := sets.New[string]()
	if minReviewers == 0 {
		return reviewers.List(), sets.List(requiredReviewers), nil
	}

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
		if r := selector(&fileUnusedLeaves); r != "" {
			reviewers.Insert(0, r)
		}
	}
	unusedLeaves := leafReviewers.Difference(reviewers.Set())
	for reviewers.Len() < minReviewers && unusedLeaves.Len() > 0 {
		if r := selector(&unusedLeaves); r != "" {
			reviewers.Insert(1, r)
		}
	}
	for _, file := range files {
		if reviewers.Len() >= minReviewers {
			break
		}
		fileReviewers := rc.Reviewers(file.Filename).Difference(authorSet)
		for reviewers.Len() < minReviewers && fileReviewers.Len() > 0 {
			if r := selector(&fileReviewers); r != "" {
				reviewers.Insert(2, r)
			}
		}
	}
	return reviewers.List(), sets.List(requiredReviewers), nil
}

func FindReviewer(ghc GitHubClient, log *logrus.Entry, useStatusAvailability bool, busyReviewers *sets.Set[string], targetSet *layeredsets.String) string {
	if !useStatusAvailability {
		return targetSet.PopRandom()
	}

	for targetSet.Len() > 0 {
		candidate := targetSet.PopRandom()
		if busyReviewers.Has(candidate) {
			continue
		}
		busy, err := IsUserBusy(ghc, candidate)
		if err != nil {
			log.WithField("user", candidate).WithError(err).Error("Error checking user availability")
		}
		if !busy {
			return candidate
		}
		log.WithField("user", candidate).Debug("User marked as a busy reviewer")
		busyReviewers.Insert(candidate)
	}
	return ""
}

type GithubAvailabilityQuery struct {
	User struct {
		Login  githubql.String
		Status struct {
			IndicatesLimitedAvailability githubql.Boolean
		}
	} `graphql:"user(login: $user)"`
}

func IsUserBusy(ghc GitHubClient, user string) (bool, error) {
	var query GithubAvailabilityQuery
	vars := map[string]any{
		"user": githubql.String(user),
	}
	ctx := context.Background()
	err := ghc.Query(ctx, &query, vars)
	return bool(query.User.Status.IndicatesLimitedAvailability), err
}
