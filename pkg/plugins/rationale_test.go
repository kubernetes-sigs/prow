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
)

func TestParseRationale(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		command string
		want    []string
		wantErr RationaleErrorKind
	}{
		{
			name:    "When /retest has a same-line rationale, it should use it",
			body:    "/retest  CI network timeout was resolved  ",
			command: "/retest",
			want:    []string{"CI network timeout was resolved"},
		},
		{
			name:    "When /retest-required has a same-line rationale, it should use it",
			body:    "/retest-required registry outage resolved",
			command: "/retest-required",
			want:    []string{"registry outage resolved"},
		},
		{
			name:    "When /override has a rationale after --, it should use it",
			body:    "/override pull-e2e-aws -- infrastructure outage confirmed",
			command: "/override",
			want:    []string{"infrastructure outage confirmed"},
		},
		{
			name:    "When /override has no contexts before --, it should still use the rationale",
			body:    "/override -- infrastructure outage confirmed",
			command: "/override",
			want:    []string{"infrastructure outage confirmed"},
		},
		{
			name:    "When a quoted context contains --, it should split at the first unquoted --",
			body:    `/override "test -- unit" pull-e2e-aws -- infrastructure outage confirmed`,
			command: "/override",
			want:    []string{"infrastructure outage confirmed"},
		},
		{
			name:    "When the rationale is on the next line, it should use it",
			body:    "/retest\nCI network timeout was resolved",
			command: "/retest",
			want:    []string{"CI network timeout was resolved"},
		},
		{
			name:    "When the rationale spans lines with blank lines, it should join the text",
			body:    "/override pull-e2e-aws\n\nThe e2e infrastructure outage\n\nwas confirmed.",
			command: "/override",
			want:    []string{"The e2e infrastructure outage was confirmed."},
		},
		{
			name:    "When another command follows, it should stop the rationale there",
			body:    "/retest\nCI network timeout\n/override pull-e2e-aws -- outage confirmed",
			command: "/retest",
			want:    []string{"CI network timeout"},
		},
		{
			name:    "When both forms are present, it should prefer the same-line rationale",
			body:    "/retest same-line rationale\nnext-line text",
			command: "/retest",
			want:    []string{"same-line rationale"},
		},
		{
			name:    "When the rationale starts with an indented slash line, it should be rejected",
			body:    "/retest\n  /override job-a\nCI outage on AWS e2e resolved",
			command: "/retest",
			wantErr: RationaleErrorStartsWithSlash,
		},
		{
			// GitHub ends comment lines with \r\n.
			name:    "When a comment uses CRLF line endings, it should parse both forms",
			body:    "/retest\r\nCI network timeout\r\n/override job-a -- outage confirmed\r\n",
			command: "/override",
			want:    []string{"outage confirmed"},
		},
		{
			name:    "When the same-line /override text has no --, it should treat it as contexts",
			body:    "/override pull-e2e-aws unit\ninfrastructure outage confirmed",
			command: "/override",
			want:    []string{"infrastructure outage confirmed"},
		},
		{
			name:    "When a code fence follows, it should stop the rationale there",
			body:    "/test job\n```\n/retest\n```\nnot part of the rationale",
			command: "/retest",
			wantErr: RationaleErrorMissing,
		},
		{
			name:    "When only /retest-required is present, it should not match /retest",
			body:    "/retest-required registry outage resolved",
			command: "/retest",
			wantErr: RationaleErrorValidation,
		},
		{
			name:    "When a comment repeats a command, it should return each rationale",
			body:    "/retest first rationale\n/retest second rationale",
			command: "/retest",
			want:    []string{"first rationale", "second rationale"},
		},
		{
			name:    "When a repeated /override has no rationale, it should report it missing",
			body:    "/override job-a -- infrastructure outage confirmed\n/override job-b",
			command: "/override",
			wantErr: RationaleErrorMissing,
		},
		{
			name:    "When the next line starts with / but is not a known command, it should end the rationale",
			body:    "/retest\n/lgtm! looks good",
			command: "/retest",
			wantErr: RationaleErrorMissing,
		},
		{
			name:    "When the next line is a bare /, it should end the rationale",
			body:    "/retest\n/ foo",
			command: "/retest",
			wantErr: RationaleErrorMissing,
		},
		{
			name:    "When a same-line rationale starts with /, it should be rejected",
			body:    "/retest /lgtm! looks good",
			command: "/retest",
			wantErr: RationaleErrorStartsWithSlash,
		},
		{
			name:    "When an /override rationale after -- starts with /, it should be rejected",
			body:    "/override job-a -- /hold this for now",
			command: "/override",
			wantErr: RationaleErrorStartsWithSlash,
		},
		{
			name:    "When indented /retest text precedes a real command, it should not supply the real command's rationale",
			body:    "  /retest\nthis text belongs to the indented line\n/retest",
			command: "/retest",
			wantErr: RationaleErrorMissing,
		},
		{
			name:    "When there is no rationale, it should report it missing",
			body:    "/retest\n\n",
			command: "/retest",
			wantErr: RationaleErrorMissing,
		},
		{
			name:    "When /override has an empty rationale after --, it should report it missing",
			body:    "/override pull-e2e-aws --",
			command: "/override",
			wantErr: RationaleErrorMissing,
		},
		{
			name:    "When the command is not in the comment, it should fail closed",
			body:    "a sufficiently long rationale is provided",
			command: "/retest",
			wantErr: RationaleErrorValidation,
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
				t.Errorf("rationales mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStripSameLineRationale(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		command string
		want    string
	}{
		{
			name:    "When /retest has a same-line rationale, it should leave the bare command",
			body:    "/retest CI network timeout\n/lgtm",
			command: "/retest",
			want:    "/retest\n/lgtm",
		},
		{
			name:    "When /retest-required has a same-line rationale, it should leave the bare command",
			body:    "/retest-required registry outage\r\n",
			command: "/retest-required",
			want:    "/retest-required\r\n",
		},
		{
			name:    "When /override has a rationale after --, it should keep the contexts",
			body:    `/Override "test -- unit" job-a -- outage confirmed`,
			command: "/override",
			want:    `/Override "test -- unit" job-a`,
		},
		{
			name:    "When /override has no --, it should leave the line unchanged",
			body:    "/override  broken-test \r\n",
			command: "/override",
			want:    "/override  broken-test \r\n",
		},
		{
			name:    "When the rationale is on the next line, it should leave the comment unchanged",
			body:    "/retest\nCI network timeout",
			command: "/retest",
			want:    "/retest\nCI network timeout",
		},
		{
			name:    "When stripping /retest, it should not touch /retest-required or /override-sticky",
			body:    "/retest-required registry outage\n/override-sticky job-a -- outage",
			command: "/retest",
			want:    "/retest-required registry outage\n/override-sticky job-a -- outage",
		},
		{
			name:    "When stripping /override, it should not touch /override-sticky or /override-cancel",
			body:    "/override-sticky job-a -- outage\n/override-cancel job-a -- outage",
			command: "/override",
			want:    "/override-sticky job-a -- outage\n/override-cancel job-a -- outage",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := StripSameLineRationale(test.body, test.command); got != test.want {
				t.Errorf("stripped body: got %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateRationale(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		command   string
		minLength int
		wantErr   RationaleErrorKind
		wantCount int
	}{
		{
			name:    "When a rationale meets the default minimum, it should be accepted",
			body:    "/retest CI outage on AWS e2e resolved",
			command: "/retest",
		},
		{
			name:    "When a rationale is missing, it should be rejected",
			body:    "/retest",
			command: "/retest",
			wantErr: RationaleErrorMissing,
		},
		{
			name:      "When a rationale is too short, it should report its non-whitespace code-point count",
			body:      "/retest flaky",
			command:   "/retest",
			wantErr:   RationaleErrorTooShort,
			wantCount: 5,
		},
		{
			name:    "When a rationale has exactly 20 non-whitespace code points, it should be accepted",
			body:    "/override job-a -- abcde fghij klmno pqrst",
			command: "/override",
		},
		{
			name:    "When a Unicode rationale has 20 code points, it should be accepted",
			body:    "/retest\n" + strings.Repeat("界", 20),
			command: "/retest",
		},
		{
			name:      "When a repeated /override has a short rationale, it should be rejected",
			body:      "/override job-a -- CI outage on AWS e2e resolved\n/override job-b -- retry",
			command:   "/override",
			wantErr:   RationaleErrorTooShort,
			wantCount: 5,
		},
		{
			name:      "When a command has its own minimum, it should use it",
			body:      "/retest-required CI outage on AWS e2e resolved",
			command:   "/retest-required",
			minLength: 30,
			wantErr:   RationaleErrorTooShort,
			wantCount: 24,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRationale(test.body, test.command, RationaleSettings{MinLength: test.minLength})
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validate rationale: %v", err)
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

func TestRationaleErrorComment(t *testing.T) {
	tests := []struct {
		name    string
		command string
		err     error
		want    []string
		notWant []string
	}{
		{
			name:    "When a rationale is missing, it should explain both forms with an example",
			command: "/retest",
			err:     &RationaleValidationError{Kind: RationaleErrorMissing},
			want: []string{
				"Rationale required for `/retest`",
				"No CI job was queued.",
				"at least 20 non-whitespace Unicode code points after the command on the same line, or on the next line",
				">/retest Retrying after",
			},
			notWant: []string{"` -- `"},
		},
		{
			name:    "When /override is missing a rationale, it should explain the -- separator",
			command: "/override",
			err:     &RationaleValidationError{Kind: RationaleErrorMissing},
			want:    []string{"Separate the contexts from the rationale with ` -- `.", ">/override pull-e2e-aws -- Overriding after"},
		},
		{
			name:    "When a rationale is too short, it should report the count and minimum",
			command: "/retest-required",
			err:     &RationaleValidationError{Kind: RationaleErrorTooShort, Count: 6, MinLength: 20},
			want:    []string{"too short (got 6 non-whitespace Unicode code points; need at least 20)", ">/retest-required Retrying"},
		},
		{
			name:    "When a rationale starts with /, it should ask for a plain-text explanation",
			command: "/retest",
			err:     &RationaleValidationError{Kind: RationaleErrorStartsWithSlash},
			want:    []string{"Rationale for `/retest` must not start with `/`.", "No CI job was queued.", ">/retest Retrying"},
		},
		{
			name:    "When validation fails unexpectedly, it should report that the command was blocked",
			command: "/retest",
			err:     errors.New("boom"),
			want:    []string{"internal error", "blocked as a precaution", "No CI job was queued."},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RationaleErrorComment(test.command, "No CI job was queued.", RationaleSettings{}, test.err)
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("comment missing %q:\n%s", want, got)
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("comment unexpectedly contains %q:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestRationaleErrorCommentDoesNotContainCommands(t *testing.T) {
	errs := []error{
		&RationaleValidationError{Kind: RationaleErrorMissing},
		&RationaleValidationError{Kind: RationaleErrorTooShort, Count: 6, MinLength: 20},
		&RationaleValidationError{Kind: RationaleErrorStartsWithSlash},
		errors.New("boom"),
	}
	for command := range rationaleCommands {
		for _, err := range errs {
			comment := RationaleErrorComment("/"+command, "No action was taken.", RationaleSettings{}, err)
			for line := range strings.SplitSeq(comment, "\n") {
				if strings.HasPrefix(line, "/") {
					t.Errorf("/%s comment for %v contains a line starting with / %q; plugins may act on the bot's own comment:\n%s", command, err, line, comment)
				}
			}
		}
	}
}
