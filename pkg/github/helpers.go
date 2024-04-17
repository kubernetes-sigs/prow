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

package github

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// SecurityForkNameRE is a regexp matching repos that are temporary security forks.
// https://help.github.com/en/github/managing-security-vulnerabilities/collaborating-in-a-temporary-private-fork-to-resolve-a-security-vulnerability
var SecurityForkNameRE = regexp.MustCompile(`^[\w-]+-ghsa-[\w-]+$`)

// ImageSizeLimit is the maximum image size GitHub allows in bytes (5MB).
const ImageSizeLimit = 5242880

// HasLabel checks if label is in the label set "issueLabels".
func HasLabel(label string, issueLabels []Label) bool {
	for _, l := range issueLabels {
		if strings.EqualFold(l.Name, label) {
			return true
		}
	}
	return false
}

// HasLabels checks if all labels are in the github.label set "issueLabels".
func HasLabels(labels []string, issueLabels []Label) bool {
	for _, label := range labels {
		if !HasLabel(label, issueLabels) {
			return false
		}
	}
	return true
}

// ImageTooBig checks if image is bigger than github limits.
func ImageTooBig(url string) (bool, error) {
	// try to get the image size from Content-Length header
	resp, err := http.Head(url)
	if err != nil {
		return true, fmt.Errorf("HEAD error: %w", err)
	}
	defer resp.Body.Close()
	if sc := resp.StatusCode; sc != http.StatusOK {
		return true, fmt.Errorf("failing %d response", sc)
	}
	size, _ := strconv.Atoi(resp.Header.Get("Content-Length"))
	if size > ImageSizeLimit {
		return true, nil
	}
	return false, nil
}

// LevelFromPermissions adapts a repo permissions struct to the
// appropriate permission level used elsewhere.
func LevelFromPermissions(permissions RepoPermissions) RepoPermissionLevel {
	if permissions.Admin {
		return Admin
	} else if permissions.Maintain {
		return Maintain
	} else if permissions.Push {
		return Write
	} else if permissions.Triage {
		return Triage
	} else if permissions.Pull {
		return Read
	} else {
		return None
	}
}

// PermissionsFromTeamPermissions
func PermissionsFromTeamPermission(permission TeamPermission) RepoPermissions {
	switch permission {
	case RepoPull:
		return RepoPermissions{Pull: true}
	case RepoTriage:
		return RepoPermissions{Pull: true, Triage: true}
	case RepoPush:
		return RepoPermissions{Pull: true, Triage: true, Push: true}
	case RepoMaintain:
		return RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true}
	case RepoAdmin:
		return RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}
	default:
		// Should never happen unless the type gets new value
		return RepoPermissions{}
	}
}

// CommentLikeEventTypes are various event types that can be coerced into
// a GenericCommentEvent.
type CommentLikeEventTypes interface {
	IssueEvent | IssueCommentEvent | PullRequestEvent | ReviewEvent | ReviewCommentEvent
}

