/*
Copyright 2019 The Kubernetes Authors.

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

// Package pjutil contains helpers for working with ProwJobs.
package pjutil

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"k8s.io/test-infra/prow/interrupts"
)

const healthPort = 8081

// Health keeps a request multiplexer for health liveness and readiness endpoints
type Health struct {
	healthMux *http.ServeMux
}

// NewHealth creates a new health request multiplexer and starts serving the liveness endpoint
// on the default port
func NewHealth() *Health {
	return NewHealthOnPort(healthPort)
}

// NewHealth creates a new health request multiplexer and starts serving the liveness endpoint
// on the given port
func NewHealthOnPort(port int) *Health {
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "OK") })
	server := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: healthMux}
	interrupts.ListenAndServe(server, 5*time.Second)
	return &Health{
		healthMux: healthMux,
	}
}

type ReadynessCheck func() bool

// ServeReady starts serving the readiness endpoint
func (h *Health) ServeReady(readynessChecks ...ReadynessCheck) {
	h.healthMux.HandleFunc("/healthz/ready", func(w http.ResponseWriter, r *http.Request) {
		for _, readynessCheck := range readynessChecks {
			if !readynessCheck() {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, "ReadynessCheck failed")
				return
			}
		}
		fmt.Fprint(w, "OK")
	})
}
