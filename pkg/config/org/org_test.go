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

package org

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/yaml"
)

func TestRepoWithDefaults(t *testing.T) {
	defaults := &RepoSettings{
		AllowSquashMerge: new(true), AllowMergeCommit: new(true), AllowRebaseMerge: new(true),
		DeleteBranchOnMerge: new(true), HasIssues: new(true), HasProjects: new(true), HasWiki: new(true),
		SquashMergeCommitTitle: new("PR_TITLE"), SquashMergeCommitMessage: new("COMMIT_MESSAGES"),
	}
	enabled := Repo{RepoSettings: *defaults}
	disabledSettings := RepoSettings{
		AllowSquashMerge: new(false), AllowMergeCommit: new(false), AllowRebaseMerge: new(false),
		DeleteBranchOnMerge: new(false), HasIssues: new(false), HasProjects: new(false), HasWiki: new(false),
		SquashMergeCommitTitle: new("COMMIT_OR_PR_TITLE"), SquashMergeCommitMessage: new("PR_BODY"),
	}
	disabled := Repo{RepoSettings: disabledSettings}
	metadata := Repo{
		Description: new("description"), HomePage: new("https://example.com"),
		Visibility: new(github.RepoVisibilityPrivate), Archived: new(true), DefaultBranch: new("main"),
		Previously: []string{"old-name"}, Collaborators: map[string]github.RepoPermissionLevel{"user": github.Read},
		OnCreate: &RepoCreateOptions{AutoInit: new(true)},
	}
	metadataWithDefaults := enabled
	metadataWithDefaults.Description = metadata.Description
	metadataWithDefaults.HomePage = metadata.HomePage
	metadataWithDefaults.Visibility = metadata.Visibility
	metadataWithDefaults.Archived = metadata.Archived
	metadataWithDefaults.DefaultBranch = metadata.DefaultBranch
	metadataWithDefaults.Previously = metadata.Previously
	metadataWithDefaults.Collaborators = metadata.Collaborators
	metadataWithDefaults.OnCreate = metadata.OnCreate

	for _, tc := range []struct {
		name     string
		repo     Repo
		defaults *RepoSettings
		want     Repo
	}{
		{name: "no defaults", repo: metadata, want: metadata},
		{name: "empty defaults", defaults: &RepoSettings{}, want: Repo{}},
		{name: "inherit all supported fields", defaults: defaults, want: enabled},
		{name: "false overrides true", repo: disabled, defaults: defaults, want: disabled},
		{name: "true overrides false", repo: enabled, defaults: &disabledSettings, want: enabled},
		{name: "partial defaults leave other fields unset", defaults: &RepoSettings{HasWiki: new(false)}, want: Repo{RepoSettings: RepoSettings{HasWiki: new(false)}}},
		{name: "explicit empty strings override defaults", repo: Repo{RepoSettings: RepoSettings{
			SquashMergeCommitTitle: new(""), SquashMergeCommitMessage: new(""),
		}}, defaults: &RepoSettings{SquashMergeCommitTitle: new("PR_TITLE"), SquashMergeCommitMessage: new("COMMIT_MESSAGES")},
			want: Repo{RepoSettings: RepoSettings{SquashMergeCommitTitle: new(""), SquashMergeCommitMessage: new("")}}},
		{name: "preserve repository metadata", repo: metadata, defaults: defaults, want: metadataWithDefaults},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeRepo, err := json.Marshal(tc.repo)
			if err != nil {
				t.Fatal(err)
			}
			beforeDefaults, err := json.Marshal(tc.defaults)
			if err != nil {
				t.Fatal(err)
			}
			if got := tc.repo.WithDefaults(tc.defaults); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("unexpected effective repo: %s", diff.Diff(tc.want, got))
			}
			afterRepo, _ := json.Marshal(tc.repo)
			afterDefaults, _ := json.Marshal(tc.defaults)
			if string(beforeRepo) != string(afterRepo) || string(beforeDefaults) != string(afterDefaults) {
				t.Fatal("resolving defaults modified the source configuration")
			}
		})
	}
}

