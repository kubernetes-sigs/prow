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

package flagutil

import (
	"testing"
)

func TestSSLEnablementOptions_Validate(t *testing.T) {
	testCases := []struct {
		name                string
		sslOptions          SSLOptions
		expectedErrorString string
	}{
		{
			name: "No_SSL_Options_Is_Valid",
		},
		{
			name: "SSL_Options_Properly_Enabled",
			sslOptions: SSLOptions{
				CertFile: "/test/path/cert.pem",
				KeyFile:  "/test/path/key.pem",
			},
		},
		{
			name: "Cert_File_Is_Missing",
			sslOptions: SSLOptions{
				KeyFile: "/test/path/key.pem",
			},
			expectedErrorString: `flag --server-key-file was set but corresponding required flag --server-cert-file was not set`,
		},
		{
			name: "Key_File_Is_Missing",
			sslOptions: SSLOptions{
				CertFile: "/test/path/cert.pem",
			},
			expectedErrorString: `flag --server-cert-file was set but corresponding required flag --server-key-file was not set`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualErrMsg string
			actualErr := tc.sslOptions.Validate(false)
			if actualErr != nil {
				actualErrMsg = actualErr.Error()
			}
			if actualErrMsg != tc.expectedErrorString {
				t.Errorf("actual error %v does not match expected error %q", actualErr, tc.expectedErrorString)
			}
		})
	}
}
