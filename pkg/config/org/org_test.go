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
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

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
	pub := RepoVisibilityPublic
	priv := RepoVisibilityPrivate
	testCases := []struct {
		description string
		repo        Repo
		expected    Repo
	}{
		{
			description: "default values are pruned",
			repo: Repo{
				Description:      &empty,
				HomePage:         &empty,
				Visibility:       &pub,
				HasIssues:        &yes,
				HasProjects:      &yes,
				HasWiki:          &yes,
				AllowSquashMerge: &yes,
				AllowMergeCommit: &yes,
				AllowRebaseMerge: &yes,
				DefaultBranch:    &master,
				Archived:         &no,
			},
			expected: Repo{HasProjects: &yes},
		},
		{
			description: "non-default values are not pruned",
			repo: Repo{
				Description:      &nonEmpty,
				HomePage:         &nonEmpty,
				Visibility:       &priv,
				HasIssues:        &no,
				HasProjects:      &no,
				HasWiki:          &no,
				AllowSquashMerge: &no,
				AllowMergeCommit: &no,
				AllowRebaseMerge: &no,
				DefaultBranch:    &notMaster,
				Archived:         &yes,
			},
			expected: Repo{Description: &nonEmpty,
				HomePage:         &nonEmpty,
				Visibility:       &priv,
				HasIssues:        &no,
				HasProjects:      &no,
				HasWiki:          &no,
				AllowSquashMerge: &no,
				AllowMergeCommit: &no,
				AllowRebaseMerge: &no,
				DefaultBranch:    &notMaster,
				Archived:         &yes,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			pruned := PruneRepoDefaults(tc.repo)
			if !reflect.DeepEqual(tc.expected, pruned) {
				t.Errorf("%s: result differs from expected:\n", diff.ObjectReflectDiff(tc.expected, pruned))
			}
		})
	}
}

func TestRepoUnmarshalJSON(t *testing.T) {
	priv := RepoVisibilityPrivate
	pub := RepoVisibilityPublic
	internal := RepoVisibilityInternal
	testCases := []struct {
		name        string
		json        string
		expected    Repo
		expectError bool
	}{
		{
			name:     "private true translates to visibility private",
			json:     `{"private": true}`,
			expected: Repo{Visibility: &priv},
		},
		{
			name:     "private false translates to visibility public",
			json:     `{"private": false}`,
			expected: Repo{Visibility: &pub},
		},
		{
			name:     "visibility internal is accepted directly",
			json:     `{"visibility": "internal"}`,
			expected: Repo{Visibility: &internal},
		},
		{
			name:     "visibility private is accepted directly",
			json:     `{"visibility": "private"}`,
			expected: Repo{Visibility: &priv},
		},
		{
			name:        "both private and visibility set produces error",
			json:        `{"private": true, "visibility": "private"}`,
			expectError: true,
		},
		{
			name:     "neither private nor visibility set leaves visibility nil",
			json:     `{"description": "test"}`,
			expected: Repo{Description: strPtr("test")},
		},
		{
			name:        "unknown visibility value produces error",
			json:        `{"visibility": "interal"}`,
			expectError: true,
		},
		{
			name:     "private null leaves visibility nil",
			json:     `{"private": null}`,
			expected: Repo{},
		},
		{
			name:     "visibility null leaves visibility nil",
			json:     `{"visibility": null}`,
			expected: Repo{},
		},
		{
			name:     "private null with visibility set does not conflict",
			json:     `{"private": null, "visibility": "internal"}`,
			expected: Repo{Visibility: &internal},
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
				t.Errorf("result differs from expected:\n%s", diff.ObjectReflectDiff(tc.expected, actual))
			}
		})
	}
}

func strPtr(s string) *string { return &s }
