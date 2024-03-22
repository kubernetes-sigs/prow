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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	stdio "io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	gerritsource "k8s.io/test-infra/prow/gerrit/source"
	"k8s.io/test-infra/prow/io/providers"
	"k8s.io/test-infra/prow/tide"

	"github.com/NYTimes/gziphandler"
	"github.com/gorilla/csrf"
	"github.com/gorilla/sessions"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	pkgFlagutil "k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/pjutil/pprof"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/yaml"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowv1 "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/deck/jobs"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	pluginsflagutil "k8s.io/test-infra/prow/flagutil/plugins"
	"k8s.io/test-infra/prow/git/v2"
	prowgithub "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/githuboauth"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/io"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/plugins"
	"k8s.io/test-infra/prow/prstatus"
	"k8s.io/test-infra/prow/simplifypath"
	"k8s.io/test-infra/prow/spyglass"
	spyglassapi "k8s.io/test-infra/prow/spyglass/api"
	"k8s.io/test-infra/prow/spyglass/lenses/common"

	// Import standard spyglass viewers

	"k8s.io/test-infra/prow/spyglass/lenses"
	_ "k8s.io/test-infra/prow/spyglass/lenses/buildlog"
	_ "k8s.io/test-infra/prow/spyglass/lenses/coverage"
	_ "k8s.io/test-infra/prow/spyglass/lenses/html"
	_ "k8s.io/test-infra/prow/spyglass/lenses/junit"
	_ "k8s.io/test-infra/prow/spyglass/lenses/links"
	_ "k8s.io/test-infra/prow/spyglass/lenses/metadata"
	_ "k8s.io/test-infra/prow/spyglass/lenses/podinfo"
	_ "k8s.io/test-infra/prow/spyglass/lenses/restcoverage"
)

// Omittable ProwJob fields.
const (
	// Annotations maps to the serialized value of <ProwJob>.Annotations.
	Annotations string = "annotations"
	// Labels maps to the serialized value of <ProwJob>.Labels.
	Labels string = "labels"
	// DecorationConfig maps to the serialized value of <ProwJob>.Spec.DecorationConfig.
	DecorationConfig string = "decoration_config"
	// PodSpec maps to the serialized value of <ProwJob>.Spec.PodSpec.
	PodSpec string = "pod_spec"

	defaultStaticFilesLocation   = "/static"
	defaultTemplateFilesLocation = "/template"
	defaultSpyglassFilesLocation = "/lenses"

	defaultPRHistLinkTemplate = "/pr-history?org={{.Org}}&repo={{.Repo}}&pr={{.Number}}"
)

type options struct {
	config                configflagutil.ConfigOptions
	pluginsConfig         pluginsflagutil.PluginOptions
	instrumentation       prowflagutil.InstrumentationOptions
	kubernetes            prowflagutil.KubernetesOptions
	github                prowflagutil.GitHubOptions
	tideURL               string
	hookURL               string
	oauthURL              string
	githubOAuthConfigFile string
	cookieSecretFile      string
	redirectHTTPTo        string
	hiddenOnly            bool
	pregeneratedData      string
	staticFilesLocation   string
	templateFilesLocation string
	showHidden            bool
	spyglass              bool
	spyglassFilesLocation string
	storage               prowflagutil.StorageClientOptions
	gcsCookieAuth         bool
	rerunCreatesJob       bool
	allowInsecure         bool
	controllerManager     prowflagutil.ControllerManagerOptions
	dryRun                bool
	tenantIDs             prowflagutil.Strings
}

func (o *options) Validate() error {
	for _, group := range []pkgFlagutil.OptionGroup{&o.kubernetes, &o.github, &o.config, &o.pluginsConfig, &o.controllerManager} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}

	if o.oauthURL != "" {
		if o.githubOAuthConfigFile == "" {
			return errors.New("an OAuth URL was provided but required flag --github-oauth-config-file was unset")
		}
		if o.cookieSecretFile == "" {
			return errors.New("an OAuth URL was provided but required flag --cookie-secret was unset")
		}
	}

	if (o.hiddenOnly && o.showHidden) || (o.tenantIDs.Strings() != nil && (o.hiddenOnly || o.showHidden)) {
		return errors.New("'--hidden-only', '--tenant-id', and '--show-hidden' are mutually exclusive, 'hidden-only' shows only hidden job, '--tenant-id' shows all jobs with matching ID and 'show-hidden' shows both hidden and non-hidden jobs")
	}
	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options
	fs.StringVar(&o.tideURL, "tide-url", "", "Path to tide. If empty, do not serve tide data.")
	fs.StringVar(&o.hookURL, "hook-url", "", "Path to hook plugin help endpoint.")
	fs.StringVar(&o.oauthURL, "oauth-url", "", "Path to deck user dashboard endpoint.")
	fs.StringVar(&o.githubOAuthConfigFile, "github-oauth-config-file", "/etc/github/secret", "Path to the file containing the GitHub App Client secret.")
	fs.StringVar(&o.cookieSecretFile, "cookie-secret", "", "Path to the file containing the cookie secret key.")
	// use when behind a load balancer
	fs.StringVar(&o.redirectHTTPTo, "redirect-http-to", "", "Host to redirect http->https to based on x-forwarded-proto == http.")
	// use when behind an oauth proxy
	fs.BoolVar(&o.hiddenOnly, "hidden-only", false, "Show only hidden jobs. Useful for serving hidden jobs behind an oauth proxy.")
	fs.StringVar(&o.pregeneratedData, "pregenerated-data", "", "Use API output from another prow instance. Used by the prow/cmd/deck/runlocal script")
	fs.BoolVar(&o.showHidden, "show-hidden", false, "Show all jobs, including hidden ones")
	fs.BoolVar(&o.spyglass, "spyglass", false, "Use Prow built-in job viewing instead of Gubernator")
	fs.StringVar(&o.spyglassFilesLocation, "spyglass-files-location", fmt.Sprintf("%s%s", os.Getenv("KO_DATA_PATH"), defaultSpyglassFilesLocation), "Location of the static files for spyglass.")
	fs.StringVar(&o.staticFilesLocation, "static-files-location", fmt.Sprintf("%s%s", os.Getenv("KO_DATA_PATH"), defaultStaticFilesLocation), "Path to the static files")
	fs.StringVar(&o.templateFilesLocation, "template-files-location", fmt.Sprintf("%s%s", os.Getenv("KO_DATA_PATH"), defaultTemplateFilesLocation), "Path to the template files")
	fs.BoolVar(&o.gcsCookieAuth, "gcs-cookie-auth", false, "Use storage.cloud.google.com instead of signed URLs")
	fs.BoolVar(&o.rerunCreatesJob, "rerun-creates-job", false, "Change the re-run option in Deck to actually create the job. **WARNING:** Only use this with non-public deck instances, otherwise strangers can DOS your Prow instance")
	fs.BoolVar(&o.allowInsecure, "allow-insecure", false, "Allows insecure requests for CSRF and GitHub oauth.")
	fs.BoolVar(&o.dryRun, "dry-run", false, "Whether or not to make mutating API calls to GitHub.")
	fs.Var(&o.tenantIDs, "tenant-id", "The tenantID(s) used by the ProwJobs that should be displayed by this instance of Deck. This flag can be repeated.")
	o.config.AddFlags(fs)
	o.instrumentation.AddFlags(fs)
	o.controllerManager.TimeoutListingProwJobsDefault = 30 * time.Second
	o.controllerManager.AddFlags(fs)
	o.kubernetes.AddFlags(fs)
	o.github.AddFlags(fs)
	o.github.AllowAnonymous = true
	o.github.AllowDirectAccess = true
	o.storage.AddFlags(fs)
	o.pluginsConfig.AddFlags(fs)
	fs.Parse(args)

	return o
}

