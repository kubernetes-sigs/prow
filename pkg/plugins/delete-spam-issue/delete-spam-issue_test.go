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

package deletespamissue

import (
	"context"
	"errors"
	"strings"
	"testing"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
	"sigs.k8s.io/prow/pkg/plugins"
)

// fakeClient wraps fakegithub.FakeClient and adds MutateWithGitHubAppsSupport,
// which is required by the githubClient interface for the delete mutation.
// It also allows injecting a TeamBySlugHasMember error.
type fakeClient struct {
	*fakegithub.FakeClient
	mutateInput   githubql.Input
	mutateErr     error
	teamMemberErr error
}

func (c *fakeClient) MutateWithGitHubAppsSupport(_ context.Context, _ any, input githubql.Input, _ map[string]any, _ string) error {
	c.mutateInput = input
	return c.mutateErr
}

func (c *fakeClient) TeamBySlugHasMember(org string, teamSlug string, memberLogin string) (bool, error) {
	if c.teamMemberErr != nil {
		return false, c.teamMemberErr
	}
	return c.FakeClient.TeamBySlugHasMember(org, teamSlug, memberLogin)
}

func TestDeleteSpamIssue(t *testing.T) {
	validEvent := github.GenericCommentEvent{
		Action: github.GenericCommentActionCreated,
		Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "test-repo"},
		Number: 101,
		NodeID: "fakeIssueNodeID",
		User:   github.User{Login: "allowed-user"},
		Body:   "/delete-spam-issue",
		IsPR:   false,
	}

	testCases := []struct {
		name           string
		event          github.GenericCommentEvent
		allowedUsers   []string
		allowedTeams   []string
		teamMembers    []string
		mutateErr      error
		teamMemberErr  error
		wantErr        bool
		expectComments []string
		expectDeleted  bool
	}{
		{
			name:           "allowed user deletes issue",
			event:          validEvent,
			allowedUsers:   []string{"allowed-user"},
			expectComments: []string{"This issue is being deleted because it has been deemed spam"},
			expectDeleted:  true,
		},
		{
			name:           "allowed user match is case-insensitive",
			event:          validEvent,
			allowedUsers:   []string{"Allowed-User"},
			expectComments: []string{"This issue is being deleted because it has been deemed spam"},
			expectDeleted:  true,
		},
		{
			name:           "allowed team member deletes issue",
			event:          validEvent,
			allowedTeams:   []string{"spam-fighters"},
			teamMembers:    []string{"allowed-user"},
			expectComments: []string{"This issue is being deleted because it has been deemed spam"},
			expectDeleted:  true,
		},
		{
			name:           "user not in allowed team posts error comment with teams",
			event:          validEvent,
			allowedTeams:   []string{"spam-fighters"},
			teamMembers:    []string{"other-user"},
			expectComments: []string{"not in one of the allowed teams and are not an allowed user. Must be a member of one of these teams: spam-fighters"},
		},
		{
			name:           "unauthorized user posts error comment",
			event:          validEvent,
			allowedUsers:   []string{"other-user"},
			expectComments: []string{"not in one of the allowed teams and are not an allowed user"},
		},
		{
			name:           "no allowed users or teams configured rejects everyone",
			event:          validEvent,
			expectComments: []string{"not in one of the allowed teams and are not an allowed user"},
		},
		{
			name: "command on pull request posts error comment",
			event: func() github.GenericCommentEvent {
				e := validEvent
				e.IsPR = true
				return e
			}(),
			allowedUsers:   []string{"allowed-user"},
			expectComments: []string{"can only be used on GitHub issues"},
		},
		{
			name: "non-created action is ignored",
			event: func() github.GenericCommentEvent {
				e := validEvent
				e.Action = github.GenericCommentActionEdited
				return e
			}(),
			allowedUsers: []string{"allowed-user"},
		},
		{
			name: "comment without command is ignored",
			event: func() github.GenericCommentEvent {
				e := validEvent
				e.Body = "just a regular comment"
				return e
			}(),
			allowedUsers: []string{"allowed-user"},
		},
		{
			name:          "team membership check failure returns error without deleting",
			event:         validEvent,
			allowedTeams:  []string{"spam-fighters"},
			teamMemberErr: errors.New("team API error"),
			wantErr:       true,
		},
		{
			name:         "delete mutation failure posts error comment",
			event:        validEvent,
			allowedUsers: []string{"allowed-user"},
			mutateErr:    errors.New("delete error"),
			expectComments: []string{
				"This issue is being deleted because it has been deemed spam",
				"Unable to delete issue #101. Please try again later.",
			},
		},
	}

	log := logrus.WithField("plugin", pluginName)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fc := fakegithub.NewFakeClient()
			if len(tc.allowedTeams) > 0 {
				fc.Teams = map[string]map[string]fakegithub.TeamWithMembers{
					"kubernetes": {
						tc.allowedTeams[0]: {Members: sets.New(tc.teamMembers...)},
					},
				}
			}
			gc := &fakeClient{FakeClient: fc, mutateErr: tc.mutateErr, teamMemberErr: tc.teamMemberErr}
			config := plugins.DeleteSpamIssue{AllowedUsers: tc.allowedUsers, AllowedTeams: tc.allowedTeams}

			err := handleGenericComment(gc, config, log, tc.event)

			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			comments := fc.IssueComments[tc.event.Number]
			if len(comments) != len(tc.expectComments) {
				t.Fatalf("expected #comments: %d, got %d: %v", len(tc.expectComments), len(comments), comments)
			}
			for i, expected := range tc.expectComments {
				if !strings.Contains(comments[i].Body, expected) {
					t.Errorf("expected comment %d to contain %q, got: %q", i, expected, comments[i].Body)
				}
			}

			if tc.expectDeleted {
				input, ok := gc.mutateInput.(githubql.DeleteIssueInput)
				if !ok {
					t.Fatalf("wrong mutation input type: %T", gc.mutateInput)
				}
				if input.IssueID != githubql.ID(tc.event.NodeID) {
					t.Errorf("wrong issue ID: %v", input.IssueID)
				}
			} else if tc.mutateErr == nil && gc.mutateInput != nil {
				t.Errorf("expected no mutation, got input: %v", gc.mutateInput)
			}
		})
	}
}
