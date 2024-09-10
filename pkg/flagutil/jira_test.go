/*
Copyright 2024 The Kubernetes Authors.

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

package flagutil

import (
	"flag"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestJiraOptions_AuthParams(t *testing.T) {
	allJiraFlags := []string{"jira-endpoint", "jira-username", "jira-password-file", "jira-bearer-token-file"}
	t.Parallel()
	testCases := []struct {
		name   string
		params []JiraFlagParameter

		expectPresent []string
		expectDefault map[string]string
	}{
		{
			name:          "no customizations",
			expectPresent: allJiraFlags,
			expectDefault: map[string]string{"jira-endpoint": "", "jira-username": "", "jira-password-file": "", "jira-bearer-token-file": ""},
		},
		{
			name:          "no basic auth",
			params:        []JiraFlagParameter{JiraNoBasicAuth()},
			expectPresent: []string{"jira-endpoint", "jira-bearer-token-file"},
			expectDefault: map[string]string{"jira-endpoint": "", "jira-bearer-token-file": ""},
		},
		{
			name:          "custom endpoint",
			params:        []JiraFlagParameter{JiraDefaultEndpoint("https://jira.example.com")},
			expectPresent: allJiraFlags,
			expectDefault: map[string]string{"jira-endpoint": "https://jira.example.com", "jira-username": "", "jira-password-file": "", "jira-bearer-token-file": ""},
		},
		{
			name:          "custom bearer token file",
			params:        []JiraFlagParameter{JiraDefaultBearerTokenFile("/path/to/bearer/token")},
			expectPresent: allJiraFlags,
			expectDefault: map[string]string{"jira-endpoint": "", "jira-username": "", "jira-password-file": "", "jira-bearer-token-file": "/path/to/bearer/token"},
		},
		{
			name: "custom endpoint and bearer token file, disabled basic auth",
			params: []JiraFlagParameter{
				JiraDefaultEndpoint("https://jira.example.com"),
				JiraDefaultBearerTokenFile("/path/to/bearer/token"),
				JiraNoBasicAuth(),
			},
			expectPresent: []string{"jira-endpoint", "jira-bearer-token-file"},
			expectDefault: map[string]string{"jira-endpoint": "https://jira.example.com", "jira-bearer-token-file": "/path/to/bearer/token"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			expect := sets.New[string](tc.expectPresent...)
			fs := flag.NewFlagSet(tc.name, flag.ExitOnError)
			opts := &JiraOptions{}
			opts.AddCustomizedFlags(fs, tc.params...)
			for _, name := range allJiraFlags {
				flg := fs.Lookup(name)
				if (flg != nil) != expect.Has(name) {
					t.Errorf("Flag --%s presence differs: expected %t got %t", name, expect.Has(name), flg != nil)
					continue
				}
				expected := tc.expectDefault[name]
				if flg != nil && flg.DefValue != expected {
					t.Errorf("Flag --%s default value differs: expected %#v got '%#v'", name, expected, flg.DefValue)
				}
			}
		})
	}
}