func staticHandlerFromDir(dir string) http.Handler {
	return gziphandler.GzipHandler(handleCached(http.FileServer(http.Dir(dir))))
}

var (
	httpRequestDuration = metrics.HttpRequestDuration("deck", 0.005, 20)
	httpResponseSize    = metrics.HttpResponseSize("deck", 16384, 33554432)
	traceHandler        = metrics.TraceHandler(simplifier, httpRequestDuration, httpResponseSize)
)

type authCfgGetter func(*prowapi.ProwJobSpec) *prowapi.RerunAuthConfig

func init() {
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(httpResponseSize)
}

var simplifier = simplifypath.NewSimplifier(l("", // shadow element mimicing the root
	l(""),
	l("badge.svg"),
	l("command-help"),
	l("config"),
	l("data.js"),
	l("favicon.ico"),
	l("github-login",
		l("redirect")),
	l("github-link"),
	l("git-provider-link"),
	l("job-history",
		v("job")),
	l("log"),
	l("plugin-config"),
	l("plugin-help"),
	l("plugins"),
	l("pr"),
	l("pr-data.js"),
	l("pr-history"),
	l("prowjob"),
	l("prowjobs.js"),
	l("rerun"),
	l("spyglass",
		l("static",
			simplifypath.VGreedy("path")),
		l("lens",
			v("lens",
				v("job")),
		)),
	l("static",
		simplifypath.VGreedy("path")),
	l("tide"),
	l("tide-history"),
	l("tide-history.js"),
	l("tide.js"),
	l("view",
		v("job"),
		l("gs", v("bucket", l("logs", v("job", v("build"))))),
	),
))

// l and v keep the tree legible

func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

func v(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.V(fragment, children...)
}

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	defer interrupts.WaitForGracefulShutdown()
	pprof.Instrument(o.instrumentation)

	// setup config agent, pod log clients etc.
	configAgent, err := o.config.ConfigAgentWithAdditionals(&config.Agent{}, []func(*config.Config) error{spglassConfigDefaulting})
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}
	cfg := configAgent.Config
	disableClustersSet := sets.New[string](cfg().DisabledClusters...)
	o.kubernetes.SetDisabledClusters(disableClustersSet)

	var pluginAgent *plugins.ConfigAgent
	if o.pluginsConfig.PluginConfigPath != "" {
		pluginAgent, err = o.pluginsConfig.PluginAgent()
		if err != nil {
			logrus.WithError(err).Fatal("Error loading Prow plugin config.")
		}
	} else {
		logrus.Info("No plugins configuration was provided to deck. You must provide one to reuse /test checks for rerun")
	}
	metrics.ExposeMetrics("deck", cfg().PushGateway, o.instrumentation.MetricsPort)

	// signal to the world that we are healthy
	// this needs to be in a separate port as we don't start the
	// main server with the main mux until we're ready
	health := pjutil.NewHealthOnPort(o.instrumentation.HealthPort)

	mux := http.NewServeMux()
	// setup common handlers for local and deployed runs
	mux.Handle("/static/", http.StripPrefix("/static", staticHandlerFromDir(o.staticFilesLocation)))
	mux.Handle("/config", gziphandler.GzipHandler(handleConfig(cfg, logrus.WithField("handler", "/config"))))
	mux.Handle("/plugin-config", gziphandler.GzipHandler(handlePluginConfig(pluginAgent, logrus.WithField("handler", "/plugin-config"))))
	mux.Handle("/favicon.ico", gziphandler.GzipHandler(handleFavicon(o.staticFilesLocation, cfg)))

	// Set up handlers for template pages.
	mux.Handle("/pr", gziphandler.GzipHandler(handleSimpleTemplate(o, cfg, "pr.html", nil)))
	mux.Handle("/command-help", gziphandler.GzipHandler(handleSimpleTemplate(o, cfg, "command-help.html", nil)))
	mux.Handle("/plugin-help", http.RedirectHandler("/command-help", http.StatusMovedPermanently))
	mux.Handle("/tide", gziphandler.GzipHandler(handleSimpleTemplate(o, cfg, "tide.html", nil)))
	mux.Handle("/tide-history", gziphandler.GzipHandler(handleSimpleTemplate(o, cfg, "tide-history.html", nil)))
	mux.Handle("/plugins", gziphandler.GzipHandler(handleSimpleTemplate(o, cfg, "plugins.html", nil)))

	runLocal := o.pregeneratedData != ""

	var fallbackHandler func(http.ResponseWriter, *http.Request)
	var pjListingClient jobs.PJListingClient
	var githubClient deckGitHubClient
	var gitClient git.ClientFactory
	var podLogClients map[string]jobs.PodLogClient
	if runLocal {
		localDataHandler := staticHandlerFromDir(o.pregeneratedData)
		fallbackHandler = localDataHandler.ServeHTTP

		var fjc fakePjListingClientWrapper
		var pjs prowapi.ProwJobList
		staticPjsPath := path.Join(o.pregeneratedData, "prowjobs.json")
		content, err := os.ReadFile(staticPjsPath)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to read jobs from prowjobs.json.")
		}
		if err = json.Unmarshal(content, &pjs); err != nil {
			logrus.WithError(err).Fatal("Failed to unmarshal jobs from prowjobs.json.")
		}
		fjc.pjs = &pjs
		pjListingClient = &fjc
	} else {
		fallbackHandler = http.NotFound

		restCfg, err := o.kubernetes.InfrastructureClusterConfig(false)
		if err != nil {
			logrus.WithError(err).Fatal("Error getting infrastructure cluster config.")
		}
		mgr, err := manager.New(restCfg, manager.Options{
			Namespace:          cfg().ProwJobNamespace,
			MetricsBindAddress: "0",
			LeaderElection:     false},
		)
		if err != nil {
			logrus.WithError(err).Fatal("Error getting manager.")
		}
		// Force a cache for ProwJobs
		if _, err := mgr.GetCache().GetInformer(interrupts.Context(), &prowapi.ProwJob{}); err != nil {
			logrus.WithError(err).Fatal("Failed to get prowjob informer")
		}
		go func() {
			if err := mgr.Start(interrupts.Context()); err != nil {
				logrus.WithError(err).Fatal("Error starting manager.")
			} else {
				logrus.Info("Manager stopped gracefully.")
			}
		}()
		mgrSyncCtx, mgrSyncCtxCancel := context.WithTimeout(context.Background(), o.controllerManager.TimeoutListingProwJobs)
		defer mgrSyncCtxCancel()
		if synced := mgr.GetCache().WaitForCacheSync(mgrSyncCtx); !synced {
			logrus.Fatal("Timed out waiting for cachesync")
		}

		// The watch apimachinery doesn't support restarts, so just exit the binary if a kubeconfig changes
		// to make the kubelet restart us.
		if err := o.kubernetes.AddKubeconfigChangeCallback(func() {
			logrus.Info("Kubeconfig changed, exiting to trigger a restart")
			interrupts.Terminate()
		}); err != nil {
			logrus.WithError(err).Fatal("Failed to register kubeconfig change callback")
		}

		pjListingClient = &pjListingClientWrapper{mgr.GetClient()}

		// We use the GH client to resolve GH teams when determining who is permitted to rerun a job.
		// When inrepoconfig is enabled, both the GitHubClient and the gitClient are used to resolve
		// presubmits dynamically which we need for the PR history page.
		if o.github.TokenPath != "" || o.github.AppID != "" {
			githubClient, err = o.github.GitHubClient(o.dryRun)
			if err != nil {
				logrus.WithError(err).Fatal("Error getting GitHub client.")
			}
			gitClient, err = o.github.GitClientFactory("", &o.config.InRepoConfigCacheDirBase, o.dryRun, false)
			if err != nil {
				logrus.WithError(err).Fatal("Error getting Git client.")
			}
		} else {
			if len(cfg().InRepoConfig.Enabled) > 0 {
				logrus.Info(" --github-token-path not configured. InRepoConfigEnabled, but current configuration won't display full PR history")
			}
		}

		buildClusterClients, err := o.kubernetes.BuildClusterClients(cfg().PodNamespace, false)
		if err != nil {
			logrus.WithError(err).Fatal("Error getting Kubernetes client.")
		}

		podLogClients = make(map[string]jobs.PodLogClient)
		for clusterContext, client := range buildClusterClients {
			podLogClients[clusterContext] = &podLogClient{client: client}
		}
	}

	authCfgGetter := func(jobSpec *prowapi.ProwJobSpec) *prowapi.RerunAuthConfig {
		return cfg().Deck.GetRerunAuthConfig(jobSpec)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			fallbackHandler(w, r)
			return
		}
		indexHandler := handleSimpleTemplate(o, cfg, "index.html", struct {
			SpyglassEnabled bool
			ReRunCreatesJob bool
		}{
			SpyglassEnabled: o.spyglass,
			ReRunCreatesJob: o.rerunCreatesJob})
		indexHandler(w, r)
	})

	ja := jobs.NewJobAgent(context.Background(), pjListingClient, o.hiddenOnly, o.showHidden, o.tenantIDs.Strings(), podLogClients, cfg)
	ja.Start()

	// setup prod only handlers. These handlers can work with runlocal as long
	// as ja is properly mocked, more specifically pjListingClient inside ja
	mux.Handle("/data.js", gziphandler.GzipHandler(handleData(ja, logrus.WithField("handler", "/data.js"))))
	mux.Handle("/prowjobs.js", gziphandler.GzipHandler(handleProwJobs(ja, logrus.WithField("handler", "/prowjobs.js"))))
	mux.Handle("/badge.svg", gziphandler.GzipHandler(handleBadge(ja)))
	mux.Handle("/log", gziphandler.GzipHandler(handleLog(ja, logrus.WithField("handler", "/log"))))

	if o.spyglass {
		initSpyglass(cfg, o, mux, ja, githubClient, gitClient)
	}

	if runLocal {
		mux = localOnlyMain(cfg, o, mux)
	} else {
		mux = prodOnlyMain(cfg, pluginAgent, authCfgGetter, githubClient, o, mux)
	}

	// signal to the world that we're ready
	health.ServeReady()

	// cookie secret will be used for CSRF protection and should be exactly 32 bytes
	// we sometimes accept different lengths to stay backwards compatible
	var csrfToken []byte
	if o.cookieSecretFile != "" {
		cookieSecretRaw, err := loadToken(o.cookieSecretFile)
		if err != nil {
			logrus.WithError(err).Fatal("Could not read cookie secret file")
		}
		decodedSecret, err := base64.StdEncoding.DecodeString(string(cookieSecretRaw))
		if err != nil {
			logrus.WithError(err).Fatal("Error decoding cookie secret")
		}
		if len(decodedSecret) == 32 {
			csrfToken = decodedSecret
		}
		if len(decodedSecret) > 32 {
			logrus.Warning("Cookie secret should be exactly 32 bytes. Consider truncating the existing cookie to that length")
			hash := sha256.Sum256(decodedSecret)
			csrfToken = hash[:]
		}
		if len(decodedSecret) < 32 {
			if o.rerunCreatesJob {
				logrus.Fatal("Cookie secret must be exactly 32 bytes")
				return
			}
			logrus.Warning("Cookie secret should be exactly 32 bytes")
		}
	}

	// if we allow direct reruns, we must protect against CSRF in all post requests using the cookie secret as a token
	// for more information about CSRF, see https://github.com/kubernetes/test-infra/blob/master/prow/cmd/deck/csrf.md
	empty := prowapi.ProwJobSpec{}
	if o.rerunCreatesJob && csrfToken == nil && !authCfgGetter(&empty).IsAllowAnyone() {
		logrus.Fatal("Rerun creates job cannot be enabled without CSRF protection, which requires --cookie-secret to be exactly 32 bytes")
		return
	}

	if csrfToken != nil {
		CSRF := csrf.Protect(csrfToken, csrf.Path("/"), csrf.Secure(!o.allowInsecure))
		logrus.WithError(http.ListenAndServe(":8080", CSRF(traceHandler(mux)))).Fatal("ListenAndServe returned.")
		return
	}
	// setup done, actually start the server
	server := &http.Server{Addr: ":8080", Handler: traceHandler(mux)}
	interrupts.ListenAndServe(server, 5*time.Second)
}

