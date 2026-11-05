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
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/prow/pkg/config/org"
	"sigs.k8s.io/prow/pkg/github"
)

type fakeRepoClient struct {
	t     *testing.T
	repos map[string]github.FullRepo
}

func (f fakeRepoClient) GetRepo(owner, name string) (github.FullRepo, error) {
	repo, ok := f.repos[name]
	if !ok {
		return repo, fmt.Errorf("repo not found")
	}
	return repo, nil
}

func (f fakeRepoClient) GetRepos(orgName string, isUser bool) ([]github.Repo, error) {
	if orgName == "fail" {
		return nil, fmt.Errorf("injected GetRepos failure")
	}

	repos := make([]github.Repo, 0, len(f.repos))
	for _, repo := range f.repos {
		repos = append(repos, repo.Repo)
	}

	// sort for deterministic output
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Name < repos[j].Name
	})

	return repos, nil
}

func (f fakeRepoClient) CreateRepo(owner string, isUser bool, repoReq github.RepoCreateRequest) (*github.FullRepo, error) {
	if *repoReq.Name == "fail" {
		return nil, fmt.Errorf("injected CreateRepo failure")
	}

	if _, hasRepo := f.repos[*repoReq.Name]; hasRepo {
		f.t.Errorf("CreateRepo() called on repo that already exists")
		return nil, fmt.Errorf("CreateRepo() called on repo that already exists")
	}

	repo := repoReq.ToRepo()
	f.repos[*repoReq.Name] = *repo

	return repo, nil
}

// updateIfSet copies *want into *have when want is non-nil.
func updateIfSet[T any](have, want *T) {
	if want != nil {
		*have = *want
	}
}

func (f fakeRepoClient) UpdateRepo(owner, name string, want github.RepoUpdateRequest) (*github.FullRepo, error) {
	if name == "fail" {
		return nil, fmt.Errorf("injected UpdateRepo failure")
	}
	if want.Archived != nil && !*want.Archived {
		f.t.Errorf("UpdateRepo() called to unarchive a repo (not supported by API)")
		return nil, fmt.Errorf("UpdateRepo() called to unarchive a repo (not supported by API)")
	}

	have, exists := f.repos[name]
	if !exists {
		f.t.Errorf("UpdateRepo() called on repo that does not exists")
		return nil, fmt.Errorf("UpdateRepo() called on repo that does not exist")
	}

	if have.Archived {
		return nil, fmt.Errorf("Repository was archived so is read-only.")
	}

	updateIfSet(&have.Name, want.Name)
	updateIfSet(&have.DefaultBranch, want.DefaultBranch)
	updateIfSet(&have.Homepage, want.Homepage)
	updateIfSet(&have.Description, want.Description)
	updateIfSet(&have.Archived, want.Archived)
	updateIfSet(&have.Visibility, want.Visibility)
	updateIfSet(&have.HasIssues, want.HasIssues)
	updateIfSet(&have.HasProjects, want.HasProjects)
	updateIfSet(&have.HasWiki, want.HasWiki)
	updateIfSet(&have.AllowSquashMerge, want.AllowSquashMerge)
	updateIfSet(&have.AllowMergeCommit, want.AllowMergeCommit)
	updateIfSet(&have.AllowRebaseMerge, want.AllowRebaseMerge)
	updateIfSet(&have.SquashMergeCommitTitle, want.SquashMergeCommitTitle)
	updateIfSet(&have.SquashMergeCommitMessage, want.SquashMergeCommitMessage)
	updateIfSet(&have.DeleteBranchOnMerge, want.DeleteBranchOnMerge)

	f.repos[name] = have
	return &have, nil
}

func makeFakeRepoClient(t *testing.T, repos ...github.FullRepo) fakeRepoClient {
	fc := fakeRepoClient{
		repos: make(map[string]github.FullRepo, len(repos)),
		t:     t,
	}
	for _, repo := range repos {
		fc.repos[repo.Name] = repo
	}

	return fc
}

