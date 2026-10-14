/*
Copyright 2026 The Kubernetes Authors.

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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLoadShardedTideContextPolicies(t *testing.T) {
	t.Parallel()
	configFile, supplementalDir := writeTideContextShards(t, map[string]string{
		"config.yaml": `tide:
  queries:
  - repos: [acme/widget]
    labels: [approved]
  merge_method:
    acme/widget: squash
  context_options:
    required-contexts: [global, shared]
    skip-unknown-contexts: true
`,
		"shards/root_prowconfig.yaml": `tide:
  context_options:
    required-contexts: [root, shared]
`,
		"shards/acme/org_prowconfig.yaml": `tide:
  context_options:
    orgs:
      acme:
        required-contexts: [org, shared]
`,
		"shards/acme/widget/repo_prowconfig.yaml": `tide:
  context_options:
    orgs:
      acme:
        repos:
          widget:
            required-contexts: [repo, shared]
            branches:
              main:
                required-contexts: [gate, shared]
                skip-unknown-contexts: false
`,
		"shards/acme/widget/extra_prowconfig.yaml": `tide:
  context_options:
    orgs:
      acme:
        repos:
          widget:
            branches:
              main:
                required-contexts: [extra, gate]
`,
	})
	cfg, err := LoadStrict(configFile, "", []string{supplementalDir}, "_prowconfig.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Tide.MergeMethod(OrgRepo{Org: "acme", Repo: "widget"}); got != "squash" {
		t.Errorf("merge method = %q, want squash", got)
	}
	if diff := cmp.Diff(TideQueries{{Repos: []string{"acme/widget"}, Labels: []string{"approved"}}}, cfg.Tide.Queries); diff != "" {
		t.Errorf("queries changed: %s", diff)
	}
	for _, tc := range []struct {
		repo, branch string
		required     []string
		skipUnknown  bool
	}{
		{"widget", "main", []string{"extra", "gate", "global", "org", "repo", "root", "shared"}, false},
		{"widget", "release", []string{"global", "org", "repo", "root", "shared"}, true},
		{"other", "main", []string{"global", "org", "root", "shared"}, true},
	} {
		policy, err := cfg.GetTideContextPolicy(nil, "acme", tc.repo, tc.branch, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(tc.required, policy.RequiredContexts); diff != "" {
			t.Errorf("%s/%s required contexts: %s", tc.repo, tc.branch, diff)
		}
		if policy.SkipUnknownContexts == nil || *policy.SkipUnknownContexts != tc.skipUnknown {
			t.Errorf("%s/%s skip-unknown-contexts = %v, want %t", tc.repo, tc.branch, policy.SkipUnknownContexts, tc.skipUnknown)
		}
	}
}

func TestLoadShardedTideContextPoliciesWithoutMergeMethod(t *testing.T) {
	configFile, supplementalDir := writeTideContextShards(t, map[string]string{
		"config.yaml": "{}\n",
		"shards/gate_prowconfig.yaml": `tide:
  context_options:
    required-contexts: [gate]
`,
	})
	cfg, err := LoadStrict(configFile, "", []string{supplementalDir}, "_prowconfig.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"gate"}, cfg.Tide.ContextOptions.RequiredContexts); diff != "" {
		t.Errorf("context policy lost when merge_method is unset: %s", diff)
	}
}

func TestShardedTideContextPolicyRejectsInheritedRegexConflict(t *testing.T) {
	configFile, supplementalDir := writeTideContextShards(t, map[string]string{
		"config.yaml":                 "tide:\n  context_options:\n    optional-regex-contexts: ['^gate$']\n",
		"shards/gate_prowconfig.yaml": "tide:\n  context_options:\n    orgs: {acme: {repos: {widget: {branches: {main: {required-contexts: [gate]}}}}}}\n",
	})
	cfg, err := LoadStrict(configFile, "", []string{supplementalDir}, "_prowconfig.yaml")
	if err == nil {
		_, err = cfg.GetTideContextPolicy(nil, "acme", "widget", "main", nil, "")
	}
	if err == nil || !strings.Contains(err.Error(), "matches required context") {
		t.Fatalf("expected inherited optional regex conflict, got %v", err)
	}
}

func writeTideContextShards(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "config.yaml"), filepath.Join(dir, "shards")
}
