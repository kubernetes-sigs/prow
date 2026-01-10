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

package issuemanagement

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
)

func Test_handleLinkIssue(t *testing.T) {
	tests := []struct {
		name            string
		event           github.GenericCommentEvent
		toLinkIssues    []string
		toUnlinkIssues  []string
		expectError     bool
		errorMessage    string
		expectedComment string
		fc              func(fc *fakegithub.FakeClient)
	}{
		{
			name: "should return if the plugin is triggered on an issue",
			event: github.GenericCommentEvent{
				IsPR:   false,
				Action: github.GenericCommentActionCreated,
			},
			expectedComment: "This command can only be used on pull requests.",
		},
		{
			name: "should return with comment when action is not comment created on a PR",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionEdited,
			},
			expectedComment: "This command can only be used on pull requests.",
		},
		{
			name: "should deny request if user who is not a part of the org",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/link-issue 1",
				User:   github.User{Login: "user"},
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo"},
			},
			expectedComment: "You must be an org member",
			fc: func(fc *fakegithub.FakeClient) {
				fc.OrgMembers["kubernetes"] = []string{}
			},
		},
		{
			name: "comment without issue numbers has no action",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/link-issue",
				User:   github.User{Login: "user"},
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo"},
			},
			fc: func(fc *fakegithub.FakeClient) {
				fc.OrgMembers["kubernetes"] = []string{"user"}
			},
		},
		{
			name: "should error when linking an issue from a different repo which does not exist",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/link-issue other/repo#12",
				User:   github.User{Login: "user"},
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo1"},
			},
			toLinkIssues:    []string{"other/repo#12"},
			expectedComment: "Failed to get repository",
			fc: func(fc *fakegithub.FakeClient) {
				fc.GetRepoError = errors.New("error")
				fc.OrgMembers["kubernetes"] = []string{"user"}
			},
		},
		{
			name: "should fail if issue does not exist",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/link-issue 99",
				User:   github.User{Login: "user"},
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo"},
			},
			toLinkIssues:    []string{"99"},
			expectedComment: "Failed to get issue",
			fc: func(fc *fakegithub.FakeClient) {
				fc.OrgMembers["kubernetes"] = []string{"user"}
			},
		},
		{
			name: "link issue should update the PR body successfully",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/link-issue 10",
				User:   github.User{Login: "user"},
				Number: 9,
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo"},
			},
			toLinkIssues: []string{"10"},
			fc: func(fc *fakegithub.FakeClient) {
				fc.OrgMembers["kubernetes"] = []string{"user"}
				fc.Issues[10] = &github.Issue{Number: 10}
				fc.PullRequests[9] = &github.PullRequest{Number: 9, Body: "Initial body"}
			},
		},
		{
			name: "unlink issue should update the PR body successfully",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/unlink-issue 11",
				Number: 10,
				User:   github.User{Login: "user"},
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo"},
			},
			toUnlinkIssues: []string{"11"},
			fc: func(fc *fakegithub.FakeClient) {
				fc.OrgMembers["kubernetes"] = []string{"user"}
				fc.Issues[11] = &github.Issue{Number: 11}
				fc.PullRequests[10] = &github.PullRequest{
					Number: 10,
					Body:   "Fixes #11",
				}
			},
		},
		{
			name: "both link and unlink issue should update the PR body successfully",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/unlink-issue 11\n/link-issue 12",
				Number: 10,
				User:   github.User{Login: "user"},
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo"},
			},
			toUnlinkIssues: []string{"11"},
			fc: func(fc *fakegithub.FakeClient) {
				fc.OrgMembers["kubernetes"] = []string{"user"}
				fc.Issues[11] = &github.Issue{Number: 11}
				fc.Issues[12] = &github.Issue{Number: 12}
				fc.PullRequests[10] = &github.PullRequest{
					Number: 10,
					Body:   "Fixes #11",
				}
			},
		},
		{
			name: "should not update the PR body when provided issue is already linked",
			event: github.GenericCommentEvent{
				IsPR:   true,
				Action: github.GenericCommentActionCreated,
				Body:   "/link-issue 12",
				Number: 11,
				User:   github.User{Login: "user"},
				Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "repo"},
			},
			toLinkIssues: []string{"12"},
			fc: func(fc *fakegithub.FakeClient) {
				fc.OrgMembers["kubernetes"] = []string{"user"}
				fc.Issues[12] = &github.Issue{Number: 12}
				fc.PullRequests[11] = &github.PullRequest{
					Number: 11,
					Body:   "Fixes #12",
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fc := fakegithub.NewFakeClient()

			if tc.fc != nil {
				tc.fc(fc)
			}

			log := logrus.WithField("plugin", pluginName)
			err := handleLinkIssue(fc, log, tc.event, tc.toLinkIssues, tc.toUnlinkIssues)

			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				if tc.errorMessage != "" && !strings.Contains(err.Error(), tc.errorMessage) {
					t.Fatalf("expected error to contain %q, got: %v", tc.errorMessage, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.expectedComment != "" {
				cmts := fc.IssueComments[tc.event.Number]
				if len(cmts) == 0 {
					t.Fatalf("expected a comment containing %q but none posted", tc.expectedComment)
				}
				if !strings.Contains(cmts[0].Body, tc.expectedComment) {
					t.Fatalf("expected comment %q but got %q", tc.expectedComment, cmts[0].Body)
				}
			}
		})
	}
}

