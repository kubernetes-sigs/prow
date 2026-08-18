/*
Copyright The Kubernetes Authors.

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

package io

import (
	"testing"

	"sigs.k8s.io/prow/pkg/testutil"
)

func TestGoogleCredentialsFileOption(t *testing.T) {
	testcases := []struct {
		name    string
		file    func(t *testing.T) string
		wantErr bool
	}{
		{
			name: "authorized_user credentials are accepted",
			file: testutil.WriteAuthorizedUserCredentialsFile,
		},
		{
			name: "unsupported credentials type is rejected",
			file: func(t *testing.T) string {
				return testutil.WriteCredentialsFile(t, `{"type": "user"}`)
			},
			wantErr: true,
		},
		{
			name: "invalid json is rejected",
			file: func(t *testing.T) string {
				return testutil.WriteCredentialsFile(t, `yaml: 123`)
			},
			wantErr: true,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GoogleCredentialsFileOption(tc.file(t), "https://www.googleapis.com/auth/cloud-platform")
			if (err != nil) != tc.wantErr {
				t.Fatalf("GoogleCredentialsFileOption() error = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}
