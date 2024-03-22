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

package bumper

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/test-infra/prow/config/secret"
)

func TestValidateOptions(t *testing.T) {
	emptyStr := ""
	trueVar := true
	cases := []struct {
		name                string
		githubToken         *string
		githubOrg           *string
		githubRepo          *string
		gerrit              *bool
		gerritAuthor        *string
		gerritPRIdentifier  *string
		gerritHostRepo      *string
		gerritCookieFile    *string
		remoteName          *string
		skipPullRequest     *bool
		signoff             *bool
		err                 bool
		upstreamBaseChanged bool
	}{
		{
			name: "Everything correct",
			err:  false,
		},
		{
			name:        "GitHubToken must not be empty when SkipPullRequest is false",
			githubToken: &emptyStr,
			err:         true,
		},
		{
			name:       "remoteName must not be empty when SkipPullRequest is false",
			remoteName: &emptyStr,
			err:        true,
		},
		{
			name:      "GitHubOrg cannot be empty when SkipPullRequest is false",
			githubOrg: &emptyStr,
			err:       true,
		},
		{
			name:       "GitHubRepo cannot be empty when SkipPullRequest is false",
			githubRepo: &emptyStr,
			err:        true,
		},
		{
			name:         "gerritAuthor cannot be empty when SkipPullRequest is false and gerrit is true",
			gerrit:       &trueVar,
			gerritAuthor: &emptyStr,
			err:          true,
		},
		{
			name:           "gerritHostRepo cannot be empty when SkipPullRequest is false and gerrit is true",
			gerrit:         &trueVar,
			gerritHostRepo: &emptyStr,
			err:            true,
		},
		{
			name:             "gerritCookieFile cannot be empty when SkipPullRequest is false and gerrit is true",
			gerrit:           &trueVar,
			gerritCookieFile: &emptyStr,
			err:              true,
		},
		{
			name:               "gerritCommitId cannot be empty when SkipPullRequest is false and gerrit is true",
			gerrit:             &trueVar,
			gerritPRIdentifier: &emptyStr,
			err:                true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gerrit := &Gerrit{
				Author:               "whatever-author",
				CookieFile:           "whatever cookie file",
				AutobumpPRIdentifier: "whatever-commit-id",
				HostRepo:             "whatever-host-repo",
			}
			defaultOption := &Options{
				GitHubOrg:       "whatever-org",
				GitHubRepo:      "whatever-repo",
				GitHubLogin:     "whatever-login",
				GitHubToken:     "whatever-token",
				GitName:         "whatever-name",
				GitEmail:        "whatever-email",
				Gerrit:          nil,
				RemoteName:      "whatever-name",
				SkipPullRequest: false,
				Signoff:         false,
			}

			if tc.skipPullRequest != nil {
				defaultOption.SkipPullRequest = *tc.skipPullRequest
			}
			if tc.signoff != nil {
				defaultOption.Signoff = *tc.signoff
			}
			if tc.githubToken != nil {
				defaultOption.GitHubToken = *tc.githubToken
			}
			if tc.remoteName != nil {
				defaultOption.RemoteName = *tc.remoteName
			}
			if tc.githubOrg != nil {
				defaultOption.GitHubOrg = *tc.githubOrg
			}
			if tc.githubRepo != nil {
				defaultOption.GitHubRepo = *tc.githubRepo
			}
			if tc.gerrit != nil {
				defaultOption.Gerrit = gerrit
			}
			if tc.gerritAuthor != nil {
				defaultOption.Gerrit.Author = *tc.gerritAuthor
			}
			if tc.gerritPRIdentifier != nil {
				defaultOption.Gerrit.AutobumpPRIdentifier = *tc.gerritPRIdentifier
			}
			if tc.gerritCookieFile != nil {
				defaultOption.Gerrit.CookieFile = *tc.gerritCookieFile
			}
			if tc.gerritHostRepo != nil {
				defaultOption.Gerrit.HostRepo = *tc.gerritHostRepo
			}

			err := validateOptions(defaultOption)
			t.Logf("err is: %v", err)
			if err == nil && tc.err {
				t.Errorf("Expected to get an error for %#v but got nil", defaultOption)
			}
			if err != nil && !tc.err {
				t.Errorf("Expected to not get an error for %#v but got %v", defaultOption, err)
			}
		})
	}
}

type fakeWriter struct {
	results []byte
}

func (w *fakeWriter) Write(content []byte) (n int, err error) {
	w.results = append(w.results, content...)
	return len(content), nil
}

func writeToFile(t *testing.T, path, content string) {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Errorf("write file %s dir with error '%v'", path, err)
	}
}

