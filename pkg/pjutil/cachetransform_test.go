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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/kube"
)

func fullProwJob() *prowapi.ProwJob {
	started := metav1.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	completed := metav1.Date(2024, 1, 1, 0, 10, 0, 0, time.UTC)
	return &prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "some-job",
			Namespace:       "ns",
			Labels:          map[string]string{kube.ReRunLabel: "2"},
			ManagedFields:   []metav1.ManagedFieldsEntry{{Manager: "prow"}},
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
		Spec: prowapi.ProwJobSpec{
			Type:    prowapi.PeriodicJob,
			Job:     "some-periodic",
			Context: "some-context",
			PodSpec: &corev1.PodSpec{Containers: []corev1.Container{{Image: "img"}}},
			DecorationConfig: &prowapi.DecorationConfig{
				GCSConfiguration: &prowapi.GCSConfiguration{Bucket: "bucket"},
			},
			TektonPipelineRunSpec: &prowapi.TektonPipelineRunSpec{},
			ExtraRefs:             []prowapi.Refs{{Org: "org"}},
			RerunAuthConfig:       &prowapi.RerunAuthConfig{AllowAnyone: true},
			RerunCommand:          "/test all",
		},
		Status: prowapi.ProwJobStatus{
			State:            prowapi.SuccessState,
			StartTime:        started,
			CompletionTime:   &completed,
			BuildID:          "12345",
			PrevReportStates: map[string]prowapi.ProwJobState{"gcsk8sreporter": prowapi.SuccessState},
		},
	}
}

func fullPod() *corev1.Pod {
	// Fixed times: fake clients round-trip pods through JSON, truncating to seconds.
	created := metav1.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	started := metav1.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "some-pod",
			Namespace:         "ns",
			CreationTimestamp: created,
			Labels: map[string]string{
				kube.CreatedByProw:  "true",
				kube.ProwJobIDLabel: "some-job",
			},
			Finalizers:      []string{"prow.x-k8s.io/gcsk8sreporter"},
			ManagedFields:   []metav1.ManagedFieldsEntry{{Manager: "plank"}},
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "test", Image: "img"}},
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodSucceeded,
			StartTime:         &started,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "test"}},
		},
	}
}

func TestTrimCachedProwJob(t *testing.T) {
	trimmed, err := TrimCachedProwJob(fullProwJob())
	if err != nil {
		t.Fatalf("TrimCachedProwJob failed: %v", err)
	}
	pj, ok := trimmed.(*prowapi.ProwJob)
	if !ok {
		t.Fatalf("expected a *prowapi.ProwJob, got %T", trimmed)
	}

	// Kept:
	original := fullProwJob()
	if pj.Name != original.Name || pj.Namespace != original.Namespace {
		t.Errorf("identity was changed: %s/%s", pj.Namespace, pj.Name)
	}
	if diff := cmp.Diff(original.Labels, pj.Labels); diff != "" {
		t.Errorf("labels were changed: %s", diff)
	}
	if diff := cmp.Diff(original.Status, pj.Status); diff != "" {
		t.Errorf("status was changed: %s", diff)
	}
	if pj.Spec.Type != original.Spec.Type || pj.Spec.Job != original.Spec.Job || pj.Spec.Context != original.Spec.Context {
		t.Errorf("the job identity in the spec was changed: %+v", pj.Spec)
	}
	// The exporter needs these for metric labels.
	if diff := cmp.Diff(original.Spec.ExtraRefs, pj.Spec.ExtraRefs); diff != "" {
		t.Errorf("extra refs were changed: %s", diff)
	}

	// Dropped:
	if pj.ManagedFields != nil || pj.OwnerReferences != nil {
		t.Error("metadata was not trimmed")
	}
	if pj.Spec.PodSpec != nil || pj.Spec.DecorationConfig != nil || pj.Spec.TektonPipelineRunSpec != nil {
		t.Error("spec was not trimmed")
	}
	if pj.Spec.RerunAuthConfig != nil || pj.Spec.RerunCommand != "" {
		t.Error("spec was not trimmed")
	}

	// Must be idempotent.
	retrimmed, err := TrimCachedProwJob(pj)
	if err != nil {
		t.Fatalf("TrimCachedProwJob failed on an already trimmed job: %v", err)
	}
	if diff := cmp.Diff(pj, retrimmed); diff != "" {
		t.Errorf("TrimCachedProwJob is not idempotent: %s", diff)
	}

	// Non-ProwJobs pass through.
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "some-pod"}}
	out, err := TrimCachedProwJob(pod)
	if err != nil {
		t.Fatalf("TrimCachedProwJob failed on a pod: %v", err)
	}
	if diff := cmp.Diff(pod, out); diff != "" {
		t.Errorf("a non-ProwJob was modified: %s", diff)
	}
}

func TestTrimCachedPod(t *testing.T) {
	trimmed, err := TrimCachedPod(fullPod())
	if err != nil {
		t.Fatalf("TrimCachedPod failed: %v", err)
	}
	pod, ok := trimmed.(*corev1.Pod)
	if !ok {
		t.Fatalf("expected a *corev1.Pod, got %T", trimmed)
	}

	// Kept:
	original := fullPod()
	if pod.Name != original.Name || pod.Namespace != original.Namespace {
		t.Errorf("identity was changed: %s/%s", pod.Namespace, pod.Name)
	}
	if diff := cmp.Diff(original.Labels, pod.Labels); diff != "" {
		t.Errorf("labels were changed: %s", diff)
	}
	if diff := cmp.Diff(original.Finalizers, pod.Finalizers); diff != "" {
		t.Errorf("finalizers were changed: %s", diff)
	}
	if !pod.CreationTimestamp.Equal(&original.CreationTimestamp) {
		t.Error("creation timestamp was dropped")
	}
	if pod.Status.StartTime == nil || !pod.Status.StartTime.Equal(original.Status.StartTime) {
		t.Error("status.startTime was dropped")
	}

	// Dropped:
	if pod.ManagedFields != nil || pod.OwnerReferences != nil {
		t.Error("metadata was not trimmed")
	}
	if diff := cmp.Diff(corev1.PodSpec{}, pod.Spec); diff != "" {
		t.Errorf("spec was not trimmed: %s", diff)
	}
	if pod.Status.Phase != "" || pod.Status.ContainerStatuses != nil {
		t.Error("status was not trimmed")
	}

	// Must be idempotent.
	retrimmed, err := TrimCachedPod(pod)
	if err != nil {
		t.Fatalf("TrimCachedPod failed on an already trimmed pod: %v", err)
	}
	if diff := cmp.Diff(pod, retrimmed); diff != "" {
		t.Errorf("TrimCachedPod is not idempotent: %s", diff)
	}

	// Non-pods pass through.
	pj := &prowapi.ProwJob{ObjectMeta: metav1.ObjectMeta{Name: "some-job"}}
	out, err := TrimCachedPod(pj)
	if err != nil {
		t.Fatalf("TrimCachedPod failed on a prowjob: %v", err)
	}
	if diff := cmp.Diff(pj, out); diff != "" {
		t.Errorf("a non-pod was modified: %s", diff)
	}
}