// localOnlyMain contains logic used only when running locally, and is mutually exclusive with
// prodOnlyMain.
func localOnlyMain(cfg config.Getter, o options, mux *http.ServeMux) *http.ServeMux {
	mux.Handle("/github-login", gziphandler.GzipHandler(handleSimpleTemplate(o, cfg, "github-login.html", nil)))

	return mux
}

type podLogClient struct {
	client corev1.PodInterface
}

func (c *podLogClient) GetLogs(name, container string) ([]byte, error) {
	reader, err := c.client.GetLogs(name, &coreapi.PodLogOptions{Container: container}).Stream(context.TODO())
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return stdio.ReadAll(reader)
}

type pjListingClientWrapper struct {
	reader ctrlruntimeclient.Reader
}

func (w *pjListingClientWrapper) List(
	ctx context.Context,
	pjl *prowapi.ProwJobList,
	opts ...ctrlruntimeclient.ListOption) error {
	return w.reader.List(ctx, pjl, opts...)
}

// fakePjListingClientWrapper implements pjListingClient for runlocal
type fakePjListingClientWrapper struct {
	pjs *prowapi.ProwJobList
}

func (fjc *fakePjListingClientWrapper) List(ctx context.Context, pjl *prowapi.ProwJobList, lo ...ctrlruntimeclient.ListOption) error {
	*pjl = *fjc.pjs
	return nil
}

