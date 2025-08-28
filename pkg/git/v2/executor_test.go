/*
Copyright 2019 The Kubernetes Authors.

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

package git

import (
	"bytes"
	"errors"
	"github.com/sirupsen/logrus"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

func TestCensorURLCredentials(t *testing.T) {
	var testCases = []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple URL with credentials",
			input:    "https://username:password@example.com/path",
			expected: "https://username:xxxxx@example.com/path",
		},
		{
			name:     "URL with no credentials",
			input:    "https://example.com/path",
			expected: "https://example.com/path",
		},
		{
			name:     "git command with URL in arguments",
			input:    "git remote set-url origin https://username:token@github.com/org/repo",
			expected: "git remote set-url origin https://username:xxxxx@github.com/org/repo",
		},
		{
			name:     "push command with URL in arguments",
			input:    "git push https://username:token@github.com/org/repo branch",
			expected: "git push https://username:xxxxx@github.com/org/repo branch",
		},
		{
			name:     "non-URL string",
			input:    "just some random text",
			expected: "just some random text",
		},
		{
			name:     "URL with port and credentials",
			input:    "https://username:password@example.com:8080/path",
			expected: "https://username:xxxxx@example.com:8080/path",
		},
		{
			name:     "URL with query parameters and credentials",
			input:    "https://username:password@example.com/path?foo=bar&baz=qux",
			expected: "https://username:xxxxx@example.com/path?foo=bar&baz=qux",
		},
		{
			name:     "URL with fragment and credentials",
			input:    "https://username:password@example.com/path#section",
			expected: "https://username:xxxxx@example.com/path#section",
		},
		{
			name:     "URL with port in regex fallback",
			input:    "git clone https://user:token@gitlab.com:443/group/project.git",
			expected: "git clone https://user:xxxxx@gitlab.com:443/group/project.git",
		},
		{
			name:     "complex URL with all components",
			input:    "https://user:pass@host.com:9000/path/to/repo?ref=main#readme",
			expected: "https://user:xxxxx@host.com:9000/path/to/repo?ref=main#readme",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := censorURLCredentials(tc.input)
			if actual != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestCensoringExecutor_Run(t *testing.T) {
	var testCases = []struct {
		name        string
		dir, git    string
		args        []string
		censor      Censor
		executeOut  []byte
		executeErr  error
		expectedOut []byte
		expectedErr bool
	}{
		{
			name: "happy path with nothing to censor returns all output",
			dir:  "/somewhere/repo",
			git:  "/usr/bin/git",
			args: []string{"status"},
			censor: func(content []byte) []byte {
				return content
			},
			executeOut:  []byte("hi"),
			executeErr:  nil,
			expectedOut: []byte("hi"),
			expectedErr: false,
		},
		{
			name: "command with URL credentials in arguments censors them",
			dir:  "/somewhere/repo",
			git:  "/usr/bin/git",
			args: []string{"remote", "set-url", "origin", "https://username:token@github.com/org/repo"},
			censor: func(content []byte) []byte {
				return content
			},
			executeOut:  []byte("ok"),
			executeErr:  nil,
			expectedOut: []byte("ok"),
			expectedErr: false,
		},
		{
			name: "happy path with nonstandard git binary",
			dir:  "/somewhere/repo",
			git:  "/usr/local/bin/git",
			args: []string{"status"},
			censor: func(content []byte) []byte {
				return content
			},
			executeOut:  []byte("hi"),
			executeErr:  nil,
			expectedOut: []byte("hi"),
			expectedErr: false,
		},
		{
			name: "happy path with something to censor returns altered output",
			dir:  "/somewhere/repo",
			git:  "/usr/bin/git",
			args: []string{"status"},
			censor: func(content []byte) []byte {
				return bytes.ReplaceAll(content, []byte("secret"), []byte("CENSORED"))
			},
			executeOut:  []byte("hi secret"),
			executeErr:  nil,
			expectedOut: []byte("hi CENSORED"),
			expectedErr: false,
		},
		{
			name: "error is propagated",
			dir:  "/somewhere/repo",
			git:  "/usr/bin/git",
			args: []string{"status"},
			censor: func(content []byte) []byte {
				return bytes.ReplaceAll(content, []byte("secret"), []byte("CENSORED"))
			},
			executeOut:  []byte("hi secret"),
			executeErr:  errors.New("oops"),
			expectedOut: []byte("hi CENSORED"),
			expectedErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// Test credential censoring directly on arguments
			if testCase.name == "command with URL credentials in arguments censors them" {
				for _, arg := range testCase.args {
					censored := censorURLCredentials(arg)
					if strings.Contains(censored, "token") {
						t.Errorf("credentials not censored in arg: %s", censored)
					}
				}
			}

			e := censoringExecutor{
				logger: logrus.WithField("name", testCase.name),
				dir:    testCase.dir,
				git:    testCase.git,
				censor: testCase.censor,
				execute: func(dir, command string, args ...string) (bytes []byte, e error) {
					if dir != testCase.dir {
						t.Errorf("%s: got incorrect dir: %v", testCase.name, diff.StringDiff(dir, testCase.dir))
					}
					if command != testCase.git {
						t.Errorf("%s: got incorrect command: %v", testCase.name, diff.StringDiff(command, testCase.git))
					}
					if !reflect.DeepEqual(args, testCase.args) {
						t.Errorf("%s: got incorrect args: %v", testCase.name, diff.ObjectReflectDiff(args, testCase.args))
					}
					return testCase.executeOut, testCase.executeErr
				},
			}
			actual, actualErr := e.Run(testCase.args...)

			if testCase.expectedErr && actualErr == nil {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if !testCase.expectedErr && actualErr != nil {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, actualErr)
			}
			if !reflect.DeepEqual(actual, testCase.expectedOut) {
				t.Errorf("%s: got incorrect command output: %v", testCase.name, diff.StringDiff(string(actual), string(testCase.expectedOut)))
			}
		})
	}
}
