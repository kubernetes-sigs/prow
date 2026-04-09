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

package invalidcommitmsg

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
)

type fakePruner struct{}

func (fp *fakePruner) PruneComments(shouldPrune func(github.IssueComment) bool) {}

func makeFakePullRequestEvent(action github.PullRequestEventAction, title string) github.PullRequestEvent {
	return github.PullRequestEvent{
		Action: action,
		Number: 3,
		Repo: github.Repo{
			Owner: github.User{
				Login: "k",
			},
			Name: "k",
		},
		PullRequest: github.PullRequest{
			Title: title,
		},
	}
}

func TestHandlePullRequest(t *testing.T) {
	var testcases = []struct {
		name string

		// PR settings
		action                       github.PullRequestEventAction
		commits                      []github.RepositoryCommit
		title                        string
		hasInvalidCommitMessageLabel bool
		enableFixupEnv               bool

		// expectations
		addedLabel   string
		removedLabel string
	}{
		{
			name:   "unsupported PR action -> no-op",
			action: github.PullRequestActionLabeled,
		},
		{
			name:   "contains valid message -> no-op",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "this is a valid message"}},
				{SHA: "sha2", Commit: github.GitCommit{Message: "fixing k/k#9999"}},
			},
			hasInvalidCommitMessageLabel: false,
		},
		{
			name:   "msg contains invalid keywords -> add label and comment",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "fixes k/k#9999"}},
				{SHA: "sha2", Commit: github.GitCommit{Message: "Close k/k#9999"}},
				{SHA: "sha3", Commit: github.GitCommit{Message: "resolved k/k#9999"}},
			},
			hasInvalidCommitMessageLabel: false,

			addedLabel: fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "msg does not contain invalid keywords but has label -> remove label",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha", Commit: github.GitCommit{Message: "this is a valid message"}},
			},
			hasInvalidCommitMessageLabel: true,

			removedLabel: fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "contains valid title -> no-op",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "this is a valid message"}},
			},
			title:                        "valid title",
			hasInvalidCommitMessageLabel: false,
		},
		{
			name:   "contains invalid title with fixes keyword -> add label and comment",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "this is a valid message"}},
			},
			title:                        "fixes #9999",
			hasInvalidCommitMessageLabel: false,
			addedLabel:                   fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "contains invalid title and invalid commits -> add label and 2 comments",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "fixes k/k#9999"}},
				{SHA: "sha2", Commit: github.GitCommit{Message: "Close k/k#9999"}},
				{SHA: "sha3", Commit: github.GitCommit{Message: "resolved k/k#9999"}},
			},
			title:                        "fixes #9999",
			hasInvalidCommitMessageLabel: false,
			addedLabel:                   fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "valid commits and invalid title, and has label -> keep label and add comment",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha", Commit: github.GitCommit{Message: "this is a valid message"}},
			},
			title:                        "fixes #9999",
			hasInvalidCommitMessageLabel: true,
		},
		{
			name:   "invalid commits and valid title, and has label -> keep label and add comment",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "fixes k/k#9999"}},
				{SHA: "sha2", Commit: github.GitCommit{Message: "Close k/k#9999"}},
				{SHA: "sha3", Commit: github.GitCommit{Message: "resolved k/k#9999"}},
			},
			title:                        "valid title",
			hasInvalidCommitMessageLabel: true,
		},
		{
			name:   "valid title and valid commits, and has label -> remove label",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha", Commit: github.GitCommit{Message: "this is a valid message"}},
			},
			title:                        "valid title",
			hasInvalidCommitMessageLabel: true,
			removedLabel:                 fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "fixup commit -> add label and comment",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "fixup! update tests"}},
			},
			hasInvalidCommitMessageLabel: false,
			enableFixupEnv:               true,
			addedLabel:                   fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "amend commit -> add label and comment",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "amend! update tests"}},
			},
			hasInvalidCommitMessageLabel: false,
			enableFixupEnv:               true,
			addedLabel:                   fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "squash commit -> add label and comment",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "squash! update tests"}},
			},
			hasInvalidCommitMessageLabel: false,
			enableFixupEnv:               true,
			addedLabel:                   fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "fixup commit ignored when feature flag disabled",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "fixup! update tests"}},
			},
			enableFixupEnv: false,
		},
		{
			name:   "fixup commit detected when feature flag enabled",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "fixup! update tests"}},
			},
			addedLabel:     fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
			enableFixupEnv: true,
		},
		{
			name:   "commit with fixup and close keyword",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "fixup! fixes #123"}},
			},
			enableFixupEnv: true,
			addedLabel:     fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
		{
			name:   "fixup commits removed -> remove label",
			action: github.PullRequestActionOpened,
			commits: []github.RepositoryCommit{
				{SHA: "sha1", Commit: github.GitCommit{Message: "normal commit"}},
			},
			hasInvalidCommitMessageLabel: true,
			enableFixupEnv:               true,
			removedLabel:                 fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			title := "fake title"
			if tc.title != "" {
				title = tc.title
			}

			event := makeFakePullRequestEvent(tc.action, title)
			fc := fakegithub.NewFakeClient()
			fc.PullRequests = map[int]*github.PullRequest{event.Number: &event.PullRequest}
			fc.IssueComments = make(map[int][]github.IssueComment)
			fc.CommitMap = map[string][]github.RepositoryCommit{
				"k/k#3": tc.commits,
			}

			if tc.hasInvalidCommitMessageLabel {
				fc.IssueLabelsAdded = append(fc.IssueLabelsAdded, fmt.Sprintf("k/k#3:%s", invalidCommitMsgLabel))
			}

			if tc.enableFixupEnv {
				t.Setenv("ENABLE_FIXUP_CHECK", "true")
			} else {
				t.Setenv("ENABLE_FIXUP_CHECK", "false")
			}

			if err := handle(fc, logrus.WithField("plugin", pluginName), event, &fakePruner{}); err != nil {
				t.Errorf("For case %s, didn't expect error from invalidcommitmsg plugin: %v", tc.name, err)
			}

			ok := tc.addedLabel == ""
			if !ok {
				for _, label := range fc.IssueLabelsAdded {
					if reflect.DeepEqual(tc.addedLabel, label) {
						ok = true
						break
					}
				}
			}
			if !ok {
				t.Errorf("Expected to add: %#v, Got %#v in case %s.", tc.addedLabel, fc.IssueLabelsAdded, tc.name)
			}

			ok = tc.removedLabel == ""
			if !ok {
				for _, label := range fc.IssueLabelsRemoved {
					if reflect.DeepEqual(tc.removedLabel, label) {
						ok = true
						break
					}
				}
			}
			if !ok {
				t.Errorf("Expected to remove: %#v, Got %#v in case %s.", tc.removedLabel, fc.IssueLabelsRemoved, tc.name)
			}

			comments := fc.IssueCommentsAdded

			if hasIssues(tc.commits, tc.title, tc.enableFixupEnv) {
				if len(comments) != 1 {
					t.Errorf("Expected 1 comment, got %d", len(comments))
					return
				}

				comment := comments[0]

				if !strings.Contains(comment, invalidCommitMsgCommentMarker) {
					t.Errorf("Missing marker in comment")
				}

				if containsInvalidCommits(tc.commits) {
					if !strings.Contains(comment, "Invalid commit messages") {
						t.Errorf("Missing invalid commit section")
					}
				}

				if tc.enableFixupEnv && containsFixup(tc.commits) {
					if !strings.Contains(comment, "Fixup/amend/squash commits") {
						t.Errorf("Missing fixup section")
					}
				}

				if isInvalidTitle(tc.title) {
					if !strings.Contains(comment, "Invalid PR title") {
						t.Errorf("Missing invalid title section")
					}
				}
			} else {
				if len(comments) != 0 {
					t.Errorf("Expected no comments, got %d", len(comments))
				}
			}
		})
	}
}

func containsInvalidCommits(commits []github.RepositoryCommit) bool {
	for _, c := range commits {
		if CloseIssueRegex.MatchString(c.Commit.Message) {
			return true
		}
	}
	return false
}

func containsFixup(commits []github.RepositoryCommit) bool {
	for _, c := range commits {
		if fixupCommitRegex.MatchString(strings.Split(c.Commit.Message, "\n")[0]) {
			return true
		}
	}
	return false
}

func isInvalidTitle(title string) bool {
	return CloseIssueRegex.MatchString(title)
}

func hasIssues(commits []github.RepositoryCommit, title string, fixupEnabled bool) bool {
	return containsInvalidCommits(commits) ||
		isInvalidTitle(title) ||
		(fixupEnabled && containsFixup(commits))
}
