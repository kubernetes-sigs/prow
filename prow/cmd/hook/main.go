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

package main

import (
	"flag"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/pjutil/pprof"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/bugzilla"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	pluginsflagutil "k8s.io/test-infra/prow/flagutil/plugins"
	"k8s.io/test-infra/prow/githubeventserver"
	"k8s.io/test-infra/prow/hook"
	"k8s.io/test-infra/prow/interrupts"
	jiraclient "k8s.io/test-infra/prow/jira"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	pluginhelp "k8s.io/test-infra/prow/pluginhelp/hook"
	"k8s.io/test-infra/prow/plugins"
	bzplugin "k8s.io/test-infra/prow/plugins/bugzilla"
	"k8s.io/test-infra/prow/plugins/jira"
	"k8s.io/test-infra/prow/plugins/ownersconfig"
	"k8s.io/test-infra/prow/repoowners"
	"k8s.io/test-infra/prow/slack"

	_ "k8s.io/test-infra/prow/version"
)

const (
	defaultWebhookPath = "/hook"
)

type options struct {
	webhookPath string
	port        int

	config        configflagutil.ConfigOptions
	pluginsConfig pluginsflagutil.PluginOptions

	dryRun                 bool
	gracePeriod            time.Duration
	kubernetes             prowflagutil.KubernetesOptions
	github                 prowflagutil.GitHubOptions
	githubEnablement       prowflagutil.GitHubEnablementOptions
	bugzilla               prowflagutil.BugzillaOptions
	instrumentationOptions prowflagutil.InstrumentationOptions
	jira                   prowflagutil.JiraOptions

	webhookSecretFile string
	slackTokenFile    string
}

