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

package plugins

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/test-infra/pkg/genyaml"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/yaml"

	"k8s.io/test-infra/prow/bugzilla"
	prowv1 "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	"k8s.io/test-infra/prow/commentpruner"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/jira"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/repoowners"
	"k8s.io/test-infra/prow/slack"
	"k8s.io/test-infra/prow/version"
)

var (
	pluginHelp                 = map[string]HelpProvider{}
	genericCommentHandlers     = map[string]GenericCommentHandler{}
	issueHandlers              = map[string]IssueHandler{}
	issueCommentHandlers       = map[string]IssueCommentHandler{}
	pullRequestHandlers        = map[string]PullRequestHandler{}
	pushEventHandlers          = map[string]PushEventHandler{}
	reviewEventHandlers        = map[string]ReviewEventHandler{}
	reviewCommentEventHandlers = map[string]ReviewCommentEventHandler{}
	statusEventHandlers        = map[string]StatusEventHandler{}
	// CommentMap is used by many plugins for printing help messages defined in
	// config.go.
	CommentMap, _ = genyaml.NewCommentMap(nil)

	//go:embed config.go
	embededConfigGoFileContent []byte
)

func init() {
	// This requires the source code to be present and to be in the right relative
	// location to the working directory. Don't even bother to try outside of the
	// hook binary, otherwise all components that load the plugin config initially
	// show an error which is confusing.
	if version.Name != "hook" {
		return
	}

	if cm, err := genyaml.NewCommentMap(map[string][]byte{"prow/plugins/config.go": embededConfigGoFileContent}); err == nil {
		CommentMap = cm
	} else {
		logrus.WithError(err).Error("Failed to initialize commentMap")
	}
}

// HelpProvider defines the function type that construct a pluginhelp.PluginHelp for enabled
// plugins. It takes into account the plugins configuration and enabled repositories.
type HelpProvider func(config *Configuration, enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error)

// HelpProviders returns the map of registered plugins with their associated HelpProvider.
func HelpProviders() map[string]HelpProvider {
	return pluginHelp
}

// IssueHandler defines the function contract for a github.IssueEvent handler.
type IssueHandler func(Agent, github.IssueEvent) error

// RegisterIssueHandler registers a plugin's github.IssueEvent handler.
func RegisterIssueHandler(name string, fn IssueHandler, help HelpProvider) {
	pluginHelp[name] = help
	issueHandlers[name] = fn
}

// IssueCommentHandler defines the function contract for a github.IssueCommentEvent handler.
type IssueCommentHandler func(Agent, github.IssueCommentEvent) error

// RegisterIssueCommentHandler registers a plugin's github.IssueCommentEvent handler.
func RegisterIssueCommentHandler(name string, fn IssueCommentHandler, help HelpProvider) {
	pluginHelp[name] = help
	issueCommentHandlers[name] = fn
}

// PullRequestHandler defines the function contract for a github.PullRequestEvent handler.
type PullRequestHandler func(Agent, github.PullRequestEvent) error

// RegisterPullRequestHandler registers a plugin's github.PullRequestEvent handler.
func RegisterPullRequestHandler(name string, fn PullRequestHandler, help HelpProvider) {
	pluginHelp[name] = help
	pullRequestHandlers[name] = fn
}

// StatusEventHandler defines the function contract for a github.StatusEvent handler.
type StatusEventHandler func(Agent, github.StatusEvent) error

// RegisterStatusEventHandler registers a plugin's github.StatusEvent handler.
func RegisterStatusEventHandler(name string, fn StatusEventHandler, help HelpProvider) {
	pluginHelp[name] = help
	statusEventHandlers[name] = fn
}

// PushEventHandler defines the function contract for a github.PushEvent handler.
type PushEventHandler func(Agent, github.PushEvent) error

// RegisterPushEventHandler registers a plugin's github.PushEvent handler.
func RegisterPushEventHandler(name string, fn PushEventHandler, help HelpProvider) {
	pluginHelp[name] = help
	pushEventHandlers[name] = fn
}

// ReviewEventHandler defines the function contract for a github.ReviewEvent handler.
type ReviewEventHandler func(Agent, github.ReviewEvent) error

// RegisterReviewEventHandler registers a plugin's github.ReviewEvent handler.
func RegisterReviewEventHandler(name string, fn ReviewEventHandler, help HelpProvider) {
	pluginHelp[name] = help
	reviewEventHandlers[name] = fn
}

