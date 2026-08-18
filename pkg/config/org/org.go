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
	"fmt"
	"time"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/logrusutil"
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

// Repo declares metadata about the GitHub repository
//
// See https://developer.github.com/v3/repos/#edit
type Repo struct {
	Description              *string                `json:"description,omitempty"`
	HomePage                 *string                `json:"homepage,omitempty"`
	Visibility               *github.RepoVisibility `json:"visibility,omitempty"`
	HasIssues                *bool                  `json:"has_issues,omitempty"`
	HasProjects              *bool                  `json:"has_projects,omitempty"`
	HasWiki                  *bool                  `json:"has_wiki,omitempty"`
	AllowSquashMerge         *bool                  `json:"allow_squash_merge,omitempty"`
	AllowMergeCommit         *bool                  `json:"allow_merge_commit,omitempty"`
	AllowRebaseMerge         *bool                  `json:"allow_rebase_merge,omitempty"`
	SquashMergeCommitTitle   *string                `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage *string                `json:"squash_merge_commit_message,omitempty"`

	DefaultBranch *string `json:"default_branch,omitempty"`
	Archived      *bool   `json:"archived,omitempty"`

	Previously []string `json:"previously,omitempty"`

	// Collaborators is a map of username to their permission level for this repository
	Collaborators map[string]github.RepoPermissionLevel `json:"collaborators,omitempty"`

	OnCreate *RepoCreateOptions `json:"on_create,omitempty"`
}

var privateFieldDeprecationWarningLast time.Time

// UnmarshalJSON supports both the old "private" bool field and the new
// "visibility" string field. If "private" is present and "visibility" is not,
// it is translated to the equivalent visibility value with a deprecation
// warning. Specifying both is an error.
func (r *Repo) UnmarshalJSON(data []byte) error {
	type RepoAlias Repo
	var alias struct {
		RepoAlias
		Private *bool `json:"private"`
	}
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	if alias.Private != nil {
		if alias.Visibility != nil {
			return fmt.Errorf("repo config may not specify both 'private' and 'visibility'")
		}
		logrusutil.ThrottledWarnf(&privateFieldDeprecationWarningLast, 5*time.Minute,
			"the 'private' field in repo config is deprecated; use 'visibility: public/private/internal' instead")
		v := github.RepoVisibilityPublic
		if *alias.Private {
			v = github.RepoVisibilityPrivate
		}
		alias.Visibility = &v
	}

	*r = Repo(alias.RepoAlias)
	return nil
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

// pruneDefault sets *p to nil when it points at the default value.
func pruneDefault[T comparable](p **T, def T) {
	if *p != nil && **p == def {
		*p = nil
	}
}

// PruneRepoDefaults finds values in org.Repo config that matches the default
// values replaces them with nil pointer. This reduces the size of an org dump
// by omitting the fields that would be set to the same value when not set at all.
// See https://developer.github.com/v3/repos/#edit
func PruneRepoDefaults(repo Repo) Repo {
	pruneDefault(&repo.Description, "")
	pruneDefault(&repo.HomePage, "")

	pruneDefault(&repo.Visibility, github.RepoVisibilityPublic)
	pruneDefault(&repo.HasIssues, true)
	// Projects' defaults depend on org setting, do not prune
	pruneDefault(&repo.HasWiki, true)
	pruneDefault(&repo.AllowRebaseMerge, true)
	pruneDefault(&repo.AllowSquashMerge, true)
	pruneDefault(&repo.AllowMergeCommit, true)

	pruneDefault(&repo.Archived, false)
	pruneDefault(&repo.DefaultBranch, "master")

	return repo
}
