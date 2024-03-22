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
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"sigs.k8s.io/yaml"
)

func TestEnsureEmbed(t *testing.T) {
	if len(embededConfigGoFileContent) == 0 {
		t.Error("EmbededConfigGoFileContent is empty.")
	}
}

func TestHasSelfApproval(t *testing.T) {
	cases := []struct {
		name     string
		cfg      string
		expected bool
	}{
		{
			name:     "self approval by default",
			expected: true,
		},
		{
			name:     "reject approval when require_self_approval set",
			cfg:      `{"require_self_approval": true}`,
			expected: false,
		},
		{
			name:     "has approval when require_self_approval set to false",
			cfg:      `{"require_self_approval": false}`,
			expected: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a Approve
			if err := yaml.Unmarshal([]byte(tc.cfg), &a); err != nil {
				t.Fatalf("failed to unmarshal cfg: %v", err)
			}
			if actual := a.HasSelfApproval(); actual != tc.expected {
				t.Errorf("%t != expected %t", actual, tc.expected)
			}
		})
	}
}

func TestConsiderReviewState(t *testing.T) {
	cases := []struct {
		name     string
		cfg      string
		expected bool
	}{
		{
			name:     "consider by default",
			expected: true,
		},
		{
			name: "do not consider when irs = true",
			cfg:  `{"ignore_review_state": true}`,
		},
		{
			name:     "consider when irs = false",
			cfg:      `{"ignore_review_state": false}`,
			expected: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a Approve
			if err := yaml.Unmarshal([]byte(tc.cfg), &a); err != nil {
				t.Fatalf("failed to unmarshal cfg: %v", err)
			}
			if actual := a.ConsiderReviewState(); actual != tc.expected {
				t.Errorf("%t != expected %t", actual, tc.expected)
			}
		})
	}
}

func TestGetPluginsLegacy(t *testing.T) {
	var testcases = []struct {
		name            string
		pluginMap       map[string][]string // this is read from the plugins.yaml file typically.
		owner           string
		repo            string
		expectedPlugins []string
	}{
		{
			name: "All plugins enabled for org should be returned for any org/repo query",
			pluginMap: map[string][]string{
				"org1": {"plugin1", "plugin2"},
			},
			owner:           "org1",
			repo:            "repo",
			expectedPlugins: []string{"plugin1", "plugin2"},
		},
		{
			name: "All plugins enabled for org/repo should be returned for a org/repo query",
			pluginMap: map[string][]string{
				"org1":      {"plugin1", "plugin2"},
				"org1/repo": {"plugin3"},
			},
			owner:           "org1",
			repo:            "repo",
			expectedPlugins: []string{"plugin1", "plugin2", "plugin3"},
		},
		{
			name: "Plugins for org1/repo should not be returned for org2/repo query",
			pluginMap: map[string][]string{
				"org1":      {"plugin1", "plugin2"},
				"org1/repo": {"plugin3"},
			},
			owner:           "org2",
			repo:            "repo",
			expectedPlugins: nil,
		},
		{
			name: "Plugins for org1 should not be returned for org2/repo query",
			pluginMap: map[string][]string{
				"org1":      {"plugin1", "plugin2"},
				"org2/repo": {"plugin3"},
			},
			owner:           "org2",
			repo:            "repo",
			expectedPlugins: []string{"plugin3"},
		},
	}
	for _, tc := range testcases {
		pa := ConfigAgent{configuration: &Configuration{Plugins: OldToNewPlugins(tc.pluginMap)}}

		plugins := pa.getPlugins(tc.owner, tc.repo)
		if diff := cmp.Diff(plugins, tc.expectedPlugins); diff != "" {
			t.Errorf("Actual plugins differ from expected: %s", diff)
		}
	}
}