// ReviewCommentEventHandler defines the function contract for a github.ReviewCommentEvent handler.
type ReviewCommentEventHandler func(Agent, github.ReviewCommentEvent) error

// RegisterReviewCommentEventHandler registers a plugin's github.ReviewCommentEvent handler.
func RegisterReviewCommentEventHandler(name string, fn ReviewCommentEventHandler, help HelpProvider) {
	pluginHelp[name] = help
	reviewCommentEventHandlers[name] = fn
}

// GenericCommentHandler defines the function contract for a github.GenericCommentEvent handler.
type GenericCommentHandler func(Agent, github.GenericCommentEvent) error

// RegisterGenericCommentHandler registers a plugin's github.GenericCommentEvent handler.
func RegisterGenericCommentHandler(name string, fn GenericCommentHandler, help HelpProvider) {
	pluginHelp[name] = help
	genericCommentHandlers[name] = fn
}

type PluginGitHubClient interface {
	github.Client
	Query(ctx context.Context, q interface{}, vars map[string]interface{}) error
}

// Agent may be used concurrently, so each entry must be thread-safe.
type Agent struct {
	GitHubClient              PluginGitHubClient
	ProwJobClient             prowv1.ProwJobInterface
	KubernetesClient          kubernetes.Interface
	BuildClusterCoreV1Clients map[string]corev1.CoreV1Interface
	GitClient                 git.ClientFactory
	SlackClient               *slack.Client
	BugzillaClient            bugzilla.Client
	JiraClient                jira.Client

	OwnersClient repoowners.Interface

	// Metrics exposes metrics that can be updated by plugins
	Metrics *Metrics

	// Config provides information about the jobs
	// that we know how to run for repos.
	Config *config.Config
	// PluginConfig provides plugin-specific options
	PluginConfig *Configuration

	Logger *logrus.Entry

	// may be nil if not initialized
	commentPruner *commentpruner.EventClient
}

// NewAgent bootstraps a new config.Agent struct from the passed dependencies.
func NewAgent(configAgent *config.Agent, pluginConfigAgent *ConfigAgent, clientAgent *ClientAgent, githubOrg string, metrics *Metrics, logger *logrus.Entry, plugin string) Agent {
	logger = logger.WithField("plugin", plugin)
	prowConfig := configAgent.Config()
	pluginConfig := pluginConfigAgent.Config()
	gitHubClient := &githubV4OrgAddingWrapper{org: githubOrg, Client: clientAgent.GitHubClient.WithFields(logger.Data).ForPlugin(plugin)}
	jiraClient := clientAgent.JiraClient
	if jiraClient != nil {
		jiraClient = clientAgent.JiraClient.WithFields(logger.Data).ForPlugin(plugin)
	}
	return Agent{
		GitHubClient:              gitHubClient,
		KubernetesClient:          clientAgent.KubernetesClient,
		BuildClusterCoreV1Clients: clientAgent.BuildClusterCoreV1Clients,
		ProwJobClient:             clientAgent.ProwJobClient,
		GitClient:                 clientAgent.GitClient,
		SlackClient:               clientAgent.SlackClient,
		OwnersClient:              clientAgent.OwnersClient.WithFields(logger.Data).WithGitHubClient(gitHubClient).ForPlugin(plugin),
		BugzillaClient:            clientAgent.BugzillaClient.WithFields(logger.Data).ForPlugin(plugin),
		JiraClient:                jiraClient,
		Metrics:                   metrics,
		Config:                    prowConfig,
		PluginConfig:              pluginConfig,
		Logger:                    logger,
	}
}

// InitializeCommentPruner attaches a commentpruner.EventClient to the agent to handle
// pruning comments.
func (a *Agent) InitializeCommentPruner(org, repo string, pr int) {
	a.commentPruner = commentpruner.NewEventClient(
		a.GitHubClient, a.Logger.WithField("client", "commentpruner"),
		org, repo, pr,
	)
}

// TookAction indicates whether any client with implemented Used() function was used
func (a *Agent) TookAction() bool {
	jiraClientTookAction := false
	if a.JiraClient != nil {
		jiraClientTookAction = a.JiraClient.Used()
	}
	return a.GitHubClient.Used() || a.OwnersClient.Used() || a.BugzillaClient.Used() || jiraClientTookAction
}

// CommentPruner will return the commentpruner.EventClient attached to the agent or an error
// if one is not attached.
func (a *Agent) CommentPruner() (*commentpruner.EventClient, error) {
	if a.commentPruner == nil {
		return nil, errors.New("comment pruner client never initialized")
	}
	return a.commentPruner, nil
}

