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
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/prow/pkg/config/org"
	"sigs.k8s.io/prow/pkg/github"
)

func TestConfigureReposWithDefaults(t *testing.T) {
	defaults := &org.RepoDefaults{
		AllowSquashMerge:         new(true),
		AllowMergeCommit:         new(true),
		AllowRebaseMerge:         new(true),
		DeleteBranchOnMerge:      new(true),
		HasIssues:                new(true),
		HasProjects:              new(true),
		HasWiki:                  new(true),
		SquashMergeCommitTitle:   new("PR_TITLE"),
		SquashMergeCommitMessage: new("COMMIT_MESSAGES"),
	}
	enabled := github.FullRepo{
		Repo: github.Repo{
			Name:        "repo",
			HasIssues:   true,
			HasProjects: true,
			HasWiki:     true,
		},
		AllowSquashMerge:         true,
		AllowMergeCommit:         true,
		AllowRebaseMerge:         true,
		DeleteBranchOnMerge:      true,
		SquashMergeCommitTitle:   "PR_TITLE",
		SquashMergeCommitMessage: "COMMIT_MESSAGES",
	}
	disabled := github.FullRepo{
		Repo: github.Repo{Name: "repo"},
	}
	deleteBranches := github.FullRepo{
		Repo:                github.Repo{Name: "repo"},
		DeleteBranchOnMerge: true,
	}
	archived := github.FullRepo{
		Repo: github.Repo{
			Name:     "repo",
			Archived: true,
		},
	}
	disabledSquashDefaults := &org.RepoDefaults{
		AllowSquashMerge:         new(false),
		DeleteBranchOnMerge:      new(true),
		SquashMergeCommitTitle:   new("COMMIT_OR_PR_TITLE"),
		SquashMergeCommitMessage: new("PR_BODY"), // Invalid pair must be ignored when squash is disabled.
	}
	for _, tc := range []struct {
		name     string
		defaults *org.RepoDefaults
		repo     org.Repo
		existing []github.FullRepo
		want     github.FullRepo
	}{
		{
			name:     "create with inherited defaults",
			defaults: defaults,
			want:     enabled,
		},
		{
			name:     "update with inherited defaults",
			defaults: defaults,
			existing: []github.FullRepo{disabled},
			want:     enabled,
		},
		{
			name: "update with false override",
			defaults: &org.RepoDefaults{
				DeleteBranchOnMerge: new(true),
			},
			repo: org.Repo{
				RepoSettings: org.RepoSettings{DeleteBranchOnMerge: new(false)},
			},
			existing: []github.FullRepo{deleteBranches},
			want:     disabled,
		},
		{
			name:     "absent defaults leave unspecified settings alone",
			existing: []github.FullRepo{enabled},
			want:     enabled,
		},
		{
			name:     "create ignores inherited disabled squash settings but applies other defaults",
			defaults: disabledSquashDefaults,
			want:     deleteBranches,
		},
		{
			name:     "update ignores inherited disabled squash settings but applies other defaults",
			defaults: disabledSquashDefaults,
			existing: []github.FullRepo{disabled},
			want:     deleteBranches,
		},
		{
			name:     "archived repo remains untouched",
			defaults: defaults,
			repo: org.Repo{
				Archived: new(true),
			},
			existing: []github.FullRepo{archived},
			want:     archived,
		},
		{
			name:     "archived repo with unspecified archival remains untouched",
			defaults: defaults,
			existing: []github.FullRepo{archived},
			want:     archived,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := makeFakeRepoClient(t, tc.existing...)
			cfg := org.Config{
				RepoDefaults: tc.defaults,
				Repos: map[string]org.Repo{
					"repo": tc.repo,
				},
			}
			if err := configureRepos(options{}, fc, "example", cfg); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(map[string]github.FullRepo{"repo": tc.want}, fc.repos); diff != "" {
				t.Errorf("unexpected repositories (-want +got): %s", diff)
			}
		})
	}

	t.Run("unlisted repositories and source configuration are unchanged", func(t *testing.T) {
		unlisted := github.FullRepo{
			Repo: github.Repo{Name: "unlisted"},
		}
		fc := makeFakeRepoClient(t, unlisted)
		cfg := org.Config{
			RepoDefaults: defaults,
			Repos:        map[string]org.Repo{"repo": {}},
		}
		before, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := configureRepos(options{}, fc, "example", cfg); err != nil {
			t.Fatal(err)
		}
		want := map[string]github.FullRepo{
			"repo":     enabled,
			"unlisted": unlisted,
		}
		if diff := cmp.Diff(want, fc.repos); diff != "" {
			t.Errorf("unexpected repositories (-want +got): %s", diff)
		}
		after, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(string(before), string(after)); diff != "" {
			t.Errorf("source configuration was modified: %s", diff)
		}
	})
}

func TestValidateSquashMergeSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		title   string
		message string
		wantErr bool
	}{
		{
			name:    "PR_TITLE with PR_BODY",
			title:   "PR_TITLE",
			message: "PR_BODY",
		},
		{
			name:    "PR_TITLE with BLANK",
			title:   "PR_TITLE",
			message: "BLANK",
		},
		{
			name:    "PR_TITLE with COMMIT_MESSAGES",
			title:   "PR_TITLE",
			message: "COMMIT_MESSAGES",
		},
		{
			name:    "COMMIT_OR_PR_TITLE with COMMIT_MESSAGES",
			title:   "COMMIT_OR_PR_TITLE",
			message: "COMMIT_MESSAGES",
		},
		{
			name:    "incompatible pair",
			title:   "COMMIT_OR_PR_TITLE",
			message: "PR_BODY",
			wantErr: true,
		},
		{
			name:    "empty title",
			message: "COMMIT_MESSAGES",
			wantErr: true,
		},
		{
			name:    "unknown title",
			title:   "INVALID",
			message: "COMMIT_MESSAGES",
			wantErr: true,
		},
		{
			name:    "empty message",
			title:   "PR_TITLE",
			wantErr: true,
		},
		{
			name:    "unknown message",
			title:   "PR_TITLE",
			message: "INVALID",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := github.RepoRequest{
				SquashMergeCommitTitle:   &tc.title,
				SquashMergeCommitMessage: &tc.message,
			}
			if err := validateSquashMergeSettings(github.FullRepo{}, request); (err != nil) != tc.wantErr {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}

	current := github.FullRepo{
		SquashMergeCommitTitle:   "PR_TITLE",
		SquashMergeCommitMessage: "PR_BODY",
	}
	for _, tc := range []struct {
		name    string
		request github.RepoRequest
		wantErr bool
	}{
		{
			name: "message-only change uses current title",
			request: github.RepoRequest{
				SquashMergeCommitMessage: new("BLANK"),
			},
		},
		{
			name: "title-only change uses current message",
			request: github.RepoRequest{
				SquashMergeCommitTitle: new("COMMIT_OR_PR_TITLE"),
			},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSquashMergeSettings(current, tc.request); (err != nil) != tc.wantErr {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
	t.Run("no squash changes ignore invalid current pair", func(t *testing.T) {
		if err := validateSquashMergeSettings(github.FullRepo{}, github.RepoRequest{}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestConfigureReposSquashSettings(t *testing.T) {
	for _, tc := range []struct {
		name         string
		existing     bool
		currentTitle string
		currentAllow *bool
		defaults     *org.RepoDefaults
		settings     org.RepoSettings
		wantErr      bool
	}{
		{
			name: "create with message only uses default title",
			settings: org.RepoSettings{
				SquashMergeCommitMessage: new("PR_BODY"),
			},
			wantErr: true,
		},
		{
			name:         "valid message-only update uses current title",
			existing:     true,
			currentTitle: "PR_TITLE",
			settings: org.RepoSettings{
				SquashMergeCommitMessage: new("PR_BODY"),
			},
		},
		{
			name:     "invalid message-only update uses current title",
			existing: true,
			settings: org.RepoSettings{
				SquashMergeCommitMessage: new("PR_BODY"),
			},
			wantErr: true,
		},
		{
			name: "inherited message and overridden title form invalid pair",
			defaults: &org.RepoDefaults{
				SquashMergeCommitTitle:   new("PR_TITLE"),
				SquashMergeCommitMessage: new("PR_BODY"),
			},
			settings: org.RepoSettings{
				SquashMergeCommitTitle: new("COMMIT_OR_PR_TITLE"),
			},
			wantErr: true,
		},
		{
			name:     "inherited invalid pair is rejected on update",
			existing: true,
			defaults: &org.RepoDefaults{
				SquashMergeCommitTitle:   new("PR_TITLE"),
				SquashMergeCommitMessage: new("PR_BODY"),
			},
			settings: org.RepoSettings{
				SquashMergeCommitTitle: new("COMMIT_OR_PR_TITLE"),
			},
			wantErr: true,
		},
		{
			name:         "enabling squash applies squash settings",
			existing:     true,
			currentAllow: new(false),
			settings: org.RepoSettings{
				AllowSquashMerge:         new(true),
				SquashMergeCommitTitle:   new("PR_TITLE"),
				SquashMergeCommitMessage: new("PR_BODY"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := makeFakeRepoClient(t)
			current := github.FullRepo{
				Repo: github.Repo{
					Name:        "repo",
					Description: "original",
				},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   "COMMIT_OR_PR_TITLE",
				SquashMergeCommitMessage: "COMMIT_MESSAGES",
			}
			if tc.currentTitle != "" {
				current.SquashMergeCommitTitle = tc.currentTitle
			}
			if tc.currentAllow != nil {
				current.AllowSquashMerge = *tc.currentAllow
			}
			if tc.existing {
				fc.repos["repo"] = current
			}
			cfg := org.Config{
				RepoDefaults: tc.defaults,
				Repos: map[string]org.Repo{
					"repo": {
						Description:  new("updated"),
						RepoSettings: tc.settings,
					},
				},
			}
			err := configureRepos(options{}, fc, "example", cfg)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "repo repo: invalid squash merge settings") {
					t.Fatalf("expected clear squash settings error, got %v", err)
				}
				if tc.existing {
					if diff := cmp.Diff(current, fc.repos["repo"]); diff != "" {
						t.Fatalf("invalid request modified repo (-want +got): %s", diff)
					}
				} else if len(fc.repos) != 0 {
					t.Fatal("invalid request created repo")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := fc.repos["repo"]
			if got.Description != "updated" {
				t.Fatal("valid request did not apply other changes")
			}
			if tc.settings.SquashMergeCommitTitle != nil && got.SquashMergeCommitTitle != *tc.settings.SquashMergeCommitTitle {
				t.Fatal("valid request did not apply squash title")
			}
			if tc.settings.SquashMergeCommitMessage != nil && got.SquashMergeCommitMessage != *tc.settings.SquashMergeCommitMessage {
				t.Fatal("valid request did not apply squash message")
			}
			if tc.settings.AllowSquashMerge != nil && got.AllowSquashMerge != *tc.settings.AllowSquashMerge {
				t.Fatal("valid request did not apply squash merge enablement")
			}
		})
	}
}

func TestRepoRequestsOmitDisabledSquashSettings(t *testing.T) {
	settings := org.RepoSettings{
		SquashMergeCommitTitle:   new("PR_TITLE"),
		SquashMergeCommitMessage: new("PR_BODY"),
	}
	for _, tc := range []struct {
		name         string
		currentAllow bool
		wantAllow    *bool
	}{
		{
			name: "already disabled",
		},
		{
			name:      "explicitly disabled",
			wantAllow: new(false),
		},
		{
			name:         "disabled in this request",
			currentAllow: true,
			wantAllow:    new(false),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := org.Repo{
				Description:  new("updated"),
				RepoSettings: settings,
			}
			repo.AllowSquashMerge = tc.wantAllow
			current := github.FullRepo{
				Repo:                     github.Repo{Name: "repo"},
				AllowSquashMerge:         tc.currentAllow,
				SquashMergeCommitTitle:   "COMMIT_OR_PR_TITLE",
				SquashMergeCommitMessage: "COMMIT_MESSAGES",
			}
			request := newRepoUpdateRequest(current, "repo", repo)
			if request.SquashMergeCommitTitle != nil || request.SquashMergeCommitMessage != nil {
				t.Fatal("disabled squash settings included in update request")
			}
			if request.Description == nil || *request.Description != "updated" {
				t.Fatal("other changes missing from request")
			}

		})
	}

	t.Run("create with squash disabled", func(t *testing.T) {
		repo := org.Repo{
			Description:  new("updated"),
			RepoSettings: settings,
		}
		repo.AllowSquashMerge = new(false)
		request := newRepoCreateRequest("repo", repo)
		if request.SquashMergeCommitTitle != nil || request.SquashMergeCommitMessage != nil {
			t.Fatal("disabled squash settings included in create request")
		}
		if request.Description == nil || *request.Description != "updated" {
			t.Fatal("other changes missing from request")
		}

	})
}

func TestConfigureArchivedReposWithUnsetArchival(t *testing.T) {
	current := github.FullRepo{
		Repo: github.Repo{Name: "repo", Archived: true, Description: "original"},
	}
	for _, tc := range []struct {
		name     string
		repoName string
		repo     org.Repo
		defaults *org.RepoDefaults
		wantErr  bool
	}{
		{
			name:     "explicit description change on archived repo with unset archived fails",
			repoName: "repo",
			repo:     org.Repo{Description: new("updated")},
			wantErr:  true,
		},
		{
			name:     "explicit description change on archived repo with defaults fails",
			repoName: "repo",
			repo:     org.Repo{Description: new("updated")},
			defaults: &org.RepoDefaults{DeleteBranchOnMerge: new(true)},
			wantErr:  true,
		},
		{
			name:     "explicit rename on archived repo with unset archived fails",
			repoName: "renamed-repo",
			repo:     org.Repo{Previously: []string{"repo"}},
			wantErr:  true,
		},
		{
			name:     "matching explicit settings on archived repo ignore differing defaults",
			repoName: "repo",
			repo:     org.Repo{Description: new("original")},
			defaults: &org.RepoDefaults{DeleteBranchOnMerge: new(true)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := makeFakeRepoClient(t, current)
			cfg := org.Config{
				RepoDefaults: tc.defaults,
				Repos:        map[string]org.Repo{tc.repoName: tc.repo},
			}
			if err := configureRepos(options{}, fc, "example", cfg); (err != nil) != tc.wantErr {
				t.Fatalf("unexpected configureRepos result: %v", err)
			}
			if diff := cmp.Diff(map[string]github.FullRepo{"repo": current}, fc.repos); diff != "" {
				t.Errorf("archived repository was modified (-want +got): %s", diff)
			}
		})
	}
}

func TestNewRepoUpdateRequestSquashSettings(t *testing.T) {
	title := "PR_TITLE"
	message := "COMMIT_MESSAGES"
	for _, tc := range []struct {
		name     string
		current  github.FullRepo
		repoName string
		repo     org.Repo
		want     github.RepoUpdateRequest
	}{
		{
			name: "request to update squash title works",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: "repo"},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   "COMMIT_OR_PR_TITLE",
				SquashMergeCommitMessage: message,
			},
			repoName: "renamed-repo",
			repo: org.Repo{
				Description: new("updated"),
				RepoSettings: org.RepoSettings{
					SquashMergeCommitTitle:   &title,
					SquashMergeCommitMessage: &message,
				},
			},
			want: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					Name:                   new("renamed-repo"),
					Description:            new("updated"),
					SquashMergeCommitTitle: &title,
				},
			},
		},
		{
			name: "message-only change omits unchanged configured title",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: "repo"},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   title,
				SquashMergeCommitMessage: "PR_BODY",
			},
			repoName: "repo",
			repo: org.Repo{
				RepoSettings: org.RepoSettings{
					SquashMergeCommitTitle:   &title,
					SquashMergeCommitMessage: &message,
				},
			},
			want: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{SquashMergeCommitMessage: &message},
			},
		},
		{
			name: "message-only change does not backfill current title",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: "repo"},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   title,
				SquashMergeCommitMessage: "BLANK",
			},
			repoName: "repo",
			repo:     org.Repo{RepoSettings: org.RepoSettings{SquashMergeCommitMessage: &message}},
			want: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{SquashMergeCommitMessage: &message},
			},
		},
		{
			name: "unchanged squash message produces no delta",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: "repo"},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   title,
				SquashMergeCommitMessage: message,
			},
			repoName: "repo",
			repo:     org.Repo{RepoSettings: org.RepoSettings{SquashMergeCommitMessage: &message}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newRepoUpdateRequest(tc.current, tc.repoName, tc.repo)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("unexpected update request (-want +got): %s", diff)
			}
		})
	}
}
