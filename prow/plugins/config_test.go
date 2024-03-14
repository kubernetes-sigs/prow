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

package plugins

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	fuzz "github.com/google/gofuzz"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"

	"sigs.k8s.io/prow/prow/bugzilla"
	"sigs.k8s.io/prow/prow/plugins/ownersconfig"
)

func TestValidateExternalPlugins(t *testing.T) {
	tests := []struct {
		name        string
		plugins     map[string][]ExternalPlugin
		expectedErr error
	}{
		{
			name: "valid config",
			plugins: map[string][]ExternalPlugin{
				"kubernetes/test-infra": {
					{
						Name: "cherrypick",
					},
					{
						Name: "configupdater",
					},
					{
						Name: "tetris",
					},
				},
				"kubernetes": {
					{
						Name: "coffeemachine",
					},
					{
						Name: "blender",
					},
				},
			},
			expectedErr: nil,
		},
		{
			name: "invalid config",
			plugins: map[string][]ExternalPlugin{
				"kubernetes/test-infra": {
					{
						Name: "cherrypick",
					},
					{
						Name: "configupdater",
					},
					{
						Name: "tetris",
					},
				},
				"kubernetes": {
					{
						Name: "coffeemachine",
					},
					{
						Name: "tetris",
					},
				},
			},
			expectedErr: errors.New("invalid plugin configuration:\n\texternal plugins [tetris] are duplicated for kubernetes/test-infra and kubernetes"),
		},
	}

	for _, test := range tests {
		t.Logf("Running scenario %q", test.name)

		err := validateExternalPlugins(test.plugins)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("unexpected error: %v, expected: %v", err, test.expectedErr)
		}
	}
}

func TestOwnersFilenames(t *testing.T) {
	cases := []struct {
		org      string
		repo     string
		config   Owners
		expected ownersconfig.Filenames
	}{
		{
			org:  "kubernetes",
			repo: "test-infra",
			config: Owners{
				Filenames: map[string]ownersconfig.Filenames{
					"kubernetes":            {Owners: "OWNERS", OwnersAliases: "OWNERS_ALIASES"},
					"kubernetes/test-infra": {Owners: ".OWNERS", OwnersAliases: ".OWNERS_ALIASES"},
				},
			},
			expected: ownersconfig.Filenames{
				Owners: ".OWNERS", OwnersAliases: ".OWNERS_ALIASES",
			},
		},
		{
			org:  "kubernetes",
			repo: "",
			config: Owners{
				Filenames: map[string]ownersconfig.Filenames{
					"kubernetes":            {Owners: "OWNERS", OwnersAliases: "OWNERS_ALIASES"},
					"kubernetes/test-infra": {Owners: ".OWNERS", OwnersAliases: ".OWNERS_ALIASES"},
				},
			},
			expected: ownersconfig.Filenames{
				Owners: "OWNERS", OwnersAliases: "OWNERS_ALIASES",
			},
		},
	}

	for _, tc := range cases {
		cfg := Configuration{
			Owners: tc.config,
		}
		actual := cfg.OwnersFilenames(tc.org, tc.repo)
		if actual != tc.expected {
			t.Errorf("%s/%s: unexpected value. Diff: %v", tc.org, tc.repo, diff.ObjectDiff(actual, tc.expected))
		}
	}
}

func TestSetDefault_Maps(t *testing.T) {
	cases := []struct {
		name     string
		config   ConfigUpdater
		expected map[string]ConfigMapSpec
	}{
		{
			name: "nothing",
			expected: map[string]ConfigMapSpec{
				"config/prow/config.yaml":  {Name: "config", Clusters: map[string][]string{"default": {""}}},
				"config/prow/plugins.yaml": {Name: "plugins", Clusters: map[string][]string{"default": {""}}},
			},
		},
		{
			name: "basic",
			config: ConfigUpdater{
				Maps: map[string]ConfigMapSpec{
					"hello.yaml": {Name: "my-cm"},
					"world.yaml": {Name: "you-cm"},
				},
			},
			expected: map[string]ConfigMapSpec{
				"hello.yaml": {Name: "my-cm", Clusters: map[string][]string{"default": {""}}},
				"world.yaml": {Name: "you-cm", Clusters: map[string][]string{"default": {""}}},
			},
		},
		{
			name: "both current and deprecated",
			config: ConfigUpdater{
				Maps: map[string]ConfigMapSpec{
					"config.yaml":        {Name: "overwrite-config"},
					"plugins.yaml":       {Name: "overwrite-plugins"},
					"unconflicting.yaml": {Name: "ignored"},
				},
			},
			expected: map[string]ConfigMapSpec{
				"config.yaml":        {Name: "overwrite-config", Clusters: map[string][]string{"default": {""}}},
				"plugins.yaml":       {Name: "overwrite-plugins", Clusters: map[string][]string{"default": {""}}},
				"unconflicting.yaml": {Name: "ignored", Clusters: map[string][]string{"default": {""}}},
			},
		},
	}
	for _, tc := range cases {
		cfg := Configuration{
			ConfigUpdater: tc.config,
		}
		cfg.setDefaults()
		actual := cfg.ConfigUpdater.Maps
		if len(actual) != len(tc.expected) {
			t.Errorf("%s: actual and expected have different keys: %v %v", tc.name, actual, tc.expected)
			continue
		}
		for k, n := range tc.expected {
			if an := actual[k]; !reflect.DeepEqual(an, n) {
				t.Errorf("%s - %s: unexpected value. Diff: %v", tc.name, k, diff.ObjectReflectDiff(an, n))
			}
		}
	}
}

func TestTriggerFor(t *testing.T) {
	config := Configuration{
		Triggers: []Trigger{
			{
				Repos:      []string{"kuber"},
				TrustedOrg: "org1",
			},
			{
				Repos:      []string{"k8s/k8s", "k8s/kuber"},
				TrustedOrg: "org2",
			},
			{
				Repos:      []string{"k8s/t-i"},
				TrustedOrg: "org3",
			},
			{
				Repos:      []string{"kuber/utils"},
				TrustedOrg: "org4",
			},
		},
	}
	config.setDefaults()

	testCases := []struct {
		name            string
		org, repo       string
		expectedTrusted string
		check           func(Trigger) error
	}{
		{
			name:            "org trigger",
			org:             "kuber",
			repo:            "kuber",
			expectedTrusted: "org1",
		},
		{
			name:            "repo trigger",
			org:             "k8s",
			repo:            "t-i",
			expectedTrusted: "org3",
		},
		{
			name:            "repo trigger",
			org:             "kuber",
			repo:            "utils",
			expectedTrusted: "org4",
		},
		{
			name: "default trigger",
			org:  "other",
			repo: "other",
		},
	}
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			actual := config.TriggerFor(tc.org, tc.repo)
			if tc.expectedTrusted != actual.TrustedOrg {
				t.Errorf("expected TrustedOrg to be %q, but got %q", tc.expectedTrusted, actual.TrustedOrg)
			}
		})
	}
}

func TestSetApproveDefaults(t *testing.T) {
	c := &Configuration{
		Approve: []Approve{
			{
				Repos: []string{
					"kubernetes/kubernetes",
					"kubernetes-client",
				},
			},
			{
				Repos: []string{
					"kubernetes-sigs/cluster-api",
				},
				CommandHelpLink: "https://prow.k8s.io/command-help",
				PrProcessLink:   "https://github.com/kubernetes/community/blob/427ccfbc7d423d8763ed756f3b8c888b7de3cf34/contributors/guide/pull-requests.md",
			},
		},
	}

	tests := []struct {
		name                    string
		org                     string
		repo                    string
		expectedCommandHelpLink string
		expectedPrProcessLink   string
	}{
		{
			name:                    "default",
			org:                     "kubernetes",
			repo:                    "kubernetes",
			expectedCommandHelpLink: "https://go.k8s.io/bot-commands",
			expectedPrProcessLink:   "https://git.k8s.io/community/contributors/guide/owners.md#the-code-review-process",
		},
		{
			name:                    "overwrite",
			org:                     "kubernetes-sigs",
			repo:                    "cluster-api",
			expectedCommandHelpLink: "https://prow.k8s.io/command-help",
			expectedPrProcessLink:   "https://github.com/kubernetes/community/blob/427ccfbc7d423d8763ed756f3b8c888b7de3cf34/contributors/guide/pull-requests.md",
		},
		{
			name:                    "default for repo without approve plugin config",
			org:                     "kubernetes",
			repo:                    "website",
			expectedCommandHelpLink: "https://go.k8s.io/bot-commands",
			expectedPrProcessLink:   "https://git.k8s.io/community/contributors/guide/owners.md#the-code-review-process",
		},
	}

	for _, test := range tests {

		a := c.ApproveFor(test.org, test.repo)

		if a.CommandHelpLink != test.expectedCommandHelpLink {
			t.Errorf("unexpected commandHelpLink: %s, expected: %s", a.CommandHelpLink, test.expectedCommandHelpLink)
		}

		if a.PrProcessLink != test.expectedPrProcessLink {
			t.Errorf("unexpected prProcessLink: %s, expected: %s", a.PrProcessLink, test.expectedPrProcessLink)
		}
	}
}

