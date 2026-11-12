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
	"slices"
)

func (t *TideGitHubConfig) resolveQueries() error {
	if t.Queries == nil {
		t.resolvedQueries = nil
		return nil
	}
	t.resolvedQueries = make(TideQueries, len(t.Queries))
	for i, spec := range t.Queries {
		query := TideQuery(spec)
		// Deduplication sorts query slices in place. Keep the authored queries
		// independent so serializing the config preserves their original form.
		query.Labels = slices.Clone(query.Labels)
		query.MissingLabels = slices.Clone(query.MissingLabels)
		query.ExcludedBranches = slices.Clone(query.ExcludedBranches)
		query.IncludedBranches = slices.Clone(query.IncludedBranches)
		query.Orgs = slices.Clone(query.Orgs)
		query.Repos = slices.Clone(query.Repos)
		query.ExcludedRepos = slices.Clone(query.ExcludedRepos)
		if err := query.Validate(); err != nil {
			return fmt.Errorf("tide query (index %d) is invalid: %w", i, err)
		}
		t.resolvedQueries[i] = query
	}
	return nil
}
