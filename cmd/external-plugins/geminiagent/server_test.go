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

package main

import (
	"errors"
	"testing"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/cmd/external-plugins/geminiagent/plugin"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

type fakeGitHubClient struct{}

func (f *fakeGitHubClient) CreateComment(_, _ string, _ int, _ string) error {
	return errors.New("unexpected CreateComment call")
}

func (f *fakeGitHubClient) GetIssue(_, _ string, _ int) (*github.Issue, error) {
	return nil, errors.New("unexpected GetIssue call")
}

func (f *fakeGitHubClient) GetPullRequest(_, _ string, _ int) (*github.PullRequest, error) {
	return nil, errors.New("unexpected GetPullRequest call")
}

func (f *fakeGitHubClient) GetPullRequestChanges(_, _ string, _ int) ([]github.PullRequestChange, error) {
	return nil, errors.New("unexpected GetPullRequestChanges call")
}

func (f *fakeGitHubClient) ListIssueComments(_, _ string, _ int) ([]github.IssueComment, error) {
	return nil, errors.New("unexpected ListIssueComments call")
}

func (f *fakeGitHubClient) TeamBySlugHasMember(_, _, _ string) (bool, error) {
	return false, errors.New("unexpected TeamBySlugHasMember call")
}

func (f *fakeGitHubClient) IsMember(_, _ string) (bool, error) {
	return false, errors.New("unexpected IsMember call")
}

func (f *fakeGitHubClient) IsCollaborator(_, _, _ string) (bool, error) {
	return false, errors.New("unexpected IsCollaborator call")
}

func TestHandleIssueCommentGeneralizesAndDispatches(t *testing.T) {
	pluginAgent := plugins.NewFakeConfigAgent()
	pluginAgent.Set(&plugins.Configuration{
		GeminiAgents: []plugins.GeminiAgentConfig{
			{Repos: []string{"org/repo"}, Model: "gemini-test-model"},
		},
	})

	var got github.GenericCommentEvent
	server := &Server{
		ghc:         &fakeGitHubClient{},
		log:         logrus.NewEntry(logrus.New()),
		pluginAgent: &pluginAgent,
		handleGenericComment: func(_ plugin.GitHubClient, cfg *plugins.Configuration, _ *logrus.Entry, event github.GenericCommentEvent) error {
			got = event
			if len(cfg.GeminiAgents) != 1 || cfg.GeminiAgents[0].Model != "gemini-test-model" {
				t.Fatalf("unexpected plugin config: %#v", cfg.GeminiAgents)
			}
			return nil
		},
	}

	err := server.handleIssueComment(logrus.NewEntry(logrus.New()), github.IssueCommentEvent{
		Action: github.IssueCommentActionCreated,
		GUID:   "event-guid",
		Issue: github.Issue{
			ID:          42,
			NodeID:      "issue-node",
			User:        github.User{Login: "issue-author"},
			Number:      7,
			Title:       "Investigate the thing",
			State:       "open",
			HTMLURL:     "https://github.example/org/repo/issues/7",
			Body:        "Issue body",
			PullRequest: &struct{}{},
		},
		Comment: github.IssueComment{
			ID:      99,
			Body:    "/gemini-agent explain this",
			User:    github.User{Login: "commenter"},
			HTMLURL: "https://github.example/org/repo/issues/7#issuecomment-99",
		},
		Repo: github.Repo{
			Name:  "repo",
			Owner: github.User{Login: "org"},
		},
	})
	if err != nil {
		t.Fatalf("handleIssueComment returned error: %v", err)
	}

	if got.GUID != "event-guid" {
		t.Fatalf("GUID %q, expected event-guid", got.GUID)
	}
	if !got.IsPR {
		t.Fatal("expected generalized event to be marked as PR")
	}
	if got.Action != github.GenericCommentActionCreated {
		t.Fatalf("action %q, expected %q", got.Action, github.GenericCommentActionCreated)
	}
	if got.Body != "/gemini-agent explain this" {
		t.Fatalf("body %q, expected command body", got.Body)
	}
	if got.Number != 7 || got.Repo.Owner.Login != "org" || got.Repo.Name != "repo" {
		t.Fatalf("unexpected repo coordinates: %s/%s#%d", got.Repo.Owner.Login, got.Repo.Name, got.Number)
	}
	if got.CommentID == nil || *got.CommentID != 99 {
		t.Fatalf("comment ID %v, expected 99", got.CommentID)
	}
}

func TestHandleIssueCommentRequiresHandler(t *testing.T) {
	pluginAgent := plugins.NewFakeConfigAgent()
	server := &Server{
		ghc:         &fakeGitHubClient{},
		log:         logrus.NewEntry(logrus.New()),
		pluginAgent: &pluginAgent,
	}

	err := server.handleIssueComment(logrus.NewEntry(logrus.New()), github.IssueCommentEvent{
		Action: github.IssueCommentActionCreated,
		Issue:  github.Issue{PullRequest: &struct{}{}},
	})
	if err == nil {
		t.Fatal("expected error for nil generic comment handler")
	}
}

var _ plugin.GitHubClient = (*fakeGitHubClient)(nil)