// GeneralizeComment takes an event that can be coerced into a GenericCommentEvent
// and returns a populated GenericCommentEvent.
func GeneralizeComment[E CommentLikeEventTypes](event E) (*GenericCommentEvent, error) {
	switch t := any(event).(type) {

	case IssueEvent:
		action := GeneralizeCommentAction(string(t.Action))
		if action == "" {
			return nil, fmt.Errorf("failed to determine events action [%v]", string(t.Action))
		}

		return &GenericCommentEvent{
			ID:           t.Issue.ID,
			NodeID:       t.Issue.NodeID,
			GUID:         t.GUID,
			IsPR:         t.Issue.IsPullRequest(),
			Action:       action,
			Body:         t.Issue.Body,
			HTMLURL:      t.Issue.HTMLURL,
			Number:       t.Issue.Number,
			Repo:         t.Repo,
			User:         t.Issue.User,
			IssueAuthor:  t.Issue.User,
			Assignees:    t.Issue.Assignees,
			IssueState:   t.Issue.State,
			IssueTitle:   t.Issue.Title,
			IssueBody:    t.Issue.Body,
			IssueHTMLURL: t.Issue.HTMLURL,
		}, nil
	case IssueCommentEvent:
		action := GeneralizeCommentAction(string(t.Action))
		if action == "" {
			return nil, fmt.Errorf("failed to determine events action [%v]", string(t.Action))
		}

		return &GenericCommentEvent{
			ID:           t.Issue.ID,
			NodeID:       t.Issue.NodeID,
			CommentID:    &t.Comment.ID,
			GUID:         t.GUID,
			IsPR:         t.Issue.IsPullRequest(),
			Action:       action,
			Body:         t.Comment.Body,
			HTMLURL:      t.Comment.HTMLURL,
			Number:       t.Issue.Number,
			Repo:         t.Repo,
			User:         t.Comment.User,
			IssueAuthor:  t.Issue.User,
			Assignees:    t.Issue.Assignees,
			IssueState:   t.Issue.State,
			IssueTitle:   t.Issue.Title,
			IssueBody:    t.Issue.Body,
			IssueHTMLURL: t.Issue.HTMLURL,
		}, nil
	case PullRequestEvent:
		action := GeneralizeCommentAction(string(t.Action))
		if action == "" {
			return nil, fmt.Errorf("failed to determine events action [%v]", string(t.Action))
		}

		return &GenericCommentEvent{
			ID:           t.PullRequest.ID,
			NodeID:       t.PullRequest.NodeID,
			GUID:         t.GUID,
			IsPR:         true,
			Action:       action,
			Body:         t.PullRequest.Body,
			HTMLURL:      t.PullRequest.HTMLURL,
			Number:       t.PullRequest.Number,
			Repo:         t.Repo,
			User:         t.PullRequest.User,
			IssueAuthor:  t.PullRequest.User,
			Assignees:    t.PullRequest.Assignees,
			IssueState:   t.PullRequest.State,
			IssueTitle:   t.PullRequest.Title,
			IssueBody:    t.PullRequest.Body,
			IssueHTMLURL: t.PullRequest.HTMLURL,
		}, nil
	case ReviewEvent:
		action := GeneralizeCommentAction(string(t.Action))
		if action == "" {
			return nil, fmt.Errorf("failed to determine events action [%v]", string(t.Action))
		}

		return &GenericCommentEvent{
			GUID:         t.GUID,
			NodeID:       t.Review.NodeID,
			IsPR:         true,
			Action:       action,
			Body:         t.Review.Body,
			HTMLURL:      t.Review.HTMLURL,
			Number:       t.PullRequest.Number,
			Repo:         t.Repo,
			User:         t.Review.User,
			IssueAuthor:  t.PullRequest.User,
			Assignees:    t.PullRequest.Assignees,
			IssueState:   t.PullRequest.State,
			IssueTitle:   t.PullRequest.Title,
			IssueBody:    t.PullRequest.Body,
			IssueHTMLURL: t.PullRequest.HTMLURL,
		}, nil
	case ReviewCommentEvent:
		action := GeneralizeCommentAction(string(t.Action))
		if action == "" {
			return nil, fmt.Errorf("failed to determine events action [%v]", string(t.Action))
		}

		return &GenericCommentEvent{
			GUID:         t.GUID,
			NodeID:       t.Comment.NodeID,
			IsPR:         true,
			CommentID:    &t.Comment.ID,
			Action:       action,
			Body:         t.Comment.Body,
			HTMLURL:      t.Comment.HTMLURL,
			Number:       t.PullRequest.Number,
			Repo:         t.Repo,
			User:         t.Comment.User,
			IssueAuthor:  t.PullRequest.User,
			Assignees:    t.PullRequest.Assignees,
			IssueState:   t.PullRequest.State,
			IssueTitle:   t.PullRequest.Title,
			IssueBody:    t.PullRequest.Body,
			IssueHTMLURL: t.PullRequest.HTMLURL,
		}, nil
	}

	return nil, fmt.Errorf("we were unable to generalize comment, unknown type encountered")
}