func TestGetPlugins(t *testing.T) {
	var testcases = []struct {
		name            string
		pluginMap       Plugins // this is read from the plugins.yaml file typically.
		owner           string
		repo            string
		expectedPlugins []string
	}{
		{
			name: "All plugins enabled for org should be returned for any org/repo query",
			pluginMap: Plugins{
				"org1": {Plugins: []string{"plugin1", "plugin2"}},
			},
			owner:           "org1",
			repo:            "repo",
			expectedPlugins: []string{"plugin1", "plugin2"},
		},
		{
			name: "All plugins enabled for org/repo should be returned for a org/repo query",
			pluginMap: Plugins{
				"org1":      {Plugins: []string{"plugin1", "plugin2"}},
				"org1/repo": {Plugins: []string{"plugin3"}},
			},
			owner:           "org1",
			repo:            "repo",
			expectedPlugins: []string{"plugin1", "plugin2", "plugin3"},
		},
		{
			name: "Excluded plugins for repo enabled for org/repo should not be returned for a org/repo query",
			pluginMap: Plugins{
				"org1":      {Plugins: []string{"plugin1", "plugin2", "plugin3"}, ExcludedRepos: []string{"repo"}},
				"org1/repo": {Plugins: []string{"plugin3"}},
			},
			owner:           "org1",
			repo:            "repo",
			expectedPlugins: []string{"plugin3"},
		},
		{
			name: "Plugins for org1/repo should not be returned for org2/repo query",
			pluginMap: Plugins{
				"org1":      {Plugins: []string{"plugin1", "plugin2"}},
				"org1/repo": {Plugins: []string{"plugin3"}},
			},
			owner:           "org2",
			repo:            "repo",
			expectedPlugins: nil,
		},
		{
			name: "Plugins for org1 should not be returned for org2/repo query",
			pluginMap: Plugins{
				"org1":      {Plugins: []string{"plugin1", "plugin2"}},
				"org2/repo": {Plugins: []string{"plugin3"}},
			},
			owner:           "org2",
			repo:            "repo",
			expectedPlugins: []string{"plugin3"},
		},
	}
	for _, tc := range testcases {
		pa := ConfigAgent{configuration: &Configuration{Plugins: tc.pluginMap}}

		plugins := pa.getPlugins(tc.owner, tc.repo)
		if diff := cmp.Diff(plugins, tc.expectedPlugins); diff != "" {
			t.Errorf("Actual plugins differ from expected: %s", diff)
		}
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	defaultedConfig := func(m ...func(*Configuration)) *Configuration {
		cfg := &Configuration{
			Owners:      Owners{LabelsDenyList: []string{"approved", "lgtm"}},
			Blunderbuss: Blunderbuss{ReviewerCount: func() *int { i := 2; return &i }()},
			CherryPickUnapproved: CherryPickUnapproved{
				BranchRegexp: "^release-.*$",
				BranchRe:     regexp.MustCompile("^release-.*$"),
				Comment:      "This PR is not for the master branch but does not have the `cherry-pick-approved`  label. Adding the `do-not-merge/cherry-pick-not-approved`  label.",
			},
			ConfigUpdater: ConfigUpdater{
				Maps: map[string]ConfigMapSpec{
					"config/prow/config.yaml":  {Name: "config", Clusters: map[string][]string{"default": {""}}},
					"config/prow/plugins.yaml": {Name: "plugins", Clusters: map[string][]string{"default": {""}}}},
			},
			Heart: Heart{CommentRe: regexp.MustCompile("")},
			SigMention: SigMention{
				Regexp: `(?m)@kubernetes/sig-([\w-]*)-(misc|test-failures|bugs|feature-requests|proposals|pr-reviews|api-reviews)`,
				Re:     regexp.MustCompile(`(?m)@kubernetes/sig-([\w-]*)-(misc|test-failures|bugs|feature-re)`),
			},
			Help: Help{
				HelpGuidelinesURL: "https://git.k8s.io/community/contributors/guide/help-wanted.md",
			},
		}
		for _, modify := range m {
			modify(cfg)
		}
		return cfg
	}

	testCases := []struct {
		name   string
		config string
		// filename -> content
		supplementalConfigs                map[string]string
		supplementalPluginConfigFileSuffix string

		expected *Configuration
	}{
		{
			name: "Single-file config gets loaded",
			config: `
plugins:
  org/repo:
  - wip`,
			expected: defaultedConfig(func(c *Configuration) {
				c.Plugins = Plugins{"org/repo": {Plugins: []string{"wip"}}}
			}),
		},
		{
			name: "Supplemental configs get loaded and merged",
			config: `
plugins:
  org/repo:
  - wip`,
			supplementalConfigs: map[string]string{
				"some-path-extra_config.yaml": `
plugins:
  org/repo2:
  - wip`,
			},
			supplementalPluginConfigFileSuffix: "extra_config.yaml",
			expected: defaultedConfig(func(c *Configuration) {
				c.Plugins = Plugins{
					"org/repo":  {Plugins: []string{"wip"}},
					"org/repo2": {Plugins: []string{"wip"}},
				}
			}),
		},
		{
			name: "Supplemental configs that do not have right suffix are ignored",
			config: `
plugins:
  org/repo:
  - wip`,
			supplementalConfigs: map[string]string{
				"some-path-extra_config.yaml": `
plugins:
  org/repo:
  - wip`,
			},
			supplementalPluginConfigFileSuffix: "nope",
			expected: defaultedConfig(func(c *Configuration) {
				c.Plugins = Plugins{"org/repo": {Plugins: []string{"wip"}}}
			}),
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tempDir, "_plugins.yaml"), []byte(tc.config), 0644); err != nil {
				t.Fatalf("failed to write config: %v", err)
			}
			for supplementalConfigName, supplementalConfig := range tc.supplementalConfigs {
				if err := os.WriteFile(filepath.Join(tempDir, supplementalConfigName), []byte(supplementalConfig), 0644); err != nil {
					t.Fatalf("failed to write supplemental config %s: %v", supplementalConfigName, err)
				}
			}

			agent := &ConfigAgent{}
			if err := agent.Load(filepath.Join(tempDir, "_plugins.yaml"), []string{tempDir}, tc.supplementalPluginConfigFileSuffix, false, false); err != nil {
				t.Fatalf("failed to load: %v", err)
			}

			if diff := cmp.Diff(tc.expected, agent.Config(), cmpopts.IgnoreTypes(regexp.Regexp{})); diff != "" {
				t.Errorf("expected config differs from actual: %s", diff)
			}

		})
	}
}

