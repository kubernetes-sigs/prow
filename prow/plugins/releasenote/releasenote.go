/*
Copyright 2016 The Kubernetes Authors.

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

package releasenote

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/test-infra/prow/labels"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/plugins"
)

const (
	// PluginName defines this plugin's registered name.
	PluginName = "release-note"
)
const (
	releaseNoteFormat            = `Adding the "%s" label because no release-note block was detected, please follow our [release note process](https://git.k8s.io/community/contributors/guide/release-notes.md) to remove it.`
	parentReleaseNoteFormat      = `All 'parent' PRs of a cherry-pick PR must have one of the %q or %q labels, or this PR must follow the standard/parent release note labeling requirement.`
	releaseNoteDeprecationFormat = `Adding the "%s" label and removing any existing "%s" label because there is a "%s" label on the PR.`

	actionRequiredNote = "action required"
)

var (
	releaseNoteBody            = fmt.Sprintf(releaseNoteFormat, labels.ReleaseNoteLabelNeeded)
	parentReleaseNoteBody      = fmt.Sprintf(parentReleaseNoteFormat, labels.ReleaseNote, labels.ReleaseNoteActionRequired)
	releaseNoteDeprecationBody = fmt.Sprintf(releaseNoteDeprecationFormat, labels.ReleaseNoteLabelNeeded, labels.ReleaseNoteNone, labels.DeprecationLabel)

	noteMatcherRE = regexp.MustCompile(`(?s)(?:Release note\*\*:\s*(?:<!--[^<>]*-->\s*)?` + "```(?:release-note)?|```release-note)(.+?)```")
	cpRe          = regexp.MustCompile(`Cherry pick of #([[:digit:]]+) on release-([[:digit:]]+\.[[:digit:]]+).`)
	noneRe        = regexp.MustCompile(`(?i)^\W*(NONE|NO)\W*$`)

	allRNLabels = []string{
		labels.ReleaseNoteNone,
		labels.ReleaseNoteActionRequired,
		labels.ReleaseNoteLabelNeeded,
		labels.ReleaseNote,
	}

	releaseNoteRe               = regexp.MustCompile(`(?mi)^/release-note\s*$`)
	releaseNoteEditRe           = regexp.MustCompile(`(?mi)^/release-note-edit\s*$`)
	releaseNoteNoneRe           = regexp.MustCompile(`(?mi)^/release-note-none\s*$`)
	releaseNoteActionRequiredRe = regexp.MustCompile(`(?mi)^/release-note-action-required\s*$`)
)

func init() {
	plugins.RegisterIssueCommentHandler(PluginName, handleIssueComment, helpProvider)
	plugins.RegisterPullRequestHandler(PluginName, handlePullRequest, helpProvider)
}

func helpProvider(_ *plugins.Configuration, _ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The releasenote plugin implements a release note process that uses a markdown 'release-note' code block to associate a release note with a pull request. Until the 'release-note' block in the pull request body is populated the PR will be assigned the '` + labels.ReleaseNoteLabelNeeded + `' label.
<br>There are three valid types of release notes that can replace this label:
<ol><li>PRs with a normal release note in the 'release-note' block are given the label '` + labels.ReleaseNote + `'.</li>
<li>PRs that have a release note of 'none' in the block are given the label '` + labels.ReleaseNoteNone + `' to indicate that the PR does not warrant a release note.</li>
<li>PRs that contain 'action required' in their 'release-note' block are given the label '` + labels.ReleaseNoteActionRequired + `' to indicate that the PR introduces potentially breaking changes that necessitate user action before upgrading to the release.</li></ol>
` + "To use the plugin, in the pull request body text:\n\n```release-note\n<release note content>\n```",
	}
	// NOTE: the other two commands re deprecated, so we're not documenting them
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/release-note-none",
		Description: "Adds the '" + labels.ReleaseNoteNone + `' label to indicate that the PR does not warrant a release note. This is deprecated and ideally <a href="https://git.k8s.io/community/contributors/guide/release-notes.md">the release note process</a> should be followed in the PR body instead.`,
		WhoCanUse:   "PR Authors and Org Members.",
		Examples:    []string{"/release-note-none"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/release-note-edit",
		Description: "Replaces the release note block in the top level comment with the provided one.",
		WhoCanUse:   "Org Members.",
		Examples:    []string{"/release-note-edit\r\n```release-note\r\nThe new release note\r\n```"},
	})
	return pluginHelp, nil
}

type githubClient interface {
	IsMember(org, user string) (bool, error)
	CreateComment(owner, repo string, number int, comment string) error
	AddLabel(owner, repo string, number int, label string) error
	RemoveLabel(owner, repo string, number int, label string) error
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	ListIssueComments(org, repo string, number int) ([]github.IssueComment, error)
	DeleteStaleComments(org, repo string, number int, comments []github.IssueComment, isStale func(github.IssueComment) bool) error
	BotUserChecker() (func(candidate string) bool, error)
	EditIssue(org, repo string, number int, issue *github.Issue) (*github.Issue, error)
}

func handleIssueComment(pc plugins.Agent, ic github.IssueCommentEvent) error {
	return handleComment(pc.GitHubClient, pc.Logger, ic)
}

func handleComment(gc githubClient, log *logrus.Entry, ic github.IssueCommentEvent) error {
	// Only consider PRs and new comments.
	if !ic.Issue.IsPullRequest() || ic.Action != github.IssueCommentActionCreated {
		return nil
	}

	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	number := ic.Issue.Number

	if releaseNoteEditRe.MatchString(ic.Comment.Body) {
		return editReleaseNote(gc, log, ic)
	}

	// Which label does the comment want us to add?
	var nl string
	switch {
	case releaseNoteRe.MatchString(ic.Comment.Body):
		nl = labels.ReleaseNote
	case releaseNoteNoneRe.MatchString(ic.Comment.Body):
		nl = labels.ReleaseNoteNone
	case releaseNoteActionRequiredRe.MatchString(ic.Comment.Body):
		nl = labels.ReleaseNoteActionRequired
	default:
		return nil
	}

	// Emit deprecation warning for /release-note and /release-note-action-required.
	if nl == labels.ReleaseNote || nl == labels.ReleaseNoteActionRequired {
		format := "the `/%s` and `/%s` commands have been deprecated.\nPlease edit the `release-note` block in the PR body text to include the release note. If the release note requires additional action include the string `action required` in the release note. For example:\n````\n```release-note\nSome release note with action required.\n```\n````"
		resp := fmt.Sprintf(format, labels.ReleaseNote, labels.ReleaseNoteActionRequired)
		return gc.CreateComment(org, repo, number, plugins.FormatICResponse(ic.Comment, resp))
	}

	// Only allow authors and org members to add currentLabels.
	isMember, err := gc.IsMember(ic.Repo.Owner.Login, ic.Comment.User.Login)
	if err != nil {
		return err
	}

	isAuthor := ic.Issue.IsAuthor(ic.Comment.User.Login)

	if !isMember && !isAuthor {
		format := "you can only set the release note label to %s if you are the PR author or an org member."
		resp := fmt.Sprintf(format, labels.ReleaseNoteNone)
		return gc.CreateComment(org, repo, number, plugins.FormatICResponse(ic.Comment, resp))
	}

	// Don't allow the /release-note-none command if the release-note block contains a valid release note.
	blockNL := determineReleaseNoteLabel(ic.Issue.Body, labelsSet(ic.Issue.Labels))
	if blockNL == labels.ReleaseNote || blockNL == labels.ReleaseNoteActionRequired {
		format := "you can only set the release note label to %s if the release-note block in the PR body text is empty or \"none\"."
		resp := fmt.Sprintf(format, labels.ReleaseNoteNone)
		return gc.CreateComment(org, repo, number, plugins.FormatICResponse(ic.Comment, resp))
	}

	// Don't allow /release-note-none command if the PR has a 'kind/deprecation'
	// label.
	if ic.Issue.HasLabel(labels.DeprecationLabel) {
		format := "you can not set the release note label to \"%s\" because the PR has the label \"%s\"."
		resp := fmt.Sprintf(format, labels.ReleaseNoteNone, labels.DeprecationLabel)
		return gc.CreateComment(org, repo, number, plugins.FormatICResponse(ic.Comment, resp))
	}

	if !ic.Issue.HasLabel(labels.ReleaseNoteNone) {
		if err := gc.AddLabel(org, repo, number, labels.ReleaseNoteNone); err != nil {
			return err
		}
	}

	currentLabels := sets.Set[string]{}
	for _, label := range ic.Issue.Labels {
		currentLabels.Insert(label.Name)
	}
	// Remove all other release-note-* currentLabels if necessary.
	return removeOtherLabels(
		func(l string) error {
			return gc.RemoveLabel(org, repo, number, l)
		},
		labels.ReleaseNoteNone,
		allRNLabels,
		currentLabels,
	)
}

func removeOtherLabels(remover func(string) error, label string, labelSet []string, currentLabels sets.Set[string]) error {
	var errs []error
	for _, elem := range labelSet {
		if elem != label && currentLabels.Has(elem) {
			if err := remover(elem); err != nil {
				errs = append(errs, err)
			}
			currentLabels.Delete(elem)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("encountered %d errors setting labels: %v", len(errs), errs)
	}
	return nil
}

func handlePullRequest(pc plugins.Agent, pr github.PullRequestEvent) error {
	return handlePR(pc.GitHubClient, pc.Logger, &pr)
}

func shouldHandlePR(pr *github.PullRequestEvent) bool {
	// Only consider events that edit the PR body or add a label
	if pr.Action != github.PullRequestActionOpened &&
		pr.Action != github.PullRequestActionEdited &&
		pr.Action != github.PullRequestActionLabeled {
		return false
	}

	// Ignoring unrelated PR labels prevents duplicate release note messages
	if pr.Action == github.PullRequestActionLabeled {
		for _, rnLabel := range allRNLabels {
			if pr.Label.Name == rnLabel {
				return true
			}
		}
		return false
	}

	return true
}

func handlePR(gc githubClient, log *logrus.Entry, pr *github.PullRequestEvent) error {
	if !shouldHandlePR(pr) {
		return nil
	}
	org := pr.Repo.Owner.Login
	repo := pr.Repo.Name

	prInitLabels, err := gc.GetIssueLabels(org, repo, pr.Number)
	if err != nil {
		return fmt.Errorf("failed to list labels on PR #%d. err: %w", pr.Number, err)
	}
	prLabels := labelsSet(prInitLabels)

	var comments []github.IssueComment
	labelToAdd := determineReleaseNoteLabel(pr.PullRequest.Body, prLabels)

	if labelToAdd == labels.ReleaseNoteLabelNeeded {
		//Do not add do not merge label when the PR is merged
		if pr.PullRequest.Merged {
			return nil
		}
		if !prMustFollowRelNoteProcess(gc, log, pr, prLabels, true) {
			ensureNoRelNoteNeededLabel(gc, log, pr, prLabels)
			return clearStaleComments(gc, log, pr, prLabels, nil)
		}

		if prLabels.Has(labels.DeprecationLabel) {
			if !prLabels.Has(labels.ReleaseNoteLabelNeeded) {
				comment := plugins.FormatSimpleResponse(releaseNoteDeprecationBody)
				if err := gc.CreateComment(org, repo, pr.Number, comment); err != nil {
					log.WithError(err).Errorf("Failed to comment on %s/%s#%d with comment %q.", org, repo, pr.Number, comment)
				}
			}
		} else {
			comments, err = gc.ListIssueComments(org, repo, pr.Number)
			if err != nil {
				return fmt.Errorf("failed to list comments on %s/%s#%d. err: %w", org, repo, pr.Number, err)
			}
			if containsNoneCommand(comments) {
				labelToAdd = labels.ReleaseNoteNone
			} else if !prLabels.Has(labels.ReleaseNoteLabelNeeded) {
				comment := plugins.FormatSimpleResponse(releaseNoteBody)
				if err := gc.CreateComment(org, repo, pr.Number, comment); err != nil {
					log.WithError(err).Errorf("Failed to comment on %s/%s#%d with comment %q.", org, repo, pr.Number, comment)
				}
			}
		}
	}

	// Add the label if needed
	if !prLabels.Has(labelToAdd) {
		if err = gc.AddLabel(org, repo, pr.Number, labelToAdd); err != nil {
			return err
		}
		prLabels.Insert(labelToAdd)
	}

	err = removeOtherLabels(
		func(l string) error {
			return gc.RemoveLabel(org, repo, pr.Number, l)
		},
		labelToAdd,
		allRNLabels,
		prLabels,
	)
	if err != nil {
		log.Error(err)
	}

	return clearStaleComments(gc, log, pr, prLabels, comments)
}

// clearStaleComments deletes old comments that are no longer applicable.
func clearStaleComments(gc githubClient, log *logrus.Entry, pr *github.PullRequestEvent, prLabels sets.Set[string], comments []github.IssueComment) error {
	// If the PR must follow the process and hasn't yet completed the process, don't remove comments.
	if prMustFollowRelNoteProcess(gc, log, pr, prLabels, false) && !releaseNoteAlreadyAdded(prLabels) {
		return nil
	}
	botUserChecker, err := gc.BotUserChecker()
	if err != nil {
		return err
	}
	return gc.DeleteStaleComments(
		pr.Repo.Owner.Login,
		pr.Repo.Name,
		pr.Number,
		comments,
		func(c github.IssueComment) bool { // isStale function
			return botUserChecker(c.User.Login) &&
				(strings.Contains(c.Body, releaseNoteBody) ||
					strings.Contains(c.Body, parentReleaseNoteBody))
		},
	)
}

func containsNoneCommand(comments []github.IssueComment) bool {
	for _, c := range comments {
		if releaseNoteNoneRe.MatchString(c.Body) {
			return true
		}
	}
	return false
}

func ensureNoRelNoteNeededLabel(gc githubClient, log *logrus.Entry, pr *github.PullRequestEvent, prLabels sets.Set[string]) {
	org := pr.Repo.Owner.Login
	repo := pr.Repo.Name
	format := "Failed to remove the label %q from %s/%s#%d."
	if prLabels.Has(labels.ReleaseNoteLabelNeeded) {
		if err := gc.RemoveLabel(org, repo, pr.Number, labels.ReleaseNoteLabelNeeded); err != nil {
			log.WithError(err).Errorf(format, labels.ReleaseNoteLabelNeeded, org, repo, pr.Number)
		}
	}
}

// determineReleaseNoteLabel returns the label to be added based on the contents of the 'release-note'
// section of a PR's body text, as well as the set of PR's labels.
func determineReleaseNoteLabel(body string, prLabels sets.Set[string]) string {
	composedReleaseNote := strings.ToLower(strings.TrimSpace(getReleaseNote(body)))
	hasNoneNoteInPRBody := noneRe.MatchString(composedReleaseNote)
	hasDeprecationLabel := prLabels.Has(labels.DeprecationLabel)

	switch {
	case composedReleaseNote == "" && hasDeprecationLabel:
		return labels.ReleaseNoteLabelNeeded
	case composedReleaseNote == "" && prLabels.Has(labels.ReleaseNoteNone):
		return labels.ReleaseNoteNone
	case composedReleaseNote == "":
		return labels.ReleaseNoteLabelNeeded
	case hasNoneNoteInPRBody && hasDeprecationLabel:
		return labels.ReleaseNoteLabelNeeded
	case hasNoneNoteInPRBody:
		return labels.ReleaseNoteNone
	case strings.Contains(composedReleaseNote, actionRequiredNote):
		return labels.ReleaseNoteActionRequired
	default:
		return labels.ReleaseNote
	}
}

// getReleaseNote returns the release note from a PR body
// assumes that the PR body followed the PR template
func getReleaseNote(body string) string {
	potentialMatch := noteMatcherRE.FindStringSubmatch(body)
	if potentialMatch == nil {
		return ""
	}
	return strings.TrimSpace(potentialMatch[1])
}

func releaseNoteAlreadyAdded(prLabels sets.Set[string]) bool {
	return prLabels.HasAny(labels.ReleaseNote, labels.ReleaseNoteActionRequired, labels.ReleaseNoteNone)
}

func prMustFollowRelNoteProcess(gc githubClient, log *logrus.Entry, pr *github.PullRequestEvent, prLabels sets.Set[string], comment bool) bool {
	if pr.PullRequest.Base.Ref == "master" {
		return true
	}

	parents := getCherrypickParentPRNums(pr.PullRequest.Body)
	// if it has no parents it needs to follow the release note process
	if len(parents) == 0 {
		return true
	}

	org := pr.Repo.Owner.Login
	repo := pr.Repo.Name

	var notelessParents []string
	for _, parent := range parents {
		// If the parent didn't set a release note, the CP must
		parentLabels, err := gc.GetIssueLabels(org, repo, parent)
		if err != nil {
			log.WithError(err).Errorf("Failed to list labels on PR #%d (parent of #%d).", parent, pr.Number)
			continue
		}
		if !github.HasLabel(labels.ReleaseNote, parentLabels) &&
			!github.HasLabel(labels.ReleaseNoteActionRequired, parentLabels) {
			notelessParents = append(notelessParents, "#"+strconv.Itoa(parent))
		}
	}
	if len(notelessParents) == 0 {
		// All of the parents set the releaseNote or releaseNoteActionRequired label,
		// so this cherrypick PR needs to do nothing.
		return false
	}

	if comment && !prLabels.Has(labels.ReleaseNoteLabelNeeded) {
		comment := plugins.FormatResponse(
			pr.PullRequest.User.Login,
			parentReleaseNoteBody,
			fmt.Sprintf("The following parent PRs have neither the %q nor the %q labels: %s.",
				labels.ReleaseNote,
				labels.ReleaseNoteActionRequired,
				strings.Join(notelessParents, ", "),
			),
		)
		if err := gc.CreateComment(org, repo, pr.Number, comment); err != nil {
			log.WithError(err).Errorf("Error creating comment on %s/%s#%d with comment %q.", org, repo, pr.Number, comment)
		}
	}
	return true
}

func getCherrypickParentPRNums(body string) []int {
	lines := strings.Split(body, "\n")

	var out []int
	for _, line := range lines {
		matches := cpRe.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		parentNum, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		out = append(out, parentNum)
	}
	return out
}

func labelsSet(labels []github.Label) sets.Set[string] {
	prLabels := sets.Set[string]{}
	for _, label := range labels {
		prLabels.Insert(label.Name)
	}
	return prLabels
}

// editReleaseNote is used to edit the top level release note.
// Since the edit itself triggers an event we don't need to worry
// about labels because the plugin will run again and handle them.
func editReleaseNote(gc githubClient, log *logrus.Entry, ic github.IssueCommentEvent) error {
	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	user := ic.Comment.User.Login

	isMember, err := gc.IsMember(org, user)
	if err != nil {
		return fmt.Errorf("unable to fetch if %s is an org member of %s: %w", user, org, err)
	}
	if !isMember {
		return gc.CreateComment(
			org, repo, ic.Issue.Number,
			plugins.FormatResponseRaw(ic.Comment.Body, ic.Issue.HTMLURL, user, "You must be an org member to edit the release note."),
		)
	}

	newNote := getReleaseNote(ic.Comment.Body)
	if newNote == "" {
		return gc.CreateComment(
			org, repo, ic.Issue.Number,
			plugins.FormatResponseRaw(ic.Comment.Body, ic.Comment.HTMLURL, user, "/release-note-edit must be used with a release note block."),
		)
	}

	// 0: start of release note block
	// 1: end of release note block
	// 2: start of release note content
	// 3: end of release note content
	i := noteMatcherRE.FindStringSubmatchIndex(ic.Issue.Body)
	if len(i) != 4 {
		return gc.CreateComment(
			org, repo, ic.Issue.Number,
			plugins.FormatResponseRaw(ic.Comment.Body, ic.Comment.HTMLURL, user, "/release-note-edit must be used with a single release note block."),
		)
	}
	// Splice in the contents of the new release note block to the top level comment
	// This accounts for all older regex matches
	b := []byte(ic.Issue.Body)
	replaced := append(b[:i[2]], append([]byte("\r\n"+strings.TrimSpace(newNote)+"\r\n"), b[i[3]:]...)...)
	ic.Issue.Body = string(replaced)

	_, err = gc.EditIssue(ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, &ic.Issue)
	if err != nil {
		return fmt.Errorf("unable to edit issue: %w", err)
	}
	log.WithFields(logrus.Fields{
		"user":        user,
		"org":         org,
		"repo":        repo,
		"issueNumber": ic.Issue.Number,
	}).Info("edited release note")
	return nil
}