func TestSetHelpDefaults(t *testing.T) {
	tests := []struct {
		name              string
		helpGuidelinesURL string

		expectedHelpGuidelinesURL string
	}{
		{
			name:                      "default",
			helpGuidelinesURL:         "",
			expectedHelpGuidelinesURL: "https://git.k8s.io/community/contributors/guide/help-wanted.md",
		},
		{
			name:                      "overwrite",
			helpGuidelinesURL:         "https://github.com/kubernetes/community/blob/master/contributors/guide/help-wanted.md",
			expectedHelpGuidelinesURL: "https://github.com/kubernetes/community/blob/master/contributors/guide/help-wanted.md",
		},
	}

	for _, test := range tests {
		c := &Configuration{
			Help: Help{
				HelpGuidelinesURL: test.helpGuidelinesURL,
			},
		}

		c.setDefaults()

		if c.Help.HelpGuidelinesURL != test.expectedHelpGuidelinesURL {
			t.Errorf("unexpected help_guidelines_url: %s, expected: %s", c.Help.HelpGuidelinesURL, test.expectedHelpGuidelinesURL)
		}
	}
}

func TestSetTriggerDefaults(t *testing.T) {
	tests := []struct {
		name string

		trustedOrg string
		joinOrgURL string

		expectedTrustedOrg string
		expectedJoinOrgURL string
	}{
		{
			name: "url defaults to org",

			trustedOrg: "kubernetes",
			joinOrgURL: "",

			expectedTrustedOrg: "kubernetes",
			expectedJoinOrgURL: "https://github.com/orgs/kubernetes/people",
		},
		{
			name: "both org and url are set",

			trustedOrg: "kubernetes",
			joinOrgURL: "https://git.k8s.io/community/community-membership.md#member",

			expectedTrustedOrg: "kubernetes",
			expectedJoinOrgURL: "https://git.k8s.io/community/community-membership.md#member",
		},
		{
			name: "only url is set",

			trustedOrg: "",
			joinOrgURL: "https://git.k8s.io/community/community-membership.md#member",

			expectedTrustedOrg: "",
			expectedJoinOrgURL: "https://git.k8s.io/community/community-membership.md#member",
		},
		{
			name: "nothing is set",

			trustedOrg: "",
			joinOrgURL: "",

			expectedTrustedOrg: "",
			expectedJoinOrgURL: "",
		},
	}

	for _, test := range tests {
		c := &Configuration{
			Triggers: []Trigger{
				{
					TrustedOrg: test.trustedOrg,
					JoinOrgURL: test.joinOrgURL,
				},
			},
		}

		c.setDefaults()

		if c.Triggers[0].TrustedOrg != test.expectedTrustedOrg {
			t.Errorf("unexpected trusted_org: %s, expected: %s", c.Triggers[0].TrustedOrg, test.expectedTrustedOrg)
		}
		if c.Triggers[0].JoinOrgURL != test.expectedJoinOrgURL {
			t.Errorf("unexpected join_org_url: %s, expected: %s", c.Triggers[0].JoinOrgURL, test.expectedJoinOrgURL)
		}
	}
}

func TestSetCherryPickUnapprovedDefaults(t *testing.T) {
	defaultBranchRegexp := `^release-.*$`
	defaultComment := `This PR is not for the master branch but does not have the ` + "`cherry-pick-approved`" + `  label. Adding the ` + "`do-not-merge/cherry-pick-not-approved`" + `  label.`

	testcases := []struct {
		name string

		branchRegexp string
		comment      string

		expectedBranchRegexp string
		expectedComment      string
	}{
		{
			name:                 "none of branchRegexp and comment are set",
			branchRegexp:         "",
			comment:              "",
			expectedBranchRegexp: defaultBranchRegexp,
			expectedComment:      defaultComment,
		},
		{
			name:                 "only branchRegexp is set",
			branchRegexp:         `release-1.1.*$`,
			comment:              "",
			expectedBranchRegexp: `release-1.1.*$`,
			expectedComment:      defaultComment,
		},
		{
			name:                 "only comment is set",
			branchRegexp:         "",
			comment:              "custom comment",
			expectedBranchRegexp: defaultBranchRegexp,
			expectedComment:      "custom comment",
		},
		{
			name:                 "both branchRegexp and comment are set",
			branchRegexp:         `release-1.1.*$`,
			comment:              "custom comment",
			expectedBranchRegexp: `release-1.1.*$`,
			expectedComment:      "custom comment",
		},
	}

	for _, tc := range testcases {
		c := &Configuration{
			CherryPickUnapproved: CherryPickUnapproved{
				BranchRegexp: tc.branchRegexp,
				Comment:      tc.comment,
			},
		}

		c.setDefaults()

		if c.CherryPickUnapproved.BranchRegexp != tc.expectedBranchRegexp {
			t.Errorf("unexpected branchRegexp: %s, expected: %s", c.CherryPickUnapproved.BranchRegexp, tc.expectedBranchRegexp)
		}
		if c.CherryPickUnapproved.Comment != tc.expectedComment {
			t.Errorf("unexpected comment: %s, expected: %s", c.CherryPickUnapproved.Comment, tc.expectedComment)
		}
	}
}

