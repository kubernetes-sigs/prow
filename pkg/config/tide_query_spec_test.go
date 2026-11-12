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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"sigs.k8s.io/prow/pkg/interrupts"
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

func loadQueryPresetConfig(t *testing.T, strict bool, main string, supplemental map[string]string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mainPath := filepath.Join(dir, "config.yaml")
	write(mainPath, main)
	var dirs []string
	if supplemental != nil {
		dirs = []string{filepath.Join(dir, "shards")}
		for path, data := range supplemental {
			write(filepath.Join(dirs[0], path), data)
		}
	}
	load := Load
	if strict {
		load = LoadStrict
	}
	return load(mainPath, nil, dirs, "_prowconfig.yaml")
}

func TestQueryPresetComposition(t *testing.T) {
	t.Parallel()
	for _, branches := range []string{"includedBranches", "excludedBranches"} {
		t.Run(branches, func(t *testing.T) {
			source := fmt.Sprintf(`tide:
  query_presets:
  - name: first
    labels: [lgtm, 'approved,lgtm']
    missingLabels: [hold, wip]
    %[1]s: [release, main]
    author: bot
  - name: second
    labels: [lgtm, approved]
    missingLabels: [hold, blocked]
    %[1]s: [main, next]
    milestone: next
    reviewApprovedRequired: true
  queries:
  - presets: [first, second]
    orgs: [b-org, inline-org, z-org]
    repos: [extra/b, inline/repo]
    excludedRepos: [z-org/b, z-org/inline]
    labels: [approved, inline]
    missingLabels: [blocked, inline-blocked]
    %[1]s: [next, inline]
`, branches)
			cfg, err := loadQueryPresetConfig(t, true, source, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := TideQuery{
				Orgs:          []string{"b-org", "inline-org", "z-org"},
				Repos:         []string{"extra/b", "inline/repo"},
				ExcludedRepos: []string{"z-org/b", "z-org/inline"},
				Labels:        []string{"approved", "approved,lgtm", "inline", "lgtm"},
				MissingLabels: []string{"blocked", "hold", "inline-blocked", "wip"},
				Author:        "bot", Milestone: "next", ReviewApprovedRequired: true,
			}
			if branches == "includedBranches" {
				want.IncludedBranches = []string{"inline", "main", "next", "release"}
			} else {
				want.ExcludedBranches = []string{"inline", "main", "next", "release"}
			}
			if diff := cmp.Diff(TideQueries{want}, cfg.Tide.ResolvedQueries(), cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("resolved queries differ (-want +got): %s", diff)
			}
			if !cfg.AllRepos.Equal(sets.New(want.Repos...)) {
				t.Errorf("AllRepos = %v, want %v", cfg.AllRepos, want.Repos)
			}
			var authored Config
			if err := yaml.Unmarshal([]byte(source), &authored); err != nil {
				t.Fatal(err)
			}
			written, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var roundTrip Config
			if err := yaml.Unmarshal(written, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(authored.Tide.Queries, roundTrip.Tide.Queries); diff != "" {
				t.Errorf("authored queries changed (-want +got): %s", diff)
			}
			if diff := cmp.Diff(authored.Tide.QueryPresets, roundTrip.Tide.QueryPresets); diff != "" {
				t.Errorf("authored presets changed (-want +got): %s", diff)
			}
			loadedAgain, err := loadQueryPresetConfig(t, true, string(written), nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(cfg.Tide.ResolvedQueries(), loadedAgain.Tide.ResolvedQueries()); diff != "" {
				t.Errorf("round trip changed runtime queries (-want +got): %s", diff)
			}
		})
	}
}

func TestQueryPresetScalarPresence(t *testing.T) {
	t.Parallel()
	const source = `tide:
  query_presets:
  - name: explicit
    author: ""
    milestone: ""
    reviewApprovedRequired: false
  queries:
  - repos: [org/first]
    presets: [explicit]
  - repos: [org/second]
    author: ""
    milestone: ""
    reviewApprovedRequired: false
`
	cfg, err := loadQueryPresetConfig(t, true, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := TideQueries{{Repos: []string{"org/first", "org/second"}}}
	if diff := cmp.Diff(want, cfg.Tide.ResolvedQueries()); diff != "" {
		t.Fatalf("resolved queries differ (-want +got): %s", diff)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadQueryPresetConfig(t, true, string(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(cfg.Tide.Queries, reloaded.Tide.Queries); diff != "" {
		t.Errorf("query scalar presence lost (-want +got): %s", diff)
	}
	if diff := cmp.Diff(cfg.Tide.QueryPresets, reloaded.Tide.QueryPresets); diff != "" {
		t.Errorf("preset scalar presence lost (-want +got): %s", diff)
	}
}

func TestInvalidQueryPresets(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name, source string
		want         []string
	}{
		{"unknown", "tide: {queries: [{repos: [org/repo], presets: [missing]}]}", []string{"tide.queries[0].presets", "unknown preset", "missing"}},
		{"case sensitive", "tide: {query_presets: [{name: Common}], queries: [{repos: [org/repo], presets: [common]}]}", []string{"unknown preset", "common"}},
		{"repeated reference", "tide: {query_presets: [{name: common}], queries: [{repos: [org/repo], presets: [common, common]}]}", []string{"tide.queries[0].presets", "referenced more than once"}},
		{"duplicate definition", "tide: {query_presets: [{name: common}, {name: common}]}", []string{"tide.query_presets", "common", "defined more than once"}},
		{"empty name", "tide: {query_presets: [{name: ''}]}", []string{"tide.query_presets", "name must not be empty"}},
		{"missing name", "tide: {query_presets: [{labels: [approved]}]}", []string{"tide.query_presets", "name must not be empty"}},
		{"unused label contradiction", "tide: {query_presets: [{name: unused, labels: [hold], missingLabels: [hold]}]}", []string{"unused", "both required and forbidden"}},
		{"unused branch contradiction", "tide: {query_presets: [{name: unused, includedBranches: [main], excludedBranches: [release]}]}", []string{"unused", "both 'includedBranches' and 'excludedBranches'"}},
		{"missing targets", "tide: {query_presets: [{name: common, labels: [lgtm]}], queries: [{presets: [common]}]}", []string{"tide.queries[0]", "'orgs' and 'repos' cannot both be empty"}},
		{"composed labels", "tide: {query_presets: [{name: common, labels: [hold]}], queries: [{repos: [org/repo], missingLabels: [hold], presets: [common]}]}", []string{"tide.queries[0]", "both required and forbidden"}},
		{"composed branches", "tide: {query_presets: [{name: common, includedBranches: [main]}], queries: [{repos: [org/repo], excludedBranches: [release], presets: [common]}]}", []string{"tide.queries[0]", "both 'includedBranches' and 'excludedBranches'"}},
	}
	for _, field := range []string{"author", "milestone", "reviewApprovedRequired"} {
		value := `""`
		if field == "reviewApprovedRequired" {
			value = "false"
		}
		for _, inline := range []bool{false, true} {
			source := fmt.Sprintf(`tide:
  query_presets:
  - {name: first, %[1]s: %[2]s}
  - {name: second, %[1]s: %[2]s}
  queries:
  - repos: [org/repo]
`, field, value)
			want := []string{"tide.queries[0]." + field, "set more than once"}
			if inline {
				source += fmt.Sprintf("    presets: [first]\n    %s: %s\n", field, value)
			} else {
				source += "    presets: [first, second]\n"
			}
			testCases = append(testCases, struct {
				name, source string
				want         []string
			}{fmt.Sprintf("%s conflict inline=%t", field, inline), source, want})
		}
	}
	for _, field := range []string{"orgs", "repos", "excludedRepos", "labels", "missingLabels", "includedBranches", "excludedBranches"} {
		value := "value"
		if field == "repos" || field == "excludedRepos" {
			value = "org/repo"
		}
		for _, preset := range []bool{false, true} {
			source := fmt.Sprintf("tide: {queries: [{orgs: [org], %s: [%s, %s]}]}", field, value, value)
			if field == "orgs" {
				source = "tide: {queries: [{orgs: [org, org]}]}"
			}
			if field == "repos" {
				source = "tide: {queries: [{repos: [org/repo, org/repo]}]}"
			}
			if preset {
				if field == "orgs" || field == "repos" || field == "excludedRepos" {
					continue
				}
				source = fmt.Sprintf("tide: {query_presets: [{name: unused, %s: [%s, %s]}]}", field, value, value)
			}
			testCases = append(testCases, struct {
				name, source string
				want         []string
			}{fmt.Sprintf("%s duplicates preset=%t", field, preset), source, []string{field, "duplicate entries"}})
		}
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				_, err := loadQueryPresetConfig(t, strict, tc.source, nil)
				if err == nil {
					t.Fatal("expected loading to fail")
				}
				for _, want := range tc.want {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("strict=%t: error %q does not contain %q", strict, err, want)
					}
				}
			}
		})
	}
}

func TestQueryPresetsAcrossFiles(t *testing.T) {
	t.Parallel()
	const main = `tide:
  query_presets:
  - {name: main, labels: [lgtm]}
  queries:
  - repos: [main/repo]
    presets: [main, later]
`
	shards := map[string]string{
		"a-org/repo/_prowconfig.yaml": `tide:
  queries:
  - repos: [a-org/repo]
    presets: [main, later]
`,
		"z-global_prowconfig.yaml": `tide:
  query_presets:
  - {name: later, labels: [approved]}
`,
		"b-org/repo/_prowconfig.yaml": `tide:
  queries:
  - repos: [b-org/repo]
    labels: [approved, lgtm]
`,
	}
	for _, strict := range []bool{false, true} {
		cfg, err := loadQueryPresetConfig(t, strict, main, shards)
		if err != nil {
			t.Fatal(err)
		}
		want := TideQueries{{Repos: []string{"main/repo", "a-org/repo", "b-org/repo"}, Labels: []string{"approved", "lgtm"}}}
		if diff := cmp.Diff(want, cfg.Tide.ResolvedQueries()); diff != "" {
			t.Errorf("resolved queries differ (-want +got): %s", diff)
		}
		if len(cfg.Tide.Queries) != 3 {
			t.Fatalf("authored queries deduplicated: %+v", cfg.Tide.Queries)
		}
		shards["duplicate_prowconfig.yaml"] = "tide: {query_presets: [{name: main, labels: [lgtm]}]}"
		_, err = loadQueryPresetConfig(t, strict, main, shards)
		delete(shards, "duplicate_prowconfig.yaml")
		if err == nil {
			t.Fatal("expected duplicate preset error")
		}
		for _, want := range []string{"main", "defined more than once"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	}
}

func TestQueryPresetsReload(t *testing.T) {
	// The watcher uses the process-wide interrupt manager. Isolate its lifetime
	// so stopping it does not cancel other tests' work.
	if os.Getenv("PROW_TEST_QUERY_PRESET_RELOAD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestQueryPresetsReload$")
		cmd.Env = append(os.Environ(), "PROW_TEST_QUERY_PRESET_RELOAD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("reload test failed: %v\n%s", err, output)
		}
		return
	}
	// Exercise the actual watcher: a rejected composition must not replace the
	// last valid snapshot, and the next valid update must resolve afresh.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	revision := 0
	write := func(label string) {
		t.Helper()
		// Publish a complete config through the same atomic symlink swap used
		// by ConfigMap mounts, avoiding an observable empty file during writes.
		revision++
		name := fmt.Sprintf("..revision-%d", revision)
		revisionDir := filepath.Join(dir, name)
		if err := os.Mkdir(revisionDir, 0755); err != nil {
			t.Fatal(err)
		}
		source := fmt.Sprintf("tide: {query_presets: [{name: common, labels: [%s]}], queries: [{repos: [org/repo], presets: [common]}]}", label)
		if err := os.WriteFile(filepath.Join(revisionDir, "config.yaml"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		next := filepath.Join(dir, "..data-next")
		if err := os.Symlink(name, next); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, filepath.Join(dir, "..data")); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	if err := os.Symlink("..data/config.yaml", path); err != nil {
		t.Fatal(err)
	}
	var agent Agent
	if err := agent.StartWatch(path, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(interrupts.Terminate)
	initial := agent.Config()
	deltas := agent.Subscribe()
	write("broken, broken")
	// A subsequent valid update also confirms that the watcher survived the
	// failed load. Observe rejection before sending that update.
	select {
	case <-deltas:
		t.Fatal("invalid config was accepted")
	case <-time.After(300 * time.Millisecond):
	}
	if agent.Config() != initial {
		t.Fatal("failed reload replaced the valid config")
	}
	write("updated")
	select {
	case delta := <-deltas:
		if diff := cmp.Diff([]string{"updated"}, delta.After.Tide.ResolvedQueries()[0].Labels); diff != "" {
			t.Errorf("new preset was not resolved (-want +got): %s", diff)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for valid reload")
	}
}

func TestQueryPresetDeduplicationKeepsTenantsSeparate(t *testing.T) {
	t.Parallel()
	const source = `prowjob_default_entries:
- repo: org/tenanted
  config:
    tenant_id: tenant

tide:
  query_presets:
  - {name: common, labels: [approved]}
  - {name: equivalent, labels: [approved]}
  queries:
  - repos: [org/first]
    presets: [common]
  - repos: [org/second]
    presets: [equivalent]
  - repos: [org/tenanted]
    presets: [common]
`
	cfg, err := loadQueryPresetConfig(t, true, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.Tide.ResolvedQueries()); got != 2 {
		t.Fatalf("got %d resolved queries, want 2 tenant-separated queries", got)
	}
	for _, query := range cfg.Tide.ResolvedQueries() {
		if len(query.Repos) == 1 {
			if query.Repos[0] != "org/tenanted" {
				t.Errorf("unexpected separate query: %+v", query)
			}
		} else if diff := cmp.Diff([]string{"org/first", "org/second"}, query.Repos); diff != "" {
			t.Errorf("equivalent presets did not deduplicate (-want +got): %s", diff)
		}
	}
}

func TestQueryPresetYAMLCompatibility(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		// Permissive loading continues to accept duplicate unrelated keys.
		"prowjob_namespace: old\nprowjob_namespace: current\ntide: {queries: [{repos: [org/repo]}]}",
		// Preset definitions can use ordinary YAML aliases and merge keys.
		`tide:
  query_presets: &presets
  - name: common
    <<: &criteria {labels: [approved]}
  - name: additional
    <<: *criteria
    missingLabels: [hold]
  queries:
  - repos: [org/repo]
    presets: [common, additional]
`,
		`shared: &shared
  tide:
    query_presets:
    - {name: common, labels: [approved]}
    queries:
    - repos: [org/repo]
      presets: [common]
<<: *shared
`,
	} {
		if _, err := loadQueryPresetConfig(t, false, source, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnusedQueryPresetsDoNotEnableTide(t *testing.T) {
	t.Parallel()
	cfg, err := loadQueryPresetConfig(t, true, `tide:
  query_presets:
  - {name: criteria, labels: [approved]}
  - {name: additional, missingLabels: [hold]}
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tide.ResolvedQueries()) != 0 || len(cfg.AllRepos) != 0 {
		t.Fatalf("unused presets activated Tide: queries=%+v, repos=%v", cfg.Tide.ResolvedQueries(), cfg.AllRepos)
	}
}

func TestQueryPresetStorageIsIndependent(t *testing.T) {
	t.Parallel()
	for _, branches := range []string{"includedBranches", "excludedBranches"} {
		t.Run(branches, func(t *testing.T) {
			source := fmt.Sprintf(`query_presets:
- name: common
  labels: [approved]
  missingLabels: [hold]
  %s: [main]
queries:
- presets: [common]
  orgs: [org]
  repos: [other/repo]
  excludedRepos: [org/excluded]
- presets: [common]
  orgs: [org]
  repos: [other/repo]
  excludedRepos: [org/excluded]
`, branches)
			var tide, original TideGitHubConfig
			for _, cfg := range []*TideGitHubConfig{&tide, &original} {
				if err := yaml.Unmarshal([]byte(source), cfg); err != nil {
					t.Fatal(err)
				}
				if err := cfg.resolveQueries(); err != nil {
					t.Fatal(err)
				}
			}
			query := &tide.ResolvedQueries()[0]
			for _, field := range [][]string{query.Orgs, query.Repos, query.ExcludedRepos, query.Labels, query.MissingLabels, query.IncludedBranches, query.ExcludedBranches} {
				if len(field) > 0 {
					field[0] = "changed"
				}
			}
			if diff := cmp.Diff(original.QueryPresets, tide.QueryPresets); diff != "" {
				t.Errorf("runtime query shares preset storage (-want +got): %s", diff)
			}
			if diff := cmp.Diff(original.ResolvedQueries()[1], tide.ResolvedQueries()[1]); diff != "" {
				t.Errorf("runtime queries share storage (-want +got): %s", diff)
			}
		})
	}
}

func TestQueryPresetUnknownFields(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"orgs", "repos", "excludedRepos", "presets", "lables"} {
		for _, value := range []string{"[org/repo]", "[]", "null"} {
			t.Run(fmt.Sprintf("%s=%s", field, value), func(t *testing.T) {
				source := fmt.Sprintf("tide: {query_presets: [{name: common, labels: [approved], %s: %s}], queries: [{repos: [target/repo], presets: [common]}]}", field, value)
				cfg, err := loadQueryPresetConfig(t, false, source, nil)
				if err != nil {
					t.Fatal(err)
				}
				want := TideQueries{{Repos: []string{"target/repo"}, Labels: []string{"approved"}}}
				if diff := cmp.Diff(want, cfg.Tide.ResolvedQueries()); diff != "" {
					t.Errorf("unknown field affected resolved queries (-want +got): %s", diff)
				}
				_, err = loadQueryPresetConfig(t, true, source, nil)
				if err == nil {
					t.Fatal("expected strict loading to reject unknown fields")
				}
				for _, want := range []string{"config.yaml", "unknown field", field} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
			})
		}
	}
}