func TestRepoDefaultsConfig(t *testing.T) {
	var cfg FullConfig
	if err := yaml.Unmarshal([]byte(`orgs:
  example:
    repo_defaults:
      allow_merge_commit: false
      delete_branch_on_merge: true
      squash_merge_commit_title: PR_TITLE
      squash_merge_commit_message: COMMIT_MESSAGES
    repos:
      inherited: {}
      overridden:
        delete_branch_on_merge: false
        squash_merge_commit_title: COMMIT_OR_PR_TITLE
        squash_merge_commit_message: PR_BODY
      null-value:
        delete_branch_on_merge: null
`), &cfg); err != nil {
		t.Fatal(err)
	}
	org := cfg.Orgs["example"]
	wantDefaults := &RepoSettings{
		AllowMergeCommit: new(false), DeleteBranchOnMerge: new(true),
		SquashMergeCommitTitle: new("PR_TITLE"), SquashMergeCommitMessage: new("COMMIT_MESSAGES"),
	}
	if !reflect.DeepEqual(org.RepoDefaults, wantDefaults) {
		t.Fatalf("unexpected defaults: %s", diff.Diff(wantDefaults, org.RepoDefaults))
	}
	for name, repo := range org.Repos {
		got := repo.WithDefaults(org.RepoDefaults)
		want := name != "overridden"
		if got.DeleteBranchOnMerge == nil || *got.DeleteBranchOnMerge != want || got.AllowMergeCommit == nil || *got.AllowMergeCommit {
			t.Errorf("repo %s did not resolve defaults and overrides correctly: %+v", name, got)
		}
		wantTitle, wantMessage := "PR_TITLE", "COMMIT_MESSAGES"
		if name == "overridden" {
			wantTitle, wantMessage = "COMMIT_OR_PR_TITLE", "PR_BODY"
		}
		if got.SquashMergeCommitTitle == nil || *got.SquashMergeCommitTitle != wantTitle || got.SquashMergeCommitMessage == nil || *got.SquashMergeCommitMessage != wantMessage {
			t.Errorf("repo %s did not resolve squash commit settings correctly: %+v", name, got)
		}
	}
	if org.Repos["inherited"].DeleteBranchOnMerge != nil {
		t.Fatal("resolving defaults changed the source repo")
	}

	for _, marshal := range []struct {
		name string
		fn   func(any) ([]byte, error)
	}{
		{name: "json", fn: json.Marshal},
		{name: "yaml", fn: yaml.Marshal},
	} {
		t.Run(marshal.name, func(t *testing.T) {
			data, err := marshal.fn(Config{Repos: map[string]Repo{"example": {}}})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "repo_defaults") {
				t.Fatalf("absent defaults should not appear in dumps: %s", data)
			}
			data, err = marshal.fn(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "RepoSettings") || strings.Contains(string(data), "repo_settings") {
				t.Fatalf("embedded settings must remain flat: %s", data)
			}
			var roundTrip FullConfig
			if err := yaml.UnmarshalStrict(data, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg, roundTrip) {
				t.Errorf("config changed during round trip: %s", diff.Diff(cfg, roundTrip))
			}
		})
	}
}

func TestPrivacy(t *testing.T) {
	get := func(v Privacy) *Privacy {
		return &v
	}
	cases := []struct {
		input    string
		expected *Privacy
	}{
		{
			"secret",
			get(Secret),
		},
		{
			"closed",
			get(Closed),
		},
		{
			"",
			nil,
		},
		{
			"unknown",
			nil,
		},
	}
	for _, tc := range cases {
		var actual Privacy
		err := json.Unmarshal([]byte("\""+tc.input+"\""), &actual)
		switch {
		case err == nil && tc.expected == nil:
			t.Errorf("%s: failed to receive an error", tc.input)
		case err != nil && tc.expected != nil:
			t.Errorf("%s: unexpected error: %v", tc.input, err)
		case err == nil && *tc.expected != actual:
			t.Errorf("%s: actual %v != expected %v", tc.input, tc.expected, actual)
		}
	}
}

func TestPruneRepoDefaults(t *testing.T) {
	empty := ""
	nonEmpty := "string that is not empty"
	yes := true
	no := false
	master := "master"
	notMaster := "not-master"
	testCases := []struct {
		description string
		repo        Repo
		expected    Repo
	}{
		{
			description: "default values are pruned",
			repo: Repo{
				RepoSettings: RepoSettings{
					HasIssues: &yes, HasProjects: &yes, HasWiki: &yes,
					AllowSquashMerge: &yes, AllowMergeCommit: &yes, AllowRebaseMerge: &yes,
					DeleteBranchOnMerge: &no,
				},
				Description: &empty, HomePage: &empty, Visibility: new(github.RepoVisibilityPublic),
				DefaultBranch: &master, Archived: &no,
			},
			expected: Repo{RepoSettings: RepoSettings{HasProjects: &yes}},
		},
		{
			description: "non-default values are not pruned",
			repo: Repo{
				RepoSettings: RepoSettings{
					HasIssues: &no, HasProjects: &no, HasWiki: &no,
					AllowSquashMerge: &no, AllowMergeCommit: &no, AllowRebaseMerge: &no,
					DeleteBranchOnMerge: &yes,
				},
				Description: &nonEmpty, HomePage: &nonEmpty, Visibility: new(github.RepoVisibilityPrivate),
				DefaultBranch: &notMaster, Archived: &yes,
			},
			expected: Repo{
				RepoSettings: RepoSettings{
					HasIssues: &no, HasProjects: &no, HasWiki: &no,
					AllowSquashMerge: &no, AllowMergeCommit: &no, AllowRebaseMerge: &no,
					DeleteBranchOnMerge: &yes,
				},
				Description: &nonEmpty, HomePage: &nonEmpty, Visibility: new(github.RepoVisibilityPrivate),
				DefaultBranch: &notMaster, Archived: &yes,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			pruned := PruneRepoDefaults(tc.repo)
			if !reflect.DeepEqual(tc.expected, pruned) {
				t.Errorf("%s: result differs from expected:\n", diff.Diff(tc.expected, pruned))
			}
		})
	}
}