func TestOptionsForItem(t *testing.T) {
	open := true
	one, two := "v1", "v2"
	var testCases = []struct {
		name     string
		item     string
		config   map[string]BugzillaBranchOptions
		expected BugzillaBranchOptions
	}{
		{
			name:     "no config means no options",
			item:     "item",
			config:   map[string]BugzillaBranchOptions{},
			expected: BugzillaBranchOptions{},
		},
		{
			name:     "unrelated config means no options",
			item:     "item",
			config:   map[string]BugzillaBranchOptions{"other": {IsOpen: &open, TargetRelease: &one}},
			expected: BugzillaBranchOptions{},
		},
		{
			name:     "global config resolves to options",
			item:     "item",
			config:   map[string]BugzillaBranchOptions{"*": {IsOpen: &open, TargetRelease: &one}},
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one},
		},
		{
			name:     "specific config resolves to options",
			item:     "item",
			config:   map[string]BugzillaBranchOptions{"item": {IsOpen: &open, TargetRelease: &one}},
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one},
		},
		{
			name: "global and specific config resolves to options that favor specificity",
			item: "item",
			config: map[string]BugzillaBranchOptions{
				"*":    {IsOpen: &open, TargetRelease: &one},
				"item": {TargetRelease: &two},
			},
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &two},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := OptionsForItem(testCase.item, testCase.config), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options for item %q: %v", testCase.name, testCase.item, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestResolveBugzillaOptions(t *testing.T) {
	open, closed := true, false
	yes, no := true, false
	one, two := "v1", "v2"
	modified, verified, post, pre := "MODIFIED", "VERIFIED", "POST", "PRE"
	modifiedState := BugzillaBugState{Status: modified}
	verifiedState := BugzillaBugState{Status: verified}
	postState := BugzillaBugState{Status: post}
	preState := BugzillaBugState{Status: pre}
	var testCases = []struct {
		name          string
		parent, child BugzillaBranchOptions
		expected      BugzillaBranchOptions
	}{
		{
			name: "no parent or child means no output",
		},
		{
			name:   "no child means a copy of parent is the output",
			parent: BugzillaBranchOptions{ValidateByDefault: &yes, IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, DependentBugStates: &[]BugzillaBugState{verifiedState}, DependentBugTargetReleases: &[]string{one}, StateAfterValidation: &postState},
			expected: BugzillaBranchOptions{
				ValidateByDefault:          &yes,
				IsOpen:                     &open,
				TargetRelease:              &one,
				ValidStates:                &[]BugzillaBugState{modifiedState},
				DependentBugStates:         &[]BugzillaBugState{verifiedState},
				DependentBugTargetReleases: &[]string{one},
				StateAfterValidation:       &postState,
			},
		},
		{
			name:  "no parent means a copy of child is the output",
			child: BugzillaBranchOptions{ValidateByDefault: &yes, IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, DependentBugStates: &[]BugzillaBugState{verifiedState}, DependentBugTargetReleases: &[]string{one}, StateAfterValidation: &postState},
			expected: BugzillaBranchOptions{
				ValidateByDefault:          &yes,
				IsOpen:                     &open,
				TargetRelease:              &one,
				ValidStates:                &[]BugzillaBugState{modifiedState},
				DependentBugStates:         &[]BugzillaBugState{verifiedState},
				DependentBugTargetReleases: &[]string{one},
				StateAfterValidation:       &postState,
			},
		},
		{
			name:     "child overrides parent on IsOpen",
			parent:   BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
			child:    BugzillaBranchOptions{IsOpen: &closed},
			expected: BugzillaBranchOptions{IsOpen: &closed, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
		},
		{
			name:     "child overrides parent on target release",
			parent:   BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
			child:    BugzillaBranchOptions{TargetRelease: &two},
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &two, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
		},
		{
			name:     "child overrides parent on states",
			parent:   BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
			child:    BugzillaBranchOptions{ValidStates: &[]BugzillaBugState{verifiedState}},
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{verifiedState}, StateAfterValidation: &postState},
		},
		{
			name:     "child overrides parent on state after validation",
			parent:   BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
			child:    BugzillaBranchOptions{StateAfterValidation: &preState},
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &preState},
		},
		{
			name:     "child overrides parent on validation by default",
			parent:   BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
			child:    BugzillaBranchOptions{ValidateByDefault: &yes},
			expected: BugzillaBranchOptions{ValidateByDefault: &yes, IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState},
		},
		{
			name:   "child overrides parent on dependent bug states",
			parent: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, DependentBugStates: &[]BugzillaBugState{verifiedState}, StateAfterValidation: &postState},
			child:  BugzillaBranchOptions{DependentBugStates: &[]BugzillaBugState{modifiedState}},
			expected: BugzillaBranchOptions{
				IsOpen:               &open,
				TargetRelease:        &one,
				ValidStates:          &[]BugzillaBugState{modifiedState},
				DependentBugStates:   &[]BugzillaBugState{modifiedState},
				StateAfterValidation: &postState,
			},
		},
		{
			name:     "child overrides parent on dependent bug target releases",
			parent:   BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState, DependentBugTargetReleases: &[]string{one}},
			child:    BugzillaBranchOptions{DependentBugTargetReleases: &[]string{two}},
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState, DependentBugTargetReleases: &[]string{two}},
		},
		{
			name:   "child overrides parent on state after merge",
			parent: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{modifiedState}, StateAfterValidation: &postState, StateAfterMerge: &postState},
			child:  BugzillaBranchOptions{StateAfterMerge: &preState},
			expected: BugzillaBranchOptions{
				IsOpen:               &open,
				TargetRelease:        &one,
				ValidStates:          &[]BugzillaBugState{modifiedState},
				StateAfterValidation: &postState,
				StateAfterMerge:      &preState,
			},
		},
		{
			name:     "status slices are correctly merged with states slices on parent",
			parent:   BugzillaBranchOptions{Statuses: &[]string{modified}, ValidStates: &[]BugzillaBugState{verifiedState}, DependentBugStatuses: &[]string{pre}, DependentBugStates: &[]BugzillaBugState{postState}},
			expected: BugzillaBranchOptions{ValidStates: &[]BugzillaBugState{modifiedState, verifiedState}, DependentBugStates: &[]BugzillaBugState{postState, preState}},
		},
		{
			name:     "status slices are correctly merged with states slices on child",
			child:    BugzillaBranchOptions{Statuses: &[]string{modified}, ValidStates: &[]BugzillaBugState{verifiedState}, DependentBugStatuses: &[]string{pre}, DependentBugStates: &[]BugzillaBugState{postState}},
			expected: BugzillaBranchOptions{ValidStates: &[]BugzillaBugState{modifiedState, verifiedState}, DependentBugStates: &[]BugzillaBugState{postState, preState}},
		},
		{
			name:     "state fields when not present re inferred from status fields on parent",
			parent:   BugzillaBranchOptions{StatusAfterMerge: &modified, StatusAfterValidation: &verified},
			expected: BugzillaBranchOptions{StateAfterMerge: &modifiedState, StateAfterValidation: &verifiedState},
		},
		{
			name:     "state fields when not present are inferred from status fields on child",
			child:    BugzillaBranchOptions{StatusAfterMerge: &modified, StatusAfterValidation: &verified},
			expected: BugzillaBranchOptions{StateAfterMerge: &modifiedState, StateAfterValidation: &verifiedState},
		},
		{
			name:     "child status overrides all statuses and states of the parent",
			parent:   BugzillaBranchOptions{Statuses: &[]string{modified}, ValidStates: &[]BugzillaBugState{verifiedState}, DependentBugStatuses: &[]string{modified}, DependentBugStates: &[]BugzillaBugState{verifiedState}, StatusAfterMerge: &pre, StateAfterMerge: &preState, StatusAfterValidation: &pre, StateAfterValidation: &preState},
			child:    BugzillaBranchOptions{Statuses: &[]string{post}, DependentBugStatuses: &[]string{post}, StatusAfterMerge: &post, StatusAfterValidation: &post},
			expected: BugzillaBranchOptions{ValidStates: &[]BugzillaBugState{postState}, DependentBugStates: &[]BugzillaBugState{postState}, StateAfterMerge: &postState, StateAfterValidation: &postState},
		},
		{
			name:     "parent dependent target release is merged on child",
			parent:   BugzillaBranchOptions{DeprecatedDependentBugTargetRelease: &one},
			child:    BugzillaBranchOptions{},
			expected: BugzillaBranchOptions{DependentBugTargetReleases: &[]string{one}},
		},
		{
			name:     "parent dependent target release is merged into target releases",
			parent:   BugzillaBranchOptions{DependentBugTargetReleases: &[]string{one}, DeprecatedDependentBugTargetRelease: &two},
			child:    BugzillaBranchOptions{},
			expected: BugzillaBranchOptions{DependentBugTargetReleases: &[]string{one, two}},
		},
		{
			name:   "child overrides parent on all fields",
			parent: BugzillaBranchOptions{ValidateByDefault: &yes, IsOpen: &open, TargetRelease: &one, ValidStates: &[]BugzillaBugState{verifiedState}, DependentBugStates: &[]BugzillaBugState{verifiedState}, DependentBugTargetReleases: &[]string{one}, StateAfterValidation: &postState, StateAfterMerge: &postState},
			child:  BugzillaBranchOptions{ValidateByDefault: &no, IsOpen: &closed, TargetRelease: &two, ValidStates: &[]BugzillaBugState{modifiedState}, DependentBugStates: &[]BugzillaBugState{modifiedState}, DependentBugTargetReleases: &[]string{two}, StateAfterValidation: &preState, StateAfterMerge: &preState},
			expected: BugzillaBranchOptions{
				ValidateByDefault:          &no,
				IsOpen:                     &closed,
				TargetRelease:              &two,
				ValidStates:                &[]BugzillaBugState{modifiedState},
				DependentBugStates:         &[]BugzillaBugState{modifiedState},
				DependentBugTargetReleases: &[]string{two},
				StateAfterValidation:       &preState,
				StateAfterMerge:            &preState,
			},
		},
		{
			name:     "parent target release is excluded on child",
			parent:   BugzillaBranchOptions{TargetRelease: &one},
			child:    BugzillaBranchOptions{ExcludeDefaults: &yes},
			expected: BugzillaBranchOptions{ExcludeDefaults: &yes},
		},
		{
			name:     "parent target release is excluded on child with other options",
			parent:   BugzillaBranchOptions{DependentBugTargetReleases: &[]string{one}},
			child:    BugzillaBranchOptions{TargetRelease: &one, ExcludeDefaults: &yes},
			expected: BugzillaBranchOptions{TargetRelease: &one, ExcludeDefaults: &yes},
		},
		{
			name:     "parent exclude merges with child options",
			parent:   BugzillaBranchOptions{DependentBugTargetReleases: &[]string{one}, ExcludeDefaults: &yes},
			child:    BugzillaBranchOptions{TargetRelease: &one},
			expected: BugzillaBranchOptions{DependentBugTargetReleases: &[]string{one}, TargetRelease: &one, ExcludeDefaults: &yes},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := ResolveBugzillaOptions(testCase.parent, testCase.child), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: resolved incorrect options for parent and child: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}

	var i int = 0
	managedCol1 := ManagedColumn{ID: &i, Name: "col1", State: "open", Labels: []string{"area/conformance", "area/testing"}, Org: "org1"}
	managedCol3 := ManagedColumn{ID: &i, Name: "col2", State: "open", Labels: []string{}, Org: "org2"}
	managedColx := ManagedColumn{ID: &i, Name: "col2", State: "open", Labels: []string{"area/conformance", "area/testing"}, Org: "org2"}
	invalidCol := ManagedColumn{State: "open", Labels: []string{"area/conformance", "area/testing2"}, Org: "org2"}
	invalidOrg := ManagedColumn{Name: "col1", State: "open", Labels: []string{"area/conformance", "area/testing2"}, Org: ""}
	managedProj2 := ManagedProject{Columns: []ManagedColumn{managedCol3}}
	managedProjx := ManagedProject{Columns: []ManagedColumn{managedCol1, managedColx}}
	managedOrgRepo2 := ManagedOrgRepo{Projects: map[string]ManagedProject{"project1": managedProj2}}
	managedOrgRepox := ManagedOrgRepo{Projects: map[string]ManagedProject{"project1": managedProjx}}

	projectManagerTestcases := []struct {
		name        string
		config      *Configuration
		expectedErr string
	}{
		{
			name: "No projects configured in a repo",
			config: &Configuration{
				ProjectManager: ProjectManager{
					OrgRepos: map[string]ManagedOrgRepo{"org1": {Projects: map[string]ManagedProject{}}},
				},
			},
			expectedErr: fmt.Sprintf("Org/repo: %s, has no projects configured", "org1"),
		},
		{
			name: "No columns configured for a project",
			config: &Configuration{
				ProjectManager: ProjectManager{
					OrgRepos: map[string]ManagedOrgRepo{"org1": {Projects: map[string]ManagedProject{"project1": {Columns: []ManagedColumn{}}}}},
				},
			},
			expectedErr: fmt.Sprintf("Org/repo: %s, project %s, has no columns configured", "org1", "project1"),
		},
		{
			name: "Columns does not have name or ID",
			config: &Configuration{
				ProjectManager: ProjectManager{
					OrgRepos: map[string]ManagedOrgRepo{"org1": {Projects: map[string]ManagedProject{"project1": {Columns: []ManagedColumn{invalidCol}}}}},
				},
			},
			expectedErr: fmt.Sprintf("Org/repo: %s, project %s, column %v, has no name/id configured", "org1", "project1", invalidCol),
		},
		{
			name: "Columns does not have owner Org/repo",
			config: &Configuration{
				ProjectManager: ProjectManager{
					OrgRepos: map[string]ManagedOrgRepo{"org1": {Projects: map[string]ManagedProject{"project1": {Columns: []ManagedColumn{invalidOrg}}}}},
				},
			},
			expectedErr: fmt.Sprintf("Org/repo: %s, project %s, column %s, has no org configured", "org1", "project1", "col1"),
		},
		{
			name: "No Labels specified in the column of the project",
			config: &Configuration{
				ProjectManager: ProjectManager{
					OrgRepos: map[string]ManagedOrgRepo{"org1": managedOrgRepo2},
				},
			},
			expectedErr: fmt.Sprintf("Org/repo: %s, project %s, column %s, has no labels configured", "org1", "project1", "col2"),
		},
		{
			name: "Same Label specified to multiple column in a project",
			config: &Configuration{
				ProjectManager: ProjectManager{
					OrgRepos: map[string]ManagedOrgRepo{"org1": managedOrgRepox},
				},
			},
			expectedErr: fmt.Sprintf("Org/repo: %s, project %s, column %s has same labels configured as another column", "org1", "project1", "col2"),
		},
	}

	for _, c := range projectManagerTestcases {
		t.Run(c.name, func(t *testing.T) {
			err := validateProjectManager(c.config.ProjectManager)
			if err != nil && len(c.expectedErr) == 0 {
				t.Fatalf("config validation error: %v", err)
			}
			if err == nil && len(c.expectedErr) > 0 {
				t.Fatalf("config validation error: %v but expecting %v", err, c.expectedErr)
			}
			if err != nil && c.expectedErr != err.Error() {
				t.Fatalf("Error running the test %s, \nexpected: %s, \nreceived: %s", c.name, c.expectedErr, err.Error())
			}
		})
	}
}