func TestCallWithWriter(t *testing.T) {
	dir := t.TempDir()

	file1 := filepath.Join(dir, "secret1")
	file2 := filepath.Join(dir, "secret2")

	writeToFile(t, file1, "abc")
	writeToFile(t, file2, "xyz")

	if err := secret.Add(file1, file2); err != nil {
		t.Errorf("failed to start secrets agent; %v", err)
	}

	var fakeOut fakeWriter
	var fakeErr fakeWriter

	stdout := HideSecretsWriter{Delegate: &fakeOut, Censor: secret.Censor}
	stderr := HideSecretsWriter{Delegate: &fakeErr, Censor: secret.Censor}

	testCases := []struct {
		description string
		command     string
		args        []string
		expectedOut string
		expectedErr string
	}{
		{
			description: "no secret in stdout are working well",
			command:     "echo",
			args:        []string{"-n", "aaa: 123"},
			expectedOut: "aaa: 123",
		},
		{
			description: "secret in stdout are censored",
			command:     "echo",
			args:        []string{"-n", "abc: 123"},
			expectedOut: "XXX: 123",
		},
		{
			description: "secret in stderr are censored",
			command:     "ls",
			args:        []string{"/tmp/file-not-exist/abc/xyz/file-not-exist"},
			expectedErr: "/tmp/file-not-exist/XXX/XXX/file-not-exist",
		},
		{
			description: "no secret in stderr are working well",
			command:     "ls",
			args:        []string{"/tmp/file-not-exist/aaa/file-not-exist"},
			expectedErr: "/tmp/file-not-exist/aaa/file-not-exist",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeOut.results = []byte{}
			fakeErr.results = []byte{}
			_ = Call(stdout, stderr, tc.command, tc.args)
			if full, want := string(fakeOut.results), tc.expectedOut; !strings.Contains(full, want) {
				t.Errorf("stdout does not contain %q, got %q", full, want)
			}
			if full, want := string(fakeErr.results), tc.expectedErr; !strings.Contains(full, want) {
				t.Errorf("stderr does not contain %q, got %q", full, want)
			}
		})
	}
}

func TestGetAssignment(t *testing.T) {
	cases := []struct {
		description          string
		assignTo             string
		oncallURL            string
		oncallGroup          string
		oncallServerResponse string
		expectResKeyword     string
	}{
		{
			description:      "AssignTo takes precedence over oncall setings",
			assignTo:         "some-user",
			expectResKeyword: "/cc @some-user",
		},
		{
			description:      "No assign to",
			assignTo:         "",
			expectResKeyword: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			res := getAssignment(tc.assignTo)
			if !strings.Contains(res, tc.expectResKeyword) {
				t.Errorf("Expect the result %q contains keyword %q but it does not", res, tc.expectResKeyword)
			}
		})
	}
}

func TestCDToRootDir(t *testing.T) {
	tmpDir := t.TempDir()
	for dir, fps := range map[string][]string{
		"testdata/dir": {"extra-file"},
	} {
		if err := os.MkdirAll(path.Join(tmpDir, dir), 0755); err != nil {
			t.Fatalf("Faile creating dir %q: %v", dir, err)
		}
		for _, f := range fps {
			if _, err := os.Create(path.Join(tmpDir, dir, f)); err != nil {
				t.Fatalf("Faile creating file %q: %v", f, err)
			}
		}
	}

	envName := "BUILD_WORKSPACE_DIRECTORY"

	cases := []struct {
		description       string
		buildWorkspaceDir string
		expectedResDir    string
		expectError       bool
	}{
		// This test case does not work when running with Bazel.
		{
			description:       "BUILD_WORKSPACE_DIRECTORY is a valid directory",
			buildWorkspaceDir: path.Join(tmpDir, "testdata/dir"),
			expectedResDir:    "testdata/dir",
			expectError:       false,
		},
		{
			description:       "BUILD_WORKSPACE_DIRECTORY is an invalid directory",
			buildWorkspaceDir: "whatever-dir",
			expectedResDir:    "",
			expectError:       true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			curtDir, _ := os.Getwd()
			curtBuildWorkspaceDir := os.Getenv(envName)
			defer os.Chdir(curtDir)
			defer os.Setenv(envName, curtBuildWorkspaceDir)

			os.Setenv(envName, tc.buildWorkspaceDir)
			err := cdToRootDir()
			if tc.expectError && err == nil {
				t.Errorf("Expected to get an error but the result is nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected to not get an error but got one: %v", err)
			}

			if !tc.expectError {
				afterDir, _ := os.Getwd()
				if !strings.HasSuffix(afterDir, tc.expectedResDir) {
					t.Errorf("Expected to switch to %q but was switched to: %q", tc.expectedResDir, afterDir)
				}
			}
		})
	}
}
