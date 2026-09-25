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

import "testing"

func cfg(sha string) *Config {
	return &Config{ProwConfig: ProwConfig{ConfigVersionSHA: sha}}
}

// TestSetCoalescesForBusySubscriber verifies that when a subscriber has not
// drained the previous delta, further Set calls coalesce into its single slot
// rather than blocking or dropping: the pending Before is preserved and the
// After advances to the newest config, so the subscriber can still trust Before.
func TestSetCoalescesForBusySubscriber(t *testing.T) {
	agent := &Agent{}
	sub := agent.Subscribe()

	// v0(empty) -> v1 lands in the buffer; nobody is reading yet.
	agent.Set(cfg("v1"))
	// v1 -> v2 -> v3 while the subscriber is still busy: each must coalesce
	// into the one slot instead of blocking Set or being dropped.
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

	// Everything coalesced into a single delta; nothing else is queued.
	select {
	case extra := <-sub:
		t.Fatalf("unexpected second delta: before=%q after=%q", extra.Before.ConfigVersionSHA, extra.After.ConfigVersionSHA)
	default:
	}
}

// TestSetDeliversChainedDeltasWhenDrained verifies that a subscriber that keeps
// up receives each transition as a properly chained delta (every Before equals
// the previous After).
func TestSetDeliversChainedDeltasWhenDrained(t *testing.T) {
	agent := &Agent{}
	sub := agent.Subscribe()

	for _, want := range []struct{ before, after string }{
		{"", "v1"},
		{"v1", "v2"},
		{"v2", "v3"},
	} {
		agent.Set(cfg(want.after))
		got := <-sub // drain immediately, so no coalescing happens
		if got.Before.ConfigVersionSHA != want.before || got.After.ConfigVersionSHA != want.after {
			t.Errorf("delta = {%q -> %q}, want {%q -> %q}", got.Before.ConfigVersionSHA, got.After.ConfigVersionSHA, want.before, want.after)
		}
	}
}
