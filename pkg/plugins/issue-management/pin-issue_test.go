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
	"context"
	"errors"
	"strings"
	"testing"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
	"sigs.k8s.io/prow/pkg/layeredsets"
	"sigs.k8s.io/prow/pkg/plugins/ownersconfig"
	"sigs.k8s.io/prow/pkg/repoowners"
)

// pinFakeClient wraps fakegithub.FakeClient and adds MutateWithGitHubAppsSupport,
// which is required by the githubClient interface for pin/unpin operations.
type pinFakeClient struct {
	*fakegithub.FakeClient
	errMutate error
}

func (c *pinFakeClient) MutateWithGitHubAppsSupport(_ context.Context, _ any, _ githubql.Input, _ map[string]any, _ string) error {
	return c.errMutate
}

// fakeOwnersClient implements repoowners.RepoOwner.
type fakeOwnersClient struct {
	topLevelApprovers sets.Set[string]
}

func (foc *fakeOwnersClient) FindApproverOwnersForFile(path string) string    { return "" }
func (foc *fakeOwnersClient) FindReviewersOwnersForFile(path string) string   { return "" }
func (foc *fakeOwnersClient) FindLabelsForFile(path string) sets.Set[string]  { return nil }
func (foc *fakeOwnersClient) IsNoParentOwners(path string) bool               { return false }
func (foc *fakeOwnersClient) IsAutoApproveUnownedSubfolders(path string) bool { return false }
func (foc *fakeOwnersClient) LeafApprovers(path string) sets.Set[string]      { return nil }
func (foc *fakeOwnersClient) Approvers(path string) layeredsets.String        { return layeredsets.String{} }
func (foc *fakeOwnersClient) LeafReviewers(path string) sets.Set[string]      { return nil }
func (foc *fakeOwnersClient) Reviewers(path string) layeredsets.String        { return layeredsets.String{} }
func (foc *fakeOwnersClient) RequiredReviewers(path string) sets.Set[string]  { return nil }
func (foc *fakeOwnersClient) ParseSimpleConfig(path string) (repoowners.SimpleConfig, error) {
	return repoowners.SimpleConfig{}, nil
}
func (foc *fakeOwnersClient) ParseFullConfig(path string) (repoowners.FullConfig, error) {
	return repoowners.FullConfig{}, nil
}
func (foc *fakeOwnersClient) TopLevelApprovers() sets.Set[string] { return foc.topLevelApprovers }
func (foc *fakeOwnersClient) Filenames() ownersconfig.Filenames   { return ownersconfig.FakeFilenames }
func (foc *fakeOwnersClient) AllOwners() sets.Set[string]         { return nil }
func (foc *fakeOwnersClient) AllApprovers() sets.Set[string]      { return nil }
func (foc *fakeOwnersClient) AllReviewers() sets.Set[string]      { return nil }

// fakeRepoownersClient implements ownersClient.
// It wraps fakeOwnersClient and returns it from LoadRepoOwners.
type fakeRepoownersClient struct {
	foc *fakeOwnersClient
	err error
}

func (froc *fakeRepoownersClient) LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error) {
	if froc.err != nil {
		return nil, froc.err
	}
	return froc.foc, nil
}

// newPinFakeClient creates a pinFakeClient pre-populated with test issues.
func newPinFakeClient(errMutate error) *pinFakeClient {
	fc := fakegithub.NewFakeClient()
	fc.Issues = map[int]*github.Issue{
		101: {Number: 101, NodeID: "node123"},
		42:  {Number: 42, NodeID: "node42"},
	}
	return &pinFakeClient{FakeClient: fc, errMutate: errMutate}
}

// newFakeRepoownersClient builds a fakeRepoownersClient from a list of approver logins.
func newFakeRepoownersClient(approvers []string) *fakeRepoownersClient {
	approverSet := sets.New[string]()
	for _, a := range approvers {
		approverSet.Insert(github.NormLogin(a))
	}
	return &fakeRepoownersClient{foc: &fakeOwnersClient{topLevelApprovers: approverSet}}
}

