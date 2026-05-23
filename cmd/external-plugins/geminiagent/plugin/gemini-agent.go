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

// Package plugin sends /gemini-agent requests to Gemini on Vertex AI.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	"google.golang.org/genai"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/markdown"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
)

const (
	// PluginName is the configured external plugin name.
	PluginName = "gemini-agent"

	defaultModel       = "gemma-4-26b-a4b-it"
	defaultLocation    = "global"
	cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

	maxContextComments = 20
	maxPRFiles         = 25
	maxPatchBytes      = 120_000
	maxResponseBytes   = 60_000
	requestTimeout     = 10 * time.Minute

	// Rate limiting: conservative for Tier 1 free accounts (typically 15 RPM
	// for flash models).
	defaultRPM     = 10
	maxRetries     = 3
	initialBackoff = 2 * time.Second
	maxBackoff     = 60 * time.Second
)

var geminiAgentRe = regexp.MustCompile(`(?m)^/gemini-agent(?:[ \t]+(.+))?[ \t]*$`)

// resolvedConfig is GeminiAgentConfig with defaults applied.
type resolvedConfig struct {
	Model        string
	AllowedTeams []string
}

func resolveConfig(cfg plugins.GeminiAgentConfig) resolvedConfig {
	rc := resolvedConfig{
		Model:        cfg.Model,
		AllowedTeams: cfg.AllowedTeams,
	}
	if rc.Model == "" {
		rc.Model = firstNonEmpty(os.Getenv("AI_AGENT_MODEL"), defaultModel)
	}
	return rc
}

// geminiLimiter is a process-wide token bucket that proactively stays within
// the Gemini RPM quota. Requests that exceed the limit wait rather than
// hammering the API and eating 429s.
var geminiLimiter = rate.NewLimiter(rate.Limit(float64(defaultRPM)/60.0), defaultRPM)

// GitHubClient is the GitHub API surface used by the Gemini agent.
type GitHubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetIssue(org, repo string, number int) (*github.Issue, error)
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetPullRequestChanges(org, repo string, number int) ([]github.PullRequestChange, error)
	ListIssueComments(org, repo string, number int) ([]github.IssueComment, error)
	TeamBySlugHasMember(org string, teamSlug string, memberLogin string) (bool, error)
	IsMember(org, user string) (bool, error)
	IsCollaborator(org, repo, user string) (bool, error)
}

type geminiClient interface {
	GenerateContent(ctx context.Context, prompt string) (string, error)
}

type vertexGeminiClient struct {
	client *genai.Client
	model  string
}

// safetySettings enforces content filtering on all Gemini responses.
// Gemini 3.x defaults to OFF (no filtering), so we explicitly enable it.
// BLOCK_MEDIUM_AND_ABOVE is restrictive enough to stop harmful content while
// not tripping on code security discussions (vulnerability reports, CVEs, etc).
var safetySettings = []*genai.SafetySetting{
	{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockMediumAndAbove},
	{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockMediumAndAbove},
	{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockMediumAndAbove},
	{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockMediumAndAbove},
}

// isRateLimited checks whether an error is a 429 from the Gemini API.
func isRateLimited(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusTooManyRequests
	}
	return false
}

