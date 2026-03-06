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

import "github.com/prometheus/client_golang/prometheus"

var statusReconcilerMetrics = struct {
	controllerHealthy prometheus.Gauge
	actuationEnabled  prometheus.Gauge
}{
	controllerHealthy: prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "status_reconciler_controller_healthy",
		Help: "Whether status-reconciler currently reports healthy for liveness checks.",
	}),
	actuationEnabled: prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "status_reconciler_actuation_enabled",
		Help: "Whether status-reconciler is currently allowed to actuate status reconciliation.",
	}),
}

func init() {
	prometheus.MustRegister(statusReconcilerMetrics.controllerHealthy)
	prometheus.MustRegister(statusReconcilerMetrics.actuationEnabled)
}
