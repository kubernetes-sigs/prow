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

package fixcommitmsg

import (
	"errors"
	"testing"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

// ---- pure-function tests ------------------------------------------------

func TestFixMessage(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "close keyword simple",
			input: "my commit\n\ncloses #123",
			want:  "my commit\n\nRelated: 123",
		},
		{
			name:  "fixes with cross-repo ref",
			input: "something\n\nfixes kubernetes/kubernetes#456",
			want:  "something\n\nRelated: kubernetes/kubernetes 456",
		},
		{
			name:  "resolves keyword",
			input: "resolves #99",
			want:  "Related: 99",
		},
		{
			name:  "keyword with colon separator",
			input: "Closes: #10",
			want:  "Related: 10",
		},
		{
			name:  "fixup prefix stripped from subject",
			input: "fixup! my commit message",
			want:  "my commit message",
		},
		{
			name:  "amend prefix stripped",
			input: "amend! my commit message\n\nsome body",
			want:  "my commit message\n\nsome body",
		},
		{
			name:  "squash prefix stripped",
			input: "squash! my commit message",
			want:  "my commit message",
		},
		{
			name:  "fixup prefix and close keyword in body",
			input: "fixup! tweak auth\n\nfixes #77",
			want:  "tweak auth\n\nRelated: 77",
		},
		{
			name:  "clean message unchanged",
			input: "my normal commit message",
			want:  "my normal commit message",
		},
		{
			name:  "body only keyword",
			input: "subject line\n\nsome text\ncloses #5\nmore text",
			want:  "subject line\n\nsome text\nRelated: 5\nmore text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fixMessage(tc.input)
			if got != tc.want {
				t.Errorf("fixMessage(%q)\n got  %q\n want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestComputeRewrites(t *testing.T) {
	commits := []github.RepositoryCommit{
		{SHA: "aaa", Commit: github.GitCommit{Message: "good message"}},
		{SHA: "bbb", Commit: github.GitCommit{Message: "closes #1"}},
		{SHA: "ccc", Commit: github.GitCommit{Message: "fixup! something"}},
		{SHA: "ddd", Commit: github.GitCommit{Message: "another good one"}},
	}

	got := computeRewrites(commits)

	if len(got) != 2 {
		t.Fatalf("expected 2 rewrites, got %d", len(got))
	}
	if got["bbb"] != "Related: 1" {
		t.Errorf("bbb: got %q, want %q", got["bbb"], "Related: 1")
	}
	if got["ccc"] != "something" {
		t.Errorf("ccc: got %q, want %q", got["ccc"], "something")
	}
	if _, ok := got["aaa"]; ok {
		t.Error("aaa should not be in rewrites")
	}
}

// ---- handle() unit tests (mocked GitHub client, no git ops) -------------

type fakeGHClient struct {
	comments        []string
	labelRemoved    bool
	pr              *github.PullRequest
	commits         []github.RepositoryCommit
	teams           []github.Team
	teamMembers     map[string][]github.TeamMember
	createCommentFn func(org, repo string, number int, comment string) error
}

func (f *fakeGHClient) CreateComment(org, repo string, number int, comment string) error {
	if f.createCommentFn != nil {
		return f.createCommentFn(org, repo, number, comment)
	}
	f.comments = append(f.comments, comment)
	return nil
}
func (f *fakeGHClient) GetPullRequest(_, _ string, _ int) (*github.PullRequest, error) {
	if f.pr == nil {
		return nil, errors.New("no PR configured")
	}
	return f.pr, nil
}
func (f *fakeGHClient) ListPullRequestCommits(_, _ string, _ int) ([]github.RepositoryCommit, error) {
	return f.commits, nil
}
func (f *fakeGHClient) ListTeams(_ string) ([]github.Team, error) {
	return f.teams, nil
}
func (f *fakeGHClient) ListTeamMembersBySlug(_, slug, _ string) ([]github.TeamMember, error) {
	return f.teamMembers[slug], nil
}
func (f *fakeGHClient) RemoveLabel(_, _ string, _ int, _ string) error {
	f.labelRemoved = true
	return nil
}

func newOpenPREvent(user, body string) github.GenericCommentEvent {
	return github.GenericCommentEvent{
		Action:     github.GenericCommentActionCreated,
		IsPR:       true,
		IssueState: "open",
		Body:       body,
		HTMLURL:    "https://github.com/org/repo/pull/1#issuecomment-1",
		Number:     1,
		User:       github.User{Login: user},
		Repo:       github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
	}
}

func TestHandleIgnoresNonCommand(t *testing.T) {
	gc := &fakeGHClient{}
	e := newOpenPREvent("user", "just a regular comment")
	if err := handle(gc, nil, nil, plugins.FixCommitMsg{}, e); err != nil {
		t.Fatal(err)
	}
	if len(gc.comments) != 0 {
		t.Errorf("expected no comments, got %d", len(gc.comments))
	}
}

func TestHandleIgnoresClosedPR(t *testing.T) {
	gc := &fakeGHClient{}
	e := newOpenPREvent("user", "/fix-commit-messages")
	e.IssueState = "closed"
	if err := handle(gc, nil, nil, plugins.FixCommitMsg{}, e); err != nil {
		t.Fatal(err)
	}
	if len(gc.comments) != 0 {
		t.Errorf("expected no comments for closed PR, got %d", len(gc.comments))
	}
}

func TestHandleUnauthorizedUser(t *testing.T) {
	gc := &fakeGHClient{
		teams:       []github.Team{{Name: "maintainers", Slug: "maintainers"}},
		teamMembers: map[string][]github.TeamMember{"maintainers": {{Login: "alice"}}},
	}
	e := newOpenPREvent("bob", "/fix-commit-messages")
	if err := handle(gc, nil, nil, plugins.FixCommitMsg{MaintainerTeam: "maintainers"}, e); err != nil {
		t.Fatal(err)
	}
	if len(gc.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(gc.comments))
	}
	if !containsStr(gc.comments[0], "maintainers") {
		t.Errorf("expected comment to mention team name, got: %s", gc.comments[0])
	}
}

func TestHandleNothingToFix(t *testing.T) {
	gc := &fakeGHClient{
		teams:       []github.Team{{Name: "maintainers", Slug: "maintainers"}},
		teamMembers: map[string][]github.TeamMember{"maintainers": {{Login: "alice"}}},
		commits: []github.RepositoryCommit{
			{SHA: "aaa", Commit: github.GitCommit{Message: "good commit"}},
		},
	}
	e := newOpenPREvent("alice", "/fix-commit-messages")
	if err := handle(gc, nil, nil, plugins.FixCommitMsg{MaintainerTeam: "maintainers"}, e); err != nil {
		t.Fatal(err)
	}
	if len(gc.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(gc.comments))
	}
	if !containsStr(gc.comments[0], "no commit messages need fixing") {
		t.Errorf("unexpected comment: %s", gc.comments[0])
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
