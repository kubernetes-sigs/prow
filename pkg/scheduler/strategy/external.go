/*
Copyright 2024 The Kubernetes Authors.

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

package strategy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
)

// SchedulingRequest represents the incoming request structure
type SchedulingRequest struct {
	Job string `json:"job"`
}

// SchedulingResponse represents the response structure
type SchedulingResponse struct {
	Cluster string `json:"cluster"`
}

type cacheEntry struct {
	r         SchedulingResponse
	timestamp time.Time
}

// External is a strategy that schedules a ProwJob on a specific cluster
// based on the response of the ProwJob's Job name to given URL
type External struct {
	cfg       config.ExternalScheduling
	cache     map[string]*cacheEntry
	timestamp time.Time
	log       *logrus.Entry
}

// NewExternal creates a new External instance with caching
func NewExternal(cfg config.ExternalScheduling, log *logrus.Entry) *External {
	e := &External{
		cfg:   cfg,
		cache: make(map[string]*cacheEntry),
		log:   log,
	}
	return e
}

// query sends a POST request to the specified URL with the provided request body
// and returns the response as a SchedulingResponse object
func query(url string, reqBody SchedulingRequest) (SchedulingResponse, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return SchedulingResponse{}, fmt.Errorf("error marshaling JSON: %w", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return SchedulingResponse{}, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return SchedulingResponse{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var respBody SchedulingResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return SchedulingResponse{}, fmt.Errorf("error decoding response: %w", err)
	}

	return respBody, nil
}

// Schedule checks the cache for a valid response or queries the REST API if the cache is stale
func (e *External) Schedule(_ context.Context, pj *prowv1.ProwJob) (Result, error) {
	if time.Since(e.timestamp) > e.cfg.Cache.CleanupInterval.Duration {
		e.cleanupCache()
	}
	entry, found := e.cache[pj.Spec.Job]

	if found && time.Since(entry.timestamp) < e.cfg.Cache.EntryTimeoutInterval.Duration {
		return Result(entry.r), nil
	}

	resp, err := query(e.cfg.URL, SchedulingRequest{Job: pj.Spec.Job})
	if err != nil {
		e.log.WithField("job", pj.Spec.Job).WithField("cluster", pj.Spec.Cluster).Warn("scheduling failed, using Spec.Cluster entry")
		return Result{Cluster: pj.Spec.Cluster}, nil
	}

	e.cache[pj.Spec.Job] = &cacheEntry{
		r:         resp,
		timestamp: time.Now(),
	}
	return Result(resp), nil
}

// cleanupCache removes stale entries from the cache
func (e *External) cleanupCache() {
	e.timestamp = time.Now()
	for key, entry := range e.cache {
		if time.Since(entry.timestamp) >= e.cfg.Cache.EntryTimeoutInterval.Duration {
			delete(e.cache, key)
		}
	}
}
