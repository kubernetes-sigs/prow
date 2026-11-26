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

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
)

func (f TideQueryFragment) query() TideQuery {
	return TideQuery{
		Author: ptr.Deref(f.Author, ""), Milestone: ptr.Deref(f.Milestone, ""),
		ReviewApprovedRequired: ptr.Deref(f.ReviewApprovedRequired, false),
		Labels:                 f.Labels, MissingLabels: f.MissingLabels,
		IncludedBranches: f.IncludedBranches, ExcludedBranches: f.ExcludedBranches,
	}
}

func (t *TideGitHubConfig) resolveQueries() error {
	presets := make(map[string]TideQueryFragment, len(t.QueryPresets))
	for i, preset := range t.QueryPresets {
		if preset.Name == "" {
			return fmt.Errorf("tide.query_presets[%d]: name must not be empty", i)
		}
		if _, exists := presets[preset.Name]; exists {
			return fmt.Errorf("tide.query_presets[%q]: defined more than once", preset.Name)
		}
		query := preset.TideQueryFragment.query()
		if err := query.validateCriteria(); err != nil {
			return fmt.Errorf("tide.query_presets[%q]: %w", preset.Name, err)
		}
		presets[preset.Name] = preset.TideQueryFragment
	}

	var resolved TideQueries
	if t.Queries != nil {
		resolved = make(TideQueries, 0, len(t.Queries))
	}
	for i, spec := range t.Queries {
		source := fmt.Sprintf("tide.queries[%d]", i)
		inline := spec.TideQueryFragment.query()
		inline.Orgs, inline.Repos, inline.ExcludedRepos = spec.Orgs, spec.Repos, spec.ExcludedRepos
		if err := inline.Validate(); err != nil {
			return fmt.Errorf("%s (inline query): %w", source, err)
		}
		query := TideQuery{
			Orgs: slices.Clone(spec.Orgs), Repos: slices.Clone(spec.Repos), ExcludedRepos: slices.Clone(spec.ExcludedRepos),
		}
		scalars := sets.New[string]()
		if err := mergeQueryFragment(&query, spec.TideQueryFragment, scalars); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		seen := sets.New[string]()
		for _, name := range spec.Presets {
			if seen.Has(name) {
				return fmt.Errorf("%s.presets: preset %q referenced more than once", source, name)
			}
			seen.Insert(name)
			fragment, exists := presets[name]
			if !exists {
				return fmt.Errorf("%s.presets: unknown preset %q", source, name)
			}
			if err := mergeQueryFragment(&query, fragment, scalars); err != nil {
				return fmt.Errorf("%s.%w", source, err)
			}
		}
		if err := query.Validate(); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		resolved = append(resolved, query)
	}
	t.resolvedQueries = resolved
	return nil
}

// mergeQueryFragment always allocates independent list storage. Deduplication
// may sort runtime slices without changing the authored queries or presets.
func mergeQueryFragment(query *TideQuery, fragment TideQueryFragment, scalars sets.Set[string]) error {
	for _, scalar := range []struct {
		field string
		set   bool
	}{
		{"author", fragment.Author != nil},
		{"milestone", fragment.Milestone != nil},
		{"reviewApprovedRequired", fragment.ReviewApprovedRequired != nil},
	} {
		if !scalar.set {
			continue
		}
		if scalars.Has(scalar.field) {
			return fmt.Errorf("%s: set more than once", scalar.field)
		}
		scalars.Insert(scalar.field)
	}
	if fragment.Author != nil {
		query.Author = *fragment.Author
	}
	if fragment.Milestone != nil {
		query.Milestone = *fragment.Milestone
	}
	if fragment.ReviewApprovedRequired != nil {
		query.ReviewApprovedRequired = *fragment.ReviewApprovedRequired
	}
	union := func(dst *[]string, src []string) {
		seen := sets.New[string]((*dst)...)
		for _, entry := range src {
			if !seen.Has(entry) {
				*dst = append(*dst, entry)
				seen.Insert(entry)
			}
		}
	}
	union(&query.Labels, fragment.Labels)
	union(&query.MissingLabels, fragment.MissingLabels)
	union(&query.IncludedBranches, fragment.IncludedBranches)
	union(&query.ExcludedBranches, fragment.ExcludedBranches)
	return nil
}