func TestOptionsForBranch(t *testing.T) {
	open, closed := true, false
	yes, no := true, false
	globalDefault, globalBranchDefault, orgDefault, orgBranchDefault, repoDefault, repoBranch, legacyBranch := "global-default", "global-branch-default", "my-org-default", "my-org-branch-default", "my-repo-default", "my-repo-branch", "my-legacy-branch"
	post, pre, release, notabug, new, reset := "POST", "PRE", "RELEASE_PENDING", "NOTABUG", "NEW", "RESET"
	verifiedState, modifiedState := BugzillaBugState{Status: "VERIFIED"}, BugzillaBugState{Status: "MODIFIED"}
	postState, preState, releaseState, notabugState, newState, resetState := BugzillaBugState{Status: post}, BugzillaBugState{Status: pre}, BugzillaBugState{Status: release}, BugzillaBugState{Status: notabug}, BugzillaBugState{Status: new}, BugzillaBugState{Status: reset}
	closedErrata := BugzillaBugState{Status: "CLOSED", Resolution: "ERRATA"}
	orgAllowedGroups, repoAllowedGroups := []string{"test"}, []string{"security", "test"}

	rawConfig := `default:
  "*":
    target_release: global-default
  "global-branch":
    is_open: false
    target_release: global-branch-default
orgs:
  my-org:
    default:
      "*":
        is_open: true
        target_release: my-org-default
        state_after_validation:
          status: "PRE"
        state_after_close:
          status: "NEW"
        allowed_groups:
        - test
      "my-org-branch":
        target_release: my-org-branch-default
        state_after_validation:
          status: "POST"
    repos:
      my-repo:
        branches:
          "*":
            is_open: false
            target_release: my-repo-default
            valid_states:
            - status: VERIFIED
            validate_by_default: false
            state_after_merge:
              status: RELEASE_PENDING
          "my-repo-branch":
            target_release: my-repo-branch
            valid_states:
            - status: MODIFIED
            - status: CLOSED
              resolution: ERRATA
            validate_by_default: true
            state_after_merge:
              status: NOTABUG
            state_after_close:
              status: RESET
            allowed_groups:
            - security
          "my-legacy-branch":
            target_release: my-legacy-branch
            statuses:
            - MODIFIED
            dependent_bug_statuses:
            - VERIFIED
            validate_by_default: true
            status_after_validation: MODIFIED
            status_after_merge: NOTABUG
          "my-special-branch":
            exclude_defaults: true
            validate_by_default: false
      another-repo:
        branches:
          "*":
            exclude_defaults: true
          "my-org-branch":
            target_release: my-repo-branch`
	var config Bugzilla
	if err := yaml.Unmarshal([]byte(rawConfig), &config); err != nil {
		t.Fatalf("couldn't unmarshal config: %v", err)
	}

	var testCases = []struct {
		name              string
		org, repo, branch string
		expected          BugzillaBranchOptions
	}{
		{
			name:     "unconfigured branch gets global default",
			org:      "some-org",
			repo:     "some-repo",
			branch:   "some-branch",
			expected: BugzillaBranchOptions{TargetRelease: &globalDefault},
		},
		{
			name:     "branch on unconfigured org/repo gets global default",
			org:      "some-org",
			repo:     "some-repo",
			branch:   "global-branch",
			expected: BugzillaBranchOptions{IsOpen: &closed, TargetRelease: &globalBranchDefault},
		},
		{
			name:     "branch on configured org but not repo gets org default",
			org:      "my-org",
			repo:     "some-repo",
			branch:   "some-branch",
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &orgDefault, StateAfterValidation: &preState, AllowedGroups: orgAllowedGroups, StateAfterClose: &newState},
		},
		{
			name:     "branch on configured org but not repo gets org branch default",
			org:      "my-org",
			repo:     "some-repo",
			branch:   "my-org-branch",
			expected: BugzillaBranchOptions{IsOpen: &open, TargetRelease: &orgBranchDefault, StateAfterValidation: &postState, AllowedGroups: orgAllowedGroups, StateAfterClose: &newState},
		},
		{
			name:     "branch on configured org and repo gets repo default",
			org:      "my-org",
			repo:     "my-repo",
			branch:   "some-branch",
			expected: BugzillaBranchOptions{ValidateByDefault: &no, IsOpen: &closed, TargetRelease: &repoDefault, ValidStates: &[]BugzillaBugState{verifiedState}, StateAfterValidation: &preState, StateAfterMerge: &releaseState, AllowedGroups: orgAllowedGroups, StateAfterClose: &newState},
		},
		{
			name:     "branch on configured org and repo gets branch config",
			org:      "my-org",
			repo:     "my-repo",
			branch:   "my-repo-branch",
			expected: BugzillaBranchOptions{ValidateByDefault: &yes, IsOpen: &closed, TargetRelease: &repoBranch, ValidStates: &[]BugzillaBugState{modifiedState, closedErrata}, StateAfterValidation: &preState, StateAfterMerge: &notabugState, AllowedGroups: repoAllowedGroups, StateAfterClose: &resetState},
		},
		{
			name:     "exclude branch on configured org and repo gets branch config",
			org:      "my-org",
			repo:     "my-repo",
			branch:   "my-special-branch",
			expected: BugzillaBranchOptions{ValidateByDefault: &no, ExcludeDefaults: &yes},
		},
		{
			name:     "exclude branch on repo cascades to branch config",
			org:      "my-org",
			repo:     "another-repo",
			branch:   "my-org-branch",
			expected: BugzillaBranchOptions{TargetRelease: &repoBranch, ExcludeDefaults: &yes},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := config.OptionsForBranch(testCase.org, testCase.repo, testCase.branch), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: resolved incorrect options for %s/%s#%s: %v", testCase.name, testCase.org, testCase.repo, testCase.branch, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}

	var repoTestCases = []struct {
		name      string
		org, repo string
		expected  map[string]BugzillaBranchOptions
	}{
		{
			name: "unconfigured repo gets global default",
			org:  "some-org",
			repo: "some-repo",
			expected: map[string]BugzillaBranchOptions{
				"*":             {TargetRelease: &globalDefault},
				"global-branch": {IsOpen: &closed, TargetRelease: &globalBranchDefault},
			},
		},
		{
			name: "repo in configured org gets org default",
			org:  "my-org",
			repo: "some-repo",
			expected: map[string]BugzillaBranchOptions{
				"*":             {IsOpen: &open, TargetRelease: &orgDefault, StateAfterValidation: &preState, AllowedGroups: orgAllowedGroups, StateAfterClose: &newState},
				"my-org-branch": {IsOpen: &open, TargetRelease: &orgBranchDefault, StateAfterValidation: &postState, AllowedGroups: orgAllowedGroups, StateAfterClose: &newState},
			},
		},
		{
			name: "configured repo gets repo config",
			org:  "my-org",
			repo: "my-repo",
			expected: map[string]BugzillaBranchOptions{
				"*": {
					ValidateByDefault:    &no,
					IsOpen:               &closed,
					TargetRelease:        &repoDefault,
					ValidStates:          &[]BugzillaBugState{verifiedState},
					StateAfterValidation: &preState,
					StateAfterMerge:      &releaseState,
					AllowedGroups:        orgAllowedGroups,
					StateAfterClose:      &newState,
				},
				"my-repo-branch": {
					ValidateByDefault:    &yes,
					IsOpen:               &closed,
					TargetRelease:        &repoBranch,
					ValidStates:          &[]BugzillaBugState{modifiedState, closedErrata},
					StateAfterValidation: &preState,
					StateAfterMerge:      &notabugState,
					AllowedGroups:        repoAllowedGroups,
					StateAfterClose:      &resetState,
				},
				"my-org-branch": {
					ValidateByDefault:    &no,
					IsOpen:               &closed,
					TargetRelease:        &repoDefault,
					ValidStates:          &[]BugzillaBugState{verifiedState},
					StateAfterValidation: &postState,
					StateAfterMerge:      &releaseState,
					AllowedGroups:        orgAllowedGroups,
					StateAfterClose:      &newState,
				},
				"my-legacy-branch": {
					ValidateByDefault:    &yes,
					IsOpen:               &closed,
					TargetRelease:        &legacyBranch,
					ValidStates:          &[]BugzillaBugState{modifiedState},
					DependentBugStates:   &[]BugzillaBugState{verifiedState},
					StateAfterValidation: &modifiedState,
					StateAfterMerge:      &notabugState,
					AllowedGroups:        orgAllowedGroups,
					StateAfterClose:      &newState,
				},
				"my-special-branch": {
					ValidateByDefault: &no,
					ExcludeDefaults:   &yes,
				},
			},
		},
		{
			name: "excluded repo gets no defaults",
			org:  "my-org",
			repo: "another-repo",
			expected: map[string]BugzillaBranchOptions{
				"*":             {ExcludeDefaults: &yes},
				"my-org-branch": {ExcludeDefaults: &yes, TargetRelease: &repoBranch},
			},
		},
	}
	for _, testCase := range repoTestCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := config.OptionsForRepo(testCase.org, testCase.repo), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: resolved incorrect options for %s/%s: %v", testCase.name, testCase.org, testCase.repo, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestBugzillaBugState_String(t *testing.T) {
	testCases := []struct {
		name     string
		state    *BugzillaBugState
		expected string
	}{
		{
			name:     "empty struct",
			state:    &BugzillaBugState{},
			expected: "",
		},
		{
			name:     "only status",
			state:    &BugzillaBugState{Status: "CLOSED"},
			expected: "CLOSED",
		},
		{
			name:     "only resolution",
			state:    &BugzillaBugState{Resolution: "NOTABUG"},
			expected: "any status with resolution NOTABUG",
		},
		{
			name:     "status and resolution",
			state:    &BugzillaBugState{Status: "CLOSED", Resolution: "NOTABUG"},
			expected: "CLOSED (NOTABUG)",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.state.String()
			if actual != tc.expected {
				t.Errorf("%s: expected %q, got %q", tc.name, tc.expected, actual)
			}
		})
	}
}

