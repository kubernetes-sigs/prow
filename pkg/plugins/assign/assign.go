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

package assign

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
)

const pluginName = "assign"

var (
	assignRe = regexp.MustCompile(`(?mi)^/(un)?assign(( @?[-\w]+?)*)\s*$`)
	// CCRegexp parses and validates /cc commands, also used by blunderbuss
	CCRegexp = regexp.MustCompile(`(?mi)^/(un)?cc(( +@?[-/\w]+?)*)\s*$`)
)

func init() {
	plugins.RegisterGenericCommentHandler(pluginName, handleGenericComment, helpProvider)
}

func helpProvider(config *plugins.Configuration, enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	configInfo := map[string]string{}
	for _, repo := range enabledRepos {
		restrict := config.AssignFor(repo.Org, repo.Repo).Restrict
		if restrict == nil {
			continue
		}
		msg := fmt.Sprintf("On issues, only members of the %s org can use /assign without restriction (action: %s). ", repo.Org, restrict.Action)
		switch restrict.Action {
		case plugins.AssignActionBlock:
			msg += "When a user who is not an org member uses /assign, nobody is assigned and the plugin replies with an explanation. "
		case plugins.AssignActionWarn:
			msg += "When a user who is not an org member uses /assign, the assignment is made and the plugin replies with a note that the issue has not been marked as available for new contributors. "
		}
		msg += "Only the membership of the user who comments is checked, for both self-assignment and assigning someone else. /unassign and pull requests are not restricted."
		if len(restrict.ExemptLabels) > 0 {
			msg += fmt.Sprintf(" On issues with any of the following labels, anyone can use /assign: %s.", strings.Join(restrict.ExemptLabels, ", "))
		}
		configInfo[repo.String()] = msg
	}
	pluginHelp := &pluginhelp.PluginHelp{
		Config:      configInfo,
		Description: "The assign plugin assigns or requests reviews from users/teams. Specific users can be assigned with the command '/assign @user1' or have reviews requested of them with the command '/cc @user1'. If no users are specified, the commands default to targeting the user who created the command. Assignments and requested reviews can be removed in the same way that they are added by prefixing the commands with 'un'.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/[un]assign [[@]<username>...|[@]<orgname/teamname>...]",
		Description: "Assigns assignee(s) or team(s) to the PR",
		Featured:    true,
		WhoCanUse:   "Anyone can use the command, but the target user(s) must be an org member, a repo collaborator, or should have previously commented on the issue or PR. A repository can additionally restrict '/assign' on issues to org members (both '/assign' and '/assign @user'); see the configuration for the repository. Pull requests are never restricted.",
		Examples:    []string{"/assign", "/unassign", "/assign @spongebob", "/assign spongebob patrick", "/assign @kubernetes/sig-foo-bar"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/[un]cc [[@]<username>...|[@]<orgname/teamname>...]",
		Description: "Requests a review from the user(s) or team(s).",
		Featured:    true,
		WhoCanUse:   "Anyone can use the command, but the target user(s) must be a member of the org that owns the repository.",
		Examples:    []string{"/cc", "/uncc", "/cc @spongebob", "/cc spongebob patrick", "/cc @kubernetes/sig-foo-bar"},
	})
	return pluginHelp, nil
}

type githubClient interface {
	AssignIssue(owner, repo string, number int, logins []string) error
	UnassignIssue(owner, repo string, number int, logins []string) error

	RequestReview(org, repo string, number int, logins []string) error
	UnrequestReview(org, repo string, number int, logins []string) error

	CreateComment(owner, repo string, number int, comment string) error

	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	IsMember(org, user string) (bool, error)
}

func handleGenericComment(pc plugins.Agent, e github.GenericCommentEvent) error {
	if e.Action != github.GenericCommentActionCreated {
		return nil
	}
	cfg := pc.PluginConfig.AssignFor(e.Repo.Owner.Login, e.Repo.Name)
	err := handle(newAssignHandler(e, pc.GitHubClient, pc.Logger, cfg))
	if e.IsPR {
		err = combineErrors(err, handle(newReviewHandler(e, pc.GitHubClient, pc.Logger)))
	}
	return err
}

func parseLogins(text string) []string {
	var parts []string
	for p := range strings.SplitSeq(text, " ") {
		t := strings.Trim(p, "@ ")
		if t == "" {
			continue
		}
		parts = append(parts, t)
	}
	return parts
}

