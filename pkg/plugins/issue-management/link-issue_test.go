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

package issuemanagement

import (
	"testing"
)

func TestParseIssueRef(t *testing.T) {
	tests := []struct {
		name          string
		issue         string
		defOrg        string
		defRepo       string
		expectedOrg   string
		expectedRepo  string
		expectedIssue int
		expectedError bool
	}{
		{
			name:          "User provided an issue number",
			issue:         "42",
			defOrg:        "kubernetes",
			defRepo:       "test-infra",
			expectedOrg:   "kubernetes",
			expectedRepo:  "test-infra",
			expectedIssue: 42,
		},
		{
			name:          "User provided an issue in format org/repo#num",
			issue:         "foo/bar#77",
			defOrg:        "org",
			defRepo:       "repo",
			expectedOrg:   "foo",
			expectedRepo:  "bar",
			expectedIssue: 77,
		},
		{
			name:          "User provided an invalid issue number",
			issue:         "x42",
			defOrg:        "org",
			defRepo:       "repo",
			expectedError: true,
		},
		{
			name:          "Invalid issue format with slash but missing #",
			issue:         "foo/bar",
			expectedError: true,
		},
		{
			name:          "Invalid org repo format",
			issue:         "foo/bar/baz#1",
			expectedError: true,
		},
		{
			name:          "Invalid issue number after #",
			issue:         "foo/bar#x",
			expectedError: true,
		},
		{
			name:          "Invalid issue reference",
			issue:         "abc",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseIssueRef(tt.issue, tt.defOrg, tt.defRepo)
			if tt.expectedError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if ref.Org != tt.expectedOrg || ref.Repo != tt.expectedRepo || ref.Num != tt.expectedIssue {
				t.Fatalf("got %+v, want org=%s repo=%s issue=%d", ref, tt.expectedOrg, tt.expectedRepo, tt.expectedIssue)
			}
		})
	}
}

func TestFormatIssueRef(t *testing.T) {
	tests := []struct {
		name             string
		ref              IssueRef
		defOrg           string
		defRepo          string
		expectedIssueRef string
	}{
		{
			name:             "Issue within the same repo",
			ref:              IssueRef{Org: "kubernetes", Repo: "test-infra", Num: 12},
			defOrg:           "kubernetes",
			defRepo:          "test-infra",
			expectedIssueRef: "#12",
		},
		{
			name:             "Issue in a different repo",
			ref:              IssueRef{Org: "foo", Repo: "bar", Num: 33},
			defOrg:           "kubernetes",
			defRepo:          "test-infra",
			expectedIssueRef: "foo/bar#33",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := formatIssueRef(&tt.ref, tt.defOrg, tt.defRepo)
			if issue != tt.expectedIssueRef {
				t.Fatalf("got %s, want %s", issue, tt.expectedIssueRef)
			}
		})
	}
}

func TestUpdateFixesLine(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		issues       []string
		addLink      bool
		expectedBody string
	}{
		{
			name:         "add fixes line when it doesn't exist",
			body:         "This is a PR body.",
			issues:       []string{"#12"},
			addLink:      true,
			expectedBody: "This is a PR body.\n\nFixes #12",
		},
		{
			name:         "append issue to existing fixes line",
			body:         "line1\nFixes #1\nline3",
			issues:       []string{"#2"},
			addLink:      true,
			expectedBody: "line1\nFixes #1 #2\nline3",
		},
		{
			name:         "unlink an issue but keep fixes line",
			body:         "Fixes #1 #2",
			issues:       []string{"#2"},
			addLink:      false,
			expectedBody: "Fixes #1",
		},
		{
			name:         "remove last issue and delete fixes line",
			body:         "line1\nFixes #99\nline2",
			issues:       []string{"#99"},
			addLink:      false,
			expectedBody: "line1\nline2",
		},
		{
			name:         "do not add duplicate issue if it is already present in the PR body",
			body:         "Fixes #7",
			issues:       []string{"#7"},
			addLink:      true,
			expectedBody: "Fixes #7",
		},
		{
			name:         "ensure the fixes line is added at end of the body",
			body:         "line1\nline2",
			issues:       []string{"foo/bar#10"},
			addLink:      true,
			expectedBody: "line1\nline2\n\nFixes foo/bar#10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			returnedBody := updateFixesLine(tt.body, tt.issues, tt.addLink)
			if returnedBody != tt.expectedBody {
				t.Fatalf("got:\n%s\nwant:\n%s", returnedBody, tt.expectedBody)
			}
		})
	}
}
