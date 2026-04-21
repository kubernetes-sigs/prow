/*
Copyright The Kubernetes Authors.

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

package issuemanagement

import (
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
)

func TestHandleIssues(t *testing.T) {
	log := logrus.WithField("test", "handleIssues")

	tests := []struct {
		name            string
		commentBody     string
		fc              func(*fakegithub.FakeClient)
		froc            func() *fakeRepoownersClient
		event           github.GenericCommentEvent
		expectComment   bool
		commentContains string
	}{
		{
			name:        "Routes to link-issue handler when /link-issue command is present",
			commentBody: "/link-issue 123",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
				fc.Issues = map[int]*github.Issue{
					123: {Number: 123},
				}
				fc.OrgMembers = map[string][]string{
					"org": {"user"},
				}
				fc.PullRequests = map[int]*github.PullRequest{
					1: {Number: 1, Body: ""},
				}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"approver1"})
			},
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/link-issue 123",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
		},
		{
			name:        "Routes to unlink-issue handler when /unlink-issue command is present",
			commentBody: "/unlink-issue 456",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
				fc.Issues = map[int]*github.Issue{
					456: {Number: 456},
				}
				fc.OrgMembers = map[string][]string{
					"org": {"user"},
				}
				fc.PullRequests = map[int]*github.PullRequest{
					1: {Number: 1, Body: "Fixes #456"},
				}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"approver1"})
			},
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/unlink-issue 456",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
		},
		{
			name:        "Routes to pin-issue handler when /pin-issue command is present",
			commentBody: "/pin-issue",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
				fc.Issues = map[int]*github.Issue{
					1: {NodeID: "node1"},
				}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"user"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "/pin-issue",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
		},
		{
			name:        "Routes to unpin-issue handler when /unpin-issue command is present",
			commentBody: "/unpin-issue",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
				fc.Issues = map[int]*github.Issue{
					1: {NodeID: "node1"},
				}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"user"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "/unpin-issue",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
		},
		{
			name:        "Returns nil when no matching command is found",
			commentBody: "Just a regular comment",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"approver1"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "Just a regular comment",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
		},
		{
			name:        "Handles case insensitive commands",
			commentBody: "/PIN-ISSUE",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
				fc.Issues = map[int]*github.Issue{
					1: {NodeID: "node1"},
				}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"user"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "/PIN-ISSUE",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
		},
		{
			name:        "Routes to transfer-issue handler when /transfer-issue command is present",
			commentBody: "/transfer-issue test-repo",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
				fc.OrgMembers = map[string][]string{
					"org": {"user"},
				}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"approver1"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "/transfer-issue test-repo",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
				NodeID: "issueNodeID",
			},
		},
		{
			name:        "Routes to transfer handler when /transfer command is present",
			commentBody: "/transfer another-repo",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
				fc.OrgMembers = map[string][]string{
					"org": {"user"},
				}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"approver1"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "/transfer another-repo",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
				NodeID: "issueNodeID",
			},
		},
		{
			name:        "Returns with comment when transfer command has multiple destinations",
			commentBody: "/transfer-issue repo1\n/transfer repo2",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"approver1"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "/transfer-issue repo1\n/transfer repo2",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
			expectComment:   true,
			commentContains: "must only be used once",
		},
		{
			name:        "Returns with comment when transfer command has no destination",
			commentBody: "/transfer",
			fc: func(fc *fakegithub.FakeClient) {
				fc.IssueComments = map[int][]github.IssueComment{}
			},
			froc: func() *fakeRepoownersClient {
				return newFakeRepoownersClient([]string{"approver1"})
			},
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
				Body:   "/transfer",
				Number: 1,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				User:   github.User{Login: "user"},
			},
			expectComment:   true,
			commentContains: "single destination repo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fc := fakegithub.NewFakeClient()
			if tc.fc != nil {
				tc.fc(fc)
			}

			gc := &pinFakeClient{FakeClient: fc}
			oc := tc.froc()

			err := handleIssues(gc, oc, log, tc.event)

			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tc.expectComment {
				comments := fc.IssueCommentsAdded
				if len(comments) == 0 {
					t.Errorf("Expected comment but none were created")
				} else if !strings.Contains(comments[0], tc.commentContains) {
					t.Errorf("Expected comment to contain %q but got: %q", tc.commentContains, comments[0])
				}
			}
		})
	}
}
