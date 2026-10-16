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

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func cfg(sha string) *Config {
	return &Config{ProwConfig: ProwConfig{ConfigVersionSHA: sha}}
}

func TestSetCoalescesForBusySubscriber(t *testing.T) {
	agent := &Agent{}
	sub := agent.Subscribe()

	// Leave updates unread to exercise coalescing.
	agent.Set(cfg("v1"))
	agent.Set(cfg("v2"))
	agent.Set(cfg("v3"))

	var got Delta
	select {
	case got = <-sub:
	default:
		t.Fatal("expected a coalesced delta in the buffer, found none")
	}
	if got.Before.ConfigVersionSHA != "" {
		t.Errorf("Before = %q, want %q (the initial config, not skipped)", got.Before.ConfigVersionSHA, "")
	}
	if got.After.ConfigVersionSHA != "v3" {
		t.Errorf("After = %q, want %q (the newest config)", got.After.ConfigVersionSHA, "v3")
	}

	select {
	case extra := <-sub:
		t.Fatalf("unexpected second delta: before=%q after=%q", extra.Before.ConfigVersionSHA, extra.After.ConfigVersionSHA)
	default:
	}
}

func TestSetDeliversChainedDeltasWhenDrained(t *testing.T) {
	agent := &Agent{}
	sub := agent.Subscribe()

	for _, want := range []struct{ before, after string }{
		{"", "v1"},
		{"v1", "v2"},
		{"v2", "v3"},
	} {
		agent.Set(cfg(want.after))
		got := receiveDelta(t, sub) // Drain before the next Set to prevent coalescing.
		if got.Before.ConfigVersionSHA != want.before || got.After.ConfigVersionSHA != want.after {
			t.Errorf("delta = {%q -> %q}, want {%q -> %q}", got.Before.ConfigVersionSHA, got.After.ConfigVersionSHA, want.before, want.after)
		}
	}
}

func TestSetPreservesDeltaChainDuringConcurrentReceive(t *testing.T) {
	agent := &Agent{}
	agent.Set(cfg("0"))
	sub := agent.Subscribe()

	for i := range 1000 {
		before := strconv.Itoa(2 * i)
		pending := strconv.Itoa(2*i + 1)
		after := strconv.Itoa(2*i + 2)
		agent.Set(cfg(pending))

		// Race a receive against replacement of a full buffer.
		start := make(chan struct{})
		received := make(chan Delta, 1)
		sent := make(chan struct{})
		go func() {
			<-start
			received <- <-sub
		}()
		go func() {
			<-start
			agent.Set(cfg(after))
			close(sent)
		}()
		close(start)

		got := receiveDelta(t, received)
		if got.Before.ConfigVersionSHA != before {
			t.Fatalf("iteration %d: Before = %q, want %q", i, got.Before.ConfigVersionSHA, before)
		}
		switch got.After.ConfigVersionSHA {
		case pending:
			got = receiveDelta(t, sub)
			if got.Before.ConfigVersionSHA != pending || got.After.ConfigVersionSHA != after {
				t.Fatalf("iteration %d: delta = {%q -> %q}, want {%q -> %q}", i, got.Before.ConfigVersionSHA, got.After.ConfigVersionSHA, pending, after)
			}
		case after:
		default:
			t.Fatalf("iteration %d: After = %q, want %q or %q", i, got.After.ConfigVersionSHA, pending, after)
		}
		<-sent
	}

	select {
	case extra := <-sub:
		t.Fatalf("unexpected extra delta: before=%q after=%q", extra.Before.ConfigVersionSHA, extra.After.ConfigVersionSHA)
	default:
	}
}

func TestSetCoalescesSubscribersIndependently(t *testing.T) {
	agent := &Agent{}
	agent.Set(cfg("0"))
	fast := agent.Subscribe()
	slow := agent.Subscribe()
	slowBefore := "0"

	for i := 1; i <= 6; i++ {
		after := strconv.Itoa(i)
		agent.Set(cfg(after))
		got := receiveDelta(t, fast)
		before := strconv.Itoa(i - 1)
		if got.Before.ConfigVersionSHA != before || got.After.ConfigVersionSHA != after {
			t.Fatalf("fast subscriber: delta = {%q -> %q}, want {%q -> %q}", got.Before.ConfigVersionSHA, got.After.ConfigVersionSHA, before, after)
		}

		// Let the slow subscriber catch up without another Set.
		if i%3 == 0 {
			got = receiveDelta(t, slow)
			if got.Before.ConfigVersionSHA != slowBefore || got.After.ConfigVersionSHA != after {
				t.Fatalf("slow subscriber: delta = {%q -> %q}, want {%q -> %q}", got.Before.ConfigVersionSHA, got.After.ConfigVersionSHA, slowBefore, after)
			}
			slowBefore = after
		}
	}
}

func receiveDelta(t *testing.T, sub DeltaChan) Delta {
	t.Helper()
	select {
	case delta := <-sub:
		return delta
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for config delta")
		return Delta{}
	}
}

func TestLastConfigModTime(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)

	testCases := []struct {
		name             string
		jobConfigs       []time.Duration
		missingJobConfig bool
		expected         time.Duration
		expectedErr      error
	}{
		{
			name:     "no job config",
			expected: 0,
		},
		{
			name:       "newer job config",
			jobConfigs: []time.Duration{time.Hour},
			expected:   time.Hour,
		},
		{
			name:       "job config older than the prow config",
			jobConfigs: []time.Duration{-time.Hour},
			expected:   0,
		},
		{
			name:       "the newest of many job configs is the last one",
			jobConfigs: []time.Duration{time.Hour, 2 * time.Hour},
			expected:   2 * time.Hour,
		},
		{
			name:       "the newest of many job configs is the first one",
			jobConfigs: []time.Duration{3 * time.Hour, 2 * time.Hour},
			expected:   3 * time.Hour,
		},
		{
			name:       "a change of any path is noticed even if the others are older than the prow config",
			jobConfigs: []time.Duration{-time.Hour, time.Minute, -2 * time.Hour},
			expected:   time.Minute,
		},
		{
			name:             "a missing path is not hidden by the paths that exist",
			jobConfigs:       []time.Duration{time.Hour},
			missingJobConfig: true,
			expectedErr:      os.ErrNotExist,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			prowConfig := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(prowConfig, nil, 0600); err != nil {
				t.Fatalf("fail to write prow config: %v", err)
			}
			if err := os.Chtimes(prowConfig, base, base); err != nil {
				t.Fatalf("fail to set the modification time of the prow config: %v", err)
			}

			var jobConfigs []string
			for i, offset := range tc.jobConfigs {
				jobConfig := filepath.Join(dir, "jobs-"+strconv.Itoa(i))
				if err := os.Mkdir(jobConfig, 0700); err != nil {
					t.Fatalf("fail to create job config dir: %v", err)
				}
				if err := os.Chtimes(jobConfig, base.Add(offset), base.Add(offset)); err != nil {
					t.Fatalf("fail to set the modification time of the job config: %v", err)
				}
				jobConfigs = append(jobConfigs, jobConfig)
			}
			if tc.missingJobConfig {
				jobConfigs = append(jobConfigs, filepath.Join(dir, "missing"))
			}

			actual, err := lastConfigModTime(prowConfig, jobConfigs)

			if !errors.Is(err, tc.expectedErr) {
				t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
			}
			if tc.expectedErr != nil {
				return
			}
			if diff := cmp.Diff(base.Add(tc.expected), actual); diff != "" {
				t.Errorf("unexpected modification time (-want +got):\n%s", diff)
			}
		})
	}
}
