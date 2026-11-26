/*
Copyright 2016 The Kubernetes Authors.

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

package slackevents

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
)

const (
	pluginName = "slackevents"
)

var sigMatcher = regexp.MustCompile(`(?m)@kubernetes/sig-([\w-]*)-(misc|test-failures|bugs|feature-requests|proposals|pr-reviews|api-reviews)`)

type slackClient interface {
	WriteMessage(text string, channel string) error
}

type githubClient interface {
	BotUserChecker() (func(candidate string) bool, error)
}

type client struct {
	GitHubClient githubClient
	SlackClient  slackClient
	SlackConfig  plugins.Slack
}

func init() {
	plugins.RegisterPushEventHandler(pluginName, handlePush, helpProvider)
	plugins.RegisterGenericCommentHandler(pluginName, handleComment, helpProvider)
	plugins.RegisterPullRequestHandler(pluginName, handlePullRequest, helpProvider)
}

func helpProvider(config *plugins.Configuration, enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	configInfo := map[string]string{
		"": fmt.Sprintf("SIG mentions on GitHub are reiterated for the following SIG Slack channels: %s.", strings.Join(config.Slack.MentionChannels, ", ")),
	}
	for _, repo := range enabledRepos {
		mw := getMergeWarning(config.Slack.MergeWarnings, repo)
		if mw != nil {
			configInfo[repo.String()] = fmt.Sprintf("In this repo merges are considered "+
				"manual and trigger manual merge warnings if the user who merged is not "+
				"a member of this universal exemption list: %s or merged to a branch they "+
				"are not specifically exempted for: %#v.<br>Warnings are sent to the "+
				"following Slack channels: %s.", strings.Join(mw.ExemptUsers, ", "),
				mw.ExemptBranches, strings.Join(mw.Channels, ", "))
		} else {
			configInfo[repo.String()] = "There are no manual merge warnings configured for this repo."
		}
	}
	yamlSnippet, err := plugins.CommentMap.GenYaml(&plugins.Configuration{
		Slack: plugins.Slack{
			MentionChannels: []string{
				"channel1",
				"channel2",
			},
			MergeWarnings: []plugins.MergeWarning{
				{
					Repos: []string{
						"org/repo1",
						"org/repo2",
					},
					Channels: []string{
						"channel3",
						"channel4",
					},
					ExemptUsers: []string{
						"alice",
						"bob",
					},
					ExemptBranches: map[string][]string{
						"dev": {
							"james",
							"joe",
						},
					},
				},
			},
			CLAAlerts: []plugins.CLAAlert{
				{
					Repos:    []string{"org/repo1", "org/repo2"},
					Channels: []string{"channel5"},
				},
			},
		},
	})
	if err != nil {
		logrus.WithError(err).Warnf("cannot generate comments for %s plugin", pluginName)
	}
	return &pluginhelp.PluginHelp{
			Description: `The slackevents plugin reacts to various GitHub events by commenting in Slack channels.
<ol>
<li>The plugin can create comments to alert on manual merges. Manual merges are merges made by a normal user instead of a bot or trusted user.</li>
<li>The plugin can send Slack alerts when a PR carrying the "cncf-cla: no" label is merged.</li>
<li>The plugin can create comments to reiterate SIG mentions like '@kubernetes/sig-testing-bugs' from GitHub.</li>
</ol>`,
			Config:  configInfo,
			Snippet: yamlSnippet,
		},
		nil
}

func handleComment(pc plugins.Agent, e github.GenericCommentEvent) error {
	c := client{
		GitHubClient: pc.GitHubClient,
		SlackConfig:  pc.PluginConfig.Slack,
		SlackClient:  pc.SlackClient,
	}
	return echoToSlack(c, e)
}

func handlePush(pc plugins.Agent, pe github.PushEvent) error {
	c := client{
		GitHubClient: pc.GitHubClient,
		SlackConfig:  pc.PluginConfig.Slack,
		SlackClient:  pc.SlackClient,
	}
	return notifyOnSlackIfManualMerge(c, pe)
}

func handlePullRequest(pc plugins.Agent, pre github.PullRequestEvent) error {
	c := client{
		GitHubClient: pc.GitHubClient,
		SlackConfig:  pc.PluginConfig.Slack,
		SlackClient:  pc.SlackClient,
	}
	return notifyOnCLAIfMerged(c, pre)
}

func notifyOnSlackIfManualMerge(pc client, pe github.PushEvent) error {
	//Fetch MergeWarning for the repo we received the merge event.
	if mw := getMergeWarning(pc.SlackConfig.MergeWarnings, config.OrgRepo{Org: pe.Repo.Owner.Login, Repo: pe.Repo.Name}); mw != nil {
		//If the MergeWarning exemption list has the merge user then no need to send a message.
		if ok := isExempted(mw, pe); !ok {
			var message string
			switch {
			case pe.Created:
				message = fmt.Sprintf("*Warning:* %s (<@%s>) pushed a new branch (%s): %s", pe.Sender.Login, pe.Sender.Login, pe.Branch(), pe.Compare)
			case pe.Deleted:
				message = fmt.Sprintf("*Warning:* %s (<@%s>) deleted a branch (%s): %s", pe.Sender.Login, pe.Sender.Login, pe.Branch(), pe.Compare)
			case pe.Forced:
				message = fmt.Sprintf("*Warning:* %s (<@%s>) *force* merged %d commit(s) into %s: %s", pe.Sender.Login, pe.Sender.Login, len(pe.Commits), pe.Branch(), pe.Compare)
			default:
				message = fmt.Sprintf("*Warning:* %s (<@%s>) manually merged %d commit(s) into %s: %s", pe.Sender.Login, pe.Sender.Login, len(pe.Commits), pe.Branch(), pe.Compare)
			}
			for _, channel := range mw.Channels {
				if err := pc.SlackClient.WriteMessage(message, channel); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func isExempted(mw *plugins.MergeWarning, pe github.PushEvent) bool {
	exemptedLogins := sets.Set[string]{}
	for _, login := range append(mw.ExemptUsers, mw.ExemptBranches[pe.Branch()]...) {
		exemptedLogins.Insert(github.NormLogin(login))
	}

	return exemptedLogins.HasAny(github.NormLogin(pe.Pusher.Name), github.NormLogin(pe.Sender.Login))
}

func getMergeWarning(mergeWarnings []plugins.MergeWarning, repo config.OrgRepo) *plugins.MergeWarning {
	// First search for repo config
	for _, mw := range mergeWarnings {
		if !sets.NewString(mw.Repos...).Has(repo.String()) {
			continue
		}
		return &mw
	}

	// If you don't find anything, loop again looking for an org config
	for _, mw := range mergeWarnings {
		if !sets.NewString(mw.Repos...).Has(repo.Org) {
			continue
		}
		return &mw
	}

	return nil
}

func echoToSlack(pc client, e github.GenericCommentEvent) error {
	// Ignore bot comments and comments that aren't new.
	botUserChecker, err := pc.GitHubClient.BotUserChecker()
	if err != nil {
		return err
	}
	if botUserChecker(e.User.Login) {
		return nil
	}
	if e.Action != github.GenericCommentActionCreated {
		return nil
	}

	sigMatches := sigMatcher.FindAllStringSubmatch(e.Body, -1)

	for _, match := range sigMatches {
		sig := "sig-" + match[1]
		// Check if this sig is a slack channel that should be messaged.
		found := slices.Contains(pc.SlackConfig.MentionChannels, sig)
		if !found {
			continue
		}

		msg := fmt.Sprintf("%s was mentioned by %s (<@%s>) on GitHub. (%s)\n>>>%s", sig, e.User.Login, e.User.Login, e.HTMLURL, e.Body)
		if err := pc.SlackClient.WriteMessage(msg, sig); err != nil {
			return fmt.Errorf("Failed to send message on slack channel: %q with message %q. Err: %w", sig, msg, err)
		}
	}
	return nil
}

// notifyOnCLAIfMerged sends a Slack alert to all configured channels when a
// PR carrying the "cncf-cla: no" label is merged. It reads labels directly
// from the PullRequestEvent payload, which works regardless of merge strategy
// (merge, squash, or rebase).
func notifyOnCLAIfMerged(pc client, pre github.PullRequestEvent) error {
	// Only act on PRs that were actually merged, not just closed.
	if pre.Action != github.PullRequestActionClosed || !pre.PullRequest.Merged {
		return nil
	}

	// Find the configured CLAAlert for this repository (exact org/repo match first,
	// then org-level fallback).
	ca := getCLAAlert(pc.SlackConfig.CLAAlerts, config.OrgRepo{
		Org:  pre.Repo.Owner.Login,
		Repo: pre.Repo.Name,
	})
	if ca == nil {
		return nil
	}

	// Check whether the merged PR carries the "cncf-cla: no" label.
	for _, l := range pre.PullRequest.Labels {
		if l.Name != labels.ClaNo {
			continue
		}
		msg := fmt.Sprintf(
			"*Alert:* PR #%d merged with %q label by %s — %s",
			pre.Number,
			labels.ClaNo,
			pre.PullRequest.User.Login,
			pre.PullRequest.HTMLURL,
		)
		for _, ch := range ca.Channels {
			if err := pc.SlackClient.WriteMessage(msg, ch); err != nil {
				return fmt.Errorf("failed to post CLA alert to %s: %w", ch, err)
			}
		}
		break
	}
	return nil
}

// getCLAAlert returns the CLAAlert config for the given repo.
// It checks for an exact org/repo match first, then falls back to org-level.
func getCLAAlert(alerts []plugins.CLAAlert, repo config.OrgRepo) *plugins.CLAAlert {
	for _, a := range alerts {
		if sets.NewString(a.Repos...).Has(repo.String()) {
			return &a
		}
	}
	for _, a := range alerts {
		if sets.NewString(a.Repos...).Has(repo.Org) {
			return &a
		}
	}
	return nil
}
