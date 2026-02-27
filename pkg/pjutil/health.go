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
	"sync"
	"time"

	"sigs.k8s.io/prow/pkg/interrupts"
)

const healthPort = 8081

// Health keeps a request multiplexer for health liveness and readiness endpoints
type Health struct {
	healthMux *http.ServeMux

	livenessLock   sync.RWMutex
	livenessChecks []LivenessCheck
}

type LivenessCheck func() bool

// NewHealth creates a new health request multiplexer and starts serving the liveness endpoint
// on the default port
func NewHealth() *Health {
	return NewHealthOnPort(healthPort)
}

// NewHealth creates a new health request multiplexer and starts serving the liveness endpoint
// on the given port
func NewHealthOnPort(port int) *Health {
	h := &Health{healthMux: http.NewServeMux()}
	h.healthMux.HandleFunc("/healthz", h.serveLive)
	server := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: h.healthMux}
	interrupts.ListenAndServe(server, 5*time.Second)
	return h
}

type ReadinessCheck func() bool

// ServeLive configures optional liveness checks for /healthz.
// If no checks are provided, /healthz continues to return OK.
func (h *Health) ServeLive(livenessChecks ...LivenessCheck) {
	h.livenessLock.Lock()
	defer h.livenessLock.Unlock()
	h.livenessChecks = append([]LivenessCheck(nil), livenessChecks...)
}

// ServeReady starts serving the readiness endpoint
func (h *Health) ServeReady(readinessChecks ...ReadinessCheck) {
	h.healthMux.HandleFunc("/healthz/ready", func(w http.ResponseWriter, r *http.Request) {
		for _, readinessCheck := range readinessChecks {
			if !readinessCheck() {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, "ReadinessCheck failed")
				return
			}
		}
		fmt.Fprint(w, "OK")
	})
}

func (h *Health) serveLive(w http.ResponseWriter, r *http.Request) {
	h.livenessLock.RLock()
	livenessChecks := append([]LivenessCheck(nil), h.livenessChecks...)
	h.livenessLock.RUnlock()
	for _, livenessCheck := range livenessChecks {
		if !livenessCheck() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "LivenessCheck failed")
			return
		}
	}
	fmt.Fprint(w, "OK")
}