func TestBugzillaBugState_Matches(t *testing.T) {
	modified, closed, errata, notabug := "MODIFIED", "CLOSED", "ERRATA", "NOTABUG"
	testCases := []struct {
		name     string
		state    *BugzillaBugState
		bug      *bugzilla.Bug
		expected bool
	}{
		{
			name: "both pointers are nil -> false",
		},
		{
			name: "state pointer is nil -> false",
			bug:  &bugzilla.Bug{},
		},
		{
			name:  "bug pointer is nil -> false",
			state: &BugzillaBugState{},
		},
		{
			name:     "statuses do not match -> false",
			state:    &BugzillaBugState{Status: modified, Resolution: errata},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: false,
		},
		{
			name:     "resolutions do not match -> false",
			state:    &BugzillaBugState{Status: closed, Resolution: notabug},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: false,
		},
		{
			name:     "no state enforced -> true",
			state:    &BugzillaBugState{},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: true,
		},
		{
			name:     "status match, resolution not enforced -> true",
			state:    &BugzillaBugState{Status: closed},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: true,
		},
		{
			name:     "status not enforced, resolution match -> true",
			state:    &BugzillaBugState{Resolution: errata},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: true,
		},
		{
			name:     "status and resolution match -> true",
			state:    &BugzillaBugState{Status: closed, Resolution: errata},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.state.Matches(tc.bug)
			if actual != tc.expected {
				t.Errorf("%s: expected %t, got %t", tc.name, tc.expected, actual)
			}
		})
	}
}

func TestBugzillaBugState_AsBugUpdate(t *testing.T) {
	modified, closed, errata, notabug := "MODIFIED", "CLOSED", "ERRATA", "NOTABUG"
	testCases := []struct {
		name     string
		state    *BugzillaBugState
		bug      *bugzilla.Bug
		expected *bugzilla.BugUpdate
	}{
		{
			name:     "bug is nil so update contains whole state",
			state:    &BugzillaBugState{Status: closed, Resolution: errata},
			expected: &bugzilla.BugUpdate{Status: closed, Resolution: errata},
		},
		{
			name:     "bug is empty so update contains whole state",
			state:    &BugzillaBugState{Status: closed, Resolution: errata},
			bug:      &bugzilla.Bug{},
			expected: &bugzilla.BugUpdate{Status: closed, Resolution: errata},
		},
		{
			name:     "state is empty so update is nil",
			state:    &BugzillaBugState{},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: nil,
		},
		{
			name:     "status differs so update contains it",
			state:    &BugzillaBugState{Status: closed},
			bug:      &bugzilla.Bug{Status: modified, Resolution: errata},
			expected: &bugzilla.BugUpdate{Status: closed},
		},
		{
			name:     "resolution differs so update contains it",
			state:    &BugzillaBugState{Status: closed, Resolution: errata},
			bug:      &bugzilla.Bug{Status: closed, Resolution: notabug},
			expected: &bugzilla.BugUpdate{Resolution: errata},
		},
		{
			name:     "status and resolution match so update is nil",
			state:    &BugzillaBugState{Status: closed, Resolution: errata},
			bug:      &bugzilla.Bug{Status: closed, Resolution: errata},
			expected: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.state.AsBugUpdate(tc.bug)
			if tc.expected != actual {
				if actual == nil {
					t.Errorf("%s: unexpected nil", tc.name)
				}
				if tc.expected == nil {
					t.Errorf("%s: expected nil, got %v", tc.name, actual)
				}
			}

			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: BugUpdate differs from expected:\n%s", tc.name, diff.ObjectReflectDiff(*actual, *tc.expected))
			}
		})
	}
}

func TestBugzillaBugStateSet_Has(t *testing.T) {
	bugInProgress := BugzillaBugState{Status: "MODIFIED"}
	bugErrata := BugzillaBugState{Status: "CLOSED", Resolution: "ERRATA"}
	bugWontfix := BugzillaBugState{Status: "CLOSED", Resolution: "WONTFIX"}

	testCases := []struct {
		name   string
		states []BugzillaBugState
		state  BugzillaBugState

		expectedLength int
		expectedHas    bool
	}{
		{
			name:           "empty set",
			state:          bugInProgress,
			expectedLength: 0,
			expectedHas:    false,
		},
		{
			name:           "membership",
			states:         []BugzillaBugState{bugInProgress},
			state:          bugInProgress,
			expectedLength: 1,
			expectedHas:    true,
		},
		{
			name:           "non-membership",
			states:         []BugzillaBugState{bugInProgress, bugErrata},
			state:          bugWontfix,
			expectedLength: 2,
			expectedHas:    false,
		},
		{
			name:           "actually a set",
			states:         []BugzillaBugState{bugInProgress, bugInProgress, bugInProgress},
			state:          bugInProgress,
			expectedLength: 1,
			expectedHas:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			set := NewBugzillaBugStateSet(tc.states)
			if len(set) != tc.expectedLength {
				t.Errorf("%s: expected set to have %d members, it has %d", tc.name, tc.expectedLength, len(set))
			}
			var not string
			if !tc.expectedHas {
				not = "not "
			}
			has := set.Has(tc.state)
			if has != tc.expectedHas {
				t.Errorf("%s: expected set to %scontain %v", tc.name, not, tc.state)
			}
		})
	}
}