const configUpdater = `---
config_updater:
  cluster_groups:
    build_farm:
      clusters:
      - app.ci
      - build01
      namespaces:
      - ci
  gzip: false
  maps:
    ci-operator/config/**/*-fcos.yaml:
      clusters:
        app.ci:
        - ci
      name: ci-operator-misc-configs
    ci-operator/templates/master-sidecar-3.yaml:
      cluster_groups:
      - build_farm
      name: prow-job-master-sidecar-3
`

func TestLoadConfigUpdater(t *testing.T) {
	testCases := []struct {
		name                     string
		config                   string
		skipResolveConfigUpdater bool
		expected                 ConfigUpdater
	}{
		{
			name:                     "skip resolve",
			config:                   configUpdater,
			skipResolveConfigUpdater: true,
			expected: ConfigUpdater{
				ClusterGroups: map[string]ClusterGroup{
					"build_farm": {
						Clusters:   []string{"app.ci", "build01"},
						Namespaces: []string{"ci"},
					},
				},
				Maps: map[string]ConfigMapSpec{
					"ci-operator/config/**/*-fcos.yaml": {
						Name:     "ci-operator-misc-configs",
						Clusters: map[string][]string{"app.ci": {"ci"}},
					},
					"ci-operator/templates/master-sidecar-3.yaml": {
						Name:          "prow-job-master-sidecar-3",
						ClusterGroups: []string{"build_farm"},
					},
				},
			},
		},
		{
			name:   "not skip resolve",
			config: configUpdater,
			expected: ConfigUpdater{
				Maps: map[string]ConfigMapSpec{
					"ci-operator/config/**/*-fcos.yaml": {
						Name:     "ci-operator-misc-configs",
						Clusters: map[string][]string{"app.ci": {"ci"}},
					},
					"ci-operator/templates/master-sidecar-3.yaml": {
						Name:     "prow-job-master-sidecar-3",
						Clusters: map[string][]string{"app.ci": {"ci"}, "build01": {"ci"}},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tempDir, "_plugins.yaml"), []byte(tc.config), 0644); err != nil {
				t.Fatalf("failed to write config: %v", err)
			}
			agent := &ConfigAgent{}
			if err := agent.Load(filepath.Join(tempDir, "_plugins.yaml"), nil, "", false, tc.skipResolveConfigUpdater); err != nil {
				t.Fatalf("failed to load: %v", err)
			}
			if diff := cmp.Diff(tc.expected, agent.Config().ConfigUpdater); diff != "" {
				t.Errorf("expected config differs from actual: %s", diff)
			}
		})
	}

}