// ClientAgent contains the various clients that are attached to the Agent.
type ClientAgent struct {
	GitHubClient              github.Client
	ProwJobClient             prowv1.ProwJobInterface
	KubernetesClient          kubernetes.Interface
	BuildClusterCoreV1Clients map[string]corev1.CoreV1Interface
	GitClient                 git.ClientFactory
	SlackClient               *slack.Client
	OwnersClient              repoowners.Interface
	BugzillaClient            bugzilla.Client
	JiraClient                jira.Client
}

// ConfigAgent contains the agent mutex and the Agent configuration.
type ConfigAgent struct {
	mut           sync.Mutex
	configuration *Configuration
}

func NewFakeConfigAgent() ConfigAgent {
	return ConfigAgent{configuration: &Configuration{}}
}

// Load attempts to load config from the path. It returns an error if either
// the file can't be read or the configuration is invalid.
// If checkUnknownPlugins is true, unrecognized plugin names will make config
// loading fail.
// If skipResolveConfigUpdater is true, the ConfigUpdater of the config will not be resolved.
func (pa *ConfigAgent) Load(path string, supplementalPluginConfigDirs []string, supplementalPluginConfigFileSuffix string, checkUnknownPlugins, skipResolveConfigUpdater bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	np := &Configuration{}
	if err := yaml.Unmarshal(b, np); err != nil {
		return err
	}

	var errs []error
	for _, supplementalPluginConfigDir := range supplementalPluginConfigDirs {
		if supplementalPluginConfigFileSuffix == "" {
			break
		}
		if err := filepath.Walk(supplementalPluginConfigDir, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Kubernetes configmap mounts create symlinks for the configmap keys that point to files prefixed with '..'.
			// This allows it to do  atomic changes by changing the symlink to a new target when the configmap content changes.
			// This means that we should ignore the '..'-prefixed files, otherwise we might end up reading a half-written file and will
			// get duplicate data.
			if strings.HasPrefix(info.Name(), "..") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if info.IsDir() || !strings.HasSuffix(path, supplementalPluginConfigFileSuffix) {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to read %s: %w", path, err))
				return nil
			}

			cfg := &Configuration{}
			if err := yaml.Unmarshal(data, cfg); err != nil {
				errs = append(errs, fmt.Errorf("failed to unmarshal %s: %w", path, err))
				return nil
			}

			if err := np.mergeFrom(cfg); err != nil {
				errs = append(errs, fmt.Errorf("failed to merge config from %s into main config: %w", path, err))
			}

			return nil

		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to walk %s: %w", supplementalPluginConfigDir, err))
		}
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		return err
	}

	if err := np.Validate(); err != nil {
		return err
	}
	if checkUnknownPlugins {
		if err := np.ValidatePluginsUnknown(); err != nil {
			return err
		}
	}
	if !skipResolveConfigUpdater {
		if err := np.ConfigUpdater.resolve(); err != nil {
			return err
		}
	}

	pa.Set(np)
	return nil
}

// Config returns the agent current Configuration.
func (pa *ConfigAgent) Config() *Configuration {
	pa.mut.Lock()
	defer pa.mut.Unlock()
	return pa.configuration
}

// Set attempts to set the plugins that are enabled on repos. Plugins are listed
// as a map from repositories to the list of plugins that are enabled on them.
// Specifying simply an org name will also work, and will enable the plugin on
// all repos in the org.
func (pa *ConfigAgent) Set(pc *Configuration) {
	pa.mut.Lock()
	defer pa.mut.Unlock()
	pa.configuration = pc
}

// Start starts polling path for plugin config. If the first attempt fails,
// then start returns the error. Future errors will halt updates but not stop.
// If checkUnknownPlugins is true, unrecognized plugin names will make config
// loading fail.
func (pa *ConfigAgent) Start(path string, supplementalPluginConfigDirs []string, supplementalPluginConfigFileSuffix string, checkUnknownPlugins, skipResolveConfigUpdater bool) error {
	if err := pa.Load(path, supplementalPluginConfigDirs, supplementalPluginConfigFileSuffix, checkUnknownPlugins, skipResolveConfigUpdater); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Minute)
	go func() {
		for range ticker.C {
			if err := pa.Load(path, supplementalPluginConfigDirs, supplementalPluginConfigFileSuffix, checkUnknownPlugins, skipResolveConfigUpdater); err != nil {
				logrus.WithField("path", path).WithError(err).Error("Error loading plugin config.")
			}
		}
	}()
	return nil
}

// GenericCommentHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) GenericCommentHandlers(owner, repo string) map[string]GenericCommentHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]GenericCommentHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := genericCommentHandlers[p]; ok {
			hs[p] = h
		}
	}
	return hs
}

// IssueHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) IssueHandlers(owner, repo string) map[string]IssueHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]IssueHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := issueHandlers[p]; ok {
			hs[p] = h
		}
	}
	return hs
}

// IssueCommentHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) IssueCommentHandlers(owner, repo string) map[string]IssueCommentHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]IssueCommentHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := issueCommentHandlers[p]; ok {
			hs[p] = h
		}
	}

	return hs
}

// PullRequestHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) PullRequestHandlers(owner, repo string) map[string]PullRequestHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]PullRequestHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := pullRequestHandlers[p]; ok {
			hs[p] = h
		}
	}

	return hs
}

// ReviewEventHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) ReviewEventHandlers(owner, repo string) map[string]ReviewEventHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]ReviewEventHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := reviewEventHandlers[p]; ok {
			hs[p] = h
		}
	}

	return hs
}

// ReviewCommentEventHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) ReviewCommentEventHandlers(owner, repo string) map[string]ReviewCommentEventHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]ReviewCommentEventHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := reviewCommentEventHandlers[p]; ok {
			hs[p] = h
		}
	}

	return hs
}

// StatusEventHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) StatusEventHandlers(owner, repo string) map[string]StatusEventHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]StatusEventHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := statusEventHandlers[p]; ok {
			hs[p] = h
		}
	}

	return hs
}

// PushEventHandlers returns a map of plugin names to handlers for the repo.
func (pa *ConfigAgent) PushEventHandlers(owner, repo string) map[string]PushEventHandler {
	pa.mut.Lock()
	defer pa.mut.Unlock()

	hs := map[string]PushEventHandler{}
	for _, p := range pa.getPlugins(owner, repo) {
		if h, ok := pushEventHandlers[p]; ok {
			hs[p] = h
		}
	}

	return hs
}

// getPlugins returns a list of plugins that are enabled on a given (org, repository).
func (pa *ConfigAgent) getPlugins(owner, repo string) []string {
	var plugins []string

	fullName := fmt.Sprintf("%s/%s", owner, repo)
	if !sets.NewString(pa.configuration.Plugins[owner].ExcludedRepos...).Has(repo) {
		plugins = append(plugins, pa.configuration.Plugins[owner].Plugins...)
	}
	plugins = append(plugins, pa.configuration.Plugins[fullName].Plugins...)

	return plugins
}

// EventsForPlugin returns the registered events for the passed plugin.
func EventsForPlugin(name string) []string {
	var events []string
	if _, ok := issueHandlers[name]; ok {
		events = append(events, "issue")
	}
	if _, ok := issueCommentHandlers[name]; ok {
		events = append(events, "issue_comment")
	}
	if _, ok := pullRequestHandlers[name]; ok {
		events = append(events, "pull_request")
	}
	if _, ok := pushEventHandlers[name]; ok {
		events = append(events, "push")
	}
	if _, ok := reviewEventHandlers[name]; ok {
		events = append(events, "pull_request_review")
	}
	if _, ok := reviewCommentEventHandlers[name]; ok {
		events = append(events, "pull_request_review_comment")
	}
	if _, ok := statusEventHandlers[name]; ok {
		events = append(events, "status")
	}
	if _, ok := genericCommentHandlers[name]; ok {
		events = append(events, "GenericCommentEvent (any event for user text)")
	}
	return events
}

var configMapSizeGauges = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "prow_configmap_size_bytes",
	Help: "Size of data fields in ConfigMaps updated automatically by Prow in bytes.",
}, []string{"name", "namespace"})

func init() {
	prometheus.MustRegister(configMapSizeGauges)
}

// Metrics is a set of metrics that are gathered by plugins.
// It is up the consumers of these metrics to ensure that they
// update the values in a thread-safe manner.
type Metrics struct {
	ConfigMapGauges *prometheus.GaugeVec
}

// NewMetrics returns a reference to the metrics plugins manage
func NewMetrics() *Metrics {
	return &Metrics{
		ConfigMapGauges: configMapSizeGauges,
	}
}

type githubV4OrgAddingWrapper struct {
	org string
	github.Client
}

func (c *githubV4OrgAddingWrapper) Query(ctx context.Context, q interface{}, args map[string]interface{}) error {
	return c.QueryWithGitHubAppsSupport(ctx, q, args, c.org)
}