func TestConfigureRepos(t *testing.T) {
	orgName := "test-org"
	isOrg := false
	no := false
	yes := true
	updated := "UPDATED"

	oldName := "old"
	oldRepo := github.Repo{
		Name:        oldName,
		FullName:    fmt.Sprintf("%s/%s", orgName, oldName),
		Description: "An old existing repository",
	}

	newName := "new"
	newDescription := "A new repository."
	newConfigRepo := org.Repo{
		Description: &newDescription,
	}
	newRepo := github.Repo{
		Name:        newName,
		Description: newDescription,
	}

	fail := "fail"
	failRepo := github.Repo{
		Name: fail,
	}

	testCases := []struct {
		description     string
		opts            options
		orgConfig       org.Config
		orgNameOverride string
		repos           []github.FullRepo

		expectError   bool
		expectedRepos []github.Repo
	}{
		{
			description:   "survives empty config",
			expectedRepos: []github.Repo{},
		},
		{
			description: "survives nil repos config",
			orgConfig: org.Config{
				Repos: nil,
			},
			expectedRepos: []github.Repo{},
		},
		{
			description: "survives empty repos config",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{},
			},
			expectedRepos: []github.Repo{},
		},
		{
			description: "nonexistent repo is created",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectedRepos: []github.Repo{newRepo, oldRepo},
		},
		{
			description:     "GetRepos failure is propagated",
			orgNameOverride: "fail",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectError:   true,
			expectedRepos: []github.Repo{oldRepo},
		},
		{
			description: "CreateRepo failure is propagated",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					fail: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectError:   true,
			expectedRepos: []github.Repo{oldRepo},
		},
		{
			description: "duplicate repo names different only by case are detected",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"repo": newConfigRepo,
					"REPO": newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectError:   true,
			expectedRepos: []github.Repo{oldRepo},
		},
		{
			description: "existing repo is updated",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},
			expectedRepos: []github.Repo{
				{
					Name:        oldName,
					Description: newDescription,
					FullName:    fmt.Sprintf("%s/%s", orgName, oldName),
				},
			},
		},
		{
			description: "UpdateRepo failure is propagated",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"fail": newConfigRepo,
				},
			},
			repos:         []github.FullRepo{{Repo: failRepo}},
			expectError:   true,
			expectedRepos: []github.Repo{failRepo},
		},
		{
			// https://developer.github.com/v3/repos/#edit
			// "Note: You cannot unarchive repositories through the API."
			// Archived repositories are read-only, and updates fail with 403:
			// "Repository was archived so is read-only."
			description: "request to unarchive a repo fails, repo is read-only",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Archived: &no, Description: &updated},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: true, Description: "OLD"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Archived: true, Description: "OLD"}},
		},
		{
			// https://developer.github.com/v3/repos/#edit
			// "Note: You cannot unarchive repositories through the API."
			// Archived repositories are read-only, and updates fail with 403:
			// "Repository was archived so is read-only."
			description: "no field changes on archived repo",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Archived: &yes, Description: &updated},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: true, Description: "OLD"}}},
			expectError:   false,
			expectedRepos: []github.Repo{{Name: oldName, Archived: true, Description: "OLD"}},
		},
		{
			description: "explicit description change on archived repo with unset archived fails",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Description: &updated},
				},
			},
			repos: []github.FullRepo{
				{
					Repo: github.Repo{
						Name:        oldName,
						Archived:    true,
						Description: "OLD",
					},
				},
			},
			expectError: true,
			expectedRepos: []github.Repo{
				{
					Name:        oldName,
					Archived:    true,
					Description: "OLD",
				},
			},
		},
		{
			description: "explicit description change on archived repo with defaults fails",
			orgConfig: org.Config{
				RepoDefaults: &org.RepoDefaults{
					DeleteBranchOnMerge: &yes,
				},
				Repos: map[string]org.Repo{
					oldName: {Description: &updated},
				},
			},
			repos: []github.FullRepo{
				{
					Repo: github.Repo{
						Name:        oldName,
						Archived:    true,
						Description: "OLD",
					},
				},
			},
			expectError: true,
			expectedRepos: []github.Repo{
				{
					Name:        oldName,
					Archived:    true,
					Description: "OLD",
				},
			},
		},
		{
			description: "explicit rename on archived repo with unset archived fails",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName}},
				},
			},
			repos: []github.FullRepo{
				{
					Repo: github.Repo{
						Name:     oldName,
						Archived: true,
					},
				},
			},
			expectError: true,
			expectedRepos: []github.Repo{
				{
					Name:     oldName,
					Archived: true,
				},
			},
		},
		{
			description: "matching explicit settings on archived repo ignore differing defaults",
			orgConfig: org.Config{
				RepoDefaults: &org.RepoDefaults{
					DeleteBranchOnMerge: &yes,
				},
				Repos: map[string]org.Repo{
					oldName: {Description: &updated},
				},
			},
			repos: []github.FullRepo{
				{
					Repo: github.Repo{
						Name:        oldName,
						Archived:    true,
						Description: updated,
					},
				},
			},
			expectedRepos: []github.Repo{
				{
					Name:        oldName,
					Archived:    true,
					Description: updated,
				},
			},
		},
		{
			description: "request to archive repo fails when not allowed, but updates other fields",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Archived: &yes, Description: &updated},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: false, Description: "OLD"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Archived: false, Description: updated}},
		},
		{
			description: "request to archive repo succeeds when allowed",
			opts: options{
				allowRepoArchival: true,
			},
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Archived: &yes},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: false}}},
			expectedRepos: []github.Repo{{Name: oldName, Archived: true}},
		},
		{
			description: "request to make a private repo public fails when not allowed, but updates other fields",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Visibility: new(github.RepoVisibilityPublic), Description: &updated},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Visibility: github.RepoVisibilityPrivate, Description: "OLD"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Visibility: github.RepoVisibilityPrivate, Description: updated}},
		},
		{
			description: "request to make a private repo public succeeds when allowed",
			opts: options{
				allowRepoPublish: true,
			},
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Visibility: new(github.RepoVisibilityPublic)},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Visibility: github.RepoVisibilityPrivate}}},
			expectedRepos: []github.Repo{{Name: oldName, Visibility: github.RepoVisibilityPublic}},
		},
		{
			description: "request to make an internal repo public fails when not allowed",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Visibility: new(github.RepoVisibilityPublic)},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Visibility: github.RepoVisibilityInternal}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Visibility: github.RepoVisibilityInternal}},
		},
		{
			description: "transitioning private to internal is allowed without flag",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Visibility: new(github.RepoVisibilityInternal)},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Visibility: github.RepoVisibilityPrivate}}},
			expectedRepos: []github.Repo{{Name: oldName, Visibility: github.RepoVisibilityInternal}},
		},
		{
			description: "transitioning internal to private is allowed without flag",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Visibility: new(github.RepoVisibilityPrivate)},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Visibility: github.RepoVisibilityInternal}}},
			expectedRepos: []github.Repo{{Name: oldName, Visibility: github.RepoVisibilityPrivate}},
		},
		{
			description: "renaming a repo is successful",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Description: "renamed repo"}}},
			expectedRepos: []github.Repo{{Name: newName, Description: "renamed repo"}},
		},
		{
			description: "renaming a repo by just changing case is successful",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"repo": {Previously: []string{"REPO"}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: "REPO", Description: "renamed repo"}}},
			expectedRepos: []github.Repo{{Name: "repo", Description: "renamed repo"}},
		},
		{
			description: "dup between a repo name and a previous name is detected",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName}},
					oldName: {Description: &newDescription},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Description: "this repo shall not be touched"}},
		},
		{
			description: "dup between two previous names is detected",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"wants-projects": {Previously: []string{oldName}, HasProjects: &yes, HasWiki: &no},
					"wants-wiki":     {Previously: []string{oldName}, HasProjects: &no, HasWiki: &yes},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Description: "this repo shall not be touched"}},
		},
		{
			description: "error detected when both a repo and a repo of its previous name exist",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName}, Description: &newDescription},
				},
			},
			repos: []github.FullRepo{
				{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}},
				{Repo: github.Repo{Name: newName, Description: "this repo shall not be touched too"}},
			},
			expectError: true,
			expectedRepos: []github.Repo{
				{Name: newName, Description: "this repo shall not be touched too"},
				{Name: oldName, Description: "this repo shall not be touched"},
			},
		},
		{
			description: "error detected when multiple previous repos exist",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName, "even-older"}, Description: &newDescription},
				},
			},
			repos: []github.FullRepo{
				{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}},
				{Repo: github.Repo{Name: "even-older", Description: "this repo shall not be touched too"}},
			},
			expectError: true,
			expectedRepos: []github.Repo{
				{Name: "even-older", Description: "this repo shall not be touched too"},
				{Name: oldName, Description: "this repo shall not be touched"},
			},
		},
		{
			description: "repos are renamed to defined case even without explicit `previously` field",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"CamelCase": {Description: &newDescription},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: "CAMELCASE", Description: newDescription}}},
			expectedRepos: []github.Repo{{Name: "CamelCase", Description: newDescription}},
		},
		{
			description: "avoid creating archived repo",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {Archived: &yes},
				},
			},
			repos:         []github.FullRepo{},
			expectError:   true,
			expectedRepos: []github.Repo{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fc := makeFakeRepoClient(t, tc.repos...)
			var err error
			if len(tc.orgNameOverride) > 0 {
				err = configureRepos(tc.opts, fc, tc.orgNameOverride, tc.orgConfig)
			} else {
				err = configureRepos(tc.opts, fc, orgName, tc.orgConfig)
			}
			if err != nil && !tc.expectError {
				t.Errorf("%s: unexpected error: %v", tc.description, err)
			}
			if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got none", tc.description)
			}

			reposAfter, err := fc.GetRepos(orgName, isOrg)
			if err != nil {
				t.Fatalf("%s: unexpected GetRepos error: %v", tc.description, err)
			}
			if !reflect.DeepEqual(reposAfter, tc.expectedRepos) {
				t.Errorf("%s: unexpected repos after configureRepos():\n%s", tc.description, cmp.Diff(reposAfter, tc.expectedRepos))
			}
		})
	}
}

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
				DeleteBranchOnMerge: new(false),
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

