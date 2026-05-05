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

package netlify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Deploy is the subset of Netlify deploy data needed to retry PR deploy previews.
type Deploy struct {
	ID           string    `json:"id"`
	Context      string    `json:"context"`
	State        string    `json:"state"`
	ReviewID     int       `json:"review_id"`
	Branch       string    `json:"branch"`
	DeploySSLURL string    `json:"deploy_ssl_url"`
	CreatedAt    time.Time `json:"created_at"`
}

type Client struct {
	baseURL        string
	httpClient     *http.Client
	tokenGenerator func() []byte
}

func NewClient(baseURL string, httpClient *http.Client, tokenGenerator func() []byte) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		httpClient:     httpClient,
		tokenGenerator: tokenGenerator,
	}
}

func (c *Client) ListDeploys(ctx context.Context, siteID string) ([]Deploy, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/sites/%s/deploys", c.baseURL, url.PathEscape(siteID)), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list deploys returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var deploys []Deploy
	if err := json.NewDecoder(resp.Body).Decode(&deploys); err != nil {
		return nil, err
	}
	return deploys, nil
}

func (c *Client) RetryDeploy(ctx context.Context, deployID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/deploys/%s/retry", c.baseURL, url.PathEscape(deployID)), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("retry deploy returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) authorize(req *http.Request) {
	if c.tokenGenerator == nil {
		return
	}
	token := strings.TrimSpace(string(c.tokenGenerator()))
	if token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}
