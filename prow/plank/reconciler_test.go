/*
Copyright 2020 The Kubernetes Authors.

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

package plank

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/go-test/deep"
	"github.com/sirupsen/logrus"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sFake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8sTesting "k8s.io/client-go/testing"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllertest"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/io"
)

func TestAdd(t *testing.T) {
	ctrlruntimelog.SetLogger(zap.New(zap.UseDevMode(true)))
	const prowJobNamespace = "prowjobs"

	testCases := []struct {
		name                  string
		additionalSelector    string
		expectedError         string
		prowJob               metav1.Object
		pod                   metav1.Object
		expectedRequest       string
		expectPredicateDenied bool
	}{
		{
			name: "Prowjob with Kubernetes agent generates event",
			prowJob: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Namespace: prowJobNamespace, Name: "my-pj"},
				Spec:       prowv1.ProwJobSpec{Agent: prowv1.KubernetesAgent},
			},
			expectedRequest: prowJobNamespace + "/my-pj",
		},
		{
			name: "Prowjob without Kubernetes agent does not generate event",
			prowJob: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Namespace: prowJobNamespace, Name: "my-pj"},
				Spec:       prowv1.ProwJobSpec{Agent: prowv1.ProwJobAgent("my-other-agent")},
			},
			expectPredicateDenied: true,
		},
		{
			name: "ProwJob that is completed does not generate event",
			prowJob: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Namespace: prowJobNamespace, Name: "my-pj"},
				Spec:       prowv1.ProwJobSpec{Agent: prowv1.KubernetesAgent},
				Status:     prowv1.ProwJobStatus{CompletionTime: &metav1.Time{}},
			},
			expectPredicateDenied: true,
		},
		{
			name: "Pod generates event",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "my-pod",
					Labels: map[string]string{"created-by-prow": "true"},
				},
			},
			expectedRequest: prowJobNamespace + "/my-pod",
		},
		{
			name: "Pod without created-by-prow does not generate event",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-pod",
				},
			},
			expectPredicateDenied: true,
		},
		{
			name: "Pod that does match additionalSelector does generate event",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-pod",
					Labels: map[string]string{
						"created-by-prow": "true",
						"unicorn":         "true",
					},
				},
			},
			additionalSelector: "unicorn=true",
			expectedRequest:    prowJobNamespace + "/my-pod",
		},
		{
			name: "Pod that doesn't match additionalSelector does not generate event",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "my-pod",
					Labels: map[string]string{"created-by-prow": "true"},
				},
			},
			additionalSelector:    "unicorn=true",
			expectPredicateDenied: true,
		},
		{
			name:               "Invalid additionalSelector causes error",
			additionalSelector: ",",
			expectedError:      "failed to construct predicate: failed to parse label selector created-by-prow=true,,: found ',', expected: identifier after ','",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fakeProwJobInformer := &controllertest.FakeInformer{Synced: true}
			fakePodInformers := &controllertest.FakeInformer{Synced: true}

			prowJobInformerStarted := make(chan struct{})
			mgr, err := mgrFromFakeInformer(prowv1.SchemeGroupVersion.WithKind("ProwJob"), fakeProwJobInformer, prowJobInformerStarted)
			if err != nil {
				t.Fatalf("failed to construct mgr: %v", err)
			}
			podInformerStarted := make(chan struct{})
			buildMgr, err := mgrFromFakeInformer(corev1.SchemeGroupVersion.WithKind("Pod"), fakePodInformers, podInformerStarted)
			if err != nil {
				t.Fatalf("failed to construct mgr: %v", err)
			}
			buildMgrs := map[string]manager.Manager{"default": buildMgr}
			cfg := func() *config.Config {
				return &config.Config{ProwConfig: config.ProwConfig{ProwJobNamespace: prowJobNamespace}}
			}

			receivedRequestChan := make(chan string, 1)
			reconcile := func(_ context.Context, r reconcile.Request) (reconcile.Result, error) {
				receivedRequestChan <- r.String()
				return reconcile.Result{}, nil
			}
			predicateResultChan := make(chan bool, 1)
			predicateCallBack := func(b bool) {
				predicateResultChan <- !b
			}
			var errMsg string
			if err := add(mgr, buildMgrs, nil, cfg, nil, "", tc.additionalSelector, reconcile, predicateCallBack, 1); err != nil {
				errMsg = err.Error()
			}
			if errMsg != tc.expectedError {
				t.Fatalf("expected error %v got error %v", tc.expectedError, errMsg)
			}
			if errMsg != "" {
				return
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				if err := mgr.Start(ctx); err != nil {
					t.Errorf("failed to start main mgr: %v", err)
				}
			}()
			go func() {
				if err := buildMgrs["default"].Start(ctx); err != nil {
					t.Errorf("failed to start build mgr: %v", err)
				}
			}()
			if err := singnalOrTimout(prowJobInformerStarted); err != nil {
				t.Fatalf("failure waiting for prowJobInformer: %v", err)
			}
			if err := singnalOrTimout(podInformerStarted); err != nil {
				t.Fatalf("failure waiting for podInformer: %v", err)
			}

			if tc.prowJob != nil {
				fakeProwJobInformer.Add(tc.prowJob)
			}
			if tc.pod != nil {
				fakePodInformers.Add(tc.pod)
			}

			var receivedRequest string
			var predicateDenied bool
			func() {
				for {
					select {
					case receivedRequest = <-receivedRequestChan:
						return
					case predicateDenied = <-predicateResultChan:
						// Actual request has to pass through the workqueue first
						// so it might take an additional moment
						if predicateDenied {
							return
						}
						// This shouldn't take longer than a couple of millisec, but in
						// CI we might be CPU starved so be generous with the timeout
					case <-time.After(15 * time.Second):
						t.Fatal("timed out waiting for event")
					}
				}
			}()

			if tc.expectedRequest != receivedRequest {
				t.Errorf("expected request %q got request %q", tc.expectedRequest, receivedRequest)
			}
			if tc.expectPredicateDenied != predicateDenied {
				t.Errorf("expected predicate to deny: %t, got predicate denied: %t", tc.expectPredicateDenied, predicateDenied)
			}
		})
	}
}

func mgrFromFakeInformer(gvk schema.GroupVersionKind, fi *controllertest.FakeInformer, ready chan struct{}) (manager.Manager, error) {
	opts := manager.Options{
		NewClient: func(cache cache.Cache, config *rest.Config, options ctrlruntimeclient.Options, uncachedObjects ...ctrlruntimeclient.Object) (ctrlruntimeclient.Client, error) {
			return nil, nil
		},
		NewCache: func(_ *rest.Config, opts cache.Options) (cache.Cache, error) {
			return &informertest.FakeInformers{
				InformersByGVK: map[schema.GroupVersionKind]toolscache.SharedIndexInformer{gvk: &eventHandlerSignalingInformer{SharedIndexInformer: fi, signal: ready}},
				Synced:         &[]bool{true}[0],
			}, nil
		},
		MapperProvider: func(_ *rest.Config) (meta.RESTMapper, error) {
			return &meta.DefaultRESTMapper{}, nil
		},
		MetricsBindAddress: "0",
	}
	return manager.New(&rest.Config{}, opts)
}

type eventHandlerSignalingInformer struct {
	toolscache.SharedIndexInformer
	signal chan struct{}
}

func (ehsi *eventHandlerSignalingInformer) AddEventHandler(handler toolscache.ResourceEventHandler) {
	ehsi.SharedIndexInformer.AddEventHandler(handler)
	close(ehsi.signal)
}

func singnalOrTimout(signal <-chan struct{}) error {
	select {
	case <-signal:
		return nil
	case <-time.After(15 * time.Second):
		return errors.New("timed out")
	}
}

func TestProwJobIndexer(t *testing.T) {
	t.Parallel()
	const pjNS = "prowjobs"
	const pjName = "my-pj"
	const pjJobQueue = "pj-queue"
	pj := func(modify ...func(*prowv1.ProwJob)) *prowv1.ProwJob {
		pj := &prowv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: pjNS,
				Name:      "some-job",
			},
			Spec: prowv1.ProwJobSpec{
				Job:          pjName,
				JobQueueName: pjJobQueue,
				Agent:        prowv1.KubernetesAgent,
			},
			Status: prowv1.ProwJobStatus{
				State: prowv1.PendingState,
			},
		}
		for _, m := range modify {
			m(pj)
		}
		return pj
	}
	testCases := []struct {
		name     string
		modify   func(*prowv1.ProwJob)
		expected []string
	}{
		{
			name: "Matches all keys",
			expected: []string{
				prowJobIndexKeyAll,
				prowJobIndexKeyPending,
				pendingTriggeredIndexKeyByName(pjName),
				pendingTriggeredIndexKeyByJobQueueName(pjJobQueue),
			},
		},
		{
			name:   "Triggered goes into triggeredPending",
			modify: func(pj *prowv1.ProwJob) { pj.Status.State = prowv1.TriggeredState },
			expected: []string{
				prowJobIndexKeyAll,
				pendingTriggeredIndexKeyByName(pjName),
				pendingTriggeredIndexKeyByJobQueueName(pjJobQueue),
			},
		},
		{
			name:   "Wrong namespace, no key",
			modify: func(pj *prowv1.ProwJob) { pj.Namespace = "wrong" },
		},
		{
			name:   "Wrong agent, no key",
			modify: func(pj *prowv1.ProwJob) { pj.Spec.Agent = prowv1.TektonAgent },
		},
		{
			name:     "Success, matches only the `all` key",
			modify:   func(pj *prowv1.ProwJob) { pj.Status.State = prowv1.SuccessState },
			expected: []string{prowJobIndexKeyAll},
		},
		{
			name:   "Changing name changes pendingTriggeredIndexKeyByName index",
			modify: func(pj *prowv1.ProwJob) { pj.Spec.Job = "some-name" },
			expected: []string{
				prowJobIndexKeyAll,
				prowJobIndexKeyPending,
				pendingTriggeredIndexKeyByName("some-name"),
				pendingTriggeredIndexKeyByJobQueueName(pjJobQueue),
			},
		},
		{
			name:   "Changing job queue name changes pendingTriggeredIndexKeyByJobQueueName index",
			modify: func(pj *prowv1.ProwJob) { pj.Spec.JobQueueName = "some-name" },
			expected: []string{
				prowJobIndexKeyAll,
				prowJobIndexKeyPending,
				pendingTriggeredIndexKeyByName(pjName),
				pendingTriggeredIndexKeyByJobQueueName("some-name"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.modify == nil {
				tc.modify = func(_ *prowv1.ProwJob) {}
			}
			result := prowJobIndexer(pjNS)(pj(tc.modify))
			if diff := deep.Equal(result, tc.expected); diff != nil {
				t.Errorf("result differs from expected: %v", diff)
			}
		})
	}
}

// TestMaxConcurrencyConsidersCacheStaleness verifies that the reconciliation considers the fact
// that there is a delay between doing a change and observing it in the client for determining
// if another copy of a given job may be started.
// It:
// * Creates two runs of the same job that has a MaxConcurrency: 1 setting
// * Using a fake client that applies Patch operations with a delay but returns instantly
// * Reconciles them in parallel
// * Verifies that one of them gets a RequeueAfter: 1 second
// * Verifies that after the other one returns, its state is set to Pending, i.E. it blocked until it observed the state transition it made
// * Verifies that there is exactly one pod
func TestMaxConcurrencyConsidersCacheStaleness(t *testing.T) {
	testConcurrency := func(pja, pjb *prowv1.ProwJob) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			pjClient := &eventuallyConsistentClient{t: t, Client: fakectrlruntimeclient.NewFakeClient(pja, pjb)}

			cfg := func() *config.Config {
				return &config.Config{ProwConfig: config.ProwConfig{Plank: config.Plank{
					Controller: config.Controller{
						JobURLTemplate: &template.Template{},
					},
					JobQueueCapacities: map[string]int{"queue-1": 1},
				}}}
			}

			r := newReconciler(context.Background(), pjClient, nil, cfg, nil, "")
			r.buildClients = map[string]buildClient{pja.Spec.Cluster: buildClient{Client: fakectrlruntimeclient.NewFakeClient()}}

			wg := &sync.WaitGroup{}
			wg.Add(2)
			// Give capacity of two so this doesn't stuck the test if we have a bug that results in two reconcile afters
			gotReconcileAfter := make(chan struct{}, 2)

			startAsyncReconcile := func(pjName string) {
				go func() {
					defer wg.Done()
					result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: pjName}})
					if err != nil {
						t.Errorf("reconciliation of pj %s failed: %v", pjName, err)
					}
					if result.RequeueAfter == time.Second {
						gotReconcileAfter <- struct{}{}
						return
					}
					pj := &prowv1.ProwJob{}
					if err := r.pjClient.Get(context.Background(), types.NamespacedName{Name: pjName}, pj); err != nil {
						t.Errorf("failed to get prowjob %s after reconciliation: %v", pjName, err)
					}
					if pj.Status.State != prowv1.PendingState {
						t.Error("pj wasn't in pending state, reconciliation didn't wait the change to appear in the cache")
					}
				}()
			}
			startAsyncReconcile(pja.Name)
			startAsyncReconcile(pjb.Name)

			wg.Wait()
			close(gotReconcileAfter)

			var numReconcielAfters int
			for range gotReconcileAfter {
				numReconcielAfters++
			}
			if numReconcielAfters != 1 {
				t.Errorf("expected to get exactly one reconcileAfter, got %d", numReconcielAfters)
			}

			pods := &corev1.PodList{}
			if err := r.buildClients[pja.Spec.Cluster].List(context.Background(), pods); err != nil {
				t.Fatalf("failed to list pods: %v", err)
			}
			if n := len(pods.Items); n != 1 {
				t.Errorf("expected exactly one pod, got %d", n)
			}
		}
	}
	pja := &prowv1.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "a"},
		Spec: prowv1.ProwJobSpec{
			Type:           prowv1.PeriodicJob,
			Cluster:        "cluster",
			MaxConcurrency: 1,
			Job:            "max-1",
			PodSpec:        &corev1.PodSpec{Containers: []corev1.Container{{}}},
			Refs:           &prowv1.Refs{},
		},
		Status: prowv1.ProwJobStatus{
			State: prowv1.TriggeredState,
		},
	}
	pjb := pja.DeepCopy()
	pjb.Name = "b"

	t.Run("job level MaxConcurrency", testConcurrency(pja.DeepCopy(), pjb))

	pja.Spec.MaxConcurrency = 0
	pja.Spec.JobQueueName = "queue-1"
	pjb = pja.DeepCopy()
	pjb.Name = "b"
	pjb.Spec.Job = "max-1-same-queue"
	t.Run("queue level JobQueueCapacities", testConcurrency(pja, pjb))
}

// eventuallyConsistentClient executes patch and create  operations with a delay but instantly returns, before applying the change.
// This simulates the behaviour of a caching client where we can observe our change only after a delay.
type eventuallyConsistentClient struct {
	t *testing.T
	ctrlruntimeclient.Client
}

func (ecc *eventuallyConsistentClient) Patch(ctx context.Context, obj ctrlruntimeclient.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := ecc.Client.Patch(ctx, obj, patch, opts...); err != nil {
			ecc.t.Errorf("eventuallyConsistentClient failed to execute patch: %v", err)
		}
	}()

	return nil
}

func (ecc *eventuallyConsistentClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := ecc.Client.Create(ctx, obj, opts...); err != nil {
			ecc.t.Errorf("eventuallyConsistentClient failed to execute create: %v", err)
		}
	}()

	return nil
}

func TestStartPodBlocksUntilItHasThePodInCache(t *testing.T) {
	t.Parallel()
	r := &reconciler{
		log: logrus.NewEntry(logrus.New()),
		buildClients: map[string]buildClient{"default": buildClient{
			Client: &eventuallyConsistentClient{t: t, Client: fakectrlruntimeclient.NewFakeClient()}}},
		config: func() *config.Config { return &config.Config{} },
	}
	pj := &prowv1.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "name"},
		Spec: prowv1.ProwJobSpec{
			PodSpec: &corev1.PodSpec{Containers: []corev1.Container{{}}},
			Refs:    &prowv1.Refs{},
			Type:    prowv1.PeriodicJob,
		},
	}
	if _, _, err := r.startPod(context.Background(), pj); err != nil {
		t.Fatalf("startPod: %v", err)
	}
	if err := r.buildClients["default"].Get(context.Background(), types.NamespacedName{Name: "name"}, &corev1.Pod{}); err != nil {
		t.Errorf("couldn't get pod, this likely means startPod didn't block: %v", err)
	}
}

type fakeOpener struct {
	io.Opener
	strings.Builder
	signal chan<- bool
}

func (fo *fakeOpener) Writer(ctx context.Context, path string, opts ...io.WriterOptions) (io.WriteCloser, error) {
	fo.Reset()
	return fo, nil
}

func (fo *fakeOpener) Write(b []byte) (int, error) {
	n, err := fo.Builder.Write(b)
	fo.signal <- true
	return n, err
}

func (fo fakeOpener) Close() error {
	return nil
}

func TestSyncClusterStatus(t *testing.T) {
	tcs := []struct {
		name             string
		location         string
		statuses         map[string]ClusterStatus
		expectedStatuses map[string]ClusterStatus // This is set to statuses ^^ if unspecified.
		knownClusters    map[string]rest.Config
		noWriteExpected  bool
	}{
		{
			name:            "No location set, don't upload.",
			statuses:        map[string]ClusterStatus{"default": ClusterStatusReachable},
			knownClusters:   map[string]rest.Config{"default": rest.Config{}},
			noWriteExpected: true,
		},
		{
			name:          "Single cluster reachable",
			location:      "gs://my-bucket/build-cluster-statuses.json",
			statuses:      map[string]ClusterStatus{"default": ClusterStatusReachable},
			knownClusters: map[string]rest.Config{"default": rest.Config{}},
		},
		{
			name:             "Single cluster build manager creation failed",
			location:         "gs://my-bucket/build-cluster-statuses.json",
			expectedStatuses: map[string]ClusterStatus{"default": ClusterStatusNoManager},
			knownClusters:    map[string]rest.Config{"default": rest.Config{}},
		},
		{
			name:     "Multiple clusters mixed reachability",
			location: "gs://my-bucket/build-cluster-statuses.json",
			statuses: map[string]ClusterStatus{
				"default":                     ClusterStatusReachable,
				"test-infra-trusted":          ClusterStatusReachable,
				"cluster-error":               ClusterStatusError,
				"cluster-missing-permissions": ClusterStatusMissingPermissions,
			},
			expectedStatuses: map[string]ClusterStatus{
				"default":                     ClusterStatusReachable,
				"test-infra-trusted":          ClusterStatusReachable,
				"always-sad-build-cluster":    ClusterStatusNoManager,
				"cluster-error":               ClusterStatusError,
				"cluster-missing-permissions": ClusterStatusMissingPermissions,
			},
			knownClusters: map[string]rest.Config{
				"default":                     rest.Config{},
				"test-infra-trusted":          rest.Config{},
				"always-sad-build-cluster":    rest.Config{},
				"cluster-error":               rest.Config{},
				"cluster-missing-permissions": rest.Config{},
			},
		},
	}
	successfulFakeClient := &k8sFake.Clientset{}
	successfulFakeClient.Fake.AddReactor("create", "selfsubjectaccessreviews", func(action k8sTesting.Action) (handled bool, ret runtime.Object, err error) {
		r := &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{
				Allowed: true,
				Reason:  "Success!",
			},
		}
		return true, r, nil
	})

	erroringFakeClient := &k8sFake.Clientset{}
	erroringFakeClient.Fake.AddReactor("create", "selfsubjectaccessreviews", func(action k8sTesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, errors.New("could not create SelfSubjectAccessReview")

	})

	missingPermissionsFakeClient := &k8sFake.Clientset{}
	missingPermissionsFakeClient.Fake.AddReactor("create", "selfsubjectaccessreviews", func(action k8sTesting.Action) (handled bool, ret runtime.Object, err error) {
		r := &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{
				Allowed: false,
				Reason:  "Permissions missing!",
			},
		}
		return true, r, nil
	})

	// Whether the authz client runs successfully or not depends on the use of
	// the plain FakeAuthorizationV1 (always success) or erroringFakeAuthzClient
	// (always fail).
	for i := range tcs {
		tc := tcs[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := func() *config.Config {
				return &config.Config{ProwConfig: config.ProwConfig{Plank: config.Plank{BuildClusterStatusFile: tc.location}}}
			}

			clients := map[string]buildClient{}
			for alias, status := range tc.statuses {
				switch status {
				case ClusterStatusReachable:
					clients[alias] = buildClient{
						Client: fakectrlruntimeclient.NewFakeClient(),
						ssar:   successfulFakeClient.AuthorizationV1().SelfSubjectAccessReviews(),
					}
				case ClusterStatusError:
					clients[alias] = buildClient{
						Client: fakectrlruntimeclient.NewFakeClient(),
						ssar:   erroringFakeClient.AuthorizationV1().SelfSubjectAccessReviews(),
					}
				case ClusterStatusMissingPermissions:
					clients[alias] = buildClient{
						Client: fakectrlruntimeclient.NewFakeClient(),
						ssar:   missingPermissionsFakeClient.AuthorizationV1().SelfSubjectAccessReviews(),
					}
				}
			}
			// Test harness signals true to indicate completion of a write, false to indicate
			// completion of cluster status sync loop.
			signal := make(chan bool)
			opener := &fakeOpener{signal: signal}
			r := &reconciler{
				config:       cfg,
				log:          logrus.WithField("component", "prow-controller-manager"),
				buildClients: clients,
				opener:       opener,
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				r.syncClusterStatus(time.Millisecond, tc.knownClusters)(ctx)
				signal <- false
			}()
			if !tc.noWriteExpected {
				<-signal // Wait for the first write
			}
			// No need to sleep to confirm no write occurs, race detector should handle it.
			cancel()
			for running := range signal {
				if !running {
					break
				}
			}

			content := opener.String()
			if tc.noWriteExpected {
				if content != "" {
					t.Errorf("No write was expected, but found: %q.", opener.String())
				}
			} else {
				result := map[string]ClusterStatus{}
				if err := json.Unmarshal([]byte(opener.String()), &result); err != nil {
					t.Fatalf("Failed to unmarshal output: %v.", err)
				}
				expected := tc.expectedStatuses
				if expected == nil {
					expected = tc.statuses
				}
				if diff := deep.Equal(result, expected); diff != nil {
					t.Errorf("result differs from expected: %v", diff)
				}
			}
		})
	}
}
