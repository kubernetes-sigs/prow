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
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"
)

func TestLoadPreservesAuthoredTideQueries(t *testing.T) {
	t.Parallel()
	const source = `tide:
  queries:
  - orgs: [second, first]
    repos: [extra/second, extra/first]
    excludedRepos: [first/second, first/first]
    labels: [z, a]
    missingLabels: [z-blocked, a-blocked]
    excludedBranches: [z, a]
    author: bot
    milestone: next
    reviewApprovedRequired: true
  - repos: [other/repo]
    labels: [a, z]
    missingLabels: [a-blocked, z-blocked]
    excludedBranches: [a, z]
    author: bot
    milestone: next
    reviewApprovedRequired: true
  - repos: [included/repo]
    includedBranches: [z, a]
`
	var authored Config
	if err := yaml.Unmarshal([]byte(source), &authored); err != nil {
		t.Fatal(err)
	}

	for _, strict := range []bool{false, true} {
		name := "Load"
		load := Load
		if strict {
			name, load = "LoadStrict", LoadStrict
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := load(path, nil, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(authored.Tide.Queries, cfg.Tide.Queries); diff != "" {
				t.Fatalf("loading changed authored queries (-want +got): %s", diff)
			}
			if got := len(cfg.Tide.ResolvedQueries()); got != 2 {
				t.Fatalf("got %d runtime queries, want 2 after deduplication", got)
			}
			if !cfg.AllRepos.HasAll("extra/second", "extra/first", "other/repo", "included/repo") {
				t.Fatalf("AllRepos is missing query repositories: %v", cfg.AllRepos)
			}

			data, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var written Config
			if err := yaml.Unmarshal(data, &written); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(authored.Tide.Queries, written.Tide.Queries); diff != "" {
				t.Fatalf("serialization changed authored queries (-want +got): %s", diff)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			reloaded, err := load(path, nil, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(cfg.Tide.ResolvedQueries(), reloaded.Tide.ResolvedQueries()); diff != "" {
				t.Errorf("round trip changed runtime queries (-want +got): %s", diff)
			}
		})
	}
}

func TestResolvedTideQueriesDoNotShareAuthoredSlices(t *testing.T) {
	t.Parallel()
	for _, branchField := range []string{"includedBranches", "excludedBranches"} {
		t.Run(branchField, func(t *testing.T) {
			var tide TideGitHubConfig
			source := `queries:
- orgs: [org]
  repos: [other/repo]
  excludedRepos: [org/repo]
  labels: [approved]
  missingLabels: [hold]
  ` + branchField + `: [main]
`
			if err := yaml.Unmarshal([]byte(source), &tide); err != nil {
				t.Fatal(err)
			}
			if err := tide.resolveQueries(); err != nil {
				t.Fatal(err)
			}
			query := &tide.ResolvedQueries()[0]
			// Deliberately mutate every populated runtime slice.
			for _, field := range [][]string{query.Orgs, query.Repos, query.ExcludedRepos, query.Labels, query.MissingLabels, query.IncludedBranches, query.ExcludedBranches} {
				if len(field) > 0 {
					field[0] = "changed"
				}
			}
			var original TideGitHubConfig
			if err := yaml.Unmarshal([]byte(source), &original); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(original.Queries, tide.Queries); diff != "" {
				t.Errorf("runtime queries share authored storage (-want +got): %s", diff)
			}
		})
	}
}
