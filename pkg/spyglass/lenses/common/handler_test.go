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

package common

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/spyglass/api"
)

type handlerTestArtifact struct{ api.Artifact }

func (handlerTestArtifact) Size() (int64, error) { return 1, nil }

type handlerTestArtifactFetcher struct{}

func (handlerTestArtifactFetcher) Artifact(context.Context, string, string, int64) (api.Artifact, error) {
	return handlerTestArtifact{}, nil
}

// handlerTestLens returns the lens configuration it was rendered with.
type handlerTestLens struct{}

func (handlerTestLens) Header([]api.Artifact, string, json.RawMessage, config.Spyglass) string {
	return ""
}
func (handlerTestLens) Body(_ []api.Artifact, _ string, _ string, cfg json.RawMessage, _ config.Spyglass) string {
	return string(cfg)
}
func (handlerTestLens) Callback(_ []api.Artifact, _ string, _ string, cfg json.RawMessage, _ config.Spyglass) string {
	return string(cfg)
}

func TestLensHandlerLensIndex(t *testing.T) {
	cfg := &config.Config{ProwConfig: config.ProwConfig{Deck: config.Deck{Spyglass: config.Spyglass{
		Lenses: []config.LensFileConfig{
			{Lens: config.LensConfig{Name: "junit", Config: json.RawMessage(`"first"`)}},
			{Lens: config.LensConfig{Name: "junit", Config: json.RawMessage(`"second"`)}},
		},
	}}}}
	handler := newLensHandler(handlerTestLens{}, lensHandlerOpts{
		StorageArtifactFetcher: handlerTestArtifactFetcher{},
		PodLogArtifactFetcher:  handlerTestArtifactFetcher{},
		ConfigGetter:           func() *config.Config { return cfg },
	})

	for _, tc := range []struct {
		name         string
		index        int
		expectedCode int
		expectedBody string
	}{
		{name: "first lens", index: 0, expectedCode: http.StatusOK, expectedBody: `"first"`},
		{name: "second lens", index: 1, expectedCode: http.StatusOK, expectedBody: `"second"`},
		{name: "index past the end is rejected", index: 2, expectedCode: http.StatusBadRequest},
		{name: "huge index from a scanner is rejected", index: 19992551, expectedCode: http.StatusBadRequest},
		{name: "negative index is rejected", index: -1, expectedCode: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(api.LensRequest{
				Action:         api.RequestActionRerender,
				ArtifactSource: "gs/bucket/logs/job/1",
				Artifacts:      []string{"junit.xml"},
				LensIndex:      tc.index,
			})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			// Must not panic.
			handler(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
			if rr.Code != tc.expectedCode {
				t.Fatalf("expected status %d, got %d: %s", tc.expectedCode, rr.Code, rr.Body.String())
			}
			if tc.expectedBody != "" && rr.Body.String() != tc.expectedBody {
				t.Errorf("expected the lens to be rendered with config %s, got %s", tc.expectedBody, rr.Body.String())
			}
		})
	}
}
