/*
Copyright 2022 The Kubernetes Authors.

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

package testfreeze

import (
	"fmt"
	"html/template"
	"slices"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/plugins/testfreeze/checker"
)

const (
	PluginName                  = "testfreeze"
	defaultKubernetesBranch     = "master"
	defaultKubernetesRepoAndOrg = "kubernetes"
	labelKindBug                = "kind/bug"
	templateString              = `{{ if .InCodeFreeze }}Please note that we're already in [Code Freeze](https://github.com/kubernetes/sig-release/blob/master/releases/release_phases.md#code-freeze) for the upcoming {{ .Tag }} release.

{{ if .IsBugFix }}This PR is labeled ` + "`kind/bug`" + `, so it **may** be included in the {{ .Tag }} release as a bug fix. Please make sure to:
1. Technical review: get the PR reviewed and approved as usual (` + "`/lgtm`" + ` and ` + "`/approve`" + `)
2. Release team awareness: tag ` + "`@kubernetes/sig-release-leads`" + ` on this PR so the release team is aware of the fix going in. You can also ping the [#sig-release Slack channel](https://kubernetes.slack.com/archives/C2C40FMNF) for time-sensitive cases.
{{ else }}**Adding the milestone to this PR is strictly prohibited without proper approval.** If this PR needs to be included in the {{ .Tag }} release:
1. Technical review: get the PR reviewed and approved as usual (` + "`/lgtm`" + ` and ` + "`/approve`" + `)
2. Inclusion in release: tag ` + "`@kubernetes/sig-release-leads`" + ` on this PR and ping the [#sig-release Slack channel](https://kubernetes.slack.com/archives/C2C40FMNF) to request adding the ` + "`{{ .Tag }}`" + ` milestone
{{ end }}{{ end }}
{{ if .InTestFreeze }}
---

We're{{ if .InCodeFreeze }} also{{ end }} in [Test Freeze](https://github.com/kubernetes/sig-release/blob/master/releases/release_phases.md#test-freeze) for the ` + "`{{ .Branch }}`" + ` branch. This means every merged PR will be automatically fast-forwarded via the periodic [ci-fast-forward](https://testgrid.k8s.io/sig-release-releng-blocking#git-repo-kubernetes-fast-forward) job to the release branch of the upcoming {{ .Tag }} release.

Fast forwards are scheduled to happen every 6 hours, whereas the most recent run was: {{ .LastFastForward }}.
{{ end }}`
)

func init() {
	plugins.RegisterPullRequestHandler(PluginName, handlePullRequestEvent, helpProvider)
}

func helpProvider(*plugins.Configuration, []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	return &pluginhelp.PluginHelp{
		Description: fmt.Sprintf(
			"The %s plugin adds additional documentation about Code Freeze and Test Freeze periods, including milestone requirements and cherry-pick processes.",
			PluginName,
		),
	}, nil
}

func handlePullRequestEvent(p plugins.Agent, e github.PullRequestEvent) error {
	h := newHandler()
	log := p.Logger
	labels := make([]string, 0, len(e.PullRequest.Labels))
	for _, l := range e.PullRequest.Labels {
		labels = append(labels, l.Name)
	}
	if err := h.handle(
		log,
		p.GitHubClient,
		e.Action,
		e.Number,
		e.Repo.Owner.Login,
		e.Repo.Name,
		e.PullRequest.Base.Ref,
		labels,
	); err != nil {
		log.WithError(err).Error("skipping")
	}
	return nil
}

type handler struct {
	verifier verifier
}

func newHandler() *handler {
	return &handler{
		verifier: &defaultVerifier{},
	}
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate
//counterfeiter:generate . verifier
type verifier interface {
	CheckInTestFreeze(*logrus.Entry) (*checker.Result, error)
	CreateComment(plugins.PluginGitHubClient, string, string, int, string) error
}

type defaultVerifier struct{}

func (*defaultVerifier) CheckInTestFreeze(log *logrus.Entry) (*checker.Result, error) {
	return checker.New(log).InTestFreeze()
}

func (*defaultVerifier) CreateComment(
	client plugins.PluginGitHubClient,
	org, repo string,
	number int,
	comment string,
) error {
	return client.CreateComment(org, repo, number, comment)
}

func (h *handler) handle(
	log *logrus.Entry,
	client plugins.PluginGitHubClient,
	action github.PullRequestEventAction,
	number int,
	org, repo, branch string,
	labels []string,
) error {
	funcStart := time.Now()
	defer func() {
		log.WithField("duration", time.Since(funcStart).String()).
			Debug("Completed handlePullRequest")
	}()

	if action != github.PullRequestActionOpened &&
		action != github.PullRequestActionReopened {
		log.Debugf("Skipping pull request action %s", action)
		return nil
	}

	if org != defaultKubernetesRepoAndOrg ||
		repo != defaultKubernetesRepoAndOrg ||
		branch != defaultKubernetesBranch {
		log.Debug("Skipping non k/k master branch PR")
		return nil
	}

	result, err := h.verifier.CheckInTestFreeze(log)
	if err != nil {
		return fmt.Errorf("get test freeze result: %w", err)
	}

	if !result.InCodeFreeze && !result.InTestFreeze {
		log.Debugf("Not in code freeze or test freeze, skipping")
		return nil
	}

	isBugFix := slices.Contains(labels, labelKindBug)

	comment := &strings.Builder{}
	tpl, err := template.New(PluginName).Parse(templateString)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	data := struct {
		*checker.Result
		IsBugFix bool
	}{result, isBugFix}
	if err := tpl.Execute(comment, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	if err := h.verifier.CreateComment(
		client, org, repo, number, comment.String(),
	); err != nil {
		return fmt.Errorf("create comment on %s/%s#%d: %q: %w", org, repo, number, comment, err)
	}

	return nil
}
