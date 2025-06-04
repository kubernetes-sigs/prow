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

func TestNumericSecretReplacement(t *testing.T) {
	var testCases = []struct {
		name     string
		secrets  []string
		input    string
		expected string
	}{
		{
			name:     "integer secret",
			secrets:  []string{"12345"},
			input:    "password: 12345",
			expected: "password: 00000",
		},
		{
			name:     "negative integer secret",
			secrets:  []string{"-987"},
			input:    "value: -987",
			expected: "value: -000",
		},
		{
			name:     "float secret",
			secrets:  []string{"3.14159"},
			input:    "pi: 3.14159",
			expected: "pi: 0.00000",
		},
		{
			name:     "negative float secret",
			secrets:  []string{"-2.5"},
			input:    "temp: -2.5",
			expected: "temp: -0.0",
		},
		{
			name:     "scientific notation secret",
			secrets:  []string{"1.23e10"},
			input:    "big: 1.23e10",
			expected: "big: 0.00000",
		},
		{
			name:     "decimal secret",
			secrets:  []string{".5"},
			input:    "half: .5",
			expected: "half: .0",
		},
		{
			name:     "negative decimal",
			secrets:  []string{"-.75"},
			input:    "neg: -.75",
			expected: "neg: -.00",
		},
		{
			name:     "mixed numeric and non-numeric secrets",
			secrets:  []string{"123", "abc456", "7.89"},
			input:    "data: 123 abc456 7.89",
			expected: "data: 000 XXXXXX 0.00",
		},
		{
			name:     "alphanumeric secret",
			secrets:  []string{"abc123"},
			input:    "token: abc123",
			expected: "token: XXXXXX",
		},
		{
			name:     "non-numeric secret",
			secrets:  []string{"v1.2.3"},
			input:    "version: v1.2.3",
			expected: "version: XXXXXX",
		},
		{
			name:     "positive integer with explicit plus sign",
			secrets:  []string{"+492"},
			input:    "value: +492",
			expected: "value: +000",
		},
		{
			name:     "positive float with explicit plus sign",
			secrets:  []string{"+3.14"},
			input:    "pi: +3.14",
			expected: "pi: +0.00",
		},
		{
			name:     "positive scientific notation explicit with plus sign",
			secrets:  []string{"+1.23e10"},
			input:    "big: +1.23e10",
			expected: "big: +0.00000",
		},
		{
			name:     "special float values treated as numeric",
			secrets:  []string{"NaN", "Inf", "+Inf", "-Inf"},
			input:    "values: NaN Inf +Inf -Inf",
			expected: "values: 000 000 +000 -000",
		},
		{
			name:     "octal numbers",
			secrets:  []string{"0755", "0644", "0o755", "0O644"},
			input:    "perms: 0755 0644 0o755 0O644",
			expected: "perms: 0000 0000 0o000 0O000",
		},
		{
			name:     "hexadecimal numbers",
			secrets:  []string{"0x1234", "0XABcd", "0xff"},
			input:    "hex: 0x1234 0XABcd 0xff",
			expected: "hex: 0x0000 0X0000 0x00",
		},
		{
			name:     "binary numbers",
			secrets:  []string{"0b1010", "0B1111"},
			input:    "binary: 0b1010 0B1111",
			expected: "binary: 0b0000 0B0000",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			censorer := NewCensorer()
			censorer.Refresh(testCase.secrets...)
			input := []byte(testCase.input)
			censorer.Censor(&input)
			if diff := cmp.Diff(testCase.expected, string(input)); diff != "" {
				t.Errorf("%s: got incorrect text after censor: %v", testCase.name, diff)
			}
		})
	}
}
