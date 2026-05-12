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
	"fmt"

	"sigs.k8s.io/prow/pkg/github"
)

// FullConfig stores the full configuration to be used by the tool, mapping
// orgs to their configuration at the top level under an `orgs` key.
type FullConfig struct {
	Orgs map[string]Config `json:"orgs,omitempty"`
}

// Metadata declares metadata about the GitHub org.
//
// See https://developer.github.com/v3/orgs/#edit-an-organization
type Metadata struct {
	BillingEmail                 *string                     `json:"billing_email,omitempty"`
	Company                      *string                     `json:"company,omitempty"`
	Email                        *string                     `json:"email,omitempty"`
	Name                         *string                     `json:"name,omitempty"`
	Description                  *string                     `json:"description,omitempty"`
	Location                     *string                     `json:"location,omitempty"`
	HasOrganizationProjects      *bool                       `json:"has_organization_projects,omitempty"`
	HasRepositoryProjects        *bool                       `json:"has_repository_projects,omitempty"`
	DefaultRepositoryPermission  *github.RepoPermissionLevel `json:"default_repository_permission,omitempty"`
	MembersCanCreateRepositories *bool                       `json:"members_can_create_repositories,omitempty"`
}

// RepoCreateOptions declares options for creating new repos
// See https://developer.github.com/v3/repos/#create
type RepoCreateOptions struct {
	AutoInit          *bool   `json:"auto_init,omitempty"`
	GitignoreTemplate *string `json:"gitignore_template,omitempty"`
	LicenseTemplate   *string `json:"license_template,omitempty"`
}

