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
				Private:          &no,
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
				Private:          &yes,
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
				Private:          &yes,
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
