/*
Copyright 2020 The Kubernetes Authors.

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

package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/collectors"
	ctrlruntimemetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
)

type fakeListenAndServer struct {
	ctx    context.Context
	server *httptest.Server
}

func (fls *fakeListenAndServer) ListenAndServe() error {
	defer fls.server.Close()
	// Already listening and serving
	<-fls.ctx.Done()
	return http.ErrServerClosed
}

func (fls *fakeListenAndServer) Shutdown(ctx context.Context) error {
	return fls.server.Config.Shutdown(ctx)
}

func (fls *fakeListenAndServer) CreateServer(handler http.Handler) interrupts.ListenAndServer {
	fls.server = httptest.NewServer(handler)
	return fls
}

func TestExposeMetrics(t *testing.T) {
	ctx := t.Context()
	fls := fakeListenAndServer{ctx: ctx}

	ExposeMetricsWithRegistry("my-component", config.PushGateway{}, flagutil.DefaultMetricsPort, nil, fls.CreateServer)
	resp, err := http.Get(fls.server.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed getting metrics: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("response status was not %d but %d", http.StatusOK, resp.StatusCode)
	}
}

// TestExposeMetricsWithControllerRuntimeCollectors verifies that the
// Unregister calls in ExposeMetricsWithRegistry match what controller-runtime
// v0.21.0 registers via its init in pkg/internal/controller/metrics. The test
// binary does not trigger that init, so we register the same collectors
// manually.
func TestExposeMetricsWithControllerRuntimeCollectors(t *testing.T) {
	goC := collectors.NewGoCollector(collectors.WithGoCollectorRuntimeMetrics(collectors.MetricsAll))
	procC := collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})
	//nolint:staticcheck
	ctrlruntimemetrics.Registry.MustRegister(goC, procC)
	t.Cleanup(func() {
		//nolint:staticcheck
		ctrlruntimemetrics.Registry.Unregister(goC)
		//nolint:staticcheck
		ctrlruntimemetrics.Registry.Unregister(procC)
	})

	ctx := t.Context()
	fls := fakeListenAndServer{ctx: ctx}
	ExposeMetricsWithRegistry("test", config.PushGateway{}, flagutil.DefaultMetricsPort, nil, fls.CreateServer)

	resp, err := http.Get(fls.server.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed getting metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed reading body: %v", err)
	}
	if strings.Contains(string(body), "collected before with the same name") {
		t.Fatal("duplicate collector: Unregister options do not match controller-runtime")
	}
}
