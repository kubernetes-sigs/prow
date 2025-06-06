/*
Copyright 2021 The Kubernetes Authors.

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

package secretutil

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestReloadingCensorer(t *testing.T) {
	text := func() []byte {
		return []byte("secret SECRET c2VjcmV0 sEcReT")
	}
	var testCases = []struct {
		name     string
		mutation func(c *ReloadingCensorer)
		expected []byte
	}{
		{
			name:     "no registered secrets",
			mutation: func(c *ReloadingCensorer) {},
			expected: text(),
		},
		{
			name: "registered strings",
			mutation: func(c *ReloadingCensorer) {
				c.Refresh("secret")
			},
			expected: []byte("XXXXXX SECRET XXXXXXXX sEcReT"),
		},
		{
			name: "registered strings with padding",
			mutation: func(c *ReloadingCensorer) {
				c.Refresh("		secret      ")
			},
			expected: []byte("XXXXXX SECRET XXXXXXXX sEcReT"),
		},
		{
			name: "registered strings only padding",
			mutation: func(c *ReloadingCensorer) {
				c.Refresh("		      ")
			},
			expected: text(),
		},
		{
			name: "registered multiple strings",
			mutation: func(c *ReloadingCensorer) {
				c.Refresh("secret", "SECRET", "sEcReT")
			},
			expected: []byte("XXXXXX XXXXXX XXXXXXXX XXXXXX"),
		},
		{
			name: "registered bytes",
			mutation: func(c *ReloadingCensorer) {
				c.RefreshBytes([]byte("secret"))
			},
			expected: []byte("XXXXXX SECRET XXXXXXXX sEcReT"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			censorer := NewCensorer()
			testCase.mutation(censorer)
			input := text()
			censorer.Censor(&input)
			if len(input) != len(text()) {
				t.Errorf("%s: length of input changed from %d to %d", testCase.name, len(text()), len(input))
			}
			if diff := cmp.Diff(string(testCase.expected), string(input)); diff != "" {
				t.Errorf("%s: got incorrect text after censor: %v", testCase.name, diff)
			}
		})
	}
}

func TestBooleanNotHidden(t *testing.T) {
	var testCases = []struct {
		name     string
		mutation func(c *ReloadingCensorer)
		text     func() []byte
		expected []byte
	}{
		{
			name: "true skipped",
			mutation: func(c *ReloadingCensorer) {
				c.Refresh("true", "True", "TRUE", " TRUE", "tRue")
			},
			text: func() []byte {
				return []byte("true True TRUE tRUE should stay")
			},
			expected: []byte("true True TRUE tRUE should stay"),
		},
		{
			name: "false skipped",
			mutation: func(c *ReloadingCensorer) {
				c.Refresh("false", "False", "FALSE", " FALSE", "fAlse")
			},
			text: func() []byte {
				return []byte("false False FALSE fALse should stay")
			},
			expected: []byte("false False FALSE fALse should stay"),
		},
		{
			name: "true bytes skipped",
			mutation: func(c *ReloadingCensorer) {
				c.RefreshBytes([]byte("true"), []byte("True"), []byte("TRUE"), []byte(" TRUE"), []byte("tRue"))
			},
			text: func() []byte {
				return []byte("true True TRUE tRUE should stay")
			},
			expected: []byte("true True TRUE tRUE should stay"),
		},
		{
			name: "false bytes skipped",
			mutation: func(c *ReloadingCensorer) {
				c.RefreshBytes([]byte("false"), []byte("False"), []byte("FALSE"), []byte(" FALSE"), []byte("fAlse"))
			},
			text: func() []byte {
				return []byte("false False FALSE fALse should stay")
			},
			expected: []byte("false False FALSE fALse should stay"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			censorer := NewCensorer()
			testCase.mutation(censorer)
			input := testCase.text()
			censorer.Censor(&input)
			if diff := cmp.Diff(string(testCase.expected), string(input)); diff != "" {
				t.Errorf("%s: got incorrect text after censor: %v", testCase.name, diff)
			}
		})
	}
}

func TestMinimumSecretLength(t *testing.T) {
	var testCases = []struct {
		name      string
		secrets   []string
		minLength int
		input     string
		expected  string
	}{
		{
			name:      "no minimum length - all secrets censored",
			secrets:   []string{"a", "bb", "ccc", "dddd"},
			minLength: 0,
			input:     "data: a bb ccc dddd",
			expected:  "dXtX: X XX XXX XXXX",
		},
		{
			name:      "minimum length 3 - short secrets not censored",
			secrets:   []string{"a", "bb", "ccc", "dddd"},
			minLength: 3,
			input:     "data: a bb ccc dddd",
			expected:  "data: a bb XXX XXXX",
		},
		{
			name:      "minimum length 5 - all secrets censored",
			secrets:   []string{"short", "verylongsecret"},
			minLength: 5,
			input:     "data: short verylongsecret",
			expected:  "data: XXXXX XXXXXXXXXXXXXX",
		},
		{
			name:      "minimum length 10 - no secrets censored",
			secrets:   []string{"short", "medium"},
			minLength: 10,
			input:     "data: short medium",
			expected:  "data: short medium",
		},
		{
			name:      "base64 encoding also respects minimum length",
			secrets:   []string{"abc"},
			minLength: 5,
			input:     "data: abc YWJj", // YWJj is base64 for "abc"
			expected:  "data: abc YWJj", // both too short to censor
		},
		{
			name:      "base64 encoding censored when long enough",
			secrets:   []string{"secret"},
			minLength: 5,
			input:     "data: secret c2VjcmV0", // c2VjcmV0 is base64 for "secret"
			expected:  "data: XXXXXX XXXXXXXX",
		},
		{
			name:      "mixed length secrets with minimum",
			secrets:   []string{"x", "password123", "key"},
			minLength: 4,
			input:     "config: x=1 password123=secret key=value",
			expected:  "config: x=1 XXXXXXXXXXX=secret key=value",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			censorer := NewCensorerWithMinLength(testCase.minLength)
			censorer.Refresh(testCase.secrets...)
			input := []byte(testCase.input)
			censorer.Censor(&input)
			if diff := cmp.Diff(testCase.expected, string(input)); diff != "" {
				t.Errorf("%s: got incorrect text after censor: %v", testCase.name, diff)
			}
		})
	}
}
