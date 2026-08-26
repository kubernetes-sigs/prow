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
	"fmt"

	"cloud.google.com/go/auth/credentials"
	"google.golang.org/api/option"
)

// GoogleCredentialsFileOption returns a client option that authenticates with
// the credentials in file, which may be of any type supported by Google's auth
// library (service_account, authorized_user, external_account, ...). Scopes
// must be given explicitly because the returned credentials bypass the
// client's default scopes.
func GoogleCredentialsFileOption(file string, scopes ...string) (option.ClientOption, error) {
	// CredentialsFile is deprecated, but no set date for removal.
	// The credential-type-specific loaders that replace CredentialsFile each
	// accept exactly one type, and callers here legitimately pass any of them.
	// The deprecation guards against credential configs from untrusted sources;
	// this path is fed by an operator-supplied flag, so the risk does not apply.
	//nolint:staticcheck
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		CredentialsFile: file,
		Scopes:          scopes,
	})
	if err != nil {
		return nil, fmt.Errorf("load Google credentials from %q: %w", file, err)
	}
	return option.WithAuthCredentials(creds), nil
}