func (o *options) Validate() error {
	for _, group := range []flagutil.OptionGroup{&o.kubernetes, &o.github, &o.bugzilla, &o.jira, &o.githubEnablement, &o.config, &o.pluginsConfig} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}

	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options
	fs.StringVar(&o.webhookPath, "webhook-path", defaultWebhookPath, "The path of webhook events, default is '/hook'.")
	fs.IntVar(&o.port, "port", 8888, "Port to listen on.")

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.DurationVar(&o.gracePeriod, "grace-period", 180*time.Second, "On shutdown, try to handle remaining events for the specified duration. ")
	o.pluginsConfig.PluginConfigPathDefault = "/etc/plugins/plugins.yaml"
	for _, group := range []flagutil.OptionGroup{&o.kubernetes, &o.github, &o.bugzilla, &o.instrumentationOptions, &o.jira, &o.githubEnablement, &o.config, &o.pluginsConfig} {
		group.AddFlags(fs)
	}

	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.slackTokenFile, "slack-token-file", "", "Path to the file containing the Slack token to use.")
	fs.Parse(args)
	return o
}

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}
	o.kubernetes.SetDisabledClusters(sets.New[string](configAgent.Config().DisabledClusters...))

	var tokens []string

	// Append the path of hmac and github secrets.
	if o.github.TokenPath != "" {
		tokens = append(tokens, o.github.TokenPath)
	}
	if o.github.AppPrivateKeyPath != "" {
		tokens = append(tokens, o.github.AppPrivateKeyPath)
	}
	tokens = append(tokens, o.webhookSecretFile)

	// This is necessary since slack token is optional.
	if o.slackTokenFile != "" {
		tokens = append(tokens, o.slackTokenFile)
	}

	if o.bugzilla.ApiKeyPath != "" {
		tokens = append(tokens, o.bugzilla.ApiKeyPath)
	}

	if err := secret.Add(tokens...); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	pluginAgent, err := o.pluginsConfig.PluginAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting plugins.")
	}

	githubClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}
	gitClient, err := o.github.GitClientFactory("", &o.config.InRepoConfigCacheDirBase, o.dryRun, false)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Git client.")
	}

	var bugzillaClient bugzilla.Client
	if orgs, repos, _ := pluginAgent.Config().EnabledReposForPlugin(bzplugin.PluginName); orgs != nil || repos != nil {
		client, err := o.bugzilla.BugzillaClient()
		if err != nil {
			logrus.WithError(err).Fatal("Error getting Bugzilla client.")
		}
		bugzillaClient = client
	} else {
		// we want something non-nil here with good no-op behavior,
		// so the test fake is a cheap way to do that
		bugzillaClient = &bugzilla.Fake{}
	}

	var jiraClient jiraclient.Client
	if orgs, repos, _ := pluginAgent.Config().EnabledReposForPlugin(jira.PluginName); orgs != nil || repos != nil {
		client, err := o.jira.Client()
		if err != nil {
			logrus.WithError(err).Fatal("Failed to construct Jira Client")
		}
		jiraClient = client
	}

	infrastructureClient, err := o.kubernetes.InfrastructureClusterClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Kubernetes client for infrastructure cluster.")
	}

	buildClusterCoreV1Clients, err := o.kubernetes.BuildClusterCoreV1Clients(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Kubernetes clients for build cluster.")
	}

	prowJobClient, err := o.kubernetes.ProwJobClient(configAgent.Config().ProwJobNamespace, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting ProwJob client for infrastructure cluster.")
	}

	var slackClient *slack.Client
	if !o.dryRun && string(secret.GetSecret(o.slackTokenFile)) != "" {
		logrus.Info("Using real slack client.")
		slackClient = slack.NewClient(secret.GetTokenGenerator(o.slackTokenFile))
	}
	if slackClient == nil {
		logrus.Info("Using fake slack client.")
		slackClient = slack.NewFakeClient()
	}

	mdYAMLEnabled := func(org, repo string) bool {
		return pluginAgent.Config().MDYAMLEnabled(org, repo)
	}
	skipCollaborators := func(org, repo string) bool {
		return pluginAgent.Config().SkipCollaborators(org, repo)
	}
	ownersDirDenylist := func() *config.OwnersDirDenylist {
		// OwnersDirDenylist struct contains some defaults that's required by all
		// repos, so this function cannot return nil
		res := &config.OwnersDirDenylist{}
		if l := configAgent.Config().OwnersDirDenylist; l != nil {
			res = l
		}
		return res
	}
	resolver := func(org, repo string) ownersconfig.Filenames {
		return pluginAgent.Config().OwnersFilenames(org, repo)
	}
	ownersClient := repoowners.NewClient(gitClient, githubClient, mdYAMLEnabled, skipCollaborators, ownersDirDenylist, resolver)

	clientAgent := &plugins.ClientAgent{
		GitHubClient:              githubClient,
		ProwJobClient:             prowJobClient,
		KubernetesClient:          infrastructureClient,
		BuildClusterCoreV1Clients: buildClusterCoreV1Clients,
		GitClient:                 gitClient,
		SlackClient:               slackClient,
		OwnersClient:              ownersClient,
		BugzillaClient:            bugzillaClient,
		JiraClient:                jiraClient,
	}

	promMetrics := githubeventserver.NewMetrics()

	defer interrupts.WaitForGracefulShutdown()

	// Expose prometheus metrics
	metrics.ExposeMetrics("hook", configAgent.Config().PushGateway, o.instrumentationOptions.MetricsPort)
	pprof.Instrument(o.instrumentationOptions)

	server := &hook.Server{
		ClientAgent:    clientAgent,
		ConfigAgent:    configAgent,
		Plugins:        pluginAgent,
		Metrics:        promMetrics,
		RepoEnabled:    o.githubEnablement.EnablementChecker(),
		TokenGenerator: secret.GetTokenGenerator(o.webhookSecretFile),
	}
	interrupts.OnInterrupt(func() {
		server.GracefulShutdown()
		if err := gitClient.Clean(); err != nil {
			logrus.WithError(err).Error("Could not clean up git client cache.")
		}
	})

	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)

	hookMux := http.NewServeMux()
	// TODO remove this health endpoint when the migration to health endpoint is done
	// Return 200 on / for health checks.
	hookMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})

	// For /hook, handle a webhook normally.
	hookMux.Handle(o.webhookPath, server)
	// Serve plugin help information from /plugin-help.
	hookMux.Handle("/plugin-help", pluginhelp.NewHelpAgent(pluginAgent, githubClient))

	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: hookMux}

	health.ServeReady()

	interrupts.ListenAndServe(httpServer, o.gracePeriod)
}
