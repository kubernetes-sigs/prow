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

package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"google.golang.org/genai"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

type fakeGitHubClient struct {
	issue    *github.Issue
	pr       *github.PullRequest
	changes  []github.PullRequestChange
	comments []github.IssueComment

	createdComments []string
	isMember        bool
	isCollaborator  bool
}

func (f *fakeGitHubClient) CreateComment(_, _ string, _ int, comment string) error {
	f.createdComments = append(f.createdComments, comment)
	return nil
}

func (f *fakeGitHubClient) GetIssue(_, _ string, _ int) (*github.Issue, error) {
	if f.issue == nil {
		return nil, errors.New("unexpected GetIssue call")
	}
	return f.issue, nil
}

func (f *fakeGitHubClient) GetPullRequest(_, _ string, _ int) (*github.PullRequest, error) {
	if f.pr == nil {
		return nil, errors.New("unexpected GetPullRequest call")
	}
	return f.pr, nil
}

func (f *fakeGitHubClient) GetPullRequestChanges(_, _ string, _ int) ([]github.PullRequestChange, error) {
	return f.changes, nil
}

func (f *fakeGitHubClient) ListIssueComments(_, _ string, _ int) ([]github.IssueComment, error) {
	return f.comments, nil
}

func (f *fakeGitHubClient) TeamBySlugHasMember(_, _ string, _ string) (bool, error) {
	return false, nil
}

func (f *fakeGitHubClient) IsMember(_, _ string) (bool, error) {
	return f.isMember, nil
}

func (f *fakeGitHubClient) IsCollaborator(_, _, _ string) (bool, error) {
	return f.isCollaborator, nil
}

type fakeGeminiClient struct {
	prompt   string
	response string
}

func (f *fakeGeminiClient) GenerateContent(_ context.Context, prompt string) (string, error) {
	f.prompt = prompt
	return f.response, nil
}

func TestParseGeminiAgentTask(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
		matched  bool
	}{
		{
			name:     "task on command line",
			body:     "/gemini-agent explain this failure",
			expected: "explain this failure",
			matched:  true,
		},
		{
			name:    "empty task",
			body:    "/gemini-agent",
			matched: true,
		},
		{
			name: "ignores code block",
			body: "```text\n/gemini-agent not a command\n```",
		},
		{
			name: "unrelated comment",
			body: "/retest",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, matched := parseGeminiAgentTask(test.body)
			if matched != test.matched {
				t.Fatalf("matched %t, expected %t", matched, test.matched)
			}
			if actual != test.expected {
				t.Fatalf("task %q, expected %q", actual, test.expected)
			}
		})
	}
}

func TestRunAgentPullRequestContext(t *testing.T) {
	ghc := &fakeGitHubClient{
		pr: &github.PullRequest{
			Title:   "Fix the frobnicator",
			Body:    "This changes the frobnicator retry path.",
			HTMLURL: "https://github.example/pr/7",
			User:    github.User{Login: "alice"},
			Base:    github.PullRequestBranch{Ref: "main", SHA: "base-sha"},
			Head:    github.PullRequestBranch{Ref: "feature", SHA: "head-sha"},
		},
		changes: []github.PullRequestChange{
			{
				Filename:  "pkg/frob/frob.go",
				Status:    string(github.PullRequestFileModified),
				Additions: 3,
				Deletions: 1,
				Patch:     "@@ -1 +1 @@\n-old\n+new",
			},
		},
		comments: []github.IssueComment{
			{
				Body:      "previous reviewer context",
				User:      github.User{Login: "reviewer"},
				CreatedAt: time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
			},
		},
	}
	gemini := &fakeGeminiClient{response: "Gemini says the retry path is risky."}
	event := github.GenericCommentEvent{
		Action:       github.GenericCommentActionCreated,
		Body:         "/gemini-agent summarize risk",
		HTMLURL:      "https://github.example/comment/1",
		Number:       7,
		IsPR:         true,
		IssueState:   "open",
		IssueTitle:   "stale title should be replaced by PR title",
		IssueBody:    "stale body should be replaced by PR body",
		IssueHTMLURL: "https://github.example/issues/7",
		IssueAuthor:  github.User{Login: "issue-author"},
		User:         github.User{Login: "bob"},
		Repo: github.Repo{
			Name:  "repo",
			Owner: github.User{Login: "org"},
		},
	}

	if err := runAgent(context.Background(), ghc, gemini, logrus.NewEntry(logrus.New()), event, "summarize risk"); err != nil {
		t.Fatalf("runAgent returned error: %v", err)
	}

	for _, expected := range []string{
		"Requested task:\nsummarize risk",
		"Repository: org/repo",
		"Pull request base: main@base-sha",
		"pkg/frob/frob.go",
		"previous reviewer context",
	} {
		if !strings.Contains(gemini.prompt, expected) {
			t.Fatalf("Gemini prompt missing %q:\n%s", expected, gemini.prompt)
		}
	}

	if len(ghc.createdComments) != 1 {
		t.Fatalf("created %d comments, expected 1", len(ghc.createdComments))
	}
	if !strings.Contains(ghc.createdComments[0], "Gemini says the retry path is risky.") {
		t.Fatalf("created comment missing Gemini response:\n%s", ghc.createdComments[0])
	}
}