func TestStatesMatch(t *testing.T) {
	modified := BugzillaBugState{Status: "MODIFIED"}
	errata := BugzillaBugState{Status: "CLOSED", Resolution: "ERRATA"}
	wontfix := BugzillaBugState{Status: "CLOSED", Resolution: "WONTFIX"}
	testCases := []struct {
		name          string
		first, second []BugzillaBugState
		expected      bool
	}{
		{
			name:     "empty slices match",
			expected: true,
		},
		{
			name:  "one empty, one non-empty do not match",
			first: []BugzillaBugState{modified},
		},
		{
			name:     "identical slices match",
			first:    []BugzillaBugState{modified},
			second:   []BugzillaBugState{modified},
			expected: true,
		},
		{
			name:     "ordering does not matter",
			first:    []BugzillaBugState{modified, errata},
			second:   []BugzillaBugState{errata, modified},
			expected: true,
		},
		{
			name:     "different slices do not match",
			first:    []BugzillaBugState{modified, errata},
			second:   []BugzillaBugState{modified, wontfix},
			expected: false,
		},
		{
			name:     "suffix in first operand is not ignored",
			first:    []BugzillaBugState{modified, errata},
			second:   []BugzillaBugState{modified},
			expected: false,
		},
		{
			name:     "suffix in second operand is not ignored",
			first:    []BugzillaBugState{modified},
			second:   []BugzillaBugState{modified, errata},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := statesMatch(tc.first, tc.second)
			if actual != tc.expected {
				t.Errorf("%s: expected %t, got %t", tc.name, tc.expected, actual)
			}
		})
	}
}

func TestValidateConfigUpdater(t *testing.T) {
	testCases := []struct {
		name        string
		cu          *ConfigUpdater
		expected    error
		expectedMsg string
	}{
		{
			name: "same key of different cms in different ns",
			cu: &ConfigUpdater{
				Maps: map[string]ConfigMapSpec{
					"core-services/prow/02_config/_plugins.yaml": {
						Name:     "plugins",
						Key:      "plugins.yaml",
						Clusters: map[string][]string{"first": {"some-namespace"}},
					},
					"somewhere/else/plugins.yaml": {
						Name:     "plugins",
						Key:      "plugins.yaml",
						Clusters: map[string][]string{"first": {"other-namespace"}},
					},
				},
			},
			expected: nil,
		},
		{
			name: "same key of a cm in the same ns",
			cu: &ConfigUpdater{
				Maps: map[string]ConfigMapSpec{
					"core-services/prow/02_config/_plugins.yaml": {
						Name:     "plugins",
						Key:      "plugins.yaml",
						Clusters: map[string][]string{"first": {"some-namespace"}},
					},
					"somewhere/else/plugins.yaml": {
						Name:     "plugins",
						Key:      "plugins.yaml",
						Clusters: map[string][]string{"first": {"some-namespace"}},
					},
				},
			},
			expected: fmt.Errorf("key plugins.yaml in configmap plugins updated with more than one file"),
		},
		{
			name: "same key of a cm in the same ns different clusters",
			cu: &ConfigUpdater{
				Maps: map[string]ConfigMapSpec{
					"core-services/prow/02_config/_plugins.yaml": {
						Name:     "plugins",
						Key:      "plugins.yaml",
						Clusters: map[string][]string{"first": {"some-namespace"}},
					},
					"somewhere/else/plugins.yaml": {
						Name:     "plugins",
						Key:      "plugins.yaml",
						Clusters: map[string][]string{"other": {"some-namespace"}},
					},
				},
			},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := validateConfigUpdater(tc.cu)
			if tc.expected == nil && actual != nil {
				t.Errorf("unexpected error: '%v'", actual)
			}
			if tc.expected != nil && actual == nil {
				t.Errorf("expected error '%v'', but it is nil", tc.expected)
			}
			if tc.expected != nil && actual != nil && tc.expected.Error() != actual.Error() {
				t.Errorf("expected error '%v', but it is '%v'", tc.expected, actual)
			}
		})
	}
}

func TestConfigUpdaterResolve(t *testing.T) {
	testCases := []struct {
		name           string
		in             ConfigUpdater
		expectedConfig ConfigUpdater
		exppectedError string
	}{
		{
			name:           "both cluster and cluster_groups is set, error",
			in:             ConfigUpdater{Maps: map[string]ConfigMapSpec{"map": {Clusters: map[string][]string{"cluster": nil}, ClusterGroups: []string{"group"}}}},
			exppectedError: "item maps.map contains both clusters and cluster_groups",
		},
		{
			name:           "inexistent cluster_group is referenced, error",
			in:             ConfigUpdater{Maps: map[string]ConfigMapSpec{"map": {ClusterGroups: []string{"group"}}}},
			exppectedError: "item maps.map.cluster_groups.0 references inexistent cluster group named group",
		},
		{
			name: "successful resolving",
			in: ConfigUpdater{
				ClusterGroups: map[string]ClusterGroup{
					"some-group":    {Clusters: []string{"cluster-a"}, Namespaces: []string{"namespace-a"}},
					"another-group": {Clusters: []string{"cluster-b"}, Namespaces: []string{"namespace-b"}},
				},
				Maps: map[string]ConfigMapSpec{"map": {
					Name:          "name",
					Key:           "key",
					GZIP:          utilpointer.Bool(true),
					ClusterGroups: []string{"some-group", "another-group"}},
				},
			},
			expectedConfig: ConfigUpdater{
				Maps: map[string]ConfigMapSpec{"map": {
					Name: "name",
					Key:  "key",
					GZIP: utilpointer.Bool(true),
					Clusters: map[string][]string{
						"cluster-a": {"namespace-a"},
						"cluster-b": {"namespace-b"},
					}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			var errMsg string
			err := tc.in.resolve()
			if err != nil {
				errMsg = err.Error()
			}
			if errMsg != tc.exppectedError {
				t.Fatalf("expected error %s, got error %s", tc.exppectedError, errMsg)
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(tc.expectedConfig, tc.in); diff != "" {
				t.Errorf("expected config differs from actual config: %s", diff)
			}
		})
	}
}

func TestEnabledReposForPlugin(t *testing.T) {
	pluginsYaml := []byte(`
orgA:
 excluded_repos:
 - repoB
 plugins:
 - pluginCommon
 - pluginNotForRepoB
orgA/repoB:
 plugins:
 - pluginCommon
 - pluginOnlyForRepoB
`)
	var p Plugins
	err := yaml.Unmarshal(pluginsYaml, &p)
	if err != nil {
		t.Errorf("cannot unmarshal plugins config: %v", err)
	}
	cfg := Configuration{
		Plugins: p,
	}
	testCases := []struct {
		name              string
		wantOrgs          []string
		wantRepos         []string
		wantExcludedRepos map[string]sets.Set[string]
	}{
		{
			name:              "pluginCommon",
			wantOrgs:          []string{"orgA"},
			wantRepos:         []string{"orgA/repoB"},
			wantExcludedRepos: map[string]sets.Set[string]{"orgA": {}},
		},
		{
			name:              "pluginNotForRepoB",
			wantOrgs:          []string{"orgA"},
			wantRepos:         nil,
			wantExcludedRepos: map[string]sets.Set[string]{"orgA": {"orgA/repoB": {}}},
		},
		{
			name:              "pluginOnlyForRepoB",
			wantOrgs:          nil,
			wantRepos:         []string{"orgA/repoB"},
			wantExcludedRepos: map[string]sets.Set[string]{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orgs, repos, excludedRepos := cfg.EnabledReposForPlugin(tc.name)
			if diff := cmp.Diff(tc.wantOrgs, orgs); diff != "" {
				t.Errorf("expected wantOrgs differ from actual: %s", diff)
			}
			if diff := cmp.Diff(tc.wantRepos, repos); diff != "" {
				t.Errorf("expected repos differ from actual: %s", diff)
			}
			if diff := cmp.Diff(tc.wantExcludedRepos, excludedRepos); diff != "" {
				t.Errorf("expected excludedRepos differ from actual: %s", diff)
			}
		})
	}
}

func TestPluginsUnmarshalFailed(t *testing.T) {
	badPluginsYaml := []byte(`
orgA:
 excluded_repos = [ repoB ]
 plugins:
 - pluginCommon
 - pluginNotForRepoB
orgA/repoB:
 plugins:
 - pluginCommon
 - pluginOnlyForRepoB
`)
	var p Plugins
	err := p.UnmarshalJSON(badPluginsYaml)
	if err == nil {
		t.Error("expected unmarshal error but didn't get one")
	}
}

func TestConfigMergingProperties(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		makeMergeable func(*Configuration)
	}{
		{
			name: "Plugins config",
			makeMergeable: func(c *Configuration) {
				*c = Configuration{Plugins: c.Plugins, Bugzilla: c.Bugzilla}
			},
		},
	}

	expectedProperties := []struct {
		name         string
		verification func(t *testing.T, fuzzedConfig *Configuration)
	}{
		{
			name: "Merging into empty config always succeeds and makes the empty config equal to the one that was merged in",
			verification: func(t *testing.T, fuzzedMergeableConfig *Configuration) {
				newConfig := &Configuration{}
				if err := newConfig.mergeFrom(fuzzedMergeableConfig); err != nil {
					t.Fatalf("merging fuzzed mergeable config into empty config failed: %v", err)
				}
				if diff := cmp.Diff(newConfig, fuzzedMergeableConfig); diff != "" {
					t.Errorf("after merging config into an empty config, the config that was merged into differs from the one we merged from:\n%s\n", diff)
				}
			},
		},
		{
			name: "Merging empty config in always succeeds",
			verification: func(t *testing.T, fuzzedMergeableConfig *Configuration) {
				if err := fuzzedMergeableConfig.mergeFrom(&Configuration{}); err != nil {
					t.Errorf("merging empty config in failed: %v", err)
				}
			},
		},
		{
			name: "Merging a config into itself always fails",
			verification: func(t *testing.T, fuzzedMergeableConfig *Configuration) {

				// An empty bugzilla org config does nothing, so clean those.
				for org, val := range fuzzedMergeableConfig.Bugzilla.Orgs {
					if reflect.DeepEqual(val, BugzillaOrgOptions{}) {
						delete(fuzzedMergeableConfig.Bugzilla.Orgs, org)
					}
				}
				// An exception to the rule is merging an empty config into itself, that is valid and will just do nothing.
				if apiequality.Semantic.DeepEqual(fuzzedMergeableConfig, &Configuration{}) {
					return
				}

				if err := fuzzedMergeableConfig.mergeFrom(fuzzedMergeableConfig); err == nil {
					serialized, serializeErr := yaml.Marshal(fuzzedMergeableConfig)
					if serializeErr != nil {
						t.Fatalf("merging non-empty config into itself did not yield an error and serializing it afterwards failed: %v. Raw object: %+v", serializeErr, fuzzedMergeableConfig)
					}
					t.Errorf("merging a config into itself did not produce an error. Serialized config:\n%s", string(serialized))
				}
			},
		},
	}

	seed := time.Now().UnixNano()
	// Print the seed so failures can easily be reproduced
	t.Logf("Seed: %d", seed)
	fuzzer := fuzz.NewWithSeed(seed)

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, propertyTest := range expectedProperties {
				propertyTest := propertyTest
				t.Run(propertyTest.name, func(t *testing.T) {
					t.Parallel()

					for i := 0; i < 100; i++ {
						fuzzedConfig := &Configuration{}
						fuzzer.Fuzz(fuzzedConfig)

						tc.makeMergeable(fuzzedConfig)

						propertyTest.verification(t, fuzzedConfig)
					}
				})
			}
		})
	}
}

