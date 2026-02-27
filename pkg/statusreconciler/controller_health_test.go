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

package statusreconciler

import (
	"testing"

	"sigs.k8s.io/prow/pkg/config"
)

type fakeStatusClient struct {
	healthy bool
}

func (f *fakeStatusClient) Load() (chan config.Delta, error) {
	return make(chan config.Delta), nil
}

func (f *fakeStatusClient) Save() error {
	return nil
}

func (f *fakeStatusClient) Healthy() bool {
	return f.healthy
}

func TestControllerHealthy(t *testing.T) {
	t.Parallel()

	statusClient := &fakeStatusClient{healthy: false}
	controller := &Controller{statusClient: statusClient}

	if !controller.Healthy() {
		t.Fatal("controller should report healthy before first config load attempt")
	}

	controller.markHealthInitialized()
	if controller.Healthy() {
		t.Fatal("controller should mirror status client health after initialization")
	}

	statusClient.healthy = true
	if !controller.Healthy() {
		t.Fatal("controller did not reflect recovered status client health")
	}
}
