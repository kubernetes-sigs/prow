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

package kube

import (
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
)

// RateLimiter creates a ratelimiting queue for a given prow controller.
func RateLimiter[T comparable](controllerName string) workqueue.TypedRateLimitingInterface[T] {
	rl := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[T](5*time.Millisecond, 2*time.Minute),
		&workqueue.TypedBucketRateLimiter[T]{Limiter: rate.NewLimiter(rate.Limit(1000), 50000)},
	)
	return workqueue.NewTypedRateLimitingQueueWithConfig(rl, workqueue.TypedRateLimitingQueueConfig[T]{
		Name: controllerName,
	})
}
