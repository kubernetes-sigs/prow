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

package plugin

import (
	"testing"
	"time"

	"sigs.k8s.io/prow/cmd/external-plugins/netlify-preview/netlify"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Command
		ok   bool
	}{
		{
			name: "retest",
			body: "/retest",
			want: RetestCommand,
			ok:   true,
		},
		{
			name: "rebuild preview",
			body: "/rebuild-preview",
			want: RebuildPreviewCommand,
			ok:   true,
		},
		{
			name: "command inside multiline comment",
			body: "please try this again\n/rebuild-preview\nthanks",
			want: RebuildPreviewCommand,
			ok:   true,
		},
		{
			name: "trailing words are rejected",
			body: "/rebuild-preview please",
			ok:   false,
		},
		{
			name: "command in code block is ignored",
			body: "```\n/rebuild-preview\n```",
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseCommand(tc.body)
			if ok != tc.ok {
				t.Fatalf("expected ok=%v, got %v", tc.ok, ok)
			}
			if got != tc.want {
				t.Fatalf("expected command %q, got %q", tc.want, got)
			}
		})
	}
}

func TestLatestDeployPreview(t *testing.T) {
	old := time.Date(2026, 4, 28, 17, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	deploys := []netlify.Deploy{
		{ID: "branch", Context: "branch-deploy", ReviewID: 5, CreatedAt: newer},
		{ID: "other-pr", Context: "deploy-preview", ReviewID: 6, CreatedAt: newer},
		{ID: "old", Context: "deploy-preview", ReviewID: 5, CreatedAt: old},
		{ID: "new", Context: "deploy-preview", ReviewID: 5, CreatedAt: newer},
	}

	got, ok := LatestDeployPreview(deploys, 5)
	if !ok {
		t.Fatal("expected to find a deploy preview")
	}
	if got.ID != "new" {
		t.Fatalf("expected latest deploy preview %q, got %q", "new", got.ID)
	}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name       string
		command    Command
		preview    *netlify.Deploy
		wantAction Action
		wantRetry  bool
	}{
		{
			name:       "no preview",
			command:    RebuildPreviewCommand,
			wantAction: ActionNoPreview,
		},
		{
			name:       "building preview is already running",
			command:    RebuildPreviewCommand,
			preview:    &netlify.Deploy{State: "building"},
			wantAction: ActionAlreadyRunning,
		},
		{
			name:       "enqueued preview is already running",
			command:    RebuildPreviewCommand,
			preview:    &netlify.Deploy{State: "enqueued"},
			wantAction: ActionAlreadyRunning,
		},
		{
			name:       "retest retries error preview",
			command:    RetestCommand,
			preview:    &netlify.Deploy{State: "error"},
			wantAction: ActionRetry,
			wantRetry:  true,
		},
		{
			name:       "rebuild preview retries error preview",
			command:    RebuildPreviewCommand,
			preview:    &netlify.Deploy{State: "error"},
			wantAction: ActionRetry,
			wantRetry:  true,
		},
		{
			name:       "retest does not retry ready preview",
			command:    RetestCommand,
			preview:    &netlify.Deploy{State: "ready"},
			wantAction: ActionReadyRequiresRebuild,
		},
		{
			name:       "rebuild preview retries ready preview",
			command:    RebuildPreviewCommand,
			preview:    &netlify.Deploy{State: "ready"},
			wantAction: ActionRetry,
			wantRetry:  true,
		},
		{
			name:       "retest does not retry unknown state",
			command:    RetestCommand,
			preview:    &netlify.Deploy{State: "uploaded"},
			wantAction: ActionUnsupportedState,
		},
		{
			name:       "rebuild preview overrides unknown state",
			command:    RebuildPreviewCommand,
			preview:    &netlify.Deploy{State: "uploaded"},
			wantAction: ActionRetry,
			wantRetry:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.command, tc.preview)
			if got.Action != tc.wantAction {
				t.Fatalf("expected action %q, got %q", tc.wantAction, got.Action)
			}
			if got.ShouldRetry != tc.wantRetry {
				t.Fatalf("expected ShouldRetry=%v, got %v", tc.wantRetry, got.ShouldRetry)
			}
		})
	}
}
