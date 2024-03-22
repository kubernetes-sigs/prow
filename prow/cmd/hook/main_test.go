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
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	pluginsflagutil "k8s.io/test-infra/prow/flagutil/plugins"
	"k8s.io/test-infra/prow/plugins"
)

// Make sure that our plugins are valid.
func TestPlugins(t *testing.T) {
	pa := &plugins.ConfigAgent{}
	if err := pa.Load("../../../config/prow/plugins.yaml", nil, "", true, false); err != nil {
		t.Fatalf("Could not load plugins: %v.", err)
	}
}

func Test_gatherOptions(t *testing.T) {
	cases := []struct {
		name     string
		args     map[string]string
		del      sets.Set[string]
		expected func(*options)
		err      bool
	}{
		{
			name: "minimal flags work",
		},
		{
			name: "explicitly set --config-path",
			args: map[string]string{
				"--config-path": "/random/value",
			},
			expected: func(o *options) {
				o.config.ConfigPath = "/random/value"
			},
		},
		{
			name: "expicitly set --dry-run=false",
			args: map[string]string{
				"--dry-run": "false",
			},
			expected: func(o *options) {
				o.dryRun = false
			},
		},
		{
			name: "explicitly set --plugin-config",
			args: map[string]string{
				"--plugin-config": "/random/value",
			},
			expected: func(o *options) {
				o.pluginsConfig.PluginConfigPath = "/random/value"
			},
		},
		{
			name: "explicitly set --webhook-path",
			args: map[string]string{
				"--webhook-path": "/random/hook",
			},
			expected: func(o *options) {
				o.webhookPath = "/random/hook"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected := &options{
				webhookPath: "/hook",
				port:        8888,
				config: configflagutil.ConfigOptions{
					ConfigPath:                            "yo",
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				pluginsConfig: pluginsflagutil.PluginOptions{
					PluginConfigPath:                         "/etc/plugins/plugins.yaml",
					PluginConfigPathDefault:                  "/etc/plugins/plugins.yaml",
					SupplementalPluginsConfigsFileNameSuffix: "_pluginconfig.yaml",
				},
				dryRun:                 true,
				gracePeriod:            180 * time.Second,
				webhookSecretFile:      "/etc/webhook/hmac",
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			}
			expectedfs := flag.NewFlagSet("fake-flags", flag.PanicOnError)
			expected.github.AddFlags(expectedfs)
			if tc.expected != nil {
				tc.expected(expected)
			}

			argMap := map[string]string{
				"--config-path": "yo",
			}
			for k, v := range tc.args {
				argMap[k] = v
			}
			for k := range tc.del {
				delete(argMap, k)
			}

			var args []string
			for k, v := range argMap {
				args = append(args, k+"="+v)
			}
			fs := flag.NewFlagSet("fake-flags", flag.PanicOnError)
			actual := gatherOptions(fs, args...)
			switch err := actual.Validate(); {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive expected error")
			case !reflect.DeepEqual(*expected, actual):
				t.Errorf("actual differs from expected: %s", cmp.Diff(actual, *expected, cmp.Exporter(func(_ reflect.Type) bool { return true })))
			}
		})
	}
}
