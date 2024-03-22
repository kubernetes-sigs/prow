/*
Copyright 2019 The Kubernetes Authors.

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

package kube

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
)

func TestMergeConfigs(t *testing.T) {
	fakeConfig := func(u string) *rest.Config { return &rest.Config{Username: u} }
	cases := []struct {
		name     string
		local    *rest.Config
		foreign  map[string]rest.Config
		current  string
		expected map[string]rest.Config
		err      bool
	}{
		{
			name: "require at least one cluster",
			err:  true,
		},
		{
			name:  "only local cluster",
			local: fakeConfig("local"),
			expected: map[string]rest.Config{
				InClusterContext:    *fakeConfig("local"),
				DefaultClusterAlias: *fakeConfig("local"),
			},
		},
		{
			name: "foreign without local uses current as default",
			foreign: map[string]rest.Config{
				"current-context": *fakeConfig("current"),
			},
			current: "current-context",
			expected: map[string]rest.Config{
				InClusterContext:    *fakeConfig("current"),
				DefaultClusterAlias: *fakeConfig("current"),
				"current-context":   *fakeConfig("current"),
			},
		},
		{
			name: "reject only foreign without a current context",
			foreign: map[string]rest.Config{
				DefaultClusterAlias: *fakeConfig("default"),
			},
			err: true,
		},
		{
			name: "accept only foreign with default",
			foreign: map[string]rest.Config{
				DefaultClusterAlias: *fakeConfig("default"),
				"random-context":    *fakeConfig("random"),
			},
			current: "random-context",
			expected: map[string]rest.Config{
				InClusterContext:    *fakeConfig("random"),
				DefaultClusterAlias: *fakeConfig("default"),
				"random-context":    *fakeConfig("random"),
			},
		},
		{
			name:  "accept local and foreign, using local for default",
			local: fakeConfig("local"),
			foreign: map[string]rest.Config{
				"random-context": *fakeConfig("random"),
			},
			current: "random-context",
			expected: map[string]rest.Config{
				InClusterContext:    *fakeConfig("local"),
				DefaultClusterAlias: *fakeConfig("local"),
				"random-context":    *fakeConfig("random"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := mergeConfigs(tc.local, tc.foreign, tc.current)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Error("failed to receive an error")
			case !equality.Semantic.DeepEqual(actual, tc.expected):
				t.Errorf("configs do not match:\n%s", diff.ObjectReflectDiff(tc.expected, actual))
			}
		})
	}
}

func TestLoadClusterConfigs(t *testing.T) {
	cases := []struct {
		name               string
		kubeconfig         string
		kubeconfigDir      string
		suffix             string
		projectedTokenFile string
		noInClusterConfig  bool
		expected           map[string]rest.Config
		expectedErr        bool
		disabledClusters   sets.Set[string]
	}{
		{
			name:       "load from kubeconfig",
			kubeconfig: filepath.Join(filepath.Join("testdata", "load_from_kubeconfig"), "kubeconfig"),
			expected: map[string]rest.Config{
				"": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"app.ci": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"build01": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"build02": {
					Host:        "https://api.build02.gcp.ci.openshift.org:6443",
					BearerToken: "REDACTED",
				},
				"default": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"hive": {
					Host:        "https://api.hive.9xw5.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
			},
		},
		{
			name:          "load from kubeconfigDir",
			kubeconfigDir: filepath.Join("testdata", "load_from_kubeconfigDir"),
			suffix:        "yaml",
			expected: map[string]rest.Config{
				"": {
					Host:        "https://api.build02.gcp.ci.openshift.org:6443",
					BearerToken: "REDACTED",
				},
				"app.ci": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"build01": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"build02": {
					Host:        "https://api.build02.gcp.ci.openshift.org:6443",
					BearerToken: "REDACTED",
				},
				"default": {
					Host:        "https://api.build02.gcp.ci.openshift.org:6443",
					BearerToken: "REDACTED",
				},
				"hive": {
					Host:        "https://api.hive.9xw5.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
			},
		},
		{
			name:             "load from kubeconfigDir with build02 disabled",
			kubeconfigDir:    filepath.Join("testdata", "load_from_kubeconfigDir"),
			suffix:           "yaml",
			disabledClusters: sets.New[string]("build02"),
			expected: map[string]rest.Config{
				"": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"app.ci": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"build01": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"default": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"hive": {
					Host:        "https://api.hive.9xw5.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
			},
		},
		{
			name:          "load from kubeconfigDir having contexts with the same name",
			kubeconfigDir: filepath.Join("testdata", "load_from_kubeconfigDir_having_contexts_with_the_same_name"),
			expectedErr:   true,
		},
		{
			name:          "load from kubeconfig and kubeconfigDir",
			kubeconfig:    filepath.Join(filepath.Join("testdata", "load_from_kubeconfig"), "kubeconfig2"),
			kubeconfigDir: filepath.Join("testdata", "load_from_kubeconfig_and_kubeconfigDir"),
			expected: map[string]rest.Config{
				"": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"app.ci": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"build01": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"build02": {
					Host:        "https://api.build02.gcp.ci.openshift.org:6443",
					BearerToken: "REDACTED",
				},
				"default": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"hive": {
					Host:        "https://api.hive.9xw5.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
			},
		},
		{
			name:              "no inCluster config",
			kubeconfig:        filepath.Join(filepath.Join("testdata", "load_from_kubeconfig"), "kubeconfig2"),
			kubeconfigDir:     filepath.Join("testdata", "no_inCluster_config"),
			noInClusterConfig: true,
			expected: map[string]rest.Config{
				"app.ci": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"build01": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"build02": {
					Host:        "https://api.build02.gcp.ci.openshift.org:6443",
					BearerToken: "REDACTED",
				},
				"hive": {
					Host:        "https://api.hive.9xw5.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := LoadClusterConfigs(NewConfig(ConfigFile(tc.kubeconfig),
				ConfigDir(tc.kubeconfigDir), ConfigProjectedTokenFile(tc.projectedTokenFile),
				NoInClusterConfig(tc.noInClusterConfig), ConfigSuffix(tc.suffix),
				DisabledClusters(tc.disabledClusters)))
			if tc.expectedErr != (actualErr != nil) {
				t.Errorf("%s: actualErr %v does not match expectedErr %v", tc.name, actualErr, tc.expectedErr)
				return
			}
			if diff := cmp.Diff(tc.expected, actual, cmpopts.IgnoreFields(rest.Config{}, "UserAgent")); !tc.expectedErr && diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