// prodOnlyMain contains logic only used when running deployed, not locally
func prodOnlyMain(cfg config.Getter, pluginAgent *plugins.ConfigAgent, authCfgGetter authCfgGetter, githubClient deckGitHubClient, o options, mux *http.ServeMux) *http.ServeMux {
	prowJobClient, err := o.kubernetes.ProwJobClient(cfg().ProwJobNamespace, false)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting ProwJob client for infrastructure cluster.")
	}

	// prowjob still needs prowJobClient for retrieving log
	mux.Handle("/prowjob", gziphandler.GzipHandler(handleProwJob(prowJobClient, logrus.WithField("handler", "/prowjob"))))

	if o.hookURL != "" {
		mux.Handle("/plugin-help.js",
			gziphandler.GzipHandler(handlePluginHelp(newHelpAgent(o.hookURL), logrus.WithField("handler", "/plugin-help.js"))))
	}

	// tide could potentially be mocked by static data
	if o.tideURL != "" {
		ta := &tideAgent{
			log:  logrus.WithField("agent", "tide"),
			path: o.tideURL,
			updatePeriod: func() time.Duration {
				return cfg().Deck.TideUpdatePeriod.Duration
			},
			hiddenRepos: func() []string {
				return cfg().Deck.HiddenRepos
			},
			hiddenOnly: o.hiddenOnly,
			showHidden: o.showHidden,
			tenantIDs:  sets.New[string](o.tenantIDs.Strings()...),
			cfg:        cfg,
		}
		go func() {
			ta.start()
			mux.Handle("/tide.js", gziphandler.GzipHandler(handleTidePools(cfg, ta, logrus.WithField("handler", "/tide.js"))))
			mux.Handle("/tide-history.js", gziphandler.GzipHandler(handleTideHistory(ta, logrus.WithField("handler", "/tide-history.js"))))
		}()
	}

	secure := !o.allowInsecure

	// Handles link to github
	mux.HandleFunc("/github-link", HandleGitHubLink(o.github.Host, secure))
	mux.HandleFunc("/git-provider-link", HandleGitProviderLink(o.github.Host, secure))

	// Enable Git OAuth feature if oauthURL is provided.
	var goa *githuboauth.Agent
	if o.oauthURL != "" {
		githubOAuthConfigRaw, err := loadToken(o.githubOAuthConfigFile)
		if err != nil {
			logrus.WithError(err).Fatal("Could not read github oauth config file.")
		}

		cookieSecretRaw, err := loadToken(o.cookieSecretFile)
		if err != nil {
			logrus.WithError(err).Fatal("Could not read cookie secret file.")
		}

		var githubOAuthConfig githuboauth.Config
		if err := yaml.Unmarshal(githubOAuthConfigRaw, &githubOAuthConfig); err != nil {
			logrus.WithError(err).Fatal("Error unmarshalling github oauth config")
		}
		if !isValidatedGitOAuthConfig(&githubOAuthConfig) {
			logrus.Fatal("Error invalid github oauth config")
		}

		decodedSecret, err := base64.StdEncoding.DecodeString(string(cookieSecretRaw))
		if err != nil {
			logrus.WithError(err).Fatal("Error decoding cookie secret")
		}
		if len(decodedSecret) == 0 {
			logrus.Fatal("Cookie secret should not be empty")
		}
		cookie := sessions.NewCookieStore(decodedSecret)
		githubOAuthConfig.InitGitHubOAuthConfig(cookie)

		goa = githuboauth.NewAgent(&githubOAuthConfig, logrus.WithField("client", "githuboauth"))
		oauthClient := githuboauth.NewClient(&oauth2.Config{
			ClientID:     githubOAuthConfig.ClientID,
			ClientSecret: githubOAuthConfig.ClientSecret,
			RedirectURL:  githubOAuthConfig.RedirectURL,
			Scopes:       githubOAuthConfig.Scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:  fmt.Sprintf("https://%s/login/oauth/authorize", o.github.Host),
				TokenURL: fmt.Sprintf("https://%s/login/oauth/access_token", o.github.Host),
			},
		})

		repos := sets.List(cfg().AllRepos)

		prStatusAgent := prstatus.NewDashboardAgent(repos, &githubOAuthConfig, logrus.WithField("client", "pr-status"))

		clientCreator := func(accessToken string) (prstatus.GitHubClient, error) {
			return o.github.GitHubClientWithAccessToken(accessToken)
		}
		mux.Handle("/pr-data.js", handleNotCached(
			prStatusAgent.HandlePrStatus(prStatusAgent, clientCreator)))
		// Handles login request.
		mux.Handle("/github-login", goa.HandleLogin(oauthClient, secure))
		// Handles redirect from GitHub OAuth server.
		mux.Handle("/github-login/redirect", goa.HandleRedirect(oauthClient, githuboauth.NewAuthenticatedUserIdentifier(&o.github), secure))
	}

	mux.Handle("/rerun", gziphandler.GzipHandler(handleRerun(cfg, prowJobClient, o.rerunCreatesJob, authCfgGetter, goa, githuboauth.NewAuthenticatedUserIdentifier(&o.github), githubClient, pluginAgent, logrus.WithField("handler", "/rerun"))))
	mux.Handle("/abort", gziphandler.GzipHandler(handleAbort(prowJobClient, authCfgGetter, goa, githuboauth.NewAuthenticatedUserIdentifier(&o.github), githubClient, pluginAgent, logrus.WithField("handler", "/abort"))))

	// optionally inject http->https redirect handler when behind loadbalancer
	if o.redirectHTTPTo != "" {
		redirectMux := http.NewServeMux()
		redirectMux.Handle("/", func(oldMux *http.ServeMux, host string) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-forwarded-proto") == "http" {
					redirectURL, err := url.Parse(r.URL.String())
					if err != nil {
						logrus.Errorf("Failed to parse URL: %s.", r.URL.String())
						http.Error(w, "Failed to perform https redirect.", http.StatusInternalServerError)
						return
					}
					redirectURL.Scheme = "https"
					redirectURL.Host = host
					http.Redirect(w, r, redirectURL.String(), http.StatusMovedPermanently)
				} else {
					oldMux.ServeHTTP(w, r)
				}
			}
		}(mux, o.redirectHTTPTo))
		mux = redirectMux
	}

	return mux
}

func initSpyglass(cfg config.Getter, o options, mux *http.ServeMux, ja *jobs.JobAgent, gitHubClient deckGitHubClient, gitClient git.ClientFactory) {
	ctx := context.TODO()
	opener, err := io.NewOpener(ctx, o.storage.GCSCredentialsFile, o.storage.S3CredentialsFile)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating opener")
	}
	sg := spyglass.New(ctx, ja, cfg, opener, o.gcsCookieAuth)
	sg.Start()

	mux.Handle("/spyglass/static/", http.StripPrefix("/spyglass/static", staticHandlerFromDir(o.spyglassFilesLocation)))
	mux.Handle("/spyglass/lens/", gziphandler.GzipHandler(http.StripPrefix("/spyglass/lens/", handleArtifactView(o, sg, cfg))))
	mux.Handle("/view/", gziphandler.GzipHandler(handleRequestJobViews(sg, cfg, o, logrus.WithField("handler", "/view"))))
	mux.Handle("/job-history/", gziphandler.GzipHandler(handleJobHistory(o, cfg, opener, logrus.WithField("handler", "/job-history"))))
	mux.Handle("/pr-history/", gziphandler.GzipHandler(handlePRHistory(o, cfg, opener, gitHubClient, gitClient, logrus.WithField("handler", "/pr-history"))))
	if err := initLocalLensHandler(cfg, o, sg); err != nil {
		logrus.WithError(err).Fatal("Failed to initialize local lens handler")
	}
}

func initLocalLensHandler(cfg config.Getter, o options, sg *spyglass.Spyglass) error {
	var localLenses []common.LensWithConfiguration
	for _, lfc := range cfg().Deck.Spyglass.Lenses {
		if !strings.HasPrefix(strings.TrimPrefix(lfc.RemoteConfig.Endpoint, "http://"), spyglassLocalLensListenerAddr) {
			continue
		}

		lens, err := lenses.GetLens(lfc.Lens.Name)
		if err != nil {
			return fmt.Errorf("couldn't find local lens %q: %w", lfc.Lens.Name, err)
		}
		localLenses = append(localLenses, common.LensWithConfiguration{
			Config: common.LensOpt{
				LensResourcesDir: lenses.ResourceDirForLens(o.spyglassFilesLocation, lfc.Lens.Name),
				LensName:         lfc.Lens.Name,
				LensTitle:        lfc.RemoteConfig.Title,
			},
			Lens: lens,
		})
	}

	lensServer, err := common.NewLensServer(spyglassLocalLensListenerAddr, sg.JobAgent, sg.StorageArtifactFetcher, sg.PodLogArtifactFetcher, cfg, localLenses)
	if err != nil {
		return fmt.Errorf("constructing local lens server: %w", err)
	}

	interrupts.ListenAndServe(lensServer, 5*time.Second)
	return nil
}

