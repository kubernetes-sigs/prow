/*
Copyright 2025 The Kubernetes Authors.

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
	"strings"

	"sigs.k8s.io/prow/pkg/github"
)

// OrgAwareClient wraps a github.Client so that FindIssues routes through
// FindIssuesWithOrg. Prow's App auth round-tripper requires the org in
// the request context to resolve the installation token, but
// UpdatePullRequestWithLabels internally calls FindIssues which
// passes an empty org.
//
// When IsAppAuth is true, BotUser() appends "[bot]" to the login so that
// GitHub's search API author: qualifier matches the App's acting identity.
type OrgAwareClient struct {
	github.Client
	Org       string
	IsAppAuth bool
}

func (c *OrgAwareClient) FindIssues(query, sort string, asc bool) ([]github.Issue, error) {
	return c.Client.FindIssuesWithOrg(c.Org, query, sort, asc)
}

// BotUser returns the bot user data. When the client is using GitHub App auth,
// it appends the "[bot]" suffix to the login. GitHub Apps act as "slug[bot]"
// users, but prow's getUserData only stores the bare slug. The search API's
// author: qualifier requires the full "slug[bot]" form to match PRs created
// by the App.
func (c *OrgAwareClient) BotUser() (*github.UserData, error) {
	user, err := c.Client.BotUser()
	if err != nil {
		return nil, err
	}
	if !c.IsAppAuth || strings.HasSuffix(user.Login, "[bot]") {
		return user, nil
	}
	return &github.UserData{
		Name:  user.Name,
		Login: user.Login + "[bot]",
		Email: user.Email,
	}, nil
}