func TestPluginsMergeFrom(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string

		from *Plugins
		to   *Plugins

		expected       *Plugins
		expectedErrMsg string
	}{
		{
			name: "Merging for two different repos succeeds",

			from: &Plugins{"org/repo-1": OrgPlugins{Plugins: []string{"wip"}}},
			to:   &Plugins{"org/repo-2": OrgPlugins{Plugins: []string{"wip"}}},

			expected: &Plugins{
				"org/repo-1": OrgPlugins{Plugins: []string{"wip"}},
				"org/repo-2": OrgPlugins{Plugins: []string{"wip"}},
			},
		},
		{
			name: "Merging the same repo fails",

			from: &Plugins{"org/repo-1": OrgPlugins{Plugins: []string{"wip"}}},
			to:   &Plugins{"org/repo-1": OrgPlugins{Plugins: []string{"wip"}}},

			expectedErrMsg: "found duplicate config for plugins.org/repo-1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var errMsg string
			err := tc.to.mergeFrom(tc.from)
			if err != nil {
				errMsg = err.Error()
			}
			if tc.expectedErrMsg != errMsg {
				t.Fatalf("expected error message %q, got %s", tc.expectedErrMsg, errMsg)
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(tc.expected, tc.to); diff != "" {
				t.Errorf("expexcted config differs from actual: %s", diff)
			}
		})
	}
}

func TestBugzillaMergeFrom(t *testing.T) {
	t.Parallel()

	yes := true
	targetRelease1 := "target-release-1"
	targetRelease2 := "target-release-2"

	testCases := []struct {
		name string

		from *Bugzilla
		to   *Bugzilla

		expected       *Bugzilla
		expectedErrMsg string
	}{
		{
			name: "Merging for two different repos",

			from: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease1,
								},
							},
						},
					},
				},
			}},
			to: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-2": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease2,
								},
							},
						},
					},
				},
			}},

			expected: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease1,
								},
							},
						},
						"repo-2": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease2,
								},
							},
						},
					},
				},
			}},
		},
		{
			name: "Merging organization defaults and repo in org",

			from: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-2": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease2,
								},
							},
						},
					},
				},
			}},
			to: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Default: map[string]BugzillaBranchOptions{
						"master": {
							IsOpen:        &yes,
							TargetRelease: &targetRelease1,
						},
					},
				},
			}},

			expected: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Default: map[string]BugzillaBranchOptions{
						"master": {
							IsOpen:        &yes,
							TargetRelease: &targetRelease1,
						},
					},
					Repos: map[string]BugzillaRepoOptions{
						"repo-2": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease2,
								},
							},
						},
					},
				},
			}},
		},
		{
			name: "Merging 2 organizations",

			from: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease1,
								},
							},
						},
					},
				},
			}},
			to: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org-2": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease2,
								},
							},
						},
					},
				},
			}},

			expected: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease1,
								},
							},
						},
					}},
				"org-2": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease2,
								},
							},
						},
					},
				},
			}},
		},
		{
			name: "Merging global defaults succeeds",

			from: &Bugzilla{Default: map[string]BugzillaBranchOptions{
				"master": {
					IsOpen:        &yes,
					TargetRelease: &targetRelease1,
				},
			}},
			to: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease1,
								},
							},
						},
					},
				},
			}},
			expected: &Bugzilla{Default: map[string]BugzillaBranchOptions{
				"master": {
					IsOpen:        &yes,
					TargetRelease: &targetRelease1,
				},
			}, Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease1,
								},
							},
						},
					},
				},
			}},
		},
		{
			name: "Merging multiple global defaults fails",

			from: &Bugzilla{Default: map[string]BugzillaBranchOptions{
				"master": {
					IsOpen:        &yes,
					TargetRelease: &targetRelease1,
				},
			}},
			to: &Bugzilla{Default: map[string]BugzillaBranchOptions{
				"master": {
					IsOpen:        &yes,
					TargetRelease: &targetRelease2,
				},
			}},
			expectedErrMsg: "configuration of global default defined in multiple places",
		},
		{
			name: "Merging same organization defaults fails",

			from: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Default: map[string]BugzillaBranchOptions{
						"master": {
							IsOpen:        &yes,
							TargetRelease: &targetRelease1,
						},
					},
				},
			}},
			to: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Default: map[string]BugzillaBranchOptions{
						"master": {
							IsOpen:        &yes,
							TargetRelease: &targetRelease2,
						},
					},
				},
			}},

			expectedErrMsg: "found duplicate organization config for bugzilla.org",
		},
		{
			name: "Merging same repository fails",

			from: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease1,
								},
							},
						},
					},
				},
			}},
			to: &Bugzilla{Orgs: map[string]BugzillaOrgOptions{
				"org": {
					Repos: map[string]BugzillaRepoOptions{
						"repo-1": {
							Branches: map[string]BugzillaBranchOptions{
								"master": {
									IsOpen:        &yes,
									TargetRelease: &targetRelease2,
								},
							},
						},
					},
				},
			}},

			expectedErrMsg: "found duplicate repository config for bugzilla.org/repo-1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var errMsg string
			err := tc.to.mergeFrom(tc.from)
			if err != nil {
				errMsg = err.Error()
			}
			if tc.expectedErrMsg != errMsg {
				t.Fatalf("expected error message %q, got %q", tc.expectedErrMsg, errMsg)
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(tc.expected, tc.to); diff != "" {
				t.Errorf("expexcted config differs from actual: %s", diff)
			}
		})
	}
}

