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

func TestGetHelp(t *testing.T) {
	type testCase struct {
		name        string
		hookPath    string
		cert        string
		certContent string
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
			name:     "NoCert_Success",
			hookPath: "http://example.com/help",
			cert:     "",
			mockResp: &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(helpBytes))},
			wantDesc: "test",
		},
		{
			name:        "WithCert_FileError",
			hookPath:    "https://example.com/help",
			cert:        "C://badfileformat://",
			wantFileErr: true,
			wantErr:     "error decoding cert file: open C://badfileformat://: no such file or directory",
		},
		{
			name:     "NoCert_NoHttps",
			hookPath: "http://example.com/help",
			cert:     "",
			mockResp: &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(helpBytes))},
			wantDesc: "test",
		},
		{
			name:        "WithCert_Success",
			hookPath:    "https://example.com/help",
			cert:        t.TempDir() + "/cert.pem",
			certContent: "dummy cert content",
			mockResp:    &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(helpBytes))},
			wantDesc:    "test",
		},
		{
			name:     "HTTPError",
			hookPath: "http://example.com/help",
			cert:     "",
			mockErr:  errors.New("fail"),
			wantErr:  "error Getting plugin help: Get \"http://example.com/help\": fail",
		},
		{
			name:     "StatusCodeError",
			hookPath: "http://example.com/help",
			cert:     "",
			mockResp: &http.Response{StatusCode: 500, Body: io.NopCloser(bytes.NewReader([]byte{}))},
			wantErr:  "response has status code 500",
		},
		{
			name:     "JSONError",
			hookPath: "http://example.com/help",
			cert:     "",
			mockResp: &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader([]byte("notjson")))},
			wantErr:  "error decoding json plugin help:",
		},
		{
			name:      "Cache",
			hookPath:  "http://example.com/help",
			cert:      "",
			mockResp:  &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(cachedBytes))},
			wantDesc:  "cached",
			cacheTest: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {

			//// Mock the HTTP client
			http.DefaultTransport = &mockRoundTripper{
				resp: tc.mockResp,
				err:  tc.mockErr,
			}

			ha := newHelpAgent(tc.hookPath, tc.cert)
			if tc.cert != "" && !tc.wantFileErr {
				err := os.WriteFile(tc.cert, []byte(tc.certContent), 0644)
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
