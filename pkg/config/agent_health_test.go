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

func TestAgentHealthy(t *testing.T) {
	t.Parallel()

	agent := &Agent{}
	if agent.Healthy() {
		t.Fatal("new agent unexpectedly reported healthy")
	}

	agent.Set(&Config{})
	if !agent.Healthy() {
		t.Fatal("Set should mark agent healthy")
	}

	agent.setHealthy(false)
	if agent.Healthy() {
		t.Fatal("setHealthy(false) should mark agent unhealthy")
	}

	agent.SetWithoutBroadcast(&Config{})
	if agent.Healthy() {
		t.Fatal("SetWithoutBroadcast should not change load health")
	}
}