func TestRepoUnmarshalJSON(t *testing.T) {
	testCases := []struct {
		name        string
		json        string
		expected    Repo
		expectError bool
	}{
		{
			name: "flat settings coexist with legacy private field",
			json: `{"private": true, "description": "test", "allow_squash_merge": true,
				"allow_merge_commit": false, "allow_rebase_merge": false, "delete_branch_on_merge": true,
				"has_issues": true, "has_projects": false, "has_wiki": false,
				"squash_merge_commit_title": "PR_TITLE", "squash_merge_commit_message": "COMMIT_MESSAGES"}`,
			expected: Repo{
				RepoSettings: RepoSettings{
					AllowSquashMerge: new(true), AllowMergeCommit: new(false), AllowRebaseMerge: new(false),
					DeleteBranchOnMerge: new(true), HasIssues: new(true), HasProjects: new(false), HasWiki: new(false),
					SquashMergeCommitTitle: new("PR_TITLE"), SquashMergeCommitMessage: new("COMMIT_MESSAGES"),
				},
				Visibility: new(github.RepoVisibilityPrivate), Description: new("test"),
			},
		},
		{
			name:     "private true translates to visibility private",
			json:     `{"private": true}`,
			expected: Repo{Visibility: new(github.RepoVisibilityPrivate)},
		},
		{
			name:     "private false translates to visibility public",
			json:     `{"private": false}`,
			expected: Repo{Visibility: new(github.RepoVisibilityPublic)},
		},
		{
			name:     "visibility internal is accepted directly",
			json:     `{"visibility": "internal"}`,
			expected: Repo{Visibility: new(github.RepoVisibilityInternal)},
		},
		{
			name:     "visibility private is accepted directly",
			json:     `{"visibility": "private"}`,
			expected: Repo{Visibility: new(github.RepoVisibilityPrivate)},
		},
		{
			name:        "both private and visibility set produces error",
			json:        `{"private": true, "visibility": "private"}`,
			expectError: true,
		},
		{
			name:     "neither private nor visibility set leaves visibility nil",
			json:     `{"description": "test"}`,
			expected: Repo{Description: new("test")},
		},
		{
			name:        "unknown visibility value produces error",
			json:        `{"visibility": "interal"}`,
			expectError: true,
		},
		{
			name:     "private null with visibility set does not conflict",
			json:     `{"private": null, "visibility": "internal"}`,
			expected: Repo{Visibility: new(github.RepoVisibilityInternal)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actual Repo
			err := json.Unmarshal([]byte(tc.json), &actual)
			if tc.expectError {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("result differs from expected:\n%s", diff.Diff(tc.expected, actual))
			}
		})
	}
}

