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

package plugins

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"sigs.k8s.io/prow/pkg/github"
)

type fakeRationaleTeamClient struct {
	members map[string][]github.TeamMember
	err     error
	calls   []string
}

func (c *fakeRationaleTeamClient) ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error) {
	c.calls = append(c.calls, org+"/"+teamSlug)
	if c.err != nil {
		return nil, c.err
	}
	return c.members[org+"/"+teamSlug], nil
}

func TestParseRationale(t *testing.T) {
	body := `/retest
reason: first reason belongs to retest
/override "ci/prow/e2e-aws"

Emergency: TRUE
Reason: outage
/retest
Reason: this repeated invocation is ignored`

	tests := []struct {
		name    string
		command string
		want    Rationale
	}{
		{
			name:    "When a comment has multiple protected commands, it should associate each reason with its command",
			command: "/retest",
			want:    Rationale{Reason: "first reason belongs to retest"},
		},
		{
			name:    "When emergency metadata precedes the reason, it should parse both within the command block",
			command: "/override",
			want:    Rationale{Reason: "outage", Emergency: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseRationale(body, test.command)
			if err != nil {
				t.Fatalf("parse rationale: %v", err)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("parsed rationale mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseRationaleUsesNativeCommandLines(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		command string
		want    Rationale
		wantErr RationaleErrorKind
	}{
		{
			name:    "When indented /retest text precedes a real command, it should not supply the real command's reason",
			body:    "  /retest\nReason: this reason belongs to the indented text\n/retest",
			command: "/retest",
			wantErr: RationaleErrorMissingReason,
		},
		{
			name:    "When indented /override text precedes a real command, it should not supply the real command's reason",
			body:    "  /override fake-context\nReason: this reason belongs to the indented text\n/override real-context",
			command: "/override",
			wantErr: RationaleErrorMissingReason,
		},
		{
			name:    "When an indented slash line follows /retest, it should not end the rationale block",
			body:    "/retest\n  /override job-a\nReason: CI outage on AWS e2e resolved",
			command: "/retest",
			want:    Rationale{Reason: "CI outage on AWS e2e resolved"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseRationale(test.body, test.command)
			if test.wantErr != "" {
				var rationaleErr *RationaleValidationError
				if !errors.As(err, &rationaleErr) || rationaleErr.Kind != test.wantErr {
					t.Fatalf("parse error: got %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse rationale: %v", err)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("parsed rationale mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateRationale(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		minLength int
		want      Rationale
		wantErr   RationaleErrorKind
		wantCount int
	}{
		{
			name:      "When a reason meets the default minimum, it should be accepted after trimming",
			body:      "/retest\nReason:   CI outage on AWS e2e resolved   ",
			want:      Rationale{Reason: "CI outage on AWS e2e resolved"},
			minLength: 20,
		},
		{
			name:      "When a reason is missing, it should be rejected",
			body:      "/retest",
			wantErr:   RationaleErrorMissingReason,
			wantCount: 0,
		},
		{
			name:      "When a reason is empty, it should be rejected",
			body:      "/retest\nReason:",
			wantErr:   RationaleErrorMissingReason,
			wantCount: 0,
		},
		{
			name:      "When a reason is too short, it should report its non-whitespace code-point count",
			body:      "/retest\nReason: retry",
			wantErr:   RationaleErrorTooShort,
			wantCount: 5,
		},
		{
			name:      "When a reason has exactly 20 non-whitespace code points, it should be accepted",
			body:      "/retest\nReason: CI outage now resolved!",
			want:      Rationale{Reason: "CI outage now resolved!"},
			minLength: 20,
		},
		{
			name:      "When a Unicode reason has 20 code points, it should be accepted",
			body:      "/retest\nReason: " + strings.Repeat("修", 20),
			want:      Rationale{Reason: strings.Repeat("修", 20)},
			minLength: 20,
		},
		{
			name:      "When emergency is false, it should still enforce the minimum",
			body:      "/retest\nEmergency: false\nReason: outage",
			wantErr:   RationaleErrorTooShort,
			wantCount: 6,
		},
		{
			name:    "When emergency has an unsupported value, it should fail closed",
			body:    "/retest\nEmergency: maybe\nReason: a sufficiently long rationale is provided",
			wantErr: RationaleErrorValidation,
		},
		{
			name:    "When a command has multiple Reason fields, it should fail closed",
			body:    "/retest\nReason: first rationale is long enough\nReason: second rationale is also long enough",
			wantErr: RationaleErrorValidation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := RationaleSettings{MinLength: test.minLength}
			got, err := ValidateRationale(
				test.body,
				RationaleContext{Command: "/retest", Actor: "alice", Org: "org", Repo: "repo", PullRequest: 7},
				settings,
				nil,
				logrus.NewEntry(logrus.New()),
			)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validate rationale: %v", err)
				}
				if diff := cmp.Diff(test.want, got); diff != "" {
					t.Errorf("rationale mismatch (-want +got):\n%s", diff)
				}
				return
			}

			var rationaleErr *RationaleValidationError
			if !errors.As(err, &rationaleErr) {
				t.Fatalf("validation error: got %v, want RationaleValidationError", err)
			}
			if rationaleErr.Kind != test.wantErr {
				t.Errorf("error kind: got %q, want %q", rationaleErr.Kind, test.wantErr)
			}
			if rationaleErr.Count != test.wantCount {
				t.Errorf("non-whitespace code-point count: got %d, want %d", rationaleErr.Count, test.wantCount)
			}
		})
	}
}

func TestValidateRationaleEmergencyBypass(t *testing.T) {
	settings := RationaleSettings{
		MinLength: 20,
		EmergencyBypass: &EmergencyBypassConfig{
			AllowedGitHubTeams: []string{"ops/ci-admins"},
		},
	}
	context := RationaleContext{Command: "/retest", Actor: "alice", Org: "org", Repo: "repo", PullRequest: 7}

	t.Run("When an authorized team member gives a short incident reason, it should bypass only the minimum length", func(t *testing.T) {
		client := &fakeRationaleTeamClient{members: map[string][]github.TeamMember{
			"ops/ci-admins": {{Login: "alice"}},
		}}
		logger, hook := logrustest.NewNullLogger()

		got, err := ValidateRationale("/retest\nEmergency: true\nReason: outage", context, settings, client, logrus.NewEntry(logger))
		if err != nil {
			t.Fatalf("validate emergency rationale: %v", err)
		}
		if diff := cmp.Diff(Rationale{Reason: "outage", Emergency: true}, got); diff != "" {
			t.Errorf("rationale mismatch (-want +got):\n%s", diff)
		}
		if len(client.calls) != 1 || client.calls[0] != "ops/ci-admins" {
			t.Errorf("team lookups: got %v, want [ops/ci-admins]", client.calls)
		}
		if hook.LastEntry() == nil {
			t.Fatal("expected an emergency bypass audit log")
		}
		fields := hook.LastEntry().Data
		for key, want := range map[string]any{
			"actor":        "alice",
			"repo":         "org/repo",
			"pull_request": 7,
			"command":      "/retest",
			"reason":       "outage",
			"team_matched": "ops/ci-admins",
		} {
			if got := fields[key]; got != want {
				t.Errorf("audit field %q: got %v, want %v", key, got, want)
			}
		}
	})

	t.Run("When an unauthorized user requests emergency bypass, it should be denied", func(t *testing.T) {
		client := &fakeRationaleTeamClient{members: map[string][]github.TeamMember{
			"ops/ci-admins": {{Login: "someone-else"}},
		}}
		_, err := ValidateRationale("/retest\nEmergency: true\nReason: outage", context, settings, client, logrus.NewEntry(logrus.New()))
		var rationaleErr *RationaleValidationError
		if !errors.As(err, &rationaleErr) || rationaleErr.Kind != RationaleErrorEmergencyBypassDenied {
			t.Fatalf("validation error: got %v, want emergency bypass denied", err)
		}
	})

	t.Run("When emergency team lookup fails, it should fail closed", func(t *testing.T) {
		client := &fakeRationaleTeamClient{err: errors.New("team API unavailable")}
		_, err := ValidateRationale("/retest\nEmergency: true\nReason: outage", context, settings, client, logrus.NewEntry(logrus.New()))
		var rationaleErr *RationaleValidationError
		if !errors.As(err, &rationaleErr) || rationaleErr.Kind != RationaleErrorValidation {
			t.Fatalf("validation error: got %v, want fail-closed validation error", err)
		}
		if !strings.Contains(err.Error(), "team API unavailable") {
			t.Errorf("validation error %q does not preserve lookup failure", err)
		}
	})

	t.Run("When emergency is requested without a reason, it should still be rejected", func(t *testing.T) {
		client := &fakeRationaleTeamClient{}
		_, err := ValidateRationale("/retest\nEmergency: true", context, settings, client, logrus.NewEntry(logrus.New()))
		var rationaleErr *RationaleValidationError
		if !errors.As(err, &rationaleErr) || rationaleErr.Kind != RationaleErrorMissingReason {
			t.Fatalf("validation error: got %v, want missing reason", err)
		}
		if len(client.calls) != 0 {
			t.Errorf("team lookups: got %v, want none without a reason", client.calls)
		}
	})

	t.Run("When no emergency team is configured, it should deny bypass", func(t *testing.T) {
		_, err := ValidateRationale("/retest\nEmergency: true\nReason: outage", context, RationaleSettings{MinLength: 20}, &fakeRationaleTeamClient{}, logrus.NewEntry(logrus.New()))
		var rationaleErr *RationaleValidationError
		if !errors.As(err, &rationaleErr) || rationaleErr.Kind != RationaleErrorEmergencyBypassDenied {
			t.Fatalf("validation error: got %v, want emergency bypass denied", err)
		}
	})
}
