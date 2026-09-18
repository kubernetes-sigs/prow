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

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"strings"
	"testing"
	"time"
)

type mockRoundTripper struct {
	resp *http.Response
	err  error
}

func (m *mockRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return m.resp, m.err
}

func TestNewHelpAgent(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		cert        string
		wantErr     string
		wantTLS     bool
		wantFileErr bool
		wantPath    string
	}{
		{
			name:    "Invalid_Url_Returns_Error",
			path:    ":",
			cert:    "",
			wantErr: "parse \":\": missing protocol scheme",
		},
		{
			name:     "Http_Scheme_Returns_Default_Client",
			path:     "http://example.com",
			cert:     "",
			wantTLS:  false,
			wantPath: "http://example.com",
		},
		{
			name:     "Https_Scheme_With_Valid_Cert",
			path:     "https://example.com",
			cert:     t.TempDir() + "/cert.pem",
			wantTLS:  true,
			wantPath: "https://example.com",
		},
		{
			name:    "Https_Scheme_With_Missing_Cert_File_Returns_Error",
			path:    "https://example.com",
			cert:    "",
			wantErr: "open : no such file or directory",
		},
		{
			name:        "Invalid_Cert_File_Path_Returns_Error",
			path:        "https://test.com/help",
			cert:        "C://badfileformat://",
			wantFileErr: true,
			wantErr:     "error decoding cert file: open C://badfileformat://: no such file or directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cert != "" && !tc.wantFileErr {
				err := os.WriteFile(tc.cert, []byte("dummy cert content"), 0644)
				if err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
			}
			ha, err := newHelpAgent(tc.path, tc.cert)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing '%s', got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ha == nil {
				t.Fatal("expected non-nil helpAgent")
			}
			if tc.wantTLS {
				tr, ok := ha.client.Transport.(*http.Transport)
				if !ok {
					t.Fatalf("expected *http.Transport, got %T", ha.client.Transport)
				}
				if tr.TLSClientConfig == nil {
					t.Error("expected non-nil TLSClientConfig")
				}
			} else {
				if ha.client != http.DefaultClient {
					t.Errorf("expected http.DefaultClient, got %v", ha.client)
				}
			}
			if tc.wantPath != "" && ha.path != tc.wantPath {
				t.Errorf("unexpected path: got %s, want %s", ha.path, tc.wantPath)
			}
		})
	}
}

func TestGetHelp(t *testing.T) {
	type testCase struct {
		name        string
		hookPath    string
		cert        string
		mockResp    *http.Response
		mockErr     error
		wantDesc    string
		wantFileErr bool
		wantErr     string
		cacheTest   bool
	}

	pluginHelp := pluginhelp.PluginHelp{Description: "test"}
	cached := pluginhelp.PluginHelp{Description: "cached"}
	help := pluginhelp.Help{PluginHelp: map[string]pluginhelp.PluginHelp{"example": pluginHelp}}
	cachedHelp := pluginhelp.Help{PluginHelp: map[string]pluginhelp.PluginHelp{"example": cached}}
	helpBytes, _ := json.Marshal(help)
	cachedBytes, _ := json.Marshal(cachedHelp)

	cases := []testCase{
		{
			name:     "No_Cert_No_Https",
			hookPath: "http://test.com/help",
			cert:     "",
			mockResp: &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(helpBytes))},
			wantDesc: "test",
		},
		{
			name:     "With_Cert_Success",
			hookPath: "https://test.com/help",
			cert:     t.TempDir() + "/cert.pem",
			mockResp: &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(helpBytes))},
			wantDesc: "test",
		},
		{
			name:     "Status_Code_Error",
			hookPath: "http://test.com/help",
			cert:     "",
			mockResp: &http.Response{StatusCode: 500, Body: io.NopCloser(bytes.NewReader([]byte{}))},
			mockErr:  errors.New("failed to fetch Plugin info"),
			wantErr:  "error Getting plugin help: Get \"http://test.com/help\": failed to fetch Plugin info",
		},
		{
			name:     "JSON_Error",
			hookPath: "http://test.com/help",
			cert:     "",
			mockResp: &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader([]byte("notjson")))},
			wantErr:  "error decoding json plugin help:",
		},
		{
			name:      "Cache",
			hookPath:  "http://test.com/help",
			cert:      "",
			mockResp:  &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(cachedBytes))},
			wantDesc:  "cached",
			cacheTest: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {

			ha := helpAgent{
				path:   tc.hookPath,
				cert:   tc.cert,
				client: &http.Client{Transport: &mockRoundTripper{resp: tc.mockResp, err: tc.mockErr}},
			}

			if tc.cert != "" && !tc.wantFileErr {
				err := os.WriteFile(tc.cert, []byte("dummy cert content"), 0644)
				if err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
			}

			if tc.cacheTest {
				// First call populates cache
				got, err := ha.getHelp()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.PluginHelp["example"].Description != tc.wantDesc {
					t.Errorf("expected description '%s', got '%s'", tc.wantDesc, got.PluginHelp["example"].Description)
				}
				// Set expiry to future to trigger cache
				ha.expiry = time.Now().Add(time.Minute)
				got2, err2 := ha.getHelp()
				if err2 != nil {
					t.Fatalf("unexpected error: %v", err2)
				}
				if got != got2 {
					t.Errorf("expected cached help, got different pointers")
				}
				return
			}
			got, err := ha.getHelp()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing '%s', got %v", tc.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.PluginHelp["example"].Description != tc.wantDesc {
					t.Errorf("expected description '%s', got '%s'", tc.wantDesc, got.PluginHelp["example"].Description)
				}
			}
		})
	}
}
