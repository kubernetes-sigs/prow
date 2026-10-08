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
	"context"
	"testing"

	"sigs.k8s.io/prow/pkg/config"
)

func TestDisabledClustersWatcher(t *testing.T) {
	testCases := []struct {
		name        string
		before      []string
		after       []string
		canceled    bool
		wantRestart bool
	}{
		{
			name:        "disable a cluster",
			after:       []string{"build04"},
			wantRestart: true,
		},
		{
			name:        "enable a cluster",
			before:      []string{"build04"},
			wantRestart: true,
		},
		{
			name:        "replace a disabled cluster",
			before:      []string{"build04"},
			after:       []string{"build05"},
			wantRestart: true,
		},
		{
			name:   "unchanged disabled clusters",
			before: []string{"build04"},
			after:  []string{"build04"},
		},
		{
			name:   "order and duplicates do not change the set",
			before: []string{"build02", "build04"},
			after:  []string{"build04", "build02", "build04"},
		},
		{
			name:  "nil and empty are equivalent",
			after: []string{},
		},
		{
			name:     "already shutting down",
			before:   []string{"build04"},
			canceled: true,
		},
	}

	for _, tc := range testCases {
		ctx, cancel := context.WithCancel(context.Background())
		if tc.canceled {
			cancel()
		}

		before := config.Config{
			ProwConfig: config.ProwConfig{
				DisabledClusters: tc.before,
			},
		}
		after := config.Config{
			ProwConfig: config.ProwConfig{
				DisabledClusters: tc.after,
			},
		}

		changes := make(chan config.Delta, 1)
		changes <- config.Delta{Before: before, After: after}
		close(changes)

		restartCtx, restart := context.WithCancel(context.Background())
		watcher := disabledClustersWatcher{
			changes:   changes,
			terminate: restart,
		}
		watcher.run(ctx)
		gotRestart := restartCtx.Err() != nil
		cancel()
		restart()

		if gotRestart != tc.wantRestart {
			t.Errorf("%s: got restart %t, want %t", tc.name, gotRestart, tc.wantRestart)
		}
	}
}