func TestIsRepoOwner(t *testing.T) {
	testCases := []struct {
		name          string
		user          string
		approvers     []string
		loadOwnersErr error
		wantMessage   string
		wantErr       string
	}{
		{
			name:      "authorized user is allowed",
			user:      "alice",
			approvers: []string{"alice", "bob"},
		},
		{
			name:        "unauthorized user is rejected",
			user:        "eve",
			approvers:   []string{"alice"},
			wantMessage: "You are not authorized",
			wantErr:     "not a top-level approver",
		},
		{
			name:          "LoadRepoOwners returns error",
			user:          "alice",
			loadOwnersErr: errors.New("file not found"),
			wantMessage:   "Unable to determine whether",
			wantErr:       "file not found",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			froc := newFakeRepoownersClient(tc.approvers)
			froc.err = tc.loadOwnersErr

			message, err := isRepoOwner(froc, "org", "repo", tc.user, "main")

			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
				if message != "" {
					t.Errorf("expected no message, got: %q", message)
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
				}
				if message == "" {
					t.Error("expected user message, got empty string")
				} else if !strings.Contains(message, tc.wantMessage) {
					t.Errorf("expected message containing %q, got: %q", tc.wantMessage, message)
				}
			}
		})
	}
}
func TestHandlePinOrUnpinIssue(t *testing.T) {
	log := logrus.WithField("plugin", pluginName)

	validEvent := github.GenericCommentEvent{
		Action: github.GenericCommentActionCreated,
		Repo:   github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "test-repo", DefaultBranch: "main"},
		Number: 101,
		User:   github.User{Login: "approver-user"},
		IsPR:   false,
	}

	testCases := []struct {
		name          string
		pin           bool
		event         github.GenericCommentEvent
		approvers     []string
		mutateErr     error
		getIssueErr   error
		wantErr       bool
		expectComment string
	}{
		{
			name:          "successfully pins issue to repository",
			pin:           true,
			event:         validEvent,
			approvers:     []string{"approver-user"},
			expectComment: "Issue #101 has been pinned to the repository.",
		},
		{
			name:          "successfully unpins issue from repository",
			pin:           false,
			event:         validEvent,
			approvers:     []string{"approver-user"},
			expectComment: "Issue #101 has been unpinned from the repository.",
		},
		{
			name:          "skip pull request posts error comment",
			pin:           true,
			event:         github.GenericCommentEvent{IsPR: true, Action: github.GenericCommentActionCreated, Repo: github.Repo{Owner: github.User{Login: "kubernetes"}, Name: "test-repo"}, Number: 101},
			expectComment: "can only be triggered on GitHub issues",
		},
		{
			name:  "skip non-created action returns nil without comment",
			pin:   true,
			event: github.GenericCommentEvent{IsPR: false, Action: github.GenericCommentActionEdited},
		},
		{
			name:          "unauthorized user posts error comment",
			pin:           true,
			event:         validEvent,
			approvers:     []string{"other-user"},
			expectComment: "You are not authorized",
		},
		{
			name:        "GetIssue returns error",
			pin:         true,
			event:       validEvent,
			approvers:   []string{"approver-user"},
			getIssueErr: errors.New("issue not found"),
			wantErr:     true,
		},
		{
			name:          "pin mutation failure posts error comment",
			pin:           true,
			event:         validEvent,
			approvers:     []string{"approver-user"},
			mutateErr:     errors.New("pin error"),
			expectComment: "Unable to pin issue #101. Please try again later.",
		},
		{
			name:          "unpin mutation failure posts error comment",
			pin:           false,
			event:         validEvent,
			approvers:     []string{"approver-user"},
			mutateErr:     errors.New("unpin error"),
			expectComment: "Unable to unpin issue #101. Please try again later.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fc := newPinFakeClient(tc.mutateErr)
			if tc.getIssueErr != nil {
				// Remove the issue from the map so GetIssue returns an error.
				delete(fc.Issues, 101)
			}
			froc := newFakeRepoownersClient(tc.approvers)

			err := handlePinOrUnpinIssue(fc, froc, log, tc.event, tc.pin)

			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			comments := fc.IssueComments[tc.event.Number]
			if tc.expectComment != "" {
				if len(comments) == 0 {
					t.Errorf("expected comment containing %q, but no comment was posted", tc.expectComment)
				} else if !strings.Contains(comments[0].Body, tc.expectComment) {
					t.Errorf("expected comment to contain %q, got: %q", tc.expectComment, comments[0].Body)
				}
			} else {
				if len(comments) != 0 {
					t.Errorf("expected no comment, got: %q", comments[0].Body)
				}
			}
		})
	}
}
