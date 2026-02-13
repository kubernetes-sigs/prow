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

type IssueRef struct {
	Org  string
	Repo string
	Num  int
}

func handleLinkIssue(gc githubClient, log *logrus.Entry, e github.GenericCommentEvent, linkIssues []string, unlinkIssues []string) error {
	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	number := e.Number
	user := e.User.Login

	var (
		errs []error
		sb   strings.Builder
	)

	if !e.IsPR || e.Action != github.GenericCommentActionCreated {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(
			e.Body, e.HTMLURL, user, "This command can only be used on pull requests."))
	}

	isMember, err := gc.IsMember(org, user)
	if err != nil {
		return fmt.Errorf("unable to fetch if %s is an org member of %s: %w", user, org, err)
	}
	if !isMember {
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(
			e.Body, e.HTMLURL, user, "You must be an org member to use this command."))
	}

	// Verify if issues priovided in the comment are valid and format them accoordingly.
	validateIssues := func(issues []string) []string {
		var issueRefs []string
		for _, issue := range issues {
			issueRef, err := parseIssueRef(issue, org, repo)
			if err != nil {
				errs = append(errs, fmt.Errorf("Invalid format for issue **%s**. Supported formats are **issue-number** and **organization/repository#issue-number**", issue))
				continue
			}

			// If repo or org of the issue reference is different from the one in which the PR is created, check if it exists
			if org != issueRef.Org || repo != issueRef.Repo {
				if _, err := gc.GetRepo(issueRef.Org, issueRef.Repo); err != nil {
					errs = append(errs, fmt.Errorf("Failed to get repository details with name **%s**", issueRef.Repo))
					continue
				}
			}
			// Verify if the issue exists
			fetchedIssue, err := gc.GetIssue(issueRef.Org, issueRef.Repo, issueRef.Num)
			if err != nil {
				errs = append(errs, fmt.Errorf("Failed to get issue **#%d** from **%s/%s**", issueRef.Num, issueRef.Org, issueRef.Repo))
				continue
			}
			// Skip linking the issue if the provided issue number is a pull request
			if fetchedIssue.IsPullRequest() {
				errs = append(errs, fmt.Errorf("Skipping #%d of **%s/%s** as it is a ***pull request***.", fetchedIssue.Number, issueRef.Repo, issueRef.Org))
				continue
			}
			issueRefs = append(issueRefs, formatIssueRef(issueRef, org, repo))
		}
		return issueRefs
	}

	toLink := validateIssues(linkIssues)
	toUnlink := validateIssues(unlinkIssues)

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
			sb.WriteString("Updated the `Fixes` line in the pull request body.\nHowever, ")
		} else {
			log.Debug("PR body is already up-to-date. No changes needed.")
		}
	}

	if len(errs) > 0 {
		sb.WriteString("There are one or more errors with issue references provided, please cross check and retry.\n")

		for _, err := range errs {
			fmt.Fprintf(&sb, "* %v\n", err)
		}
		return gc.CreateComment(org, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, user, sb.String()))
	}

	return nil
}

func parseIssueRef(issue, defaultOrg, defaultRepo string) (*IssueRef, error) {
	// Handling single issue references
	if num, err := strconv.Atoi(issue); err == nil {
		return &IssueRef{Org: defaultOrg, Repo: defaultRepo, Num: num}, nil
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
	return &IssueRef{Org: orgRepo[0], Repo: orgRepo[1], Num: num}, nil

}

func formatIssueRef(ref *IssueRef, defaultOrg, defaultRepo string) string {
	if ref.Org == defaultOrg && ref.Repo == defaultRepo {
		return fmt.Sprintf("#%d", ref.Num)
	}
	return fmt.Sprintf("%s/%s#%d", ref.Org, ref.Repo, ref.Num)
}

func updateFixesLine(body string, toAdd []string, toRemove []string) string {
	lines := strings.Split(body, "\n")
	var fixesLine string
	fixesIndex := -1
	issueSet := sets.NewString()

	// Find and parse existing Fixes line
	for i, line := range lines {
		if m := fixesRegex.FindStringSubmatch(line); m != nil {
			fixesIndex = i
			for _, issue := range strings.Fields(m[1]) {
				issueSet.Insert(issue)
			}
			break
		}
	}

	issueSet = issueSet.Difference(sets.NewString(toRemove...)).Union(sets.NewString(toAdd...))

	if issueSet.Len() == 0 {
		// If all linked issues have been removed, the fixes line can be deleted from the PR body.
		if fixesIndex != -1 {
			lines = append(lines[:fixesIndex], lines[fixesIndex+1:]...)
		}
		return strings.Join(lines, "\n")
	}

	newIssueRefs := issueSet.List()
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
