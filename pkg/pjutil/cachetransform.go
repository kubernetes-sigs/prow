/*
Copyright The Kubernetes Authors.

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

package pjutil

import (
	corev1 "k8s.io/api/core/v1"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

// The transforms below are for informer caches of components that only observe
// ProwJobs and their pods. They keep the union of what those components read,
// so that adopting one does not need a bespoke field set, and they must stay
// idempotent: DeltaFIFO can hand back objects that already went through them.
// Do not use them where a cached object is read, modified and written back.

// TrimCachedProwJob drops the parts of a ProwJob that describe what it runs:
// the pod and pipeline run specs, the decoration config and the rerun
// configuration. ExtraRefs is kept, the exporter builds metric labels out of
// it. Objects that are not ProwJobs pass through untouched.
func TrimCachedProwJob(obj any) (any, error) {
	pj, ok := obj.(*prowapi.ProwJob)
	if !ok {
		return obj, nil
	}

	pj.ManagedFields = nil
	pj.OwnerReferences = nil

	pj.Spec.PodSpec = nil
	pj.Spec.DecorationConfig = nil
	pj.Spec.PipelineRunSpec = nil
	pj.Spec.TektonPipelineRunSpec = nil
	pj.Spec.RerunAuthConfig = nil
	pj.Spec.RerunCommand = ""

	return pj, nil
}

// TrimCachedPod drops everything but the metadata and Status.StartTime of a
// prow-created pod. Labels are kept, such pods are listed out of caches with a
// label selector. Objects that are not pods pass through untouched.
func TrimCachedPod(obj any) (any, error) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return obj, nil
	}

	pod.ManagedFields = nil
	pod.OwnerReferences = nil
	pod.Spec = corev1.PodSpec{}
	pod.Status = corev1.PodStatus{StartTime: pod.Status.StartTime}

	return pod, nil
}
