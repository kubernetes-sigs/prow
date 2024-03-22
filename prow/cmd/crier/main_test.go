/*
Copyright 2018 The Kubernetes Authors.

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
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
)

func TestOptions(t *testing.T) {

	var defaultGitHubOptions flagutil.GitHubOptions
	defaultGitHubOptions.AddFlags(flag.NewFlagSet("", flag.ContinueOnError))

	cases := []struct {
		name     string
		args     []string
		expected *options
	}{
		//General
		{
			name: "no args, reject",
			args: []string{},
		},
		{
			name: "config-path is empty string, reject",
			args: []string{"--pubsub-workers=1", "--config-path="},
		},
		//Gerrit Reporter
		{
			name: "gerrit supports multiple workers",
			args: []string{"--gerrit-workers=99", "--cookiefile=foobar", "--config-path=foo"},
			expected: &options{
				gerritWorkers:  99,
				cookiefilePath: "foobar",
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "foo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				github:                 defaultGitHubOptions,
				k8sReportFraction:      1.0,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
		{
			name: "gerrit missing --cookiefile",
			args: []string{"--gerrit-workers=5", "--config-path=foo"},
			expected: &options{
				gerritWorkers: 5,
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "foo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				github:                 defaultGitHubOptions,
				k8sReportFraction:      1.0,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
		//PubSub Reporter
		{
			name: "pubsub workers, sets workers",
			args: []string{"--pubsub-workers=7", "--config-path=baz"},
			expected: &options{
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "baz",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				pubsubWorkers:          7,
				github:                 defaultGitHubOptions,
				k8sReportFraction:      1.0,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
		{
			name: "pubsub workers set to negative, rejects",
			args: []string{"--pubsub-workers=-3", "--config-path=foo"},
		},
		//Slack Reporter
		{
			name: "slack workers, sets workers",
			args: []string{"--slack-workers=13", "--slack-token-file=/bar/baz", "--config-path=foo"},
			expected: &options{
				slackWorkers:   13,
				slackTokenFile: "/bar/baz",
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "foo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				github:                 defaultGitHubOptions,
				k8sReportFraction:      1.0,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
		{
			name: "slack missing --slack-token, rejects",
			args: []string{"--slack-workers=1", "--config-path=foo"},
		},
		{
			name: "slack with --dry-run, sets",
			args: []string{"--slack-workers=13", "--slack-token-file=/bar/baz", "--config-path=foo", "--dry-run"},
			expected: &options{
				slackWorkers:   13,
				slackTokenFile: "/bar/baz",
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "foo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				dryrun:                 true,
				github:                 defaultGitHubOptions,
				k8sReportFraction:      1.0,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
		{
			name: "k8s-gcs enables k8s-gcs",
			args: []string{"--kubernetes-blob-storage-workers=3", "--config-path=foo"},
			expected: &options{
				k8sBlobStorageWorkers: 3,
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "foo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				github:                 defaultGitHubOptions,
				k8sReportFraction:      1.0,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
		{
			name: "k8s-gcs with report fraction sets report fraction",
			args: []string{"--kubernetes-blob-storage-workers=3", "--config-path=foo", "--kubernetes-report-fraction=0.5"},
			expected: &options{
				k8sBlobStorageWorkers: 3,
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "foo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				github:                 defaultGitHubOptions,
				k8sReportFraction:      0.5,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
		{
			name: "k8s-gcs with too large report fraction rejects",
			args: []string{"--kubernetes-blob-storage-workers=3", "--config-path=foo", "--kubernetes-report-fraction=1.5"},
		},
		{
			name: "k8s-gcs with negative report fraction rejects",
			args: []string{"--kubernetes-blob-storage-workers=3", "--config-path=foo", "--kubernetes-report-fraction=-1.2"},
		},
		{
			name: "resultstore workers, sets workers",
			args: []string{"--resultstore-workers=3", "--config-path=foo"},
			expected: &options{
				resultStoreWorkers: 3,
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "foo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				github:                 defaultGitHubOptions,
				k8sReportFraction:      1.0,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			var actual options
			err := actual.parseArgs(flags, tc.args)
			switch {
			case err == nil && tc.expected == nil:
				t.Fatalf("%s: failed to return an error", tc.name)
			case err != nil && tc.expected != nil:
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}

			if tc.expected == nil {
				return
			}
			if diff := cmp.Diff(actual, *tc.expected, cmp.Exporter(func(_ reflect.Type) bool { return true })); diff != "" {
				t.Errorf("Result differs from expected: %s", diff)
			}

		})
	}
}

/*
The GitHubOptions object has several private fields and objects
This unit testing covers only the public portions
*/
func TestGitHubOptions(t *testing.T) {
	cases := []struct {
		name              string
		args              []string
		expectedWorkers   int
		expectedTokenPath string
	}{
		{
			name:              "github workers, only support single worker",
			args:              []string{"--github-workers=5", "--github-token-path=tkpath", "--config-path=foo"},
			expectedWorkers:   5,
			expectedTokenPath: "tkpath",
		},
	}

	for _, tc := range cases {
		flags := flag.NewFlagSet(tc.name, flag.ContinueOnError)
		actual := options{}
		err := actual.parseArgs(flags, tc.args)

		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
		if actual.githubWorkers != tc.expectedWorkers {
			t.Errorf("%s: worker mismatch: actual %d != expected %d",
				tc.name, actual.githubWorkers, tc.expectedWorkers)
		}
		if actual.github.TokenPath != tc.expectedTokenPath {
			t.Errorf("%s: path mismatch: actual %s != expected %s",
				tc.name, actual.github.TokenPath, tc.expectedTokenPath)
		}
	}
}