func TestHasConfigFor(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name            string
		resultGenerator func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string])
	}{
		{
			name: "Any non-empty config with empty Plugins and Bugzilla is considered to be global",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				fuzzedConfig.Plugins = nil
				fuzzedConfig.Bugzilla = Bugzilla{}
				fuzzedConfig.Approve = nil
				fuzzedConfig.Label.RestrictedLabels = nil
				fuzzedConfig.Lgtm = nil
				fuzzedConfig.Triggers = nil
				fuzzedConfig.Welcome = nil
				fuzzedConfig.ExternalPlugins = nil
				return fuzzedConfig, !reflect.DeepEqual(fuzzedConfig, &Configuration{}), nil, nil
			},
		},
		{
			name: "Any config with plugins is considered to be for the orgs and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				// exclude non-plugins configs to test plugins specifically
				fuzzedConfig = &Configuration{Plugins: fuzzedConfig.Plugins}
				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}
				for orgOrRepo := range fuzzedConfig.Plugins {
					if strings.Contains(orgOrRepo, "/") {
						expectRepos.Insert(orgOrRepo)
					} else {
						expectOrgs.Insert(orgOrRepo)
					}
				}
				return fuzzedConfig, !reflect.DeepEqual(fuzzedConfig, &Configuration{Plugins: fuzzedConfig.Plugins}), expectOrgs, expectRepos
			},
		},
		{
			name: "Any config with bugzilla is considered to be for the orgs and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				// exclude non-plugins configs to test bugzilla specifically
				fuzzedConfig = &Configuration{Bugzilla: fuzzedConfig.Bugzilla}
				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}
				for org, orgConfig := range fuzzedConfig.Bugzilla.Orgs {
					if orgConfig.Default != nil {
						expectOrgs.Insert(org)
					}
					for repo := range orgConfig.Repos {
						expectRepos.Insert(org + "/" + repo)
					}
				}
				return fuzzedConfig, len(fuzzedConfig.Bugzilla.Default) > 0, expectOrgs, expectRepos
			},
		},
		{
			name: "Any config with approve is considered to be for the orgs and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				fuzzedConfig = &Configuration{Approve: fuzzedConfig.Approve}
				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}

				for _, approveConfig := range fuzzedConfig.Approve {
					for _, orgOrRepo := range approveConfig.Repos {
						if strings.Contains(orgOrRepo, "/") {
							expectRepos.Insert(orgOrRepo)
						} else {
							expectOrgs.Insert(orgOrRepo)
						}
					}
				}

				return fuzzedConfig, false, expectOrgs, expectRepos
			},
		},
		{
			name: "Any config with lgtm is considered to be for the orgs and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				fuzzedConfig = &Configuration{Lgtm: fuzzedConfig.Lgtm}
				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}

				for _, lgtm := range fuzzedConfig.Lgtm {
					for _, orgOrRepo := range lgtm.Repos {
						if strings.Contains(orgOrRepo, "/") {
							expectRepos.Insert(orgOrRepo)
						} else {
							expectOrgs.Insert(orgOrRepo)
						}
					}
				}

				return fuzzedConfig, false, expectOrgs, expectRepos
			},
		},
		{
			name: "Any config with triggers is considered to be for the orgs and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				fuzzedConfig = &Configuration{Triggers: fuzzedConfig.Triggers}
				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}

				for _, trigger := range fuzzedConfig.Triggers {
					for _, orgOrRepo := range trigger.Repos {
						if strings.Contains(orgOrRepo, "/") {
							expectRepos.Insert(orgOrRepo)
						} else {
							expectOrgs.Insert(orgOrRepo)
						}
					}
				}

				return fuzzedConfig, false, expectOrgs, expectRepos
			},
		},
		{
			name: "Any config with welcome is considered to be for the orgs and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				fuzzedConfig = &Configuration{Welcome: fuzzedConfig.Welcome}
				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}

				for _, welcome := range fuzzedConfig.Welcome {
					for _, orgOrRepo := range welcome.Repos {
						if strings.Contains(orgOrRepo, "/") {
							expectRepos.Insert(orgOrRepo)
						} else {
							expectOrgs.Insert(orgOrRepo)
						}
					}
				}

				return fuzzedConfig, false, expectOrgs, expectRepos
			},
		},
		{
			name: "Any config with external-plugins is considered to be for the orgs and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				fuzzedConfig = &Configuration{ExternalPlugins: fuzzedConfig.ExternalPlugins}
				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}

				for orgOrRepo := range fuzzedConfig.ExternalPlugins {
					if strings.Contains(orgOrRepo, "/") {
						expectRepos.Insert(orgOrRepo)
					} else {
						expectOrgs.Insert(orgOrRepo)
					}
				}
				return fuzzedConfig, false, expectOrgs, expectRepos
			},
		},
		{
			name: "Any config with label.restricted_labels is considered to be for the org and repos references there",
			resultGenerator: func(fuzzedConfig *Configuration) (toCheck *Configuration, expectGlobal bool, expectOrgs sets.Set[string], expectRepos sets.Set[string]) {
				fuzzedConfig = &Configuration{Label: fuzzedConfig.Label}
				if len(fuzzedConfig.Label.AdditionalLabels) > 0 {
					expectGlobal = true
				}

				expectOrgs, expectRepos = sets.Set[string]{}, sets.Set[string]{}

				for orgOrRepo := range fuzzedConfig.Label.RestrictedLabels {
					if orgOrRepo == "*" {
						expectGlobal = true
					} else if strings.Contains(orgOrRepo, "/") {
						expectRepos.Insert(orgOrRepo)
					} else {
						expectOrgs.Insert(orgOrRepo)
					}
				}
				return fuzzedConfig, expectGlobal, expectOrgs, expectRepos
			},
		},
	}

	seed := time.Now().UnixNano()
	// Print the seed so failures can easily be reproduced
	t.Logf("Seed: %d", seed)
	fuzzer := fuzz.NewWithSeed(seed)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				fuzzedConfig := &Configuration{}
				fuzzer.Fuzz(fuzzedConfig)

				fuzzedAndManipulatedConfig, expectIsGlobal, expectOrgs, expectRepos := tc.resultGenerator(fuzzedConfig)
				actualIsGlobal, actualOrgs, actualRepos := fuzzedAndManipulatedConfig.HasConfigFor()

				if expectIsGlobal != actualIsGlobal {
					t.Errorf("exepcted isGlobal: %t, got: %t", expectIsGlobal, actualIsGlobal)
				}

				if diff := cmp.Diff(expectOrgs, actualOrgs); diff != "" {
					t.Errorf("expected orgs differ from actual: %s", diff)
				}

				if diff := cmp.Diff(expectRepos, actualRepos); diff != "" {
					t.Errorf("expected repos differ from actual: %s", diff)
				}
			}
		})
	}
}

func TestMergeFrom(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                string
		in                  Configuration
		supplementalConfigs []Configuration
		expected            Configuration
		errorExpected       bool
	}{
		{
			name:                "Approve config gets merged",
			in:                  Configuration{Approve: []Approve{{Repos: []string{"foo/bar"}}}},
			supplementalConfigs: []Configuration{{Approve: []Approve{{Repos: []string{"foo/baz"}}}}},
			expected: Configuration{Approve: []Approve{
				{Repos: []string{"foo/bar"}},
				{Repos: []string{"foo/baz"}},
			}},
		},
		{
			name:                "LGTM config gets merged",
			in:                  Configuration{Lgtm: []Lgtm{{Repos: []string{"foo/bar"}}}},
			supplementalConfigs: []Configuration{{Lgtm: []Lgtm{{Repos: []string{"foo/baz"}}}}},
			expected: Configuration{Lgtm: []Lgtm{
				{Repos: []string{"foo/bar"}},
				{Repos: []string{"foo/baz"}},
			}},
		},
		{
			name:                "Triggers config gets merged",
			in:                  Configuration{Triggers: []Trigger{{Repos: []string{"foo/bar"}}}},
			supplementalConfigs: []Configuration{{Triggers: []Trigger{{Repos: []string{"foo/baz"}}}}},
			expected: Configuration{Triggers: []Trigger{
				{Repos: []string{"foo/bar"}},
				{Repos: []string{"foo/baz"}},
			}},
		},
		{
			name:                "Welcome config gets merged",
			in:                  Configuration{Welcome: []Welcome{{Repos: []string{"foo/bar"}}}},
			supplementalConfigs: []Configuration{{Welcome: []Welcome{{Repos: []string{"foo/baz"}}}}},
			expected: Configuration{Welcome: []Welcome{
				{Repos: []string{"foo/bar"}},
				{Repos: []string{"foo/baz"}},
			}},
		},
		{
			name: "ExternalPlugins get merged",
			in: Configuration{
				ExternalPlugins: map[string][]ExternalPlugin{
					"foo/bar": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}},
				},
			},
			supplementalConfigs: []Configuration{{ExternalPlugins: map[string][]ExternalPlugin{"foo/baz": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}}}}},
			expected: Configuration{
				ExternalPlugins: map[string][]ExternalPlugin{
					"foo/bar": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}},
					"foo/baz": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}},
				},
			},
		},
		{
			name:                "Labels.restricted_config gets merged",
			in:                  Configuration{Label: Label{AdditionalLabels: []string{"foo"}}},
			supplementalConfigs: []Configuration{{Label: Label{RestrictedLabels: map[string][]RestrictedLabel{"org": {{Label: "cherry-pick-approved", AllowedTeams: []string{"patch-managers"}}}}}}},
			expected: Configuration{
				Label: Label{
					AdditionalLabels: []string{"foo"},
					RestrictedLabels: map[string][]RestrictedLabel{"org": {{Label: "cherry-pick-approved", AllowedTeams: []string{"patch-managers"}}}},
				},
			},
		},
		{
			name:                "main config has no ExternalPlugins config, supplemental config has, it gets merged",
			supplementalConfigs: []Configuration{{ExternalPlugins: map[string][]ExternalPlugin{"foo/bar": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}}}}},
			expected: Configuration{
				ExternalPlugins: map[string][]ExternalPlugin{
					"foo/bar": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}},
				},
			},
		},
		{
			name: "ExternalPlugins cant't merge duplicated configs",
			in: Configuration{
				ExternalPlugins: map[string][]ExternalPlugin{
					"foo/bar": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}},
				},
			},
			supplementalConfigs: []Configuration{{ExternalPlugins: map[string][]ExternalPlugin{"foo/bar": {{Name: "refresh", Endpoint: "http://refresh", Events: []string{"issue_comment"}}}}}},
			errorExpected:       true,
		},
	}

	for _, tc := range testCases {
		for idx, supplementalConfig := range tc.supplementalConfigs {
			err := tc.in.mergeFrom(&supplementalConfig)
			if err != nil && !tc.errorExpected {
				t.Fatalf("failed to merge supplemental config %d: %v", idx, err)
			}
			if err == nil && tc.errorExpected {
				t.Fatal("expected error but got nothing")
			}
		}

		if diff := cmp.Diff(tc.expected, tc.in); !tc.errorExpected && diff != "" {
			t.Errorf("expected config differs from expected: %s", diff)
		}
	}
}