func TestValidateRoles(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorPart   string
	}{
		{
			name: "no roles - should pass",
			config: Config{
				Teams: map[string]Team{
					"team1": {Members: []string{"user1"}},
				},
			},
			expectError: false,
		},
		{
			name: "valid role with existing team",
			config: Config{
				Teams: map[string]Team{
					"security-team": {Members: []string{"user1"}},
				},
				Admins: []string{"user1"},
				Roles: map[string]Role{
					"security-manager": {
						Teams: []string{"security-team"},
						Users: []string{"user1"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "invalid role with non-existent team",
			config: Config{
				Teams: map[string]Team{
					"existing-team": {Members: []string{"user1"}},
				},
				Members: []string{"user1"},
				Roles: map[string]Role{
					"security-manager": {
						Teams: []string{"non-existent-team"},
					},
				},
			},
			expectError: true,
			errorPart:   "non-existent-team",
		},
		{
			name: "multiple roles with mixed valid and invalid teams",
			config: Config{
				Teams: map[string]Team{
					"valid-team": {Members: []string{"user1"}},
				},
				Members: []string{"user1"},
				Roles: map[string]Role{
					"role1": {
						Teams: []string{"valid-team"},
					},
					"role2": {
						Teams: []string{"invalid-team"},
					},
				},
			},
			expectError: true,
			errorPart:   "invalid-team",
		},
		{
			name: "role with nested team reference",
			config: Config{
				Teams: map[string]Team{
					"parent-team": {
						Members: []string{"user1"},
						Children: map[string]Team{
							"child-team": {
								Members: []string{"user2"},
							},
						},
					},
				},
				Members: []string{"user1", "user2"},
				Roles: map[string]Role{
					"security-manager": {
						Teams: []string{"child-team"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "role with only users (no teams)",
			config: Config{
				Teams: map[string]Team{
					"some-team": {Members: []string{"user1"}},
				},
				Admins:  []string{"user1"},
				Members: []string{"user2"},
				Roles: map[string]Role{
					"security-manager": {
						Users: []string{"user1", "user2"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "role references user who is not org member",
			config: Config{
				Admins:  []string{"admin-user"},
				Members: []string{"member-user"},
				Roles: map[string]Role{
					"security-manager": {
						Users: []string{"non-member-user"},
					},
				},
			},
			expectError: true,
			errorPart:   "non-member-user",
		},
		{
			name: "role references user with different casing - should pass",
			config: Config{
				Admins: []string{"AdminUser"},
				Roles: map[string]Role{
					"security-manager": {
						Users: []string{"adminuser"}, // Different casing but same user
					},
				},
			},
			expectError: false,
		},
		{
			name: "role with both valid and invalid users",
			config: Config{
				Admins: []string{"valid-user"},
				Roles: map[string]Role{
					"security-manager": {
						Users: []string{"valid-user", "invalid-user"},
					},
				},
			},
			expectError: true,
			errorPart:   "invalid-user",
		},
		{
			name: "role with case-insensitive team matching",
			config: Config{
				Teams: map[string]Team{
					"Security-Team": {Members: []string{"user1"}},
				},
				Admins: []string{"user1"},
				Roles: map[string]Role{
					"admin": {
						Teams: []string{"security-team"}, // Different case
					},
				},
			},
			expectError: false,
		},
		{
			name: "role with deeply nested teams",
			config: Config{
				Teams: map[string]Team{
					"level1": {
						Children: map[string]Team{
							"level2": {
								Children: map[string]Team{
									"level3": {Members: []string{"user1"}},
								},
							},
						},
					},
				},
				Members: []string{"user1"},
				Roles: map[string]Role{
					"role1": {
						Teams: []string{"level3"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "role with duplicate team references (should pass)",
			config: Config{
				Teams: map[string]Team{
					"team1": {Members: []string{"user1"}},
				},
				Admins: []string{"user1"},
				Roles: map[string]Role{
					"role1": {
						Teams: []string{"team1", "team1"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "case-insensitive role name collision",
			config: Config{
				Teams: map[string]Team{
					"team1": {},
				},
				Admins: []string{"user1"},
				Roles: map[string]Role{
					"Security-Manager": {Teams: []string{"team1"}},
					"security-manager": {Users: []string{"user1"}},
				},
			},
			expectError: true,
			errorPart:   "role name collision",
		},
		{
			name: "role user not validated when membership is not declared",
			config: Config{
				Teams: map[string]Team{
					"security-team": {},
				},
				// No Members/Admins declared: org membership is managed elsewhere
				// (--fix-org-roles requires --fix-teams, not --fix-org-members),
				// so role user references cannot and must not be validated here.
				Roles: map[string]Role{
					"security-manager": {
						Teams: []string{"security-team"},
						Users: []string{"externally-managed-user"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "role team still validated when membership is not declared",
			config: Config{
				Teams: map[string]Team{
					"security-team": {},
				},
				// No Members/Admins, but an undefined team must still fail.
				Roles: map[string]Role{
					"security-manager": {
						Teams: []string{"missing-team"},
						Users: []string{"externally-managed-user"},
					},
				},
			},
			expectError: true,
			errorPart:   "missing-team",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.ValidateRoles()
			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				} else if tc.errorPart != "" && !strings.Contains(err.Error(), tc.errorPart) {
					t.Errorf("Expected error to contain %q, but got: %v", tc.errorPart, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestRepoUnmarshalYAML(t *testing.T) {
	const configYAML = `
orgs:
  test-org:
    repos:
      test-repo:
        private: true
`

	var config FullConfig
	if err := yaml.Unmarshal([]byte(configYAML), &config); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repo := config.Orgs["test-org"].Repos["test-repo"]
	if repo.Visibility == nil {
		t.Fatal("expected visibility to be set")
	}
	if got, want := *repo.Visibility, github.RepoVisibilityPrivate; got != want {
		t.Errorf("visibility = %q, want %q", got, want)
	}
}
