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

// netlify-preview retries Netlify deploy previews for pull requests.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	previewconfig "sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/config"
	"sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/netlify"
	netlifypreview "sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/plugin"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	pluginsflagutil "sigs.k8s.io/prow/pkg/flagutil/plugins"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pluginhelp/externalplugins"
)

type options struct {
	port int

	pluginsConfig          pluginsflagutil.PluginOptions
	dryRun                 bool
	github                 prowflagutil.GitHubOptions
	instrumentationOptions prowflagutil.InstrumentationOptions
	logLevel               string

	webhookSecretFile string
	netlifyTokenFile  string
	configPath        string
	netlifyAPIURL     string
}

func (o *options) Validate() error {
	for idx, group := range []flagutil.OptionGroup{&o.github} {
		if err := group.Validate(o.dryRun); err != nil {
			return fmt.Errorf("%d: %w", idx, err)
		}
	}
	if o.netlifyTokenFile == "" {
		return fmt.Errorf("--netlify-token-file is required")
	}
	if o.configPath == "" {
		return fmt.Errorf("--config-path is required")
	}
	if o.netlifyAPIURL == "" {
		return fmt.Errorf("--netlify-api-url is required")
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8888, "Port to listen on.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.netlifyTokenFile, "netlify-token-file", "/etc/netlify-preview/token", "Path to the file containing the Netlify API token.")
	fs.StringVar(&o.configPath, "config-path", "/etc/netlify-preview/config.yaml", "Path to the netlify-preview plugin config file.")
	fs.StringVar(&o.netlifyAPIURL, "netlify-api-url", "https://api.netlify.com", "Base URL for the Netlify API.")
	fs.StringVar(&o.logLevel, "log-level", "debug", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	o.pluginsConfig.PluginConfigPathDefault = "/etc/plugins/plugins.yaml"
	for _, group := range []flagutil.OptionGroup{&o.github, &o.instrumentationOptions, &o.pluginsConfig} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])
	return o
}

func main() {
	logrusutil.ComponentInit()
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	logLevel, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to parse loglevel")
	}
	logrus.SetLevel(logLevel)
	log := logrus.StandardLogger().WithField("plugin", netlifypreview.PluginName)

	if err := secret.Add(o.webhookSecretFile); err != nil {
		logrus.WithError(err).Fatal("Error starting webhook secret agent.")
	}
	if err := secret.Add(o.netlifyTokenFile); err != nil {
		logrus.WithError(err).Fatal("Error starting Netlify token secret agent.")
	}

	pluginAgent, err := o.pluginsConfig.PluginAgent()
	if err != nil {
		log.WithError(err).Fatal("Error loading plugin config.")
	}
	previewConfig, err := previewconfig.Load(o.configPath)
	if err != nil {
		log.WithError(err).Fatal("Error loading netlify-preview config.")
	}
	githubClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	serv := &server{
		tokenGenerator: secret.GetTokenGenerator(o.webhookSecretFile),
		ghc:            githubClient,
		netlifyClient:  netlify.NewClient(o.netlifyAPIURL, http.DefaultClient, secret.GetTokenGenerator(o.netlifyTokenFile)),
		pluginConfig:   pluginAgent,
		previewConfig:  previewConfig,
		log:            log,
		dryRun:         o.dryRun,
	}

	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	health.ServeReady()

	mux := http.NewServeMux()
	mux.Handle("/", serv)
	externalplugins.ServeExternalPluginHelp(mux, log, netlifypreview.HelpProvider)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}
	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)
}