func TestParseIssueRef(t *testing.T) {
	tests := []struct {
		name          string
		issue         string
		defOrg        string
		defRepo       string
		expectedOrg   string
		expectedRepo  string
		expectedIssue int
		expectedError bool
	}{
		{
			name:          "User provided an issue number",
			issue:         "42",
			defOrg:        "kubernetes",
			defRepo:       "test-infra",
			expectedOrg:   "kubernetes",
			expectedRepo:  "test-infra",
			expectedIssue: 42,
		},
		{
			name:          "User provided an issue in format org/repo#num",
			issue:         "foo/bar#77",
			defOrg:        "org",
			defRepo:       "repo",
			expectedOrg:   "foo",
			expectedRepo:  "bar",
			expectedIssue: 77,
		},
		{
			name:          "User provided an invalid issue number",
			issue:         "x42",
			defOrg:        "org",
			defRepo:       "repo",
			expectedError: true,
		},
		{
			name:          "Invalid issue format with slash but missing #",
			issue:         "foo/bar",
			expectedError: true,
		},
		{
			name:          "Invalid org repo format",
			issue:         "foo/bar/baz#1",
			expectedError: true,
		},
		{
			name:          "Invalid issue number after #",
			issue:         "foo/bar#x",
			expectedError: true,
		},
		{
			name:          "Invalid issue reference",
			issue:         "abc",
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := parseIssueRef(tc.issue, tc.defOrg, tc.defRepo)
			if tc.expectedError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if ref.Org != tc.expectedOrg || ref.Repo != tc.expectedRepo || ref.Num != tc.expectedIssue {
				t.Fatalf("got %+v, want org=%s repo=%s issue=%d", ref, tc.expectedOrg, tc.expectedRepo, tc.expectedIssue)
			}
		})
	}
}

func TestFormatIssueRef(t *testing.T) {
	tests := []struct {
		name             string
		ref              IssueRef
		defOrg           string
		defRepo          string
		expectedIssueRef string
	}{
		{
			name:             "Issue within the same repo",
			ref:              IssueRef{Org: "kubernetes", Repo: "test-infra", Num: 12},
			defOrg:           "kubernetes",
			defRepo:          "test-infra",
			expectedIssueRef: "#12",
		},
		{
			name:             "Issue in a different repo",
			ref:              IssueRef{Org: "foo", Repo: "bar", Num: 33},
			defOrg:           "kubernetes",
			defRepo:          "test-infra",
			expectedIssueRef: "foo/bar#33",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issue := formatIssueRef(&tc.ref, tc.defOrg, tc.defRepo)
			if issue != tc.expectedIssueRef {
				t.Fatalf("got %s, want %s", issue, tc.expectedIssueRef)
			}
		})
	}
}

