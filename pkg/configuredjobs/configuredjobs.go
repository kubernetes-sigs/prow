/*
Copyright 2025 The Kubernetes Authors.

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

package configuredjobs

import v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

// JobsByRepo contains a list of RepoInfo that is used to display the Configured Jobs subpages
type JobsByRepo struct {
	IncludedRepos []RepoInfo `json:"includedRepos"`
	AllRepos      []string   `json:"allRepos"`
}

// Index contains a list of Org that is used to display the Configured Jobs index page
type Index struct {
	Orgs []Org `json:"orgs"`
}

// Org contains the information necessary to display the org level Configured Jobs information
type Org struct {
	Name  string   `json:"name"`
	Repos []string `json:"repos"`
}

// RepoInfo contains the information necessary to display the Configured Jobs page for a repo
type RepoInfo struct {
	Org Org `json:"org"`
	// SafeName is the Name with unsupported chars stripped
	SafeName string    `json:"safeName"`
	Name     string    `json:"name"`
	Jobs     []JobInfo `json:"jobs"`
}

// JobInfo contains the necessary information for a job for the Configured Jobs page
type JobInfo struct {
	Name           string         `json:"name"`
	Type           v1.ProwJobType `json:"type"`
	JobHistoryLink string         `json:"jobHistoryLink"`
	YAMLDefinition string         `json:"yamlDefinition"`
}
