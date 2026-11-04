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

package netlify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestForEachDeployPage(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/sites/site-123/deploys" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("deploy-previews"); got != "true" {
			t.Fatalf("unexpected deploy-previews query %q", got)
		}
		if got := r.URL.Query().Get("page"); got != "1" {
			t.Fatalf("unexpected page query %q", got)
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("unexpected per_page query %q", got)
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
	var deploys []Deploy
	err := client.ForEachDeployPage(context.Background(), "site-123", func(page []Deploy) bool {
		deploys = append(deploys, page...)
		return true
	})
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

func TestForEachDeployPageFollowsNextLink(t *testing.T) {
	var requests []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Fatalf("unexpected auth header %q", got)
		}
		switch r.URL.Query().Get("page") {
		case "1":
			header := make(http.Header)
			header.Set("Link", `<https://api.netlify.test/api/v1/sites/site-123/deploys?deploy-previews=true&page=2&per_page=100>; rel="next"`)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body: io.NopCloser(strings.NewReader(`[{
					"id": "deploy-1",
					"context": "deploy-preview",
					"state": "ready",
					"review_id": 5,
					"branch": "feature",
					"deploy_ssl_url": "https://deploy-preview-5.example.netlify.app",
					"created_at": "2026-04-28T22:10:06.585Z"
				}]`)),
				Header: header,
			}, nil
		case "2":
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body: io.NopCloser(strings.NewReader(`[{
					"id": "deploy-2",
					"context": "deploy-preview",
					"state": "error",
					"review_id": 6,
					"branch": "other-feature",
					"deploy_ssl_url": "https://deploy-preview-6.example.netlify.app",
					"created_at": "2026-04-28T23:10:06.585Z"
				}]`)),
				Header: make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected page query %q", r.URL.Query().Get("page"))
			return nil, nil
		}
	})}

	client := NewClient("https://api.netlify.test", httpClient, func() []byte { return []byte("token-123") })
	var deploys []Deploy
	err := client.ForEachDeployPage(context.Background(), "site-123", func(page []Deploy) bool {
		deploys = append(deploys, page...)
		return true
	})
	if err != nil {
		t.Fatalf("failed to list deploys: %v", err)
	}
	if len(deploys) != 2 {
		t.Fatalf("expected two deploys, got %d", len(deploys))
	}
	if deploys[0].ID != "deploy-1" || deploys[1].ID != "deploy-2" {
		t.Fatalf("unexpected deploys: %#v", deploys)
	}
	if len(requests) != 2 {
		t.Fatalf("expected two requests, got %d: %v", len(requests), requests)
	}
}

func TestForEachDeployPageStopsWhenCallbackIsDone(t *testing.T) {
	var requests int
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		header := make(http.Header)
		header.Set("Link", `<https://api.netlify.test/api/v1/sites/site-123/deploys?deploy-previews=true&page=2&per_page=100>; rel="next"`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(`[{
				"id": "deploy-1",
				"context": "deploy-preview",
				"state": "error",
				"review_id": 5,
				"branch": "feature",
				"deploy_ssl_url": "https://deploy-preview-5.example.netlify.app",
				"created_at": "2026-04-28T22:10:06.585Z"
			}]`)),
			Header: header,
		}, nil
	})}

	client := NewClient("https://api.netlify.test", httpClient, func() []byte { return []byte("token-123") })
	err := client.ForEachDeployPage(context.Background(), "site-123", func(page []Deploy) bool {
		return false
	})
	if err != nil {
		t.Fatalf("failed to list deploys: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one request, got %d", requests)
	}
}

func TestForEachDeployPageStopsAtPageCap(t *testing.T) {
	var requests int
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Fatalf("unexpected page query %q", r.URL.Query().Get("page"))
		}
		header := make(http.Header)
		header.Set("Link", fmt.Sprintf(`<https://api.netlify.test/api/v1/sites/site-123/deploys?deploy-previews=true&page=%d&per_page=100>; rel="next"`, page+1))
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`[]`)),
			Header:     header,
		}, nil
	})}

	client := NewClient("https://api.netlify.test", httpClient, func() []byte { return []byte("token-123") })
	err := client.ForEachDeployPage(context.Background(), "site-123", func(page []Deploy) bool {
		return true
	})
	if err != nil {
		t.Fatalf("failed to list deploys: %v", err)
	}
	if requests != maxListDeployPages {
		t.Fatalf("expected %d requests, got %d", maxListDeployPages, requests)
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
