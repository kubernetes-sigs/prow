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

package pjutil

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestHelp(t *testing.T) {
	tests := []struct {
		name          string
		userComment   string
		matchingTests int
		mayNeedHelp   bool
		shouldRespond bool
		note          string
		shouldPrune   bool
	}{
		{
			name:        "empty comment is neither a failed nor a successful test trigger",
			userComment: "",
			mayNeedHelp: false,
			shouldPrune: false,
		},
		{
			name:        "random comment is neither a failed nor a successful test trigger",
			userComment: "This is a comment about testing.",
			mayNeedHelp: false,
			shouldPrune: false,
		},
		{
			name:          "request for existing test is a successful test trigger",
			userComment:   "/test e2e",
			matchingTests: 1,
			mayNeedHelp:   true,
			shouldRespond: false,
			shouldPrune:   true,
		},
		{
			name:          "request for non-existent test is an unsuccessful test trigger",
			userComment:   "/test f2f",
			matchingTests: 0,
			mayNeedHelp:   true,
			shouldRespond: true,
			note:          TargetNotFoundNote,
			shouldPrune:   false,
		},
		{
			name:          "test all when tests exist is a successful test trigger",
			userComment:   "/test all",
			matchingTests: 1,
			mayNeedHelp:   true,
			shouldRespond: false,
			shouldPrune:   true,
		},
		{
			name:          "test all when no tests exist is an unsuccessful test trigger",
			userComment:   "/test all",
			matchingTests: 0,
			mayNeedHelp:   true,
			shouldRespond: true,
			note:          ThereAreNoTestAllJobsNote,
			shouldPrune:   false,
		},
		{
			name:          "retest is a successful test trigger",
			userComment:   "/retest",
			matchingTests: 1,
			mayNeedHelp:   false,
			shouldPrune:   true,
		},
		{
			name:          "retest is a successful test trigger, even when no tests exist",
			userComment:   "/retest",
			matchingTests: 0,
			mayNeedHelp:   false,
			shouldPrune:   true,
		},
		{
			name:          "empty /test is invalid",
			userComment:   "/test",
			mayNeedHelp:   true,
			shouldRespond: true,
			note:          TestWithoutTargetNote,
			shouldPrune:   false,
		},
		{
			name:          "retest with target is invalid",
			userComment:   "/retest e2e",
			mayNeedHelp:   true,
			shouldRespond: true,
			note:          RetestWithTargetNote,
			shouldPrune:   false,
		},
		{
			name:          "/test ? is a request for help, not a trigger",
			userComment:   "/test ?",
			mayNeedHelp:   true,
			shouldRespond: true,
			note:          "",
			shouldPrune:   false,
		},
	}

	required := sets.New("/test e2e", "/test e2e-serial", "/test unit")
	optional := sets.New("/test lint", "/test e2e-conformance-commodore64")
	all := required.Union(optional)
	helpBody := HelpMessage("", "", "", "", all, optional, required)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// None of the user comments should look like our help comment.
			if IsHelpComment(tc.userComment) {
				t.Errorf("Expected IsHelpComment(%q) to return false, got true", tc.userComment)
			}

			// MayNeedHelpComment is true if the comment uses `/test` or
			// `/retest` at all (whether valid or invalid).
			mayNeedHelp := MayNeedHelpComment(tc.userComment)
			if mayNeedHelp != tc.mayNeedHelp {
				t.Errorf("Expected MayNeedHelpComment(%q) to return %v, got %v", tc.userComment, tc.mayNeedHelp, mayNeedHelp)
			}

			// ShouldRespondWithHelp will check if userComment contains a
			// `/test` or `/retest` invocation that is invalid given the
			// number of matching tests.
			shouldRespond, note := ShouldRespondWithHelp(tc.userComment, tc.matchingTests)
			if shouldRespond != tc.shouldRespond {
				t.Errorf("Expected ShouldRespondWithHelp(%q, %d) to return %v, got %v", tc.userComment, tc.matchingTests, tc.shouldRespond, shouldRespond)
			}
			if note != tc.note {
				t.Errorf("Expected ShouldRespondWithHelp(%q) to return note %q, got %q", tc.userComment, tc.note, note)
			}

			// If we should respond with a help comment, then HelpMessage
			// should return the expected message, and IsHelpComment should
			// recognize it.
			if shouldRespond {
				expectHelpMessage := tc.note + helpBody
				helpMessage := HelpMessage("", "", "", note, all, optional, required)
				if helpMessage != expectHelpMessage {
					t.Errorf("Expected HelpMessage() to return %q, got %q", expectHelpMessage, helpMessage)
				}
				if !IsHelpComment(helpMessage) {
					t.Errorf("Expected IsHelpComment(%q) to return true, got false", helpMessage)
				}
			}

			// If we shouldn't respond with a help comment, then we possibly
			// should respond by deleted old help comments.
			if !shouldRespond {
				shouldPrune := ShouldRespondByPruningHelp(tc.userComment)
				if shouldPrune != tc.shouldPrune {
					t.Errorf("Expected ShouldRespondByPruningHelp(%q) to return %v, got %v", tc.userComment, tc.shouldPrune, shouldPrune)
				}
			}
		})
	}
}