// HandleGenericComment handles a generalized GitHub comment event.
func HandleGenericComment(ghc GitHubClient, pluginConfig *plugins.Configuration, log *logrus.Entry, e github.GenericCommentEvent) error {
	if e.Action != github.GenericCommentActionCreated {
		return nil
	}
	task, ok := parseGeminiAgentTask(e.Body)
	if !ok {
		return nil
	}
	if task == "" {
		return respond(ghc, e, "Usage: `/gemini-agent <task_description>`")
	}

	org := e.Repo.Owner.Login
	repo := e.Repo.Name

	cfg := geminiAgentFor(pluginConfig, org, repo)

	trusted, err := isAllowed(ghc, cfg, org, repo, e.User.Login)
	if err != nil {
		return err
	}
	if !trusted {
		return respond(ghc, e, "`/gemini-agent` can only be used by org members, repository collaborators, or members of configured teams.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	gemini, err := newVertexGeminiClient(ctx, cfg.Model)
	if err != nil {
		log.WithError(err).Error("Failed to initialize Gemini client.")
		if commentErr := respond(ghc, e, "Failed to initialize the AI backend. Please contact the platform team if this persists."); commentErr != nil {
			return errors.Join(err, commentErr)
		}
		return err
	}

	return runAgent(ctx, ghc, newRateLimitedClient(gemini), log, e, task)
}

func parseGeminiAgentTask(body string) (string, bool) {
	matches := geminiAgentRe.FindStringSubmatch(markdown.DropCodeBlock(body))
	if matches == nil {
		return "", false
	}
	if len(matches) < 2 {
		return "", true
	}
	return strings.TrimSpace(matches[1]), true
}

// geminiAgentFor finds the GeminiAgent config for a repo. Prioritizes
// repo-level ("org/repo") over org-level ("org") entries.
func geminiAgentFor(pc *plugins.Configuration, org, repo string) resolvedConfig {
	fullName := fmt.Sprintf("%s/%s", org, repo)
	for _, cfg := range pc.GeminiAgents {
		if slices.Contains(cfg.Repos, fullName) {
			return resolveConfig(cfg)
		}
	}
	for _, cfg := range pc.GeminiAgents {
		if slices.Contains(cfg.Repos, org) {
			return resolveConfig(cfg)
		}
	}
	return resolveConfig(plugins.GeminiAgentConfig{})
}

// isAllowed checks if a user can invoke /gemini-agent. A user is allowed if
// they are an org member, a repository collaborator, or a member of any
// configured AllowedTeams.
func isAllowed(ghc GitHubClient, cfg resolvedConfig, org, repo, user string) (bool, error) {
	// Org members are always trusted.
	if member, err := ghc.IsMember(org, user); err != nil {
		return false, fmt.Errorf("check org membership: %w", err)
	} else if member {
		return true, nil
	}

	// Repository collaborators are trusted.
	if ok, err := ghc.IsCollaborator(org, repo, user); err != nil {
		return false, fmt.Errorf("check collaborator: %w", err)
	} else if ok {
		return true, nil
	}

	// Check configured team membership.
	for _, team := range cfg.AllowedTeams {
		isMember, err := ghc.TeamBySlugHasMember(org, team, user)
		if err != nil {
			return false, fmt.Errorf("check team %s membership: %w", team, err)
		}
		if isMember {
			return true, nil
		}
	}

	return false, nil
}

func newVertexGeminiClient(ctx context.Context, model string) (*vertexGeminiClient, error) {
	projectID := firstNonEmpty(os.Getenv("AI_AGENT_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT"), os.Getenv("GCLOUD_PROJECT"))
	location := firstNonEmpty(os.Getenv("AI_AGENT_LOCATION"), os.Getenv("GOOGLE_CLOUD_LOCATION"), defaultLocation)
	if projectID == "" {
		creds, err := google.FindDefaultCredentials(ctx, cloudPlatformScope)
		if err == nil {
			projectID = creds.ProjectID
		}
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  projectID,
		Location: location,
	})
	if err != nil {
		return nil, err
	}
	return &vertexGeminiClient{client: client, model: model}, nil
}

func (c *vertexGeminiClient) GenerateContent(ctx context.Context, prompt string) (string, error) {
	resp, err := c.client.Models.GenerateContent(ctx, c.model, genai.Text(prompt), &genai.GenerateContentConfig{
		SafetySettings: safetySettings,
	})
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return "", errors.New("response was blocked by content safety filters")
	}
	return text, nil
}