func loadToken(file string) ([]byte, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return []byte{}, err
	}
	return bytes.TrimSpace(raw), nil
}

func handleCached(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Since all static assets have a cache busting parameter
		// attached, which forces a reload whenever Deck is updated,
		// we can send strong cache headers.
		w.Header().Set("Cache-Control", "public, max-age=315360000") // 315360000 is 10 years, i.e. forever
		next.ServeHTTP(w, r)
	})
}

func setHeadersNoCaching(w http.ResponseWriter) {
	// This follows the "ignore IE6, but allow prehistoric HTTP/1.0-only proxies"
	// recommendation from https://stackoverflow.com/a/2068407 to prevent clients
	// from caching the HTTP response.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("Expires", "0")
}

func writeJSONResponse(w http.ResponseWriter, r *http.Request, d []byte) {
	// If we have a "var" query, then write out "var value = {...};".
	// Otherwise, just write out the JSON.
	if v := r.URL.Query().Get("var"); v != "" {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprintf(w, "var %s = %s;", v, string(d))
	} else {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, string(d))
	}
}

func handleNotCached(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		next.ServeHTTP(w, r)
	}
}

func handleProwJobs(ja *jobs.JobAgent, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		jobs := ja.ProwJobs()
		omit := r.URL.Query().Get("omit")

		if set := sets.New[string](strings.Split(omit, ",")...); set.Len() > 0 {
			for i := range jobs {
				jobs[i].ManagedFields = nil
				if set.Has(Annotations) {
					jobs[i].Annotations = nil
				}
				if set.Has(Labels) {
					jobs[i].Labels = nil
				}
				if set.Has(DecorationConfig) {
					jobs[i].Spec.DecorationConfig = nil
				}
				if set.Has(PodSpec) {
					// when we omit the podspec, we don't set it completely to nil
					// instead, we set it to a new podspec that just has an empty container for each container that exists in the actual podspec
					// this is so we can determine how many containers there are for a given prowjob without fetching all of the podspec details
					// this is necessary for prow/cmd/deck/static/prow/prow.ts to determine whether the logIcon should link to a log endpoint or to spyglass
					if jobs[i].Spec.PodSpec != nil {
						emptyContainers := []coreapi.Container{}
						for range jobs[i].Spec.PodSpec.Containers {
							emptyContainers = append(emptyContainers, coreapi.Container{})
						}
						jobs[i].Spec.PodSpec = &coreapi.PodSpec{
							Containers: emptyContainers,
						}
					}
				}
			}
		}

		jd, err := json.Marshal(struct {
			Items []prowapi.ProwJob `json:"items"`
		}{jobs})
		if err != nil {
			log.WithError(err).Error("Error marshaling jobs.")
			jd = []byte("{}")
		}
		writeJSONResponse(w, r, jd)
	}
}

func handleData(ja *jobs.JobAgent, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		jobs := ja.Jobs()
		jd, err := json.Marshal(jobs)
		if err != nil {
			log.WithError(err).Error("Error marshaling jobs.")
			jd = []byte("[]")
		}
		writeJSONResponse(w, r, jd)
	}
}

// handleBadge handles requests to get a badge for one or more jobs
// The url must look like this, where `jobs` is a comma-separated
// list of globs:
//
// /badge.svg?jobs=<glob>[,<glob2>]
//
// Examples:
// - /badge.svg?jobs=pull-kubernetes-bazel-build
// - /badge.svg?jobs=pull-kubernetes-*
// - /badge.svg?jobs=pull-kubernetes-e2e*,pull-kubernetes-*,pull-kubernetes-integration-*
func handleBadge(ja *jobs.JobAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		wantJobs := r.URL.Query().Get("jobs")
		if wantJobs == "" {
			http.Error(w, "missing jobs query parameter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")

		allJobs := ja.ProwJobs()
		_, _, svg := renderBadge(pickLatestJobs(allJobs, wantJobs))
		w.Write(svg)
	}
}

// handleJobHistory handles requests to get the history of a given job
// There is also a new format since we started supporting other storageProvider
// like s3 and not only GCS.
// The url must look like one of these for presubmits:
//
// - /job-history/<gcs-bucket-name>/pr-logs/directory/<job-name>
// - /job-history/<storage-provider>/<bucket-name>/pr-logs/directory/<job-name>
//
// Example:
// - /job-history/kubernetes-jenkins/pr-logs/directory/pull-test-infra-verify-gofmt
// - /job-history/gs/kubernetes-jenkins/pr-logs/directory/pull-test-infra-verify-gofmt
//
// For periodics or postsubmits, the url must look like one of these:
//
// - /job-history/<gcs-bucket-name>/logs/<job-name>
// - /job-history/<storage-provider>/<bucket-name>/logs/<job-name>
//
// Example:
// - /job-history/kubernetes-jenkins/logs/ci-kubernetes-e2e-prow-canary
// - /job-history/gs/kubernetes-jenkins/logs/ci-kubernetes-e2e-prow-canary
func handleJobHistory(o options, cfg config.Getter, opener io.Opener, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		tmpl, err := getJobHistory(r.Context(), r.URL, cfg, opener)
		if err != nil {
			msg := fmt.Sprintf("failed to get job history: %v", err)
			if shouldLogHTTPErrors(err) {
				log.WithField("url", r.URL.String()).WithError(err).Warn(msg)
			} else {
				log.WithField("url", r.URL.String()).WithError(err).Debug(msg)
			}
			http.Error(w, msg, httpStatusForError(err))
			return
		}
		for idx, build := range tmpl.Builds {
			tmpl.Builds[idx].Result = strings.ToUpper(build.Result)

		}
		handleSimpleTemplate(o, cfg, "job-history.html", tmpl)(w, r)
	}
}

// handlePRHistory handles requests to get the test history if a given PR
// The url must look like this:
//
// /pr-history?org=<org>&repo=<repo>&pr=<pr number>
func handlePRHistory(o options, cfg config.Getter, opener io.Opener, gitHubClient deckGitHubClient, gitClient git.ClientFactory, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		tmpl, err := getPRHistory(r.Context(), r.URL, cfg(), opener, gitHubClient, gitClient, o.github.Host)
		if err != nil {
			msg := fmt.Sprintf("failed to get PR history: %v", err)
			log.WithField("url", r.URL.String()).Info(msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
		for idx := range tmpl.Jobs {
			for jdx, build := range tmpl.Jobs[idx].Builds {
				tmpl.Jobs[idx].Builds[jdx].Result = strings.ToUpper(build.Result)
			}
		}
		handleSimpleTemplate(o, cfg, "pr-history.html", tmpl)(w, r)
	}
}

// handleRequestJobViews handles requests to get all available artifact views for a given job.
// The url must specify a storage key type, such as "prowjob" or "gcs":
//
// /view/<key-type>/<key>
//
// Examples:
// - /view/gcs/kubernetes-jenkins/pr-logs/pull/test-infra/9557/pull-test-infra-verify-gofmt/15688/
// - /view/prowjob/echo-test/1046875594609922048
func handleRequestJobViews(sg *spyglass.Spyglass, cfg config.Getter, o options, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		setHeadersNoCaching(w)
		src := strings.TrimPrefix(r.URL.Path, "/view/")

		csrfToken := csrf.Token(r)
		page, err := renderSpyglass(r.Context(), sg, cfg, src, o, csrfToken, log)
		if err != nil {
			msg := fmt.Sprintf("error rendering spyglass page: %v", err)
			if shouldLogHTTPErrors(err) {
				log.WithError(err).Debug(msg)
			}
			http.Error(w, msg, httpStatusForError(err))
			return
		}

		fmt.Fprint(w, page)
		elapsed := time.Since(start)
		log.WithFields(logrus.Fields{
			"duration": elapsed.String(),
			"endpoint": r.URL.Path,
			"source":   src,
		}).Info("Loading view completed.")
	}
}

