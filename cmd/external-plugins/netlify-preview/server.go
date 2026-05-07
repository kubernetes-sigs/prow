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
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	previewconfig "sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/config"
	"sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/netlify"
	netlifypreview "sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/plugin"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/plugins/trigger"
)

type githubClient interface {
	BotUserChecker() (func(candidate string) bool, error)
	CreateComment(org, repo string, number int, comment string) error
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	IsCollaborator(owner, repo, login string) (bool, error)
	IsMember(org, user string) (bool, error)
}

type netlifyClient interface {
	ListDeploys(ctx context.Context, siteID string) ([]netlify.Deploy, error)
	RetryDeploy(ctx context.Context, deployID string) error
}

type pluginConfigAgent interface {
	Config() *plugins.Configuration
}

type server struct {
	tokenGenerator func() []byte
	ghc            githubClient
	netlifyClient  netlifyClient
	pluginConfig   pluginConfigAgent
	previewConfig  *previewconfig.Config
	log            *logrus.Entry
	dryRun         bool
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := github.ValidateWebhook(w, r, s.tokenGenerator)
	if !ok {
		return
	}
	fmt.Fprint(w, "Event received. Have a nice day.")

	if err := s.handleEvent(eventType, eventGUID, payload); err != nil {
		s.log.WithError(err).Error("Error parsing event.")
	}
}

func (s *server) handleEvent(eventType, eventGUID string, payload []byte) error {
	l := s.log.WithFields(logrus.Fields{
		"event-type":     eventType,
		github.EventGUID: eventGUID,
	})
	switch eventType {
	case "issue_comment":
		var ic github.IssueCommentEvent
		if err := json.Unmarshal(payload, &ic); err != nil {
			return err
		}
		go func() {
			if err := s.handleIssueComment(l, ic); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Failed to handle issue comment.")
			}
		}()
	default:
		l.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) error {
	if !ic.Issue.IsPullRequest() || ic.Action != github.IssueCommentActionCreated || ic.Issue.State == "closed" {
		return nil
	}

	command, ok := netlifypreview.ParseCommand(ic.Comment.Body)
	if !ok {
		return nil
	}

	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	number := ic.Issue.Number
	commentAuthor := ic.Comment.User.Login
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   number,
		"command":           command,
	})

	botUserChecker, err := s.ghc.BotUserChecker()
	if err != nil {
		return err
	}
	if botUserChecker(commentAuthor) {
		l.Debug("Comment is made by the bot, skipping.")
		return nil
	}

	if trusted, err := s.trustedForCommand(org, repo, number, ic.Issue.User.Login, commentAuthor); err != nil {
		return err
	} else if !trusted {
		return s.comment(ic, "Cannot retry the Netlify deploy preview until a trusted user reviews the PR and leaves an `/ok-to-test` message.")
	}

	repoConfig, ok := s.previewConfig.Repo(org, repo)
	if !ok {
		l.WithField("action", "config_error").Info("Repository has no Netlify preview site mapping.")
		return s.comment(ic, "This repository does not have a Netlify preview site mapping configured, so I can't retry a deploy preview for it.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deploys, err := s.netlifyClient.ListDeploys(ctx, repoConfig.SiteID)
	if err != nil {
		return err
	}
	preview, found := netlifypreview.LatestDeployPreview(deploys, number)
	var previewPtr *netlify.Deploy
	if found {
		previewPtr = &preview
	}
	decision := netlifypreview.Evaluate(command, previewPtr)
	l = l.WithFields(logrus.Fields{
		"site-id": repoConfig.SiteID,
		"action":  decision.Action,
	})
	if found {
		l = l.WithFields(logrus.Fields{
			"deploy-id":    preview.ID,
			"deploy-state": preview.State,
			"preview-link": preview.DeploySSLURL,
		})
	}

	if decision.ShouldRetry {
		if !s.dryRun {
			if err := s.netlifyClient.RetryDeploy(ctx, preview.ID); err != nil {
				return err
			}
		}
		l.Info("Requested Netlify deploy preview retry.")
		return s.comment(ic, fmt.Sprintf("Retrying the latest Netlify deploy preview for this PR: %s", preview.DeploySSLURL))
	}

	l.Info("Did not request Netlify deploy preview retry.")
	return s.comment(ic, responseForDecision(command, decision, previewPtr))
}

func (s *server) trustedForCommand(org, repo string, number int, issueAuthor, commentAuthor string) (bool, error) {
	triggerConfig := s.pluginConfig.Config().TriggerFor(org, repo)
	trustedResponse, err := trigger.TrustedUser(s.ghc, triggerConfig.OnlyOrgMembers, triggerConfig.TrustedApps, triggerConfig.TrustedOrg, commentAuthor, org, repo)
	if err != nil {
		return false, fmt.Errorf("error checking trust of %s: %w", commentAuthor, err)
	}
	if trustedResponse.IsTrusted {
		return true, nil
	}
	_, trusted, err := trigger.TrustedPullRequest(s.ghc, triggerConfig, issueAuthor, org, repo, number, nil)
	return trusted, err
}

func (s *server) comment(ic github.IssueCommentEvent, response string) error {
	return s.ghc.CreateComment(ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, plugins.FormatICResponse(ic.Comment, response))
}

func responseForDecision(command netlifypreview.Command, decision netlifypreview.Decision, preview *netlify.Deploy) string {
	switch decision.Action {
	case netlifypreview.ActionNoPreview:
		return "No Netlify deploy preview was found for this PR, so there is nothing to retry."
	case netlifypreview.ActionAlreadyRunning:
		return fmt.Sprintf("A Netlify deploy preview is already in progress for this PR: %s", preview.DeploySSLURL)
	case netlifypreview.ActionReadyRequiresRebuild:
		return fmt.Sprintf("The latest Netlify deploy preview is `ready`. `/retest` only retries previews in `error` state. Use `/rebuild-preview` to refresh it: %s", preview.DeploySSLURL)
	case netlifypreview.ActionUnsupportedState:
		return fmt.Sprintf("The latest Netlify deploy preview is in state `%s`. `/retest` does not retry this state. Use `/rebuild-preview` to force a retry: %s", preview.State, preview.DeploySSLURL)
	default:
		return fmt.Sprintf("No Netlify deploy preview retry was requested for command `%s`.", command)
	}
}