// rateLimitedClient wraps a geminiClient with proactive rate limiting (token
// bucket) and reactive retry with exponential backoff on 429 responses.
type rateLimitedClient struct {
	inner          geminiClient
	limiter        *rate.Limiter
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

func newRateLimitedClient(inner geminiClient) *rateLimitedClient {
	return &rateLimitedClient{
		inner:          inner,
		limiter:        geminiLimiter,
		maxRetries:     maxRetries,
		initialBackoff: initialBackoff,
		maxBackoff:     maxBackoff,
	}
}

func (c *rateLimitedClient) GenerateContent(ctx context.Context, prompt string) (string, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limiter: %w", err)
	}

	var lastErr error
	for attempt := range c.maxRetries {
		result, err := c.inner.GenerateContent(ctx, prompt)
		if err == nil {
			return result, nil
		}

		if !isRateLimited(err) {
			return "", err
		}

		lastErr = err
		backoff := c.retryBackoff(attempt)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while retrying rate limit (attempt %d): %w", attempt+1, ctx.Err())
		}
	}

	return "", fmt.Errorf("rate limited after %d retries: %w", c.maxRetries, lastErr)
}

func (c *rateLimitedClient) retryBackoff(attempt int) time.Duration {
	backoff := min(c.initialBackoff*(1<<attempt), c.maxBackoff)
	// Add 0-25% jitter.
	jitter := time.Duration(rand.Int64N(int64(backoff) / 4))
	return backoff + jitter
}

func runAgent(ctx context.Context, ghc GitHubClient, gemini geminiClient, log *logrus.Entry, e github.GenericCommentEvent, task string) error {
	githubContext, err := collectGitHubContext(ghc, e)
	if err != nil {
		return err
	}

	response, err := gemini.GenerateContent(ctx, buildPrompt(task, githubContext))
	if err != nil {
		log.WithError(err).Error("Gemini request failed.")
		if commentErr := respond(ghc, e, "The AI request failed. Check Prow logs for details."); commentErr != nil {
			return errors.Join(err, commentErr)
		}
		return err
	}

	return respond(ghc, e, truncate(response, maxResponseBytes))
}

func collectGitHubContext(ghc GitHubClient, e github.GenericCommentEvent) (string, error) {
	org := e.Repo.Owner.Login
	repo := e.Repo.Name

	title := e.IssueTitle
	body := e.IssueBody
	htmlURL := e.IssueHTMLURL
	author := e.IssueAuthor.Login
	kind := "issue"

	var b strings.Builder
	if e.IsPR {
		kind = "pull request"
		pr, err := ghc.GetPullRequest(org, repo, e.Number)
		if err != nil {
			return "", fmt.Errorf("get pull request: %w", err)
		}
		title = pr.Title
		body = pr.Body
		htmlURL = pr.HTMLURL
		author = pr.User.Login
		fmt.Fprintf(&b, "Pull request base: %s@%s\n", pr.Base.Ref, pr.Base.SHA)
		fmt.Fprintf(&b, "Pull request head: %s@%s\n\n", pr.Head.Ref, pr.Head.SHA)
	} else if title == "" && body == "" {
		issue, err := ghc.GetIssue(org, repo, e.Number)
		if err != nil {
			return "", fmt.Errorf("get issue: %w", err)
		}
		title = issue.Title
		body = issue.Body
		htmlURL = issue.HTMLURL
		author = issue.User.Login
	}

	fmt.Fprintf(&b, "Repository: %s/%s\n", org, repo)
	fmt.Fprintf(&b, "Kind: %s\n", kind)
	fmt.Fprintf(&b, "Number: #%d\n", e.Number)
	fmt.Fprintf(&b, "State: %s\n", e.IssueState)
	fmt.Fprintf(&b, "Title: %s\n", title)
	fmt.Fprintf(&b, "URL: %s\n", htmlURL)
	fmt.Fprintf(&b, "Author: @%s\n", author)
	fmt.Fprintf(&b, "Requester: @%s\n\n", e.User.Login)
	fmt.Fprintf(&b, "Body:\n%s\n\n", truncate(body, maxPatchBytes/4))
	fmt.Fprintf(&b, "Triggering comment:\n%s\n\n", truncate(e.Body, maxPatchBytes/8))

	if e.IsPR {
		changes, err := ghc.GetPullRequestChanges(org, repo, e.Number)
		if err != nil {
			return "", fmt.Errorf("get pull request changes: %w", err)
		}
		writePullRequestChanges(&b, changes)
	}

	comments, err := ghc.ListIssueComments(org, repo, e.Number)
	if err != nil {
		return "", fmt.Errorf("list issue comments: %w", err)
	}
	writeRecentComments(&b, comments)

	return b.String(), nil
}