func TestUpdateFixesLine(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		toLink       []string
		toUnlink     []string
		expectedBody string
	}{
		{
			name:         "should add fixes line when it doesn't exist",
			body:         "This is a PR body.",
			toLink:       []string{"#12"},
			expectedBody: "This is a PR body.\n\nFixes #12",
		},
		{
			name:         "should unlink an issue but keep fixes line",
			body:         "Fixes #1 #2",
			toUnlink:     []string{"#2"},
			expectedBody: "Fixes #1",
		},
		{
			name:         "should remove last issue and delete fixes line",
			body:         "line1\nFixes #99\nline2",
			toUnlink:     []string{"#99"},
			expectedBody: "line1\nline2",
		},
		{
			name:         "should do not add duplicate issue if it is already present in the PR body",
			body:         "Fixes #7",
			toLink:       []string{"#7"},
			expectedBody: "Fixes #7",
		},
		{
			name:         "should ensure the fixes line is added at end of the body",
			body:         "line1\nline2",
			toLink:       []string{"foo/bar#10"},
			expectedBody: "line1\nline2\n\nFixes foo/bar#10",
		},
		{
			name:         "should append issue to existing fixes line",
			body:         "line1\nFixes #1\nline3",
			toLink:       []string{"#2", "foo/bar#10"},
			expectedBody: "line1\nFixes #1 #2 foo/bar#10\nline3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			returnedBody := updateFixesLine(tc.body, tc.toLink, tc.toUnlink)
			if returnedBody != tc.expectedBody {
				t.Fatalf("got:\n%s\nwant:\n%s", returnedBody, tc.expectedBody)
			}
		})
	}
}

func TestParseCommentForLinkCommands(t *testing.T) {
	tests := []struct {
		name         string
		commentBody  string
		wantToLink   []string
		wantToUnlink []string
	}{
		{
			name:         "Single link-issue comment",
			commentBody:  "/link-issue 12",
			wantToLink:   []string{"12"},
			wantToUnlink: nil,
		},
		{
			name:         "Single unlink-issue comment",
			commentBody:  "/unlink-issue 10",
			wantToLink:   nil,
			wantToUnlink: []string{"10"},
		},
		{
			name:         "Link multiple issues",
			commentBody:  "/link-issue 101 102 103",
			wantToLink:   []string{"101", "102", "103"},
			wantToUnlink: nil,
		},
		{
			name:         "Multiple linking commands on different lines",
			commentBody:  "/link-issue 1\nRandom text in the body\n/unlink-issue 2\n/link-issue 3 4",
			wantToLink:   []string{"1", "3", "4"},
			wantToUnlink: []string{"2"},
		},
		{
			name:         "Case insensitivity check",
			commentBody:  "/LINK-ISSUE 7\n/Unlink-Issue 1",
			wantToLink:   []string{"7"},
			wantToUnlink: []string{"1"},
		},
		{
			name:         "Ignore comments with  don't start with linking commands ",
			commentBody:  "I want to /link-issue 55 but this should fail",
			wantToLink:   nil,
			wantToUnlink: nil,
		},
		{
			name:         "Handles org/repo#number format",
			commentBody:  "/link-issue kubernetes/kubernetes#12",
			wantToLink:   []string{"kubernetes/kubernetes#12"},
			wantToUnlink: nil,
		},
		{
			name:         "Comment with no linking commands",
			commentBody:  "Just a regular comment with no linking commands.",
			wantToLink:   nil,
			wantToUnlink: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotToLink, gotToUnlink, err := parseCommentForLinkCommands(tc.commentBody)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(gotToLink, tc.wantToLink) {
				t.Fatalf("gotToLink = %v, want %v", gotToLink, tc.wantToLink)
			}
			if !reflect.DeepEqual(gotToUnlink, tc.wantToUnlink) {
				t.Fatalf("gotToUnlink = %v, want %v", gotToUnlink, tc.wantToUnlink)
			}
		})
	}
}
