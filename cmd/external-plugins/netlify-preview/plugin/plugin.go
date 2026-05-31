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
	"regexp"

	"sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/netlify"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/markdown"
	"sigs.k8s.io/prow/pkg/pluginhelp"
)

const PluginName = "netlify-preview"

type Command string

const (
	RetestCommand         Command = "retest"
	RebuildPreviewCommand Command = "rebuild-preview"
)

var commandRe = regexp.MustCompile(`(?mi)^/(retest|rebuild-preview)\s*$`)

func ParseCommand(body string) (Command, bool) {
	body = markdown.DropCodeBlock(body)
	matches := commandRe.FindStringSubmatch(body)
	if len(matches) != 2 {
		return "", false
	}
	return Command(matches[1]), true
}

func LatestDeployPreview(deploys []netlify.Deploy, reviewID int) (netlify.Deploy, bool) {
	var latest netlify.Deploy
	var found bool
	for _, deploy := range deploys {
		if deploy.Context != "deploy-preview" || deploy.ReviewID != reviewID {
			continue
		}
		if !found || deploy.CreatedAt.After(latest.CreatedAt) {
			latest = deploy
			found = true
		}
	}
	return latest, found
}

type Action string

const (
	ActionRetry                Action = "retry"
	ActionNoPreview            Action = "no_preview"
	ActionAlreadyRunning       Action = "already_running"
	ActionReadyRequiresRebuild Action = "ready_requires_rebuild"
	ActionUnsupportedState     Action = "unsupported_state"
)

type Decision struct {
	Action      Action
	ShouldRetry bool
}

func Evaluate(command Command, preview *netlify.Deploy) Decision {
	if preview == nil {
		return Decision{Action: ActionNoPreview}
	}
	if preview.State == "building" || preview.State == "enqueued" {
		return Decision{Action: ActionAlreadyRunning}
	}
	if command == RebuildPreviewCommand {
		return Decision{Action: ActionRetry, ShouldRetry: true}
	}
	switch preview.State {
	case "error":
		return Decision{Action: ActionRetry, ShouldRetry: true}
	case "ready":
		return Decision{Action: ActionReadyRequiresRebuild}
	default:
		return Decision{Action: ActionUnsupportedState}
	}
}

func HelpProvider(_ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The netlify-preview plugin retries Netlify deploy previews for pull requests.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/retest",
		Description: "Retry the latest Netlify deploy preview for a PR when that preview is in error state.",
		Featured:    true,
		WhoCanUse:   "Anyone can trigger this command on a trusted PR.",
		Examples:    []string{"/retest"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/rebuild-preview",
		Description: "Force a retry of the latest Netlify deploy preview for a PR regardless of its current state, except when a build is already running.",
		Featured:    true,
		WhoCanUse:   "Anyone can trigger this command on a trusted PR.",
		Examples:    []string{"/rebuild-preview"},
	})
	return pluginHelp, nil
}