// fakeRateLimitedGeminiClient simulates 429 responses followed by success.
type fakeRateLimitedGeminiClient struct {
	failCount int // number of 429s to return before succeeding
	calls     int
	response  string
}

func (f *fakeRateLimitedGeminiClient) GenerateContent(_ context.Context, _ string) (string, error) {
	f.calls++
	if f.calls <= f.failCount {
		return "", genai.APIError{
			Code:    http.StatusTooManyRequests,
			Message: "Resource exhausted",
			Status:  "429 Too Many Requests",
		}
	}
	return f.response, nil
}

func TestIsRateLimited(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "429 APIError",
			err:      genai.APIError{Code: http.StatusTooManyRequests, Message: "rate limited"},
			expected: true,
		},
		{
			name:     "500 APIError",
			err:      genai.APIError{Code: http.StatusInternalServerError, Message: "server error"},
			expected: false,
		},
		{
			name:     "wrapped 429 APIError",
			err:      fmt.Errorf("call failed: %w", genai.APIError{Code: http.StatusTooManyRequests}),
			expected: true,
		},
		{
			name:     "generic error",
			err:      errors.New("network timeout"),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRateLimited(tc.err); got != tc.expected {
				t.Errorf("isRateLimited() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestRetryBackoff(t *testing.T) {
	rl := &rateLimitedClient{
		initialBackoff: 2 * time.Second,
		maxBackoff:     60 * time.Second,
	}
	for attempt := range 4 {
		d := rl.retryBackoff(attempt)
		// Should be at least initialBackoff * 2^attempt and capped at maxBackoff.
		minExpected := min(rl.initialBackoff*(1<<attempt), rl.maxBackoff)
		if d < minExpected {
			t.Errorf("attempt %d: backoff %v < expected minimum %v", attempt, d, minExpected)
		}
		// Should not exceed max + 25% jitter.
		maxExpected := rl.maxBackoff + rl.maxBackoff/4
		if d > maxExpected {
			t.Errorf("attempt %d: backoff %v > expected maximum %v", attempt, d, maxExpected)
		}
	}
}

// newTestRateLimitedClient creates a rateLimitedClient with minimal backoff for fast tests.
func newTestRateLimitedClient(inner geminiClient) *rateLimitedClient {
	return &rateLimitedClient{
		inner:          inner,
		limiter:        rate.NewLimiter(rate.Inf, 1), // no rate limit in tests
		maxRetries:     maxRetries,
		initialBackoff: time.Millisecond,
		maxBackoff:     5 * time.Millisecond,
	}
}

func TestRunAgentRetriesOnRateLimit(t *testing.T) {
	gemini := &fakeRateLimitedGeminiClient{
		failCount: 2,
		response:  "Success after retries",
	}
	ghc := &fakeGitHubClient{
		issue: &github.Issue{
			Title:   "Test issue",
			Body:    "Test body",
			HTMLURL: "https://github.example/issues/1",
			User:    github.User{Login: "alice"},
		},
		comments: []github.IssueComment{},
	}
	event := github.GenericCommentEvent{
		Action:     github.GenericCommentActionCreated,
		Body:       "/gemini-agent do the thing",
		HTMLURL:    "https://github.example/comment/1",
		Number:     1,
		IssueState: "open",
		IssueTitle: "Test issue",
		IssueBody:  "Test body",
		User:       github.User{Login: "bob"},
		Repo: github.Repo{
			Name:  "repo",
			Owner: github.User{Login: "org"},
		},
	}

	err := runAgent(context.Background(), ghc, newTestRateLimitedClient(gemini), logrus.NewEntry(logrus.New()), event, "do the thing")
	if err != nil {
		t.Fatalf("runAgent returned error: %v", err)
	}
	if gemini.calls != 3 {
		t.Errorf("expected 3 calls (2 retries + 1 success), got %d", gemini.calls)
	}
	if len(ghc.createdComments) != 1 || !strings.Contains(ghc.createdComments[0], "Success after retries") {
		t.Errorf("unexpected comment: %v", ghc.createdComments)
	}
}

func TestRunAgentExhaustsRetries(t *testing.T) {
	gemini := &fakeRateLimitedGeminiClient{
		failCount: maxRetries + 1, // more failures than retries allowed
		response:  "never reached",
	}
	ghc := &fakeGitHubClient{
		issue: &github.Issue{
			Title:   "Test issue",
			Body:    "Test body",
			HTMLURL: "https://github.example/issues/1",
			User:    github.User{Login: "alice"},
		},
		comments: []github.IssueComment{},
	}
	event := github.GenericCommentEvent{
		Action:     github.GenericCommentActionCreated,
		Body:       "/gemini-agent do the thing",
		HTMLURL:    "https://github.example/comment/1",
		Number:     1,
		IssueState: "open",
		IssueTitle: "Test issue",
		IssueBody:  "Test body",
		User:       github.User{Login: "bob"},
		Repo: github.Repo{
			Name:  "repo",
			Owner: github.User{Login: "org"},
		},
	}

	err := runAgent(context.Background(), ghc, newTestRateLimitedClient(gemini), logrus.NewEntry(logrus.New()), event, "do the thing")
	// runAgent returns the error after posting the failure comment.
	if err == nil {
		t.Fatal("expected error from runAgent after exhausting retries")
	}
	if !strings.Contains(err.Error(), "rate limited after") {
		t.Errorf("expected rate limit exhaustion error, got: %v", err)
	}
	// Should have posted an error comment.
	if len(ghc.createdComments) != 1 {
		t.Fatalf("expected 1 error comment, got %d", len(ghc.createdComments))
	}
	if !strings.Contains(ghc.createdComments[0], "The AI request failed") {
		t.Errorf("expected failure message in comment, got: %s", ghc.createdComments[0])
	}
}

func TestGeminiAgentForLookup(t *testing.T) {
	pc := &plugins.Configuration{
		GeminiAgents: []plugins.GeminiAgentConfig{
			{
				Repos:        []string{"org/specific-repo"},
				Model:        "gemma4-27b",
				AllowedTeams: []string{"ml-team"},
			},
			{
				Repos:        []string{"org"},
				Model:        "gemini-3-flash",
				AllowedTeams: []string{"platform-team"},
			},
		},
	}

	// Repo-level match wins over org-level.
	cfg := geminiAgentFor(pc, "org", "specific-repo")
	if cfg.Model != "gemma4-27b" {
		t.Errorf("expected gemma4-27b for org/specific-repo, got %s", cfg.Model)
	}
	if len(cfg.AllowedTeams) != 1 || cfg.AllowedTeams[0] != "ml-team" {
		t.Errorf("expected ml-team for org/specific-repo, got %v", cfg.AllowedTeams)
	}

	// Org-level fallback.
	cfg = geminiAgentFor(pc, "org", "other-repo")
	if cfg.Model != "gemini-3-flash" {
		t.Errorf("expected gemini-3-flash for org/other-repo, got %s", cfg.Model)
	}

	// No config: defaults.
	cfg = geminiAgentFor(pc, "unknown-org", "repo")
	if cfg.Model != defaultModel {
		t.Errorf("expected default model for unknown org, got %s", cfg.Model)
	}
}

func TestGeminiAgentForDefaultModel(t *testing.T) {
	pc := &plugins.Configuration{}
	cfg := geminiAgentFor(pc, "org", "repo")
	if cfg.Model != defaultModel {
		t.Errorf("expected %s, got %s", defaultModel, cfg.Model)
	}
}

func TestIsAllowed(t *testing.T) {
	tests := []struct {
		name           string
		isMember       bool
		isCollaborator bool
		allowedTeams   []string
		expected       bool
	}{
		{
			name:     "org member is allowed",
			isMember: true,
			expected: true,
		},
		{
			name:           "collaborator is allowed",
			isCollaborator: true,
			expected:       true,
		},
		{
			name:     "stranger is denied",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGitHubClient{
				isMember:       tc.isMember,
				isCollaborator: tc.isCollaborator,
			}
			cfg := resolvedConfig{AllowedTeams: tc.allowedTeams}
			allowed, err := isAllowed(ghc, cfg, "org", "repo", "user")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if allowed != tc.expected {
				t.Errorf("isAllowed() = %v, want %v", allowed, tc.expected)
			}
		})
	}
}
