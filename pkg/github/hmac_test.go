/*
Copyright 2016 The Kubernetes Authors.

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

package github

import (
	"testing"
)

var tokens = `
'*':
  - value: abc
    created_at: 2020-10-02T15:00:00Z
  - value: key
    created_at: 2018-10-02T15:00:00Z
'org1':
  - value: abc1
    created_at: 2020-10-02T15:00:00Z
  - value: key1
    created_at: 2018-10-02T15:00:00Z
'org2/repo':
  - value: abc2
    created_at: 2020-10-02T15:00:00Z
  - value: key2
    created_at: 2018-10-02T15:00:00Z
`

var defaultTokenGenerator = func() []byte {
	return []byte(tokens)
}

// echo -n 'BODY' | openssl dgst -sha256 -hmac KEY
func TestValidatePayload(t *testing.T) {
	var testcases = []struct {
		name           string
		payload        string
		sig            string
		tokenGenerator func() []byte
		valid          bool
	}{
		{
			"empty payload with a correct signature can pass the check",
			"{}",
			"sha256=19092633e5aa9a849dfcc9d2df4e76db2df1fcba7f38915f2c7833bd8a510f2f",
			defaultTokenGenerator,
			true,
		},
		{
			"empty payload with a wrong formatted signature cannot pass the check",
			"{}",
			"19092633e5aa9a849dfcc9d2df4e76db2df1fcba7f38915f2c7833bd8a510f2f",
			defaultTokenGenerator,
			false,
		},
		{
			"empty signature is not valid",
			"{}",
			"",
			defaultTokenGenerator,
			false,
		},
		{
			"org-level webhook event with a correct signature can pass the check",
			`{"organization": {"login": "org1"}}`,
			"sha256=87e9dcd175b5188a43e6ba0511da057b51f38f5a88088b198f26db13f1409280",
			defaultTokenGenerator,
			true,
		},
		{
			"repo-level webhook event with a correct signature can pass the check",
			`{"repository": {"full_name": "org2/repo"}}`,
			"sha256=489ff461f227239a8b2ae0ec5b452dc134712ef2e23a9939ecf7a889b467351b",
			defaultTokenGenerator,
			true,
		},
		{
			"payload with both repository and organization is considered as a repo-level webhook event",
			`{"repository": {"full_name": "org2/repo"}, "organization": {"login": "org2"}}`,
			"sha256=1ec548cd58f7846a1af4736d08d29604de0b85edd19e3b8569c5eda544c200fc",
			defaultTokenGenerator,
			true,
		},
	}
	for _, tc := range testcases {
		res := ValidatePayload([]byte(tc.payload), tc.sig, tc.tokenGenerator)
		if res != tc.valid {
			t.Errorf("Wrong validation for the test %q: expected %t but got %t", tc.name, tc.valid, res)
		}
	}
}

func TestPayloadSignatureGitHubVector(t *testing.T) {
	// GitHub's official test vector from
	// https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
	secret := []byte("It's a Secret to Everybody")
	payload := []byte("Hello, World!")
	expected := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"

	got := PayloadSignature(payload, secret)
	if got != expected {
		t.Errorf("PayloadSignature mismatch: got %s, want %s", got, expected)
	}
}
