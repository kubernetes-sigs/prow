/*
Copyright 2016 The Kubernetes Authors.

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

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	corev1api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/kube"
)

const (
	maxProwJobAge    = 2 * 24 * time.Hour
	maxPodAge        = 12 * time.Hour
	terminatedPodTTL = 30 * time.Minute // must be less than maxPodAge
)

func newDefaultFakeSinkerConfig() config.Sinker {
	return config.Sinker{
		MaxProwJobAge:    &metav1.Duration{Duration: maxProwJobAge},
		MaxPodAge:        &metav1.Duration{Duration: maxPodAge},
		TerminatedPodTTL: &metav1.Duration{Duration: terminatedPodTTL},
	}
}

type fca struct {
	c *config.Config
}

func newFakeConfigAgent(s config.Sinker) *fca {
	return &fca{
		c: &config.Config{
			ProwConfig: config.ProwConfig{
				ProwJobNamespace: "ns",
				PodNamespace:     "ns",
				Sinker:           s,
			},
			JobConfig: config.JobConfig{
				Periodics: []config.Periodic{
					{JobBase: config.JobBase{Name: "retester"}},
				},
			},
		},
	}

}

func (f *fca) Config() *config.Config {
	return f.c
}

func startTime(s time.Time) *metav1.Time {
	start := metav1.NewTime(s)
	return &start
}

type unreachableCluster struct{ ctrlruntimeclient.Client }

func (unreachableCluster) Delete(_ context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	return fmt.Errorf("I can't hear you.")
}

func (unreachableCluster) List(_ context.Context, _ ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	return fmt.Errorf("I can't hear you.")
}

func (unreachableCluster) Patch(_ context.Context, _ ctrlruntimeclient.Object, _ ctrlruntimeclient.Patch, _ ...ctrlruntimeclient.PatchOption) error {
	return errors.New("I can't hear you.")
}

func TestClean(t *testing.T) {

	pods := []runtime.Object{
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-running-pod-failed",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-running",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-running-pod-succeeded",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-running",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-complete-pod-failed",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-complete",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-complete-pod-succeeded",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-complete",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-complete-pod-pending",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-complete",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodPending,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-unknown-pod-pending",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-unknown",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodPending,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-unknown-pod-failed",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-unknown",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-unknown-pod-succeeded",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-unknown",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-failed",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-succeeded",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-just-complete",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-pending",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodPending,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-pending-abort",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodPending,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-failed",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-10 * time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-running-no-pj",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodRunning,
				StartTime: startTime(time.Now().Add(-10 * time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-running",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodRunning,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unrelated-failed",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "not really",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unrelated-complete",
				Namespace: "ns",
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ttl-expired",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-terminatedPodTTL * 2)),
				ContainerStatuses: []corev1api.ContainerStatus{
					{
						State: corev1api.ContainerState{
							Terminated: &corev1api.ContainerStateTerminated{
								FinishedAt: metav1.Time{Time: time.Now().Add(-terminatedPodTTL).Add(-time.Second)},
							},
						},
					},
				},
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ttl-not-expired",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-terminatedPodTTL * 2)),
				ContainerStatuses: []corev1api.ContainerStatus{
					{
						State: corev1api.ContainerState{
							Terminated: &corev1api.ContainerStateTerminated{
								FinishedAt: metav1.Time{Time: time.Now().Add(-terminatedPodTTL).Add(-time.Second)},
							},
						},
					},
					{
						State: corev1api.ContainerState{
							Terminated: &corev1api.ContainerStateTerminated{
								FinishedAt: metav1.Time{Time: time.Now().Add(-time.Second)},
							},
						},
					},
				},
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "completed-prowjob-ttl-expired-while-pod-still-pending",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodPending,
				StartTime: startTime(time.Now().Add(-terminatedPodTTL * 2)),
				ContainerStatuses: []corev1api.ContainerStatus{
					{
						State: corev1api.ContainerState{
							Waiting: &corev1api.ContainerStateWaiting{
								Reason: "ImgPullBackoff",
							},
						},
					},
				},
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "completed-and-reported-prowjob-pod-still-has-kubernetes-finalizer",
				Namespace:  "ns",
				Finalizers: []string{"prow.x-k8s.io/gcsk8sreporter"},
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodPending,
				StartTime: startTime(time.Now().Add(-terminatedPodTTL * 2)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "completed-pod-without-prowjob-that-still-has-finalizer",
				Namespace:  "ns",
				Finalizers: []string{"prow.x-k8s.io/gcsk8sreporter"},
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodPending,
				StartTime: startTime(time.Now().Add(-terminatedPodTTL * 2)),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "very-young-orphaned-pod-is-kept-to-account-for-cache-staleness",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
				CreationTimestamp: metav1.Now(),
			},
		},
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				// The corresponding prowjob will only show up in a GET and not in a list requests. We do this to make
				// sure that the orphan check does another get on the prowjob before declaring a pod orphaned rather
				// than relying on the possibly outdated list created in the very beginning of the sync.
				Name:      "get-only-prowjob",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
		},
	}
	deletedPods := sets.New[string](
		"job-complete-pod-failed",
		"job-complete-pod-pending",
		"job-complete-pod-succeeded",
		"job-unknown-pod-failed",
		"job-unknown-pod-pending",
		"job-unknown-pod-succeeded",
		"new-running-no-pj",
		"old-failed",
		"old-succeeded",
		"old-pending-abort",
		"old-running",
		"ttl-expired",
		"completed-prowjob-ttl-expired-while-pod-still-pending",
		"completed-and-reported-prowjob-pod-still-has-kubernetes-finalizer",
		"completed-pod-without-prowjob-that-still-has-finalizer",
	)
	setComplete := func(d time.Duration) *metav1.Time {
		completed := metav1.NewTime(time.Now().Add(d))
		return &completed
	}
	prowJobs := []runtime.Object{
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-complete",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-running",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime: metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-failed",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-succeeded",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-just-complete",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime: metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-complete",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-incomplete",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime: metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-pending",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime: metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-pending-abort",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime: metav1.NewTime(time.Now().Add(-time.Second)),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "newer-periodic",
				Namespace: "ns",
			},
			Spec: prowv1.ProwJobSpec{
				Type: prowv1.PeriodicJob,
				Job:  "retester",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-failed",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime: metav1.NewTime(time.Now().Add(-time.Minute)),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "older-periodic",
				Namespace: "ns",
			},
			Spec: prowv1.ProwJobSpec{
				Type: prowv1.PeriodicJob,
				Job:  "retester",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Minute)),
				CompletionTime: setComplete(-time.Minute),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "oldest-periodic",
				Namespace: "ns",
			},
			Spec: prowv1.ProwJobSpec{
				Type: prowv1.PeriodicJob,
				Job:  "retester",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Hour)),
				CompletionTime: setComplete(-time.Hour),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-failed-trusted",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ttl-expired",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-terminatedPodTTL * 2)),
				CompletionTime: setComplete(-terminatedPodTTL - time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ttl-not-expired",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-terminatedPodTTL * 2)),
				CompletionTime: setComplete(-time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "completed-prowjob-ttl-expired-while-pod-still-pending",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-terminatedPodTTL * 2)),
				CompletionTime: setComplete(-terminatedPodTTL - time.Second),
			},
		},
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name: "completed-and-reported-prowjob-pod-still-has-kubernetes-finalizer",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:        metav1.NewTime(time.Now().Add(-terminatedPodTTL * 2)),
				CompletionTime:   setComplete(-terminatedPodTTL - time.Second),
				PrevReportStates: map[string]prowv1.ProwJobState{"gcsk8sreporter": prowv1.AbortedState},
			},
		},
	}

	deletedProwJobs := sets.New[string](
		"job-complete",
		"old-failed",
		"old-succeeded",
		"old-complete",
		"old-pending-abort",
		"older-periodic",
		"oldest-periodic",
		"old-failed-trusted",
	)
	podsTrusted := []runtime.Object{
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old-failed-trusted",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw: "true",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodFailed,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
	}
	deletedPodsTrusted := sets.New[string]("old-failed-trusted")

	fpjc := &clientWrapper{
		Client:          fakectrlruntimeclient.NewFakeClient(prowJobs...),
		getOnlyProwJobs: map[string]*prowv1.ProwJob{"ns/get-only-prowjob": {}},
	}
	fkc := []*podClientWrapper{
		{t: t, Client: fakectrlruntimeclient.NewFakeClient(pods...)},
		{t: t, Client: fakectrlruntimeclient.NewFakeClient(podsTrusted...)},
	}
	fpc := map[string]ctrlruntimeclient.Client{"unreachable": unreachableCluster{}}
	for idx, fakeClient := range fkc {
		fpc[strconv.Itoa(idx)] = &podClientWrapper{t: t, Client: fakeClient}
	}
	// Run
	c := controller{
		logger:        logrus.WithField("component", "sinker"),
		prowJobClient: fpjc,
		podClients:    fpc,
		config:        newFakeConfigAgent(newDefaultFakeSinkerConfig()).Config,
	}
	c.clean()
	assertSetsEqual(deletedPods, fkc[0].deletedPods, t, "did not delete correct Pods")
	assertSetsEqual(deletedPodsTrusted, fkc[1].deletedPods, t, "did not delete correct trusted Pods")

	remainingProwJobs := &prowv1.ProwJobList{}
	if err := fpjc.List(context.Background(), remainingProwJobs); err != nil {
		t.Fatalf("failed to get remaining prowjobs: %v", err)
	}
	actuallyDeletedProwJobs := sets.Set[string]{}
	for _, initalProwJob := range prowJobs {
		actuallyDeletedProwJobs.Insert(initalProwJob.(metav1.Object).GetName())
	}
	for _, remainingProwJob := range remainingProwJobs.Items {
		actuallyDeletedProwJobs.Delete(remainingProwJob.Name)
	}
	assertSetsEqual(deletedProwJobs, actuallyDeletedProwJobs, t, "did not delete correct ProwJobs")
}

func TestNotClean(t *testing.T) {

	pods := []runtime.Object{
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-complete-pod-succeeded",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-complete",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
	}
	podsExcluded := []runtime.Object{
		&corev1api.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-complete-pod-succeeded-on-excluded-cluster",
				Namespace: "ns",
				Labels: map[string]string{
					kube.CreatedByProw:  "true",
					kube.ProwJobIDLabel: "job-complete",
				},
			},
			Status: corev1api.PodStatus{
				Phase:     corev1api.PodSucceeded,
				StartTime: startTime(time.Now().Add(-maxPodAge).Add(-time.Second)),
			},
		},
	}
	setComplete := func(d time.Duration) *metav1.Time {
		completed := metav1.NewTime(time.Now().Add(d))
		return &completed
	}
	prowJobs := []runtime.Object{
		&prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-complete",
				Namespace: "ns",
			},
			Status: prowv1.ProwJobStatus{
				StartTime:      metav1.NewTime(time.Now().Add(-maxProwJobAge).Add(-time.Second)),
				CompletionTime: setComplete(-60 * time.Second),
			},
		},
	}

	deletedPods := sets.New[string](
		"job-complete-pod-succeeded",
	)

	fpjc := &clientWrapper{
		Client:          fakectrlruntimeclient.NewFakeClient(prowJobs...),
		getOnlyProwJobs: map[string]*prowv1.ProwJob{"ns/get-only-prowjob": {}},
	}
	podClientValid := podClientWrapper{
		t: t, Client: fakectrlruntimeclient.NewFakeClient(pods...),
	}
	podClientExcluded := podClientWrapper{
		t: t, Client: fakectrlruntimeclient.NewFakeClient(podsExcluded...),
	}
	fpc := map[string]ctrlruntimeclient.Client{
		"build-cluster-valid":    &podClientValid,
		"build-cluster-excluded": &podClientExcluded,
	}
	// Run
	fakeSinkerConfig := newDefaultFakeSinkerConfig()
	fakeSinkerConfig.ExcludeClusters = []string{"build-cluster-excluded"}
	fakeConfigAgent := newFakeConfigAgent(fakeSinkerConfig).Config
	c := controller{
		logger:        logrus.WithField("component", "sinker"),
		prowJobClient: fpjc,
		podClients:    fpc,
		config:        fakeConfigAgent,
	}
	c.clean()
	assertSetsEqual(deletedPods, podClientValid.deletedPods, t, "did not delete correct Pods")
	assertSetsEqual(sets.Set[string]{}, podClientExcluded.deletedPods, t, "did not delete correct Pods")
}

func assertSetsEqual(expected, actual sets.Set[string], t *testing.T, prefix string) {
	if expected.Equal(actual) {
		return
	}

	if missing := expected.Difference(actual); missing.Len() > 0 {
		t.Errorf("%s: missing expected: %v", prefix, sets.List(missing))
	}
	if extra := actual.Difference(expected); extra.Len() > 0 {
		t.Errorf("%s: found unexpected: %v", prefix, sets.List(extra))
	}
}

func TestFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     map[string]string
		del      sets.Set[string]
		expected func(*options)
		err      bool
	}{
		{
			name: "minimal flags work",
		},
		{
			name: "explicitly set --config-path",
			args: map[string]string{
				"--config-path": "/random/path",
			},
			expected: func(o *options) {
				o.config.ConfigPath = "/random/path"
			},
		},
		{
			name: "explicitly set --dry-run=false",
			args: map[string]string{
				"--dry-run": "false",
			},
			expected: func(o *options) {
			},
		},
		{
			name: "explicitly set --dry-run=true",
			args: map[string]string{
				"--dry-run": "true",
			},
			expected: func(o *options) {
				o.dryRun = true
			},
		},
		{
			name: "dry run defaults to true",
			args: map[string]string{},
			del:  sets.New[string]("--dry-run"),
			expected: func(o *options) {
				o.dryRun = true
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected := &options{
				config: configflagutil.ConfigOptions{
					ConfigPathFlagName:                    "config-path",
					JobConfigPathFlagName:                 "job-config-path",
					ConfigPath:                            "yo",
					SupplementalProwConfigsFileNameSuffix: "_prowconfig.yaml",
					InRepoConfigCacheSize:                 200,
				},
				dryRun:                 false,
				instrumentationOptions: flagutil.DefaultInstrumentationOptions(),
			}
			if tc.expected != nil {
				tc.expected(expected)
			}

			argMap := map[string]string{
				"--config-path": "yo",
				"--dry-run":     "false",
			}
			for k, v := range tc.args {
				argMap[k] = v
			}
			for k := range tc.del {
				delete(argMap, k)
			}

			var args []string
			for k, v := range argMap {
				args = append(args, k+"="+v)
			}
			fs := flag.NewFlagSet("fake-flags", flag.PanicOnError)
			actual := gatherOptions(fs, args...)
			switch err := actual.Validate(); {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive expected error")
			case !reflect.DeepEqual(*expected, actual):
				t.Errorf("\n%#v\n != expected \n%#v\n", actual, *expected)
			}
		})
	}
}

func TestDeletePodToleratesNotFound(t *testing.T) {
	m := &sinkerReconciliationMetrics{
		podsRemoved:      map[string]int{},
		podRemovalErrors: map[string]int{},
	}
	c := &controller{config: newFakeConfigAgent(newDefaultFakeSinkerConfig()).Config}
	l := logrus.NewEntry(logrus.New())
	pod := &corev1api.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing",
			Namespace: "ns",
			Labels: map[string]string{
				kube.CreatedByProw:  "true",
				kube.ProwJobIDLabel: "job-running",
			},
		},
	}
	client := fakectrlruntimeclient.NewFakeClient(pod)

	c.deletePod(l, &corev1api.Pod{}, "reason", client, m)
	c.deletePod(l, pod, "reason", client, m)

	if n := len(m.podRemovalErrors); n != 1 {
		t.Errorf("Expected 1 pod removal errors, got %v", m.podRemovalErrors)
	}
	if n := len(m.podsRemoved); n != 1 {
		t.Errorf("Expected 1 pod removal, got %v", m.podsRemoved)
	}
}

type podClientWrapper struct {
	t *testing.T
	ctrlruntimeclient.Client
	deletedPods sets.Set[string]
}

func (c *podClientWrapper) Delete(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	var pod corev1api.Pod
	name := types.NamespacedName{
		Namespace: obj.(metav1.Object).GetNamespace(),
		Name:      obj.(metav1.Object).GetName(),
	}
	if err := c.Get(ctx, name, &pod); err != nil {
		return err
	}
	// The kube api allows this but we want to ensure in tests that we first clean up finalizers before deleting a pod
	if len(pod.Finalizers) > 0 {
		c.t.Errorf("attempting to delete pod %s that still has %v finalizers", pod.Name, pod.Finalizers)
	}
	if err := c.Client.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	if c.deletedPods == nil {
		c.deletedPods = sets.Set[string]{}
	}
	c.deletedPods.Insert(pod.Name)
	return nil
}

type clientWrapper struct {
	ctrlruntimeclient.Client
	getOnlyProwJobs map[string]*prowv1.ProwJob
}

func (c *clientWrapper) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object) error {
	if pj, exists := c.getOnlyProwJobs[key.String()]; exists {
		*obj.(*prowv1.ProwJob) = *pj
		return nil
	}
	return c.Client.Get(ctx, key, obj)
}

func TestGetConfigMapSize(t *testing.T) {
	toplevel, err := os.MkdirTemp("", "job-config")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(toplevel)

	timestampDir := filepath.Join(toplevel, "..2024_01_01")
	err = os.Mkdir(timestampDir, 0750)
	if err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}

	// Create files inside timestampDir, just like how K8s does it.
	file1 := filepath.Join(timestampDir, "key1")
	if err := os.WriteFile(file1, []byte("val1"), 0666); err != nil {
		t.Fatal(err)
	}

	file2 := filepath.Join(timestampDir, "key2")
	if err := os.WriteFile(file2, []byte("val2"), 0666); err != nil {
		t.Fatal(err)
	}

	file3 := filepath.Join(timestampDir, "key3")
	if err := os.WriteFile(file3, []byte("val3"), 0666); err != nil {
		t.Fatal(err)
	}

	// Symlink ..data to point to timestampDir.
	dataDir := getDataDir(toplevel)
	os.Symlink(timestampDir, dataDir)

	// Create symlinks at the toplevel that point to files in ..data
	// (again, like how K8s does it).
	os.Symlink(filepath.Join(dataDir, "key1"), filepath.Join(toplevel, "key1"))
	os.Symlink(filepath.Join(dataDir, "key2"), filepath.Join(toplevel, "key2"))
	os.Symlink(filepath.Join(dataDir, "key3"), filepath.Join(toplevel, "key3"))

	gotBytes, err := getConfigMapSize(toplevel)
	if err != nil {
		t.Error(err)
	}

	// Expect 12 bytes, because the 3 files each have 4 bytes of content.
	if gotBytes != 12 {
		t.Errorf("expected 12 bytes but got %v", gotBytes)
	}
}

func TestGetConfigMapDirs(t *testing.T) {
	toplevel, err := os.MkdirTemp("", "job-config")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(toplevel)

	subdir1 := filepath.Join(toplevel, "part-1")
	if err := os.Mkdir(subdir1, 0750); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}

	subdir2 := filepath.Join(toplevel, "part-2")
	if err := os.Mkdir(subdir2, 0750); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}

	subdir3 := filepath.Join(toplevel, "part-3")
	if err := os.Mkdir(subdir3, 0750); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}

	expected := []string{subdir1, subdir2, subdir3}
	dirs, err := getConfigMapDirs(toplevel)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(dirs, expected); diff != "" {
		t.Fatal(diff)
	}

	// Now create a "..data" symlink inside the toplevel dir. We now expect
	// getConfigMapDirs() to only give us back the toplevel directory itself,
	// because it should see the "..data" and treat the toplevel directory as
	// the only ConfigMap-mounted directory (that it is not partitioned into
	// multiple ConfigMap-mounted subdirectories).
	//
	// For purposes of this test the target of the symlink doesn't matter (we
	// just check that getConfigMapDirs() sees the "..data" symlink and treats
	// the toplevel folder as a single ConfigMap). However, getConfigMapDirs()
	// uses os.Stat() (instead of os.Lstat()) so in order to pass/fail the
	// existence check, we have to create a valid symlink with a target that
	// exists.
	dataDir := getDataDir(toplevel)
	if err := os.Symlink(toplevel, dataDir); err != nil {
		t.Fatal(err)
	}

	expectedToplevelOnly := []string{toplevel}
	dirs, err = getConfigMapDirs(toplevel)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(dirs, expectedToplevelOnly); diff != "" {
		t.Fatal(diff)
	}
}