// renderSpyglass returns a pre-rendered Spyglass page from the given source string
func renderSpyglass(ctx context.Context, sg *spyglass.Spyglass, cfg config.Getter, src string, o options, csrfToken string, log *logrus.Entry) (string, error) {
	renderStart := time.Now()

	src = strings.TrimSuffix(src, "/")
	realPath, err := sg.ResolveSymlink(src)
	if err != nil {
		return "", fmt.Errorf("error when resolving real path %s: %w", src, err)
	}
	src = realPath
	artifactNames, err := sg.ListArtifacts(ctx, src)
	if err != nil {
		return "", fmt.Errorf("error listing artifacts: %w", err)
	}
	if len(artifactNames) == 0 {
		log.Infof("found no artifacts for %s", src)
	}

	regexCache := cfg().Deck.Spyglass.RegexCache
	lensCache := map[int][]string{}
	var lensIndexes []int
lensesLoop:
	for i, lfc := range cfg().Deck.Spyglass.Lenses {
		matches := sets.Set[string]{}
		for _, re := range lfc.RequiredFiles {
			found := false
			for _, a := range artifactNames {
				if regexCache[re].MatchString(a) {
					matches.Insert(a)
					found = true
				}
			}
			if !found {
				continue lensesLoop
			}
		}

		for _, re := range lfc.OptionalFiles {
			for _, a := range artifactNames {
				if regexCache[re].MatchString(a) {
					matches.Insert(a)
				}
			}
		}

		lensCache[i] = sets.List(matches)
		lensIndexes = append(lensIndexes, i)
	}

	lensIndexes, ls := sg.Lenses(lensIndexes)

	jobHistLink := ""
	jobPath, err := sg.JobPath(src)
	if err == nil {
		jobHistLink = path.Join("/job-history", jobPath)
	}

	var prowJobLink string
	prowJob, prowJobName, prowJobState, err := sg.ProwJob(src)
	if err == nil {
		if prowJobName != "" {
			u, err := url.Parse("/prowjob")
			if err != nil {
				return "", fmt.Errorf("error parsing prowjob path: %w", err)
			}
			query := url.Values{}
			query.Set("prowjob", prowJobName)
			u.RawQuery = query.Encode()
			prowJobLink = u.String()
		}
	} else {
		log.WithError(err).Warningf("Error getting ProwJob name for source %q.", src)
	}

	prHistLink := ""
	org, repo, number, err := sg.RunToPR(src)
	if err == nil && !cfg().Deck.Spyglass.HidePRHistLink {
		prHistLinkTemplate := cfg().Deck.Spyglass.PRHistLinkTemplate
		if prHistLinkTemplate == "" { // Not defined globally
			prHistLinkTemplate = defaultPRHistLinkTemplate
		}
		prHistLink, err = prHistLinkFromTemplate(prHistLinkTemplate, org, repo, number)
		if err != nil {
			return "", err
		}
	}

	artifactsLink := ""
	bucket := ""
	if jobPath != "" && (strings.HasPrefix(jobPath, providers.GS) || strings.HasPrefix(jobPath, providers.S3)) {
		bucket = strings.Split(jobPath, "/")[1] // The provider (gs) will be in index 0, followed by the bucket name
	}
	gcswebPrefix := cfg().Deck.Spyglass.GetGCSBrowserPrefix(org, repo, bucket)
	if gcswebPrefix != "" {
		runPath, err := sg.RunPath(src)
		if err == nil {
			artifactsLink = gcswebPrefix + runPath
			// gcsweb wants us to end URLs with a trailing slash
			if !strings.HasSuffix(artifactsLink, "/") {
				artifactsLink += "/"
			}
		}
	}

	jobName, buildID, err := common.KeyToJob(src)
	if err != nil {
		return "", fmt.Errorf("error determining jobName / buildID: %w", err)
	}

	prLink := ""
	j, err := sg.JobAgent.GetProwJob(jobName, buildID)
	if err == nil && j.Spec.Refs != nil && len(j.Spec.Refs.Pulls) > 0 {
		prLink = j.Spec.Refs.Pulls[0].Link
	}

	announcement := ""
	if cfg().Deck.Spyglass.Announcement != "" {
		announcementTmpl, err := template.New("announcement").Parse(cfg().Deck.Spyglass.Announcement)
		if err != nil {
			return "", fmt.Errorf("error parsing announcement template: %w", err)
		}
		runPath, err := sg.RunPath(src)
		if err != nil {
			runPath = ""
		}
		var announcementBuf bytes.Buffer
		err = announcementTmpl.Execute(&announcementBuf, struct {
			ArtifactPath string
		}{
			ArtifactPath: runPath,
		})
		if err != nil {
			return "", fmt.Errorf("error executing announcement template: %w", err)
		}
		announcement = announcementBuf.String()
	}

	tgLink, err := sg.TestGridLink(src)
	if err != nil {
		tgLink = ""
	}

	extraLinks, err := sg.ExtraLinks(ctx, src)
	if err != nil {
		log.WithError(err).WithField("page", src).Warn("Failed to fetch extra links.")
		// This is annoying but not a fatal error, should keep going so that the
		// other infos fetched above are displayed to user.
		extraLinks = nil
	}

	var viewBuf bytes.Buffer
	type spyglassTemplate struct {
		Lenses          map[int]spyglass.LensConfig
		LensIndexes     []int
		Source          string
		LensArtifacts   map[int][]string
		JobHistLink     string
		ProwJobLink     string
		ArtifactsLink   string
		PRHistLink      string
		Announcement    template.HTML
		TestgridLink    string
		JobName         string
		BuildID         string
		PRLink          string
		ExtraLinks      []spyglass.ExtraLink
		ReRunCreatesJob bool
		ProwJob         string
		ProwJobName     string
		ProwJobState    string
	}
	sTmpl := spyglassTemplate{
		Lenses:          ls,
		LensIndexes:     lensIndexes,
		Source:          src,
		LensArtifacts:   lensCache,
		JobHistLink:     jobHistLink,
		ProwJobLink:     prowJobLink,
		ArtifactsLink:   artifactsLink,
		PRHistLink:      prHistLink,
		Announcement:    template.HTML(announcement),
		TestgridLink:    tgLink,
		JobName:         jobName,
		BuildID:         buildID,
		PRLink:          prLink,
		ExtraLinks:      extraLinks,
		ReRunCreatesJob: o.rerunCreatesJob,
		ProwJob:         prowJob,
		ProwJobName:     prowJobName,
		ProwJobState:    string(prowJobState),
	}
	t := template.New("spyglass.html")

	if _, err := prepareBaseTemplate(o, cfg, csrfToken, t); err != nil {
		return "", fmt.Errorf("error preparing base template: %w", err)
	}
	t, err = t.ParseFiles(path.Join(o.templateFilesLocation, "spyglass.html"))
	if err != nil {
		return "", fmt.Errorf("error parsing template: %w", err)
	}

	if err = t.Execute(&viewBuf, sTmpl); err != nil {
		return "", fmt.Errorf("error rendering template: %w", err)
	}
	renderElapsed := time.Since(renderStart)
	log.WithFields(logrus.Fields{
		"duration": renderElapsed.String(),
		"source":   src,
	}).Info("Rendered spyglass views.")
	return viewBuf.String(), nil
}

