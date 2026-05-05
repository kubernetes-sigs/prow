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
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestListDeploys(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/sites/site-123/deploys" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Fatalf("unexpected auth header %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(`[{
			"id": "deploy-123",
			"context": "deploy-preview",
			"state": "ready",
			"review_id": 5,
			"branch": "feature",
			"deploy_ssl_url": "https://deploy-preview-5.example.netlify.app",
			"created_at": "2026-04-28T22:10:06.585Z"
		}]`)),
			Header: make(http.Header),
		}, nil
	})}

	client := NewClient("https://api.netlify.test", httpClient, func() []byte { return []byte("token-123") })
	deploys, err := client.ListDeploys(context.Background(), "site-123")
	if err != nil {
		t.Fatalf("failed to list deploys: %v", err)
	}
	if len(deploys) != 1 {
		t.Fatalf("expected one deploy, got %d", len(deploys))
	}
	if deploys[0].ID != "deploy-123" || deploys[0].ReviewID != 5 {
		t.Fatalf("unexpected deploy: %#v", deploys[0])
	}
}

func TestRetryDeploy(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %q", r.Method)
		}
		if r.URL.Path != "/api/v1/deploys/deploy-123/retry" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})}

	client := NewClient("https://api.netlify.test", httpClient, func() []byte { return []byte("token-123") })
	if err := client.RetryDeploy(context.Background(), "deploy-123"); err != nil {
		t.Fatalf("failed to retry deploy: %v", err)
	}
}

func TestRetryDeployReportsNonSuccess(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Body:       io.NopCloser(strings.NewReader("rate limited")),
			Header:     make(http.Header),
		}, nil
	})}

	client := NewClient("https://api.netlify.test", httpClient, func() []byte { return []byte("token-123") })
	if err := client.RetryDeploy(context.Background(), "deploy-123"); err == nil {
		t.Fatal("expected error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