func combineErrors(err1, err2 error) error {
	if err1 != nil && err2 != nil {
		return fmt.Errorf("two errors: 1) %v 2) %w", err1, err2)
	} else if err1 != nil {
		return err1
	} else {
		return err2
	}
}

// handle is the generic handler for the assign plugin. It uses the handler's regexp and affectedLogins
// functions to identify the users to add and/or remove and then passes the appropriate users to the
// handler's add and remove functions. If add fails to add some of the users, a response comment is
// created where the body of the response is generated by the handler's addFailureResponse function.
func handle(h *handler) error {
	e := h.event
	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	matches := h.regexp.FindAllStringSubmatch(e.Body, -1)
	if matches == nil {
		return nil
	}
	users := make(map[string]bool)
	for _, re := range matches {
		add := re[1] != "un" // un<cmd> == !add
		if re[2] == "" {
			users[e.User.Login] = add
		} else {
			for _, login := range parseLogins(re[2]) {
				users[login] = add
			}
		}
	}
	var toAdd, toRemove []string
	for login, add := range users {
		if add {
			toAdd = append(toAdd, login)
		} else {
			toRemove = append(toRemove, login)
		}
	}

	if len(toRemove) > 0 {
		h.log.Printf("Removing %s from %s/%s#%d: %v", h.userType, org, repo, e.Number, toRemove)
		if err := h.remove(org, repo, e.Number, toRemove); err != nil {
			return err
		}
	}
	if len(toAdd) > 0 {
		h.log.Printf("Adding %s to %s/%s#%d: %v", h.userType, org, repo, e.Number, toAdd)
		if err := h.add(org, repo, e.Number, toAdd); err != nil {
			if mu, ok := err.(github.MissingUsers); ok {
				msg := h.addFailureResponse(mu)
				if len(msg) == 0 {
					return nil
				}
				h.log.Printf("Failed to add %s to %s/%s#%d: %s", h.userType, org, repo, e.Number, mu.Error())
				if err := h.gc.CreateComment(org, repo, e.Number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, e.User.Login, msg)); err != nil {
					return fmt.Errorf("comment err: %w", err)
				}
				return nil
			}
			return err
		}
	}
	return nil
}

// handler is a struct that contains data about a github event and provides functions to help handle it.
type handler struct {
	// addFailureResponse generates the body of a response comment in the event that the add function fails.
	addFailureResponse func(mu github.MissingUsers) string
	// remove is the function that is called on the affected logins for a command prefixed with 'un'.
	remove func(org, repo string, number int, users []string) error
	// add is the function that is called on the affected logins for a command with no 'un' prefix.
	add func(org, repo string, number int, users []string) error

	// event is a pointer to the github.GenericCommentEvent struct that triggered the handler.
	event *github.GenericCommentEvent
	// regexp is the regular expression describing the command. It must have an optional 'un' prefix
	// as the first subgroup and the arguments to the command as the second subgroup.
	regexp *regexp.Regexp
	// gc is the githubClient to use for creating response comments in the event of a failure.
	gc githubClient

	// log is a logrus.Entry used to record actions the handler takes.
	log *logrus.Entry
	// userType is a string that represents the type of users affected by this handler. (e.g. 'assignees')
	userType string
}

func newAssignHandler(e github.GenericCommentEvent, gc githubClient, log *logrus.Entry, cfg *plugins.Assign) *handler {
	org := e.Repo.Owner.Login
	addFailureResponse := func(mu github.MissingUsers) string {
		return fmt.Sprintf("GitHub didn't allow me to assign the following users: %s.\n\nNote that only [%s members](https://%s/orgs/%s/people) with read permissions, repo collaborators and people who have commented on this issue/PR can be assigned. Additionally, issues/PRs can only have 10 assignees at the same time.\nFor more information please see [the contributor guide](https://git.k8s.io/community/contributors/guide/first-contribution.md#issue-assignment-in-github)", strings.Join(mu.Users, ", "), org, github.DefaultHost, org)
	}

	add := gc.AssignIssue
	// The restriction only applies to issues: on a PR, anyone must still be able
	// to /assign the reviewers and approvers the approval instructions point to.
	if restrict := cfg.Restrict; restrict != nil && !e.IsPR {
		add = restrictedAssignFunc(e, gc, log, restrict, org)
	}

	return &handler{
		addFailureResponse: addFailureResponse,
		remove:             gc.UnassignIssue,
		add:                add,
		event:              &e,
		regexp:             assignRe,
		gc:                 gc,
		log:                log,
		userType:           "assignee(s)",
	}
}