func prHistLinkFromTemplate(prHistLinkTemplate, org, repo string, number int) (string, error) {
	tmp, err := template.New("t").Parse(prHistLinkTemplate)
	if err != nil {
		return "", fmt.Errorf("failed compiling template %q: %v", prHistLinkTemplate, err)
	}
	tmpBuff := bytes.Buffer{}
	if err = tmp.Execute(&tmpBuff, struct {
		Org    string
		Repo   string
		Number int
	}{org, repo, number}); err != nil {
		return "", fmt.Errorf("failed executing template %q: %v", prHistLinkTemplate, err)
	}

	return tmpBuff.String(), nil
}

// handleArtifactView handles requests to load a single view for a job. This is what viewers
// will use to call back to themselves.
// Query params:
// - name: required, specifies the name of the viewer to load
// - src: required, specifies the job source from which to fetch artifacts
func handleArtifactView(o options, sg *spyglass.Spyglass, cfg config.Getter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		pathSegments := strings.Split(r.URL.Path, "/")
		if len(pathSegments) != 2 {
			http.NotFound(w, r)
			return
		}
		lensName := pathSegments[0]
		resource := pathSegments[1]

		var lens *config.LensFileConfig
		for _, configLens := range cfg().Deck.Spyglass.Lenses {
			if configLens.Lens.Name == lensName {

				// Directly followed by break, so this is ok
				// nolint: exportloopref
				lens = &configLens
				break
			}
		}
		if lens == nil {
			http.Error(w, fmt.Sprintf("No such template: %s", lensName), http.StatusNotFound)
			return
		}

		reqString := r.URL.Query().Get("req")
		var request spyglass.LensRequest
		if err := json.Unmarshal([]byte(reqString), &request); err != nil {
			http.Error(w, fmt.Sprintf("Failed to parse request: %v", err), http.StatusBadRequest)
			return
		}
		if err := validateStoragePath(cfg, request.Source); err != nil {
			http.Error(w, fmt.Sprintf("Failed to process request: %v", err), httpStatusForError(err))
			return
		}

		handleRemoteLens(*lens, w, r, resource, request)
	}
}

func handleRemoteLens(lens config.LensFileConfig, w http.ResponseWriter, r *http.Request, resource string, request spyglass.LensRequest) {
	var requestType spyglassapi.RequestAction
	switch resource {
	case "iframe":
		requestType = spyglassapi.RequestActionInitial
	case "rerender":
		requestType = spyglassapi.RequestActionRerender
	case "callback":
		requestType = spyglassapi.RequestActionCallBack
	default:
		http.NotFound(w, r)
		return
	}

	var data string
	if requestType != spyglassapi.RequestActionInitial {
		dataBytes, err := stdio.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusInternalServerError)
			return
		}
		data = string(dataBytes)
	}

	lensRequest := spyglassapi.LensRequest{
		Action:         requestType,
		Data:           data,
		Config:         lens.Lens.Config,
		ResourceRoot:   "/spyglass/static/" + lens.Lens.Name + "/",
		Artifacts:      request.Artifacts,
		ArtifactSource: request.Source,
		LensIndex:      request.Index,
	}
	serializedRequest, err := json.Marshal(lensRequest)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to marshal request to lens backend: %v", err), http.StatusInternalServerError)
		return
	}

	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL = lens.RemoteConfig.ParsedEndpoint
			r.ContentLength = int64(len(serializedRequest))
			r.Body = stdio.NopCloser(bytes.NewBuffer(serializedRequest))
		},
	}).ServeHTTP(w, r)
}

func handleTidePools(cfg config.Getter, ta *tideAgent, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		queryConfigs := ta.filterQueries(cfg().Tide.Queries)
		queries := make([]string, 0, len(queryConfigs))
		for _, qc := range queryConfigs {
			queries = append(queries, qc.Query())
		}

		ta.Lock()
		pools := ta.pools
		ta.Unlock()

		var poolsForDeck []tide.PoolForDeck
		for _, pool := range pools {
			poolsForDeck = append(poolsForDeck, *tide.PoolToPoolForDeck(&pool))
		}
		payload := tidePools{
			Queries:     queries,
			TideQueries: queryConfigs,
			Pools:       poolsForDeck,
		}
		pd, err := json.Marshal(payload)
		if err != nil {
			log.WithError(err).Error("Error marshaling payload.")
			pd = []byte("{}")
		}
		writeJSONResponse(w, r, pd)
	}
}

func handleTideHistory(ta *tideAgent, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)

		ta.Lock()
		history := ta.history
		ta.Unlock()

		payload := tideHistory{
			History: history,
		}
		pd, err := json.Marshal(payload)
		if err != nil {
			log.WithError(err).Error("Error marshaling payload.")
			pd = []byte("{}")
		}
		writeJSONResponse(w, r, pd)
	}
}

func handlePluginHelp(ha *helpAgent, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		help, err := ha.getHelp()
		if err != nil {
			log.WithError(err).Error("Getting plugin help from hook.")
			help = &pluginhelp.Help{}
		}
		b, err := json.Marshal(*help)
		if err != nil {
			log.WithError(err).Error("Marshaling plugin help.")
			b = []byte("[]")
		}
		writeJSONResponse(w, r, b)
	}
}

type logClient interface {
	GetJobLog(job, id, container string) ([]byte, error)
}

// TODO(spxtr): Cache, rate limit.
func handleLog(lc logClient, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setHeadersNoCaching(w)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		job := r.URL.Query().Get("job")
		id := r.URL.Query().Get("id")
		container := r.URL.Query().Get("container")
		if container == "" {
			container = kube.TestContainerName
		}
		logger := log.WithFields(logrus.Fields{"job": job, "id": id, "container": container})
		if err := validateLogRequest(r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jobLog, err := lc.GetJobLog(job, id, container)
		if err != nil {
			http.Error(w, fmt.Sprintf("Log not found: %v", err), http.StatusNotFound)
			logger := logger.WithError(err)
			msg := "Log not found."
			if strings.Contains(err.Error(), "PodInitializing") || strings.Contains(err.Error(), "not found") ||
				strings.Contains(err.Error(), "terminated") {
				// PodInitializing is really common and not something
				// that has any actionable items for administrators
				// monitoring logs, so we should log it as information.
				// Similarly, if a user asks us to proxy through logs
				// for a Pod or ProwJob that doesn't exit, it's not
				// something an administrator wants to see in logs.
				logger.Info(msg)
			} else {
				logger.Warning(msg)
			}
			return
		}
		if _, err = w.Write(jobLog); err != nil {
			logger.WithError(err).Warning("Error writing log.")
		}
	}
}

func validateLogRequest(r *http.Request) error {
	job := r.URL.Query().Get("job")
	id := r.URL.Query().Get("id")

	if job == "" {
		return errors.New("request did not provide the 'job' query parameter")
	}
	if id == "" {
		return errors.New("request did not provide the 'id' query parameter")
	}
	return nil
}

func handleProwJob(prowJobClient prowv1.ProwJobInterface, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("prowjob")
		l := log.WithField("prowjob", name)
		if name == "" {
			http.Error(w, "request did not provide the 'prowjob' query parameter", http.StatusBadRequest)
			return
		}

		pj, err := prowJobClient.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			http.Error(w, fmt.Sprintf("ProwJob not found: %v", err), http.StatusNotFound)
			if !kerrors.IsNotFound(err) {
				// admins only care about errors other than not found
				l.WithError(err).Debug("ProwJob not found.")
			}
			return
		}
		pj.ManagedFields = nil
		handleSerialize(w, "prowjob", pj, l)
	}
}