func writePullRequestChanges(b *strings.Builder, changes []github.PullRequestChange) {
	if len(changes) == 0 {
		return
	}

	fmt.Fprintf(b, "Changed files (%d total, showing up to %d):\n", len(changes), maxPRFiles)
	remainingPatchBytes := maxPatchBytes
	for i, change := range changes {
		if i >= maxPRFiles {
			fmt.Fprintf(b, "\n%d additional files omitted.\n\n", len(changes)-maxPRFiles)
			return
		}
		fmt.Fprintf(b, "- %s (%s, +%d/-%d)\n", change.Filename, change.Status, change.Additions, change.Deletions)
		if change.PreviousFilename != "" {
			fmt.Fprintf(b, "  previous filename: %s\n", change.PreviousFilename)
		}
		if change.Patch == "" {
			continue
		}
		if remainingPatchBytes <= 0 {
			fmt.Fprintln(b, "  patch omitted: aggregate patch limit reached")
			continue
		}
		patch := change.Patch
		if len(patch) > remainingPatchBytes {
			patch = truncate(patch, remainingPatchBytes)
		}
		remainingPatchBytes -= len(patch)
		fmt.Fprintf(b, "  patch:\n~~~diff\n%s\n~~~\n", patch)
	}
	fmt.Fprintln(b)
}

func writeRecentComments(b *strings.Builder, comments []github.IssueComment) {
	if len(comments) == 0 {
		return
	}

	start := max(len(comments)-maxContextComments, 0)
	fmt.Fprintf(b, "Recent comments (%d total, showing %d):\n", len(comments), len(comments)-start)
	for _, comment := range comments[start:] {
		created := ""
		if !comment.CreatedAt.IsZero() {
			created = " at " + comment.CreatedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(b, "- @%s%s:\n%s\n", comment.User.Login, created, truncate(comment.Body, maxPatchBytes/20))
	}
}

func buildPrompt(task, githubContext string) string {
	return fmt.Sprintf(`You are a senior Go developer and Kubernetes/Prow platform engineer.

Answer the requested task using the GitHub issue or pull request context below.
Be concise, technical, and safe. If the request needs repository changes that you cannot make from this Vertex AI call, explain the exact next implementation steps instead of pretending the work was done.

Requested task:
%s

GitHub context:
%s

Return a GitHub comment in Markdown.`, task, githubContext)
}

func respond(ghc GitHubClient, e github.GenericCommentEvent, response string) error {
	org := e.Repo.Owner.Login
	repo := e.Repo.Name
	return ghc.CreateComment(org, repo, e.Number, plugins.FormatResponseRaw(e.Body, e.HTMLURL, e.User.Login, response))
}

// HelpProvider constructs help for the external plugin help endpoint.
func HelpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The gemini-agent plugin sends `/gemini-agent` requests and GitHub context to Gemini on Vertex AI.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/gemini-agent <task_description>",
		Description: "Ask Gemini on Vertex AI to analyze an issue or pull request with surrounding GitHub context.",
		Featured:    true,
		WhoCanUse:   "Org members, repository collaborators, or members of configured teams.",
		Examples:    []string{"/gemini-agent Explain why this PR is failing tests.", "/gemini-agent Summarize the risk in this change."},
	})
	return pluginHelp, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	// Back up from the byte limit to avoid splitting a multi-byte UTF-8 char.
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "\n...[truncated]"
}
