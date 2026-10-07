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
	"strconv"
	"testing"
	"time"
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
