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

package markdown

import (
	"encoding/json"
	"strings"
	"testing"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/spyglass/api"
	"sigs.k8s.io/prow/pkg/spyglass/lenses/fake"
)

func TestBody(t *testing.T) {
	link1 := "http://link.to/README.md"
	link2 := "http://link.to/build-log.txt"
	
	testCases := []struct {
		name               string
		artifacts          []api.Artifact
		config             json.RawMessage
		expectedSubstrings []string
		unexpectedSubstrings []string
	}{
		{
			name: "renders markdown",
			artifacts: []api.Artifact{
				&fake.Artifact{
					Path:    "README.md",
					Content: []byte("# Hello Goldmark\nThis is **bold**."),
					Link:    &link1,
				},
			},
			expectedSubstrings: []string{
				"<h1>Hello Goldmark</h1>",
				"<strong>bold</strong>",
			},
		},
		{
			name: "hides lens when no matching file",
			artifacts: []api.Artifact{
				&fake.Artifact{
					Path:    "build-log.txt",
					Content: []byte("some log"),
					Link:    &link2,
				},
			},
			config: json.RawMessage(`{"file": "README.md"}`),
			expectedSubstrings: []string{
				"container.style.display = 'none'",
			},
		},
		{
			name: "file filter",
			artifacts: []api.Artifact{
				&fake.Artifact{
					Path:    "README.md",
					Content: []byte("readme content"),
					Link:    &link1,
				},
				&fake.Artifact{
					Path:    "build-log.txt",
					Content: []byte("log content"),
					Link:    &link2,
				},
			},
			config: json.RawMessage(`{"file": "README.md"}`),
			expectedSubstrings: []string{
				"readme content",
			},
			unexpectedSubstrings: []string{
				"log content",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lens := Lens{}
			got := lens.Body(tc.artifacts, ".", "", tc.config, config.Spyglass{})
			
			for _, expected := range tc.expectedSubstrings {
				if !strings.Contains(got, expected) {
					t.Errorf("Expected substring %q not found in output: %s", expected, got)
				}
			}
			for _, unexpected := range tc.unexpectedSubstrings {
				if strings.Contains(got, unexpected) {
					t.Errorf("Unexpected substring %q found in output: %s", unexpected, got)
				}
			}
		})
	}
}