// restrictedAssignFunc returns an add function for the assign handler that
// restricts /assign on an issue to commenters who are members of the org,
// unless the issue has an exempt label. Only the membership of the commenter
// is checked, not the membership of the users being assigned, so an org member
// can assign anyone and a non-member is restricted from assigning anyone.
func restrictedAssignFunc(e github.GenericCommentEvent, gc githubClient, log *logrus.Entry, restrict *plugins.AssignRestrict, org string) func(string, string, int, []string) error {
	return func(owner, repo string, number int, logins []string) error {
		allowed, err := commenterMayAssign(gc, org, e.User.Login, owner, repo, number, restrict.ExemptLabels)
		if err != nil {
			return err
		}
		if allowed {
			return gc.AssignIssue(owner, repo, number, logins)
		}

		if restrict.Action == plugins.AssignActionBlock {
			log.Infof("Not assigning %v to %s/%s#%d: %s is not a member of the %s org", logins, owner, repo, number, e.User.Login, org)
			if err := gc.CreateComment(owner, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, e.User.Login, blockMessage(org, restrict.ExemptLabels))); err != nil {
				return fmt.Errorf("comment err: %w", err)
			}
			return nil
		}

		// AssignActionWarn: the commenter is not an org member and the issue has no
		// exempt label. Assign the users as without the restriction, then post a
		// friendly nudge to the commenter.
		assignErr := gc.AssignIssue(owner, repo, number, logins)
		if err := gc.CreateComment(owner, repo, number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, e.User.Login, warnMessage(org, restrict.ExemptLabels))); err != nil {
			log.WithError(err).Error("failed to post warning comment for /assign by a non-org-member")
		}
		return assignErr
	}
}

// commenterMayAssign reports whether the commenter may use /assign on the issue
// without restriction: either the issue has an exempt label or the commenter is
// a member of the org.
func commenterMayAssign(gc githubClient, org, commenter, owner, repo string, number int, exemptLabels []string) (bool, error) {
	if len(exemptLabels) > 0 {
		labels, err := gc.GetIssueLabels(owner, repo, number)
		if err != nil {
			return false, err
		}
		if hasExemptLabel(labels, exemptLabels) {
			return true, nil
		}
	}
	return gc.IsMember(org, commenter)
}

func blockMessage(org string, exemptLabels []string) string {
	msg := fmt.Sprintf("Only [%s members](https://%s/orgs/%s/people) can use `/assign` on this issue", org, github.DefaultHost, org)
	if len(exemptLabels) > 0 {
		msg += fmt.Sprintf(", unless it has one of the following labels: %s", formatLabels(exemptLabels))
	}
	return msg + "."
}

func warnMessage(org string, exemptLabels []string) string {
	msg := fmt.Sprintf("Thanks for your interest! Note that you are not a member of the **%s** organization", org)
	if len(exemptLabels) == 0 {
		return msg + ". Before you start working on this issue, please check with the maintainers that it is ready for new contributors."
	}
	return msg + fmt.Sprintf(" and this issue has not been marked as available for new contributors. "+
		"If you are new to this project, issues labeled %s are a good place to start.", formatLabels(exemptLabels))
}

func formatLabels(labels []string) string {
	quoted := make([]string, 0, len(labels))
	for _, l := range labels {
		quoted = append(quoted, "`"+l+"`")
	}
	return strings.Join(quoted, ", ")
}

func hasExemptLabel(issueLabels []github.Label, exemptLabels []string) bool {
	for _, il := range issueLabels {
		for _, el := range exemptLabels {
			if strings.EqualFold(il.Name, el) {
				return true
			}
		}
	}
	return false
}

func newReviewHandler(e github.GenericCommentEvent, gc githubClient, log *logrus.Entry) *handler {
	org := e.Repo.Owner.Login
	addFailureResponse := func(mu github.MissingUsers) string {
		return fmt.Sprintf("GitHub didn't allow me to request PR reviews from the following users: %s.\n\nNote that only [%s members](https://%s/orgs/%s/people) and repo collaborators can review this PR, and authors cannot review their own PRs.", strings.Join(mu.Users, ", "), org, github.DefaultHost, org)
	}

	return &handler{
		addFailureResponse: addFailureResponse,
		remove:             gc.UnrequestReview,
		add:                gc.RequestReview,
		event:              &e,
		regexp:             CCRegexp,
		gc:                 gc,
		log:                log,
		userType:           "reviewer(s)",
	}
}
