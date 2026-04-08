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

// The `/link-issue` and `/unlink-issue` command allows
// members of the org to link and unlink issues to PRs.
package issuemanagement

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

var (
	fixesRegex = regexp.MustCompile(`(?i)^fixes\s+(.*)$`)
)

type issueRef struct {
	org  string
	repo string
	num  int
}

func handleLinkIssue(gc githubClient, log *logrus.Entry, e github.GenericCommentEvent, linkIssues []string, unlinkIssues []string) error {
	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	number := e.Number
	user := e.User.Login

	var (
		errMessages []string
		sb          strings.Builder
	)

	if e.Action != github.GenericCommentActionCreated {
		return nil
	}

	if !e.IsPR {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(
			e.Body, e.HTMLURL, user, "`/link-issue` and `/unlink-issue` can only be used on pull requests."))
	}

	isMember, err := gc.IsMember(org, user)
	if err != nil {
		return fmt.Errorf("unable to fetch if %s is an org member of %s: %w", user, org, err)
	}
	if !isMember {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(
			e.Body, e.HTMLURL, user, "You must be an org member to use this command."))
	}

	// For linking (checkExistence=true): formats valid issue references and checks if it exists.
	// For unlinking (checkExistence=false): formats valid issue references or pass through as-is to allow unlinking malformed references.
	processIssues := func(issues []string, checkExistence bool) []string {
		var result []string
		for _, issue := range issues {
			issueRef, err := parseIssueRef(issue, org, repo)
			if err != nil {
				if checkExistence {
					errMessages = append(errMessages, fmt.Sprintf("Invalid format for issue **%s**. Supported formats are **issue-number** (e.g., `123`) and **organization/repository#issue-number** (e.g., `kubernetes/test-infra#456`)", issue))
				} else {
					result = append(result, issue)
				}
				continue
			}

			if checkExistence {
				// If repo or org of the issue reference is different from the one in which the PR is created, check if it exists
				if org != issueRef.org || repo != issueRef.repo {
					if _, err := gc.GetRepo(issueRef.org, issueRef.repo); err != nil {
						errMessages = append(errMessages, fmt.Sprintf("Repository **%s/%s** does not exist.", issueRef.org, issueRef.repo))
						continue
					}
				}
				// Verify if the issue exists
				fetchedIssue, err := gc.GetIssue(issueRef.org, issueRef.repo, issueRef.num)
				if err != nil {
					errMessages = append(errMessages, fmt.Sprintf("Issue **#%d** in repository **%s/%s** does not exist.", issueRef.num, issueRef.org, issueRef.repo))
					continue
				}
				// Skip linking the issue if the provided issue number is a pull request
				if fetchedIssue.IsPullRequest() {
					errMessages = append(errMessages, fmt.Sprintf("Skipped issue #%d of **%s/%s** as it is a ***pull request***.", fetchedIssue.Number, issueRef.org, issueRef.repo))
					continue
				}
			}

			result = append(result, formatIssueRef(issueRef, org, repo))
		}
		return result
	}

	toLink := processIssues(linkIssues, true)
	toUnlink := processIssues(unlinkIssues, false)

	if len(toLink) > 0 || len(toUnlink) > 0 {
		pr, err := gc.GetPullRequest(org, repo, number)
		if err != nil {
			return fmt.Errorf("failed to get pull request: %w", err)
		}

		newBody := updateFixesLine(pr.Body, toLink, toUnlink)
		if newBody != pr.Body {
			if err := gc.UpdatePullRequest(org, repo, number, nil, &newBody, nil, nil, nil); err != nil {
				return fmt.Errorf("failed to update PR body: %w", err)
			}

			log.Infof("Successfully updated the PR body by linking issues: %s and unlinking issues: %s", toLink, toUnlink)
			sb.WriteString("Updated the `Fixes` line in the pull request body.")
		} else {
			log.Infof("PR body is already up-to-date. No changes needed for linking: %s, unlinking: %s", toLink, toUnlink)
			sb.WriteString("The PR body is already up-to-date. No changes were made.")
		}
	}

	if len(errMessages) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\nHowever, ")
		}
		sb.WriteString("there are one or more errors with issue references provided. Please cross check and retry.\n")
		for _, msg := range errMessages {
			fmt.Fprintf(&sb, "* %s\n", msg)
		}
	}

	return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, sb.String()))
}

func parseIssueRef(issue, defaultOrg, defaultRepo string) (*issueRef, error) {
	// Handling single issue references
	if num, err := strconv.Atoi(issue); err == nil {
		return &issueRef{org: defaultOrg, repo: defaultRepo, num: num}, nil
	}

	if !strings.Contains(issue, "/") {
		return nil, fmt.Errorf("unrecognized issue reference: %s", issue)
	}

	// Handling issue references in format org/repo#issue-number
	parts := strings.Split(issue, "#")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid issue ref: %s", issue)
	}
	orgRepo := strings.Split(parts[0], "/")
	if len(orgRepo) != 2 {
		return nil, fmt.Errorf("invalid org/repo format: %s", issue)
	}
	num, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid issue number: %s", issue)
	}
	return &issueRef{org: orgRepo[0], repo: orgRepo[1], num: num}, nil

}

func formatIssueRef(ref *issueRef, defaultOrg, defaultRepo string) string {
	if ref.org == defaultOrg && ref.repo == defaultRepo {
		return fmt.Sprintf("#%d", ref.num)
	}
	return fmt.Sprintf("%s/%s#%d", ref.org, ref.repo, ref.num)
}

func updateFixesLine(body string, toAdd []string, toRemove []string) string {
	lines := strings.Split(body, "\n")
	var fixesLine string
	fixesIndex := -1
	issueSet := sets.New[string]()

	// Find existing Fixes line
	for i, line := range lines {
		if fixesRegex.MatchString(line) {
			fixesIndex = i
			break
		}
	}
	// Parse issues from the found line
	if fixesIndex >= 0 {
		m := fixesRegex.FindStringSubmatch(lines[fixesIndex])
		for issue := range strings.FieldsSeq(m[1]) {
			issueSet.Insert(issue)
		}
	}

	issueSet = issueSet.Difference(sets.New(toRemove...)).Union(sets.New(toAdd...))

	if issueSet.Len() == 0 {
		// If all linked issues have been removed, the fixes line can be deleted from the PR body.
		if fixesIndex != -1 {
			lines = append(lines[:fixesIndex], lines[fixesIndex+1:]...)
		}
		return strings.Join(lines, "\n")
	}

	newIssueRefs := sets.List(issueSet)
	fixesLine = "Fixes " + strings.Join(newIssueRefs, " ")

	if fixesIndex >= 0 {
		lines[fixesIndex] = fixesLine
	} else {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, fixesLine)
	}

	return strings.Join(lines, "\n")
}
