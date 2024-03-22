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
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	pluginsflagutil "k8s.io/test-infra/prow/flagutil/plugins"
)

func newSetStringsFlagForTest(vals ...string) flagutil.Strings {
	ss := flagutil.NewStrings()
	for _, v := range vals {
		ss.Set(v)
	}
	return ss
}

func TestGatherOptions(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		expected    func(*options)
		expectedErr error
	}{
		{
			name: "minimal flags work",
		},
		{
			name: "gcs-credentials-file sets the GCS credentials on the storage client",
			args: []string{
				"-gcs-credentials-file=/creds",
			},
			expected: func(o *options) {
				o.storage = flagutil.StorageClientOptions{
					GCSCredentialsFile: "/creds",
				}
			},
		},
		{
			name: "s3-credentials-file sets the S3 credentials on the storage client",
			args: []string{
				"-s3-credentials-file=/creds",
			},
			expected: func(o *options) {
				o.storage = flagutil.StorageClientOptions{
					S3CredentialsFile: "/creds",
				}
			},
		},
		{
			name: "support denylist",
			args: []string{
				"-denylist=a",
				"-denylist=b",
			},
			expected: func(o *options) {
				o.addedPresubmitDenylist = newSetStringsFlagForTest("a", "b")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected := &options{
				dryRun: true,
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
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			}
			expectedfs := flag.NewFlagSet("fake-flags", flag.PanicOnError)
			expected.github.AddCustomizedFlags(expectedfs, flagutil.ThrottlerDefaults(300, 100))
			if tc.expected != nil {
				tc.expected(expected)
			}

			args := append(tc.args,
				"--config-path=yo")
			fs := flag.NewFlagSet("fake-flags", flag.PanicOnError)
			actual := gatherOptions(fs, args...)
			switch err := actual.Validate(); {
			case err == nil && tc.expectedErr != nil:
				t.Errorf("Expect err, got nil")
			case err != nil && tc.expectedErr == nil:
				t.Errorf("Expect no error, got: %v", err)
			case err != nil && err.Error() != tc.expectedErr.Error():
				t.Errorf("Expect error: %v\ngot:\n%v", err, tc.expectedErr)
			case !reflect.DeepEqual(*expected, actual):
				t.Errorf("actual differs from expected: %s", cmp.Diff(actual, *expected, cmp.Exporter(func(_ reflect.Type) bool { return true })))
			}
		})
	}
}

func TestGetDenyList(t *testing.T) {
	tests := []struct {
		name string
		o    options
		want sets.Set[string]
	}{
		{
			name: "black list only",
			o: options{
				addedPresubmitDenylist: newSetStringsFlagForTest("a", "b"),
			},
			want: sets.New[string]("a", "b"),
		},
		{
			name: "deny list only",
			o: options{
				addedPresubmitDenylist: newSetStringsFlagForTest("c", "d"),
			},
			want: sets.New[string]("c", "d"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.o.getDenyList()
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("Want(-), got(+):\n%s", diff)
			}
		})
	}
}