// RepoMetadata declares metadata about the GitHub repository.
// These fields map directly to the GitHub Repos API.
//
// See https://docs.github.com/en/rest/repos/repos#update-a-repository
type RepoMetadata struct {
	Description              *string `json:"description,omitempty"`
	HomePage                 *string `json:"homepage,omitempty"`
	Private                  *bool   `json:"private,omitempty"`
	HasIssues                *bool   `json:"has_issues,omitempty"`
	HasProjects              *bool   `json:"has_projects,omitempty"`
	HasWiki                  *bool   `json:"has_wiki,omitempty"`
	AllowSquashMerge         *bool   `json:"allow_squash_merge,omitempty"`
	AllowMergeCommit         *bool   `json:"allow_merge_commit,omitempty"`
	AllowRebaseMerge         *bool   `json:"allow_rebase_merge,omitempty"`
	SquashMergeCommitTitle   *string `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage *string `json:"squash_merge_commit_message,omitempty"`
	DefaultBranch            *string `json:"default_branch,omitempty"`
	Archived                 *bool   `json:"archived,omitempty"`
}

// Repo declares a repository and its configuration.
type Repo struct {
	// RepoMetadata contains fields that map directly to the GitHub Repos API.
	// See https://docs.github.com/en/rest/repos/repos#update-a-repository
	RepoMetadata

	//
	// Peribolos-specific fields (not part of any GitHub API)
	//

	// Previously allows handling repo renames. Peribolos will use the first
	// entry in previously that exists on GitHub as the source for the rename.
	Previously []string `json:"previously,omitempty"`

	// OnCreate specifies options that are only applied when creating a new repo.
	// See https://docs.github.com/en/rest/repos/repos#create-an-organization-repository
	OnCreate *RepoCreateOptions `json:"on_create,omitempty"`

	//
	// GitHub Forks API fields
	// See https://docs.github.com/en/rest/repos/forks
	//

	// ForkFrom specifies an upstream repository to fork from, in the format "owner/repo".
	// When set, Peribolos will create this repository as a fork of the upstream.
	// The config key name will be used as the fork's name (via GitHub's fork API name parameter).
	//
	// Metadata behavior for forks:
	//   - Forks initially inherit metadata (description, has_issues, etc.) from the upstream.
	//   - If a RepoMetadata field is set in config, it overrides the inherited/current value.
	//   - If a RepoMetadata field is not set (nil), the fork keeps its current value.
	//   - Some metadata changes may be restricted by GitHub (e.g., public forks cannot be
	//     made private on GitHub.com). Such restrictions vary by GitHub edition (Enterprise
	//     may allow more). Restricted changes will result in an API error.
	ForkFrom *string `json:"fork_from,omitempty"`

	// DefaultBranchOnly specifies whether to fork only the default branch.
	// Only applicable when ForkFrom is set.
	DefaultBranchOnly *bool `json:"default_branch_only,omitempty"`

	//
	// GitHub Collaborators API fields
	// See https://docs.github.com/en/rest/collaborators/collaborators
	//

	// Collaborators is a map of username to their permission level for this repository.
	Collaborators map[string]github.RepoPermissionLevel `json:"collaborators,omitempty"`
}

// Config declares org metadata as well as its people and teams.
type Config struct {
	Metadata
	Teams   map[string]Team `json:"teams,omitempty"`
	Members []string        `json:"members,omitempty"`
	Admins  []string        `json:"admins,omitempty"`
	Repos   map[string]Repo `json:"repos,omitempty"`
}

// TeamMetadata declares metadata about the github team.
//
// See https://developer.github.com/v3/teams/#edit-team
type TeamMetadata struct {
	Description *string  `json:"description,omitempty"`
	Privacy     *Privacy `json:"privacy,omitempty"`
}

// Team declares metadata as well as its people.
type Team struct {
	TeamMetadata
	Members     []string        `json:"members,omitempty"`
	Maintainers []string        `json:"maintainers,omitempty"`
	Children    map[string]Team `json:"teams,omitempty"`

	Previously []string `json:"previously,omitempty"`

	// This is injected to the Team structure by listing privilege
	// levels on dump and if set by users will cause privileges to
	// be added on sync.
	// https://developer.github.com/v3/teams/#list-team-repos
	// https://developer.github.com/v3/teams/#add-or-update-team-repository
	Repos map[string]github.RepoPermissionLevel `json:"repos,omitempty"`
}

// Privacy is secret or closed.
//
// See https://developer.github.com/v3/teams/#edit-team
type Privacy string

const (
	// Closed means it is only visible to org members
	Closed Privacy = "closed"
	// Secret means it is only visible to team members.
	Secret Privacy = "secret"
)

var privacySettings = map[Privacy]bool{
	Closed: true,
	Secret: true,
}

// MarshalText returns bytes that equal secret or closed
func (p Privacy) MarshalText() ([]byte, error) {
	return []byte(p), nil
}

// UnmarshalText returns an error if text != secret or closed
func (p *Privacy) UnmarshalText(text []byte) error {
	v := Privacy(text)
	if _, ok := privacySettings[v]; !ok {
		return fmt.Errorf("bad privacy setting: %s", v)
	}
	*p = v
	return nil
}

// PruneRepoDefaults finds values in org.Repo config that match the default
// values and replaces them with nil pointer. This reduces the size of an org dump
// by omitting the fields that would be set to the same value when not set at all.
// See https://docs.github.com/en/rest/repos/repos#update-a-repository
func PruneRepoDefaults(repo Repo) Repo {
	pruneString := func(p **string, def string) {
		if *p != nil && **p == def {
			*p = nil
		}
	}
	pruneBool := func(p **bool, def bool) {
		if *p != nil && **p == def {
			*p = nil
		}
	}

	pruneString(&repo.Description, "")
	pruneString(&repo.HomePage, "")

	pruneBool(&repo.Private, false)
	pruneBool(&repo.HasIssues, true)
	// Projects' defaults depend on org setting, do not prune
	pruneBool(&repo.HasWiki, true)
	pruneBool(&repo.AllowRebaseMerge, true)
	pruneBool(&repo.AllowSquashMerge, true)
	pruneBool(&repo.AllowMergeCommit, true)

	pruneBool(&repo.Archived, false)
	pruneString(&repo.DefaultBranch, "master")

	return repo
}
