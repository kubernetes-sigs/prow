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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sigs.k8s.io/prow/pkg/config"
)

type fakeConfigAgent struct {
	c config.Config
}

func (fca fakeConfigAgent) Config() *config.Config {
	return &fca.c
}

func TestHandleSimpleTemplateUsesBasePath(t *testing.T) {
	o := options{
		basePath:              "/prow",
		templateFilesLocation: "template",
	}

	param := struct {
		SpyglassEnabled bool
		ReRunCreatesJob bool
	}{
		SpyglassEnabled: true,
		ReRunCreatesJob: false,
	}

	req := httptest.NewRequest(http.MethodGet, "/prow", nil)
	rr := httptest.NewRecorder()
	handleSimpleTemplate(o, fakeConfigAgent{}.Config, "index.html", param).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code %d", rr.Code)
	}

	body := rr.Body.String()
	expectedSnippets := []string{
		`window.prowBasePath = "\/prow";`,
		`href="/prow/static/style.css?v=`,
		`href="/prow"`,
		`src="/prow/static/prow_bundle.min.js?v=`,
		`src="/prow/prowjobs.js?var=allBuilds&omit=annotations,labels,decoration_config,pod_spec"`,
	}
	for _, snippet := range expectedSnippets {
		if !strings.Contains(body, snippet) {
			t.Fatalf("rendered template missing %q", snippet)
		}
	}
}

// Made with Bob
