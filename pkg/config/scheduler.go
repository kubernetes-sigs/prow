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

package config

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type Scheduler struct {
	Enabled bool `json:"enabled,omitempty"`

	// Scheduling strategies
	Failover *FailoverScheduling `json:"failover,omitempty"`
	External *ExternalScheduling `json:"external,omitempty"`
}

// FailoverScheduling is a configuration for the Failover scheduling strategy
type FailoverScheduling struct {
	// ClusterMappings maps a cluster to another one. It is used when we
	// want to schedule a ProJob to a cluster other than the one it was
	// configured to in the first place.
	ClusterMappings map[string]string `json:"mappings,omitempty"`
}

// ExternalSchedulingCache is a cache configuration for the external
// scheduling strategy
type ExternalSchedulingCache struct {
	// EntryTimeoutInterval is the interval to consider an entry in the cache
	EntryTimeoutInterval *metav1.Duration `json:"entry_timeout_interval,omitempty"`
	// CacheCleanupInterval is the interval to clean up the cache
	CleanupInterval *metav1.Duration `json:"cleanup_interval,omitempty"`
}

// ExternalScheduling is a configuration for the external scheduling strategy
// querying by prowjob job field
type ExternalScheduling struct {
	// URL is the URL of the REST API to query for the cluster assigned to prowjob
	URL string `json:"url,omitempty"`
	// Cache is the cache configuration for the external scheduling strategy
	Cache ExternalSchedulingCache `json:"cache,omitempty"`
}