func handleSerialize(w http.ResponseWriter, name string, data interface{}, l *logrus.Entry) {
	setHeadersNoCaching(w)
	b, err := yaml.Marshal(data)
	if err != nil {
		msg := fmt.Sprintf("Error marshaling %q.", name)
		l.WithError(err).Error(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	buff := bytes.NewBuffer(b)
	_, err = buff.WriteTo(w)
	if err != nil {
		msg := fmt.Sprintf("Error writing %q.", name)
		l.WithError(err).Error(msg)
		http.Error(w, msg, http.StatusInternalServerError)
	}
}

func handleConfig(cfg config.Getter, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: add the ability to query for any portions of the config?
		k := r.URL.Query().Get("key")
		switch k {
		case "disabled-clusters":
			l := sets.New[string](cfg().DisabledClusters...).UnsortedList()
			sort.Strings(l)
			handleSerialize(w, "disabled-clusters.yaml", l, log)
		case "":
			handleSerialize(w, "config.yaml", cfg(), log)
		default:
			msg := fmt.Sprintf("getting config for key %s is not supported", k)
			log.Error(msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
	}
}

func handlePluginConfig(pluginAgent *plugins.ConfigAgent, log *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pluginAgent != nil {
			handleSerialize(w, "plugins.yaml", pluginAgent.Config(), log)
			return
		}
		msg := "Please use the --plugin-config flag to specify the location of the plugin config."
		log.Infof("Could not serve request. %s", msg)
		http.Error(w, msg, http.StatusInternalServerError)
	}
}

func handleFavicon(staticFilesLocation string, cfg config.Getter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		config := cfg()
		if config.Deck.Branding != nil && config.Deck.Branding.Favicon != "" {
			http.ServeFile(w, r, staticFilesLocation+"/"+config.Deck.Branding.Favicon)
		} else {
			http.ServeFile(w, r, staticFilesLocation+"/favicon.ico")
		}
	}
}

func HandleGitHubLink(githubHost string, secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if secure {
			scheme = "https"
		}
		redirectURL := scheme + "://" + githubHost + "/" + r.URL.Query().Get("dest")
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// HandleGenericProviderLink returns link based on different providers.
func HandleGitProviderLink(githubHost string, secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var redirectURL string

		vals := r.URL.Query()
		target := vals.Get("target")
		repo, branch, number, commit, author := vals.Get("repo"), vals.Get("branch"), vals.Get("number"), vals.Get("commit"), vals.Get("author")
		// repo could be passed in with single quote, as it might contains https://
		repo = strings.Trim(repo, "'")
		if gerritsource.IsGerritOrg(repo) {
			org, repo, err := gerritsource.OrgRepoFromCloneURI(repo)
			if err != nil {
				logrus.WithError(err).WithField("cloneURI", repo).Warn("Failed resolve org and repo from cloneURI.")
				http.Redirect(w, r, "", http.StatusNotFound)
				return
			}
			orgCodeURL, err := gerritsource.CodeURL(org)
			if err != nil {
				logrus.WithError(err).WithField("cloneURI", repo).Warn("Failed deriving source code URL from cloneURI.")
				http.Redirect(w, r, "", http.StatusNotFound)
				return
			}
			switch target {
			case "commit":
				fallthrough
			case "prcommit":
				redirectURL = orgCodeURL + "/" + repo + "/+/" + commit
			case "branch":
				redirectURL = orgCodeURL + "/" + repo + "/+/refs/heads/" + branch
			case "pr":
				redirectURL = org + "/c/" + repo + "/+/" + number
			}
		} else {
			scheme := "http"
			if secure {
				scheme = "https"
			}
			prefix := scheme + "://" + githubHost + "/"
			switch target {
			case "commit":
				redirectURL = prefix + repo + "/commit/" + commit
			case "branch":
				redirectURL = prefix + repo + "/tree/" + branch
			case "pr":
				redirectURL = prefix + repo + "/pull/" + number
			case "prcommit":
				redirectURL = prefix + repo + "/pull/" + number + "/" + commit
			case "author":
				redirectURL = prefix + author
			}
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

func isValidatedGitOAuthConfig(githubOAuthConfig *githuboauth.Config) bool {
	return githubOAuthConfig.ClientID != "" && githubOAuthConfig.ClientSecret != "" &&
		githubOAuthConfig.RedirectURL != ""
}

type deckGitHubClient interface {
	prowgithub.RerunClient
	GetPullRequest(org, repo string, number int) (*prowgithub.PullRequest, error)
	GetRef(org, repo, ref string) (string, error)
	BotUserChecker() (func(candidate string) bool, error)
}

func spglassConfigDefaulting(c *config.Config) error {

	for idx := range c.Deck.Spyglass.Lenses {
		if err := defaultLensRemoteConfig(&c.Deck.Spyglass.Lenses[idx]); err != nil {
			return err
		}
		parsedEndpoint, err := url.Parse(c.Deck.Spyglass.Lenses[idx].RemoteConfig.Endpoint)
		if err != nil {
			return fmt.Errorf("failed to parse url %q for remote lens %q: %w", c.Deck.Spyglass.Lenses[idx].RemoteConfig.Endpoint, c.Deck.Spyglass.Lenses[idx].Lens.Name, err)
		}
		c.Deck.Spyglass.Lenses[idx].RemoteConfig.ParsedEndpoint = parsedEndpoint
	}

	return nil
}

const spyglassLocalLensListenerAddr = "127.0.0.1:1234"

func defaultLensRemoteConfig(lfc *config.LensFileConfig) error {
	if lfc.RemoteConfig != nil && lfc.RemoteConfig.Endpoint != "" {
		return nil
	}

	lens, err := lenses.GetLens(lfc.Lens.Name)
	if err != nil {
		return fmt.Errorf("lens %q has no remote_config and could not get default: %w", lfc.Lens.Name, err)
	}

	if lfc.RemoteConfig == nil {
		lfc.RemoteConfig = &config.LensRemoteConfig{}
	}

	if lfc.RemoteConfig.Endpoint == "" {
		// Must not have a slash in between, DyanmicPathForLens already returns a slash-prefixed path
		lfc.RemoteConfig.Endpoint = fmt.Sprintf("http://%s%s", spyglassLocalLensListenerAddr, common.DyanmicPathForLens(lfc.Lens.Name))
	}

	if lfc.RemoteConfig.Title == "" {
		lfc.RemoteConfig.Title = lens.Config().Title
	}

	if lfc.RemoteConfig.Priority == nil {
		p := lens.Config().Priority
		lfc.RemoteConfig.Priority = &p
	}

	if lfc.RemoteConfig.HideTitle == nil {
		hideTitle := lens.Config().HideTitle
		lfc.RemoteConfig.HideTitle = &hideTitle
	}

	return nil
}

func validateStoragePath(cfg config.Getter, path string) error {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return fmt.Errorf("invalid path: %s (expecting format <storageType>/<bucket>/<folders...>)", path)
	}
	bucketName := parts[1]
	if err := cfg().ValidateStorageBucket(bucketName); err != nil {
		return httpError{
			error:      err,
			statusCode: http.StatusBadRequest,
		}
	}
	return nil
}

type httpError struct {
	error
	statusCode int
}

func httpStatusForError(e error) int {
	var httpErr httpError
	if ok := errors.As(e, &httpErr); ok {
		return httpErr.statusCode
	}
	return http.StatusInternalServerError
}

func shouldLogHTTPErrors(e error) bool {
	return !errors.Is(e, context.Canceled) || httpStatusForError(e) >= http.StatusInternalServerError // 5XX
}
