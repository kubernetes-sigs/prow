/*
Copyright 2017 The Kubernetes Authors.

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
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp/externalplugins"
)

type options struct {
	port int

	dryRun                 bool
	github                 prowflagutil.GitHubOptions
	labels                 prowflagutil.Strings
	instrumentationOptions prowflagutil.InstrumentationOptions
	logLevel               string

	webhookSecretFile string
	prowAssignments   bool
	allowAll          bool
	issueOnConflict   bool
	labelPrefix       string
}

func (o *options) Validate() error {
	for idx, group := range []flagutil.OptionGroup{&o.github} {
		if err := group.Validate(o.dryRun); err != nil {
			return fmt.Errorf("%d: %w", idx, err)
		}
	}

	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8888, "Port to listen on.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.Var(&o.labels, "labels", "Labels to apply to the cherrypicked PR.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.logLevel, "log-level", "debug", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.BoolVar(&o.prowAssignments, "use-prow-assignments", true, "Use prow commands to assign cherrypicked PRs.")
	fs.BoolVar(&o.allowAll, "allow-all", false, "Allow anybody to use automated cherrypicks by skipping GitHub organization membership checks.")
	fs.BoolVar(&o.issueOnConflict, "create-issue-on-conflict", false, "Create a GitHub issue and assign it to the requestor on cherrypick conflict.")
	fs.StringVar(&o.labelPrefix, "label-prefix", defaultLabelPrefix, "Set a custom label prefix.")
	for _, group := range []flagutil.OptionGroup{&o.github, &o.instrumentationOptions} {
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
	log := logrus.StandardLogger().WithField("plugin", pluginName)

	if err := secret.Add(o.webhookSecretFile); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	githubClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}
	gitClient, err := o.github.GitClientFactory("", nil, o.dryRun, false)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Git client.")
	}
	interrupts.OnInterrupt(func() {
		if err := gitClient.Clean(); err != nil {
			logrus.WithError(err).Error("Could not clean up git client cache.")
		}
	})

	email, err := githubClient.Email()
	if err != nil {
		log.WithError(err).Fatal("Error getting bot e-mail.")
	}

	botUser, err := githubClient.BotUser()
	if err != nil {
		logrus.WithError(err).Fatal("Error getting bot name.")
	}
	repos, err := githubClient.GetRepos(botUser.Login, true)
	if err != nil {
		log.WithError(err).Fatal("Error listing bot repositories.")
	}

	server := &Server{
		tokenGenerator: secret.GetTokenGenerator(o.webhookSecretFile),
		botUser:        botUser,
		email:          email,

		gc:  gitClient,
		ghc: githubClient,
		log: log,

		labels:          o.labels.Strings(),
		prowAssignments: o.prowAssignments,
		allowAll:        o.allowAll,
		issueOnConflict: o.issueOnConflict,
		labelPrefix:     o.labelPrefix,

		bare:     &http.Client{},
		patchURL: "https://patch-diff.githubusercontent.com",

		repos: repos,
	}

	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	health.ServeReady()

	mux := http.NewServeMux()
	mux.Handle("/", server)
	externalplugins.ServeExternalPluginHelp(mux, log, HelpProvider)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}
	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)
}
