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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
)

// Mock REST API server to simulate scheduling response
func mockServer(response SchedulingResponse, statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(response)
	}))
}

func TestExternalSchedule(t *testing.T) {
	tests := []struct {
		name        string
		pj          *prowv1.ProwJob
		cache       map[string]*cacheEntry
		response    SchedulingResponse
		statusCode  int
		want        Result
		wantErr     bool
		mockCleanup bool
	}{
		{
			name: "cache hit, valid entry",
			pj: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{Job: "test-job"},
			},
			cache: map[string]*cacheEntry{
				"test-job": {
					r:         SchedulingResponse{Cluster: "cluster-1"},
					timestamp: time.Now(),
				},
			},
			response:    SchedulingResponse{Cluster: "cluster-1"},
			statusCode:  http.StatusOK,
			want:        Result{Cluster: "cluster-1"},
			wantErr:     false,
			mockCleanup: false,
		},
		{
			name: "cache miss, fetch from REST API",
			pj: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{Job: "test-job-2"},
			},
			cache:       map[string]*cacheEntry{},
			response:    SchedulingResponse{Cluster: "cluster-2"},
			statusCode:  http.StatusOK,
			want:        Result{Cluster: "cluster-2"},
			wantErr:     false,
			mockCleanup: true,
		},
		{
			name: "cache stale, fetch from REST API",
			pj: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{Job: "test-job-3"},
			},
			cache: map[string]*cacheEntry{
				"test-job-3": {
					r:         SchedulingResponse{Cluster: "cluster-3"},
					timestamp: time.Now().Add(-15 * time.Minute),
				},
			},
			response:    SchedulingResponse{Cluster: "cluster-4"},
			statusCode:  http.StatusOK,
			want:        Result{Cluster: "cluster-4"},
			wantErr:     false,
			mockCleanup: true,
		},
		{
			name: "allback to configured Spec.Cluster entry",
			pj: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{Job: "test-job-4", Cluster: "cluster-99"},
			},
			cache:       map[string]*cacheEntry{},
			response:    SchedulingResponse{},
			statusCode:  http.StatusInternalServerError,
			want:        Result{Cluster: "cluster-99"},
			wantErr:     false,
			mockCleanup: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mockServer(tt.response, tt.statusCode)
			defer server.Close()

			rbj := &External{
				cfg: config.ExternalScheduling{
					URL: server.URL,
					Cache: config.ExternalSchedulingCache{
						EntryTimeoutInterval: &metav1.Duration{Duration: 10 * time.Minute},
						CleanupInterval:      &metav1.Duration{Duration: 1 * time.Hour},
					},
				},
				cache:     tt.cache,
				timestamp: time.Now().Add(-3 * time.Hour), // To trigger cache cleanup if mockCleanup is true
				log:       logrus.NewEntry(logrus.New()),
			}

			got, err := rbj.Schedule(context.Background(), tt.pj)
			if (err != nil) != tt.wantErr {
				t.Errorf("external.Schedule() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("external.Schedule() = %v, want %v", got, tt.want)
			}

			if tt.mockCleanup && len(rbj.cache) > 0 {
				for key, entry := range rbj.cache {
					if time.Since(entry.timestamp) >= rbj.cfg.Cache.EntryTimeoutInterval.Duration {
						t.Errorf("Cache not cleaned up properly for key: %s", key)
					}
				}
			}
		})
	}
}