func TestValidateRepos(t *testing.T) {
	description := "cool repo"
	testCases := []struct {
		description string
		config      map[string]org.Repo
		expectError bool
	}{
		{
			description: "handles nil map",
		},
		{
			description: "handles empty map",
			config:      map[string]org.Repo{},
		},
		{
			description: "handles valid config",
			config: map[string]org.Repo{
				"repo": {Description: &description},
			},
		},
		{
			description: "finds repo names duplicate when normalized",
			config: map[string]org.Repo{
				"repo": {Description: &description},
				"Repo": {Description: &description},
			},
			expectError: true,
		},
		{
			description: "finds name conflict between previous and current names",
			config: map[string]org.Repo{
				"repo":     {Previously: []string{"conflict"}},
				"conflict": {Description: &description},
			},
			expectError: true,
		},
		{
			description: "finds name conflict between two previous names",
			config: map[string]org.Repo{
				"repo":         {Previously: []string{"conflict"}},
				"another-repo": {Previously: []string{"conflict"}},
			},
			expectError: true,
		},
		{
			description: "allows case-duplicate name between former and current name",
			config: map[string]org.Repo{
				"repo": {Previously: []string{"REPO"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			err := validateRepos(tc.config)
			if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got none", tc.description)
			} else if err != nil && !tc.expectError {
				t.Errorf("%s: unexpected error: %v", tc.description, err)
			}
		})
	}
}

func TestNewRepoUpdateRequest(t *testing.T) {
	repoName := "repo-name"
	newRepoName := "renamed-repo"
	description := "description of repo-name"
	homepage := "https://somewhe.re"
	master := "master"
	branch := "branch"
	squashMergeCommitTitle := "PR_TITLE"
	squashMergeCommitMessage := "COMMIT_MESSAGES"

	testCases := []struct {
		description string
		current     github.FullRepo
		name        string
		newState    org.Repo

		expected github.RepoUpdateRequest
	}{
		{
			description: "update is just a delta from current state",
			current: github.FullRepo{
				Repo: github.Repo{
					Name:          repoName,
					Description:   description,
					Homepage:      homepage,
					DefaultBranch: master,
				},
			},
			name: repoName,
			newState: org.Repo{
				Description:   &description,
				DefaultBranch: &branch,
			},
			expected: github.RepoUpdateRequest{
				DefaultBranch: &branch,
			},
		},
		{
			description: "empty delta is returned when no update is needed",
			current: github.FullRepo{Repo: github.Repo{
				Name:        repoName,
				Description: description,
			}},
			name: repoName,
			newState: org.Repo{
				Description: &description,
			},
		},
		{
			description: "request to rename a repo works",
			current: github.FullRepo{Repo: github.Repo{
				Name: repoName,
			}},
			name: newRepoName,
			newState: org.Repo{
				Description: &description,
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					Name:        &newRepoName,
					Description: &description,
				},
			},
		},
		{
			description: "request to update squash title works",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: repoName},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   "COMMIT_OR_PR_TITLE",
				SquashMergeCommitMessage: squashMergeCommitMessage,
			},
			name: newRepoName,
			newState: org.Repo{
				Description:              &description,
				SquashMergeCommitTitle:   &squashMergeCommitTitle,
				SquashMergeCommitMessage: &squashMergeCommitMessage,
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					Name:                   &newRepoName,
					Description:            &description,
					SquashMergeCommitTitle: &squashMergeCommitTitle,
				},
			},
		},
		{
			description: "message-only change omits unchanged configured title",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: repoName},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   squashMergeCommitTitle,
				SquashMergeCommitMessage: "PR_BODY",
			},
			name: repoName,
			newState: org.Repo{
				SquashMergeCommitTitle:   &squashMergeCommitTitle,
				SquashMergeCommitMessage: &squashMergeCommitMessage,
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					SquashMergeCommitMessage: &squashMergeCommitMessage,
				},
			},
		},
		{
			description: "message-only change does not backfill current title",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: repoName},
				AllowSquashMerge:         true,
				SquashMergeCommitTitle:   "PR_TITLE",
				SquashMergeCommitMessage: "BLANK",
			},
			name: repoName,
			newState: org.Repo{
				SquashMergeCommitMessage: &squashMergeCommitMessage,
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					SquashMergeCommitMessage: &squashMergeCommitMessage,
				},
			},
		},
		{
			description: "unchanged squash message produces no delta",
			current: github.FullRepo{
				Repo:                     github.Repo{Name: repoName},
				SquashMergeCommitTitle:   squashMergeCommitTitle,
				SquashMergeCommitMessage: squashMergeCommitMessage,
			},
			name: repoName,
			newState: org.Repo{
				SquashMergeCommitMessage: &squashMergeCommitMessage,
			},
		},
		{
			description: "enabling delete branch on merge produces a delta",
			current: github.FullRepo{
				Repo: github.Repo{Name: repoName},
			},
			name: repoName,
			newState: org.Repo{
				DeleteBranchOnMerge: new(true),
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					DeleteBranchOnMerge: new(true),
				},
			},
		},
		{
			description: "unchanged delete branch on merge produces no delta",
			current: github.FullRepo{
				Repo:                github.Repo{Name: repoName},
				DeleteBranchOnMerge: true,
			},
			name: repoName,
			newState: org.Repo{
				DeleteBranchOnMerge: new(true),
			},
		},
		{
			description: "visibility change produces a delta",
			current: github.FullRepo{
				Repo: github.Repo{
					Name:       repoName,
					Visibility: github.RepoVisibilityPublic,
				},
			},
			name: repoName,
			newState: org.Repo{
				Visibility: new(github.RepoVisibilityPrivate),
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					Visibility: new(github.RepoVisibilityPrivate),
				},
			},
		},
		{
			description: "same visibility produces no delta",
			current: github.FullRepo{
				Repo: github.Repo{
					Name:       repoName,
					Visibility: github.RepoVisibilityPublic,
				},
			},
			name: repoName,
			newState: org.Repo{
				Visibility: new(github.RepoVisibilityPublic),
			},
		},
		{
			description: "nil visibility produces no delta",
			current: github.FullRepo{
				Repo: github.Repo{
					Name:       repoName,
					Visibility: github.RepoVisibilityPublic,
				},
			},
			name:     repoName,
			newState: org.Repo{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			update := newRepoUpdateRequest(tc.current, tc.name, tc.newState)
			if !reflect.DeepEqual(tc.expected, update) {
				t.Errorf("%s: update request differs from expected:%s", tc.description, cmp.Diff(tc.expected, update))
			}
		})
	}
}
