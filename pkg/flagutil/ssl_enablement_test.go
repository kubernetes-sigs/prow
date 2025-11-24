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
		name                 string
		sslEnablementOptions SSLEnablementOptions
		expectedErrorString  string
	}{
		{
			name: "No_SSL_Options_Is_Valid",
		},
		{
			name: "SSL_Options_Properly_Enabled",
			sslEnablementOptions: SSLEnablementOptions{
				EnableSSL:      true,
				ServerCertFile: "/test/path/cert.pem",
				ServerKeyFile:  "/test/path/key.pem",
			},
		},
		{
			name: "SSL_Options_Explicitly_Disabled_Is_Ok",
			sslEnablementOptions: SSLEnablementOptions{
				EnableSSL: false,
			},
		},
		{
			name: "Cert_File_And_Key_File_Are_Missing",
			sslEnablementOptions: SSLEnablementOptions{
				EnableSSL: true,
			},
			expectedErrorString: `flag --enable-ssl was set to true but required flag --cert-file was not set`,
		},
		{
			name: "Cert_File_Is_Missing",
			sslEnablementOptions: SSLEnablementOptions{
				EnableSSL:     true,
				ServerKeyFile: "/test/path/key.pem",
			},
			expectedErrorString: `flag --enable-ssl was set to true but required flag --cert-file was not set`,
		},
		{
			name: "Key_File_Is_Missing",
			sslEnablementOptions: SSLEnablementOptions{
				EnableSSL:      true,
				ServerCertFile: "/test/path/cert.pem",
			},
			expectedErrorString: `flag --enable-ssl was set to true but required flag --key-file was not set`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualErrMsg string
			actualErr := tc.sslEnablementOptions.Validate(false)
			if actualErr != nil {
				actualErrMsg = actualErr.Error()
			}
			if actualErrMsg != tc.expectedErrorString {
				t.Errorf("actual error %v does not match expected error %q", actualErr, tc.expectedErrorString)
			}
		})
	}
}
