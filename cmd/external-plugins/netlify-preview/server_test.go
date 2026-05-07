/*
Copyright 2026 The Kubernetes Authors.

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
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	previewconfig "sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/config"
	"sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/netlify"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/plugins"
)

func TestHandleIssueCommentIgnoresNonActionableComments(t *testing.T) {
	tests := []struct {
		name string
		ice  github.IssueCommentEvent
	}{
		{
			name: "non pr comment",
			ice:  issueCommentEvent("/rebuild-preview", "open", false),
		},
		{
			name: "closed pr comment",
			ice:  issueCommentEvent("/rebuild-preview", "closed", true),
		},
		{
			name: "non command comment",
			ice:  issueCommentEvent("hello", "open", true),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGitHubClient{member: true}
			netlifyClient := &fakeNetlifyClient{}
			s := newTestServer(ghc, netlifyClient, &previewconfig.Config{})
			if err := s.handleIssueComment(logrus.NewEntry(logrus.New()), tc.ice); err != nil {
				t.Fatalf("handleIssueComment returned error: %v", err)
			}
			if len(ghc.comments) != 0 {
				t.Fatalf("expected no comments, got %v", ghc.comments)
			}
			if netlifyClient.listCalled {
				t.Fatal("expected Netlify not to be called")
			}
		})
	}
}

func TestHandleIssueCommentRejectsUntrustedComment(t *testing.T) {
	ghc := &fakeGitHubClient{}
	s := newTestServer(ghc, &fakeNetlifyClient{}, &previewconfig.Config{})

	if err := s.handleIssueComment(logrus.NewEntry(logrus.New()), issueCommentEvent("/rebuild-preview", "open", true)); err != nil {
		t.Fatalf("handleIssueComment returned error: %v", err)
	}
	if len(ghc.comments) != 1 {
		t.Fatalf("expected one comment, got %d", len(ghc.comments))
	}
	if !strings.Contains(ghc.comments[0], "Cannot retry the Netlify deploy preview") {
		t.Fatalf("unexpected comment: %s", ghc.comments[0])
	}
}

func TestHandleIssueCommentAllowsTrustedPullRequest(t *testing.T) {
	ghc := &fakeGitHubClient{labels: []github.Label{{Name: labels.OkToTest}}}
	netlifyClient := &fakeNetlifyClient{deploys: []netlify.Deploy{{
		ID:           "deploy-123",
		Context:      "deploy-preview",
		State:        "error",
		ReviewID:     5,
		DeploySSLURL: "https://deploy-preview-5.example.netlify.app",
		CreatedAt:    time.Now(),
	}}}
	cfg := &previewconfig.Config{Repos: map[string]previewconfig.Repo{"kubernetes/website": {SiteID: "site-123"}}}
	s := newTestServer(ghc, netlifyClient, cfg)

	if err := s.handleIssueComment(logrus.NewEntry(logrus.New()), issueCommentEvent("/retest", "open", true)); err != nil {
		t.Fatalf("handleIssueComment returned error: %v", err)
	}
	if !netlifyClient.retryCalled {
		t.Fatal("expected Netlify retry")
	}
	if len(ghc.comments) != 1 || !strings.Contains(ghc.comments[0], "Retrying the latest Netlify deploy preview") {
		t.Fatalf("unexpected comments: %v", ghc.comments)
	}
}

func TestHandleIssueCommentFailsClosedWhenMappingMissing(t *testing.T) {
	ghc := &fakeGitHubClient{member: true}
	s := newTestServer(ghc, &fakeNetlifyClient{}, &previewconfig.Config{})

	if err := s.handleIssueComment(logrus.NewEntry(logrus.New()), issueCommentEvent("/rebuild-preview", "open", true)); err != nil {
		t.Fatalf("handleIssueComment returned error: %v", err)
	}
	if len(ghc.comments) != 1 {
		t.Fatalf("expected one comment, got %d", len(ghc.comments))
	}
	if !strings.Contains(ghc.comments[0], "does not have a Netlify preview site mapping configured") {
		t.Fatalf("unexpected comment: %s", ghc.comments[0])
	}
}

func newTestServer(ghc *fakeGitHubClient, netlifyClient *fakeNetlifyClient, cfg *previewconfig.Config) *server {
	return &server{
		ghc:           ghc,
		netlifyClient: netlifyClient,
		pluginConfig:  fakePluginConfigAgent{cfg: &plugins.Configuration{}},
		previewConfig: cfg,
		log:           logrus.NewEntry(logrus.New()),
	}
}

func issueCommentEvent(body, state string, isPR bool) github.IssueCommentEvent {
	issue := github.Issue{
		Number: 5,
		State:  state,
		User:   github.User{Login: "pr-author"},
	}
	if isPR {
		issue.PullRequest = &struct{}{}
	}
	return github.IssueCommentEvent{
		Action: github.IssueCommentActionCreated,
		Issue:  issue,
		Comment: github.IssueComment{
			Body: body,
			User: github.User{Login: "commenter"},
		},
		Repo: github.Repo{
			Owner: github.User{Login: "kubernetes"},
			Name:  "website",
		},
	}
}

type fakeGitHubClient struct {
	member       bool
	collaborator bool
	labels       []github.Label
	comments     []string
}

func (f *fakeGitHubClient) BotUserChecker() (func(candidate string) bool, error) {
	return func(candidate string) bool { return candidate == "k8s-ci-robot" }, nil
}

func (f *fakeGitHubClient) CreateComment(org, repo string, number int, comment string) error {
	f.comments = append(f.comments, comment)
	return nil
}

func (f *fakeGitHubClient) GetIssueLabels(org, repo string, number int) ([]github.Label, error) {
	return f.labels, nil
}

func (f *fakeGitHubClient) IsCollaborator(owner, repo, login string) (bool, error) {
	return f.collaborator, nil
}

func (f *fakeGitHubClient) IsMember(org, user string) (bool, error) {
	return f.member, nil
}

type fakeNetlifyClient struct {
	deploys     []netlify.Deploy
	listCalled  bool
	retryCalled bool
}

func (f *fakeNetlifyClient) ListDeploys(ctx context.Context, siteID string) ([]netlify.Deploy, error) {
	f.listCalled = true
	return f.deploys, nil
}

func (f *fakeNetlifyClient) RetryDeploy(ctx context.Context, deployID string) error {
	f.retryCalled = true
	return nil
}

type fakePluginConfigAgent struct {
	cfg *plugins.Configuration
}

func (f fakePluginConfigAgent) Config() *plugins.Configuration {
	return f.cfg
}
