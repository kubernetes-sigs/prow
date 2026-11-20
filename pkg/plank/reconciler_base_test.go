/*
Copyright 2026 The Kubernetes Authors.

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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"
	"sigs.k8s.io/prow/pkg/testutil"
)

func TestRefreshQueuedPresubmitBase(t *testing.T) {
	baseJob := func() *prowapi.ProwJob {
		return &prowapi.ProwJob{Spec: prowapi.ProwJobSpec{
			Type: prowapi.PresubmitJob,
			Refs: &prowapi.Refs{
				Org: "org", Repo: "repo", RepoLink: "https://github.com/org/repo",
				BaseRef: "main", BaseSHA: "old-sha", Pulls: []prowapi.Pull{{Number: 7}},
			},
		}}
	}

	tests := []struct {
		name        string
		job         *prowapi.ProwJob
		client      *fakeBaseRefClient
		wantUpdated bool
		wantErr     bool
		wantSHA     string
	}{
		{
			name:        "refreshes SHA and link",
			job:         baseJob(),
			client:      &fakeBaseRefClient{sha: "new-sha"},
			wantUpdated: true,
			wantSHA:     "new-sha",
		},
		{
			name:    "leaves current SHA alone",
			job:     baseJob(),
			client:  &fakeBaseRefClient{sha: "old-sha"},
			wantSHA: "old-sha",
		},
		{
			name: "skips non-presubmits", job: &prowapi.ProwJob{Spec: prowapi.ProwJobSpec{Type: prowapi.PeriodicJob}},
			client: &fakeBaseRefClient{sha: "new-sha"},
		},
		{
			name: "skips job without pull refs", job: &prowapi.ProwJob{Spec: prowapi.ProwJobSpec{Type: prowapi.PresubmitJob, Refs: &prowapi.Refs{BaseRef: "main"}}},
			client: &fakeBaseRefClient{sha: "new-sha"},
		},
		{
			name: "skips job without refs", job: &prowapi.ProwJob{Spec: prowapi.ProwJobSpec{Type: prowapi.PresubmitJob}},
			client: &fakeBaseRefClient{sha: "new-sha"},
		},
		{
			name: "skips when GitHub client is unavailable", job: baseJob(),
		},
		{
			name: "fails when GitHub lookup errors", job: baseJob(),
			client: &fakeBaseRefClient{err: errors.New("GitHub unavailable")}, wantErr: true, wantSHA: "old-sha",
		},
		{
			name: "rejects an empty SHA", job: baseJob(),
			client: &fakeBaseRefClient{}, wantErr: true, wantSHA: "old-sha",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &reconciler{log: logrus.NewEntry(logrus.New())}
			if test.client != nil {
				r.githubClient = test.client
			}
			updated, err := r.refreshQueuedPresubmitBase(test.job)
			if (err != nil) != test.wantErr {
				t.Fatalf("refreshQueuedPresubmitBase() error = %v, wantErr %t", err, test.wantErr)
			}
			if updated != test.wantUpdated {
				t.Errorf("refreshQueuedPresubmitBase() updated = %t, want %t", updated, test.wantUpdated)
			}
			if test.wantSHA != "" && test.job.Spec.Refs.BaseSHA != test.wantSHA {
				t.Errorf("BaseSHA = %q, want %q", test.job.Spec.Refs.BaseSHA, test.wantSHA)
			}
			if test.wantUpdated && test.job.Spec.Refs.BaseLink != "https://github.com/org/repo/commit/new-sha" {
				t.Errorf("BaseLink = %q, want link to refreshed SHA", test.job.Spec.Refs.BaseLink)
			}
			if test.client != nil && test.client.calls != 0 && (test.client.owner != "org" || test.client.repo != "repo" || test.client.ref != "heads/main") {
				t.Errorf("GetRef(%q, %q, %q), want org/repo:heads/main", test.client.owner, test.client.repo, test.client.ref)
			}
		})
	}
}

// This exercises the queue-to-pod path through Plank's reconciler and fake API
// clients. It verifies the existing ProwJob is updated before its pod is built.
func TestQueuedPresubmitPodUsesPersistedBaseSHA(t *testing.T) {
	ctx := context.Background()
	configAgent := newFakeConfigAgent(t, 0, nil)
	totServer := httptest.NewServer(http.HandlerFunc(handleTot))
	defer totServer.Close()

	pj := &prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "queued-presubmit", Namespace: "prowjobs", UID: types.UID("same-job-uid")},
		Spec: prowapi.ProwJobSpec{
			Agent: prowapi.KubernetesAgent,
			Job:   "test-e2e",
			Type:  prowapi.PresubmitJob,
			Refs: &prowapi.Refs{
				Org: "kubernetes", Repo: "kubernetes", RepoLink: "https://github.com/kubernetes/kubernetes",
				BaseRef: "main", BaseSHA: "old-base-sha",
				Pulls: []prowapi.Pull{{Number: 42, SHA: "pull-head-sha"}},
			},
			PodSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "test-image"}}},
		},
		Status: prowapi.ProwJobStatus{State: prowapi.TriggeredState},
	}
	fakeManager, err := testutil.NewFakeManager(ctx, []runtime.Object{pj}, func(ctx context.Context, indexer ctrlruntimeclient.FieldIndexer) error {
		return setupIndexes(ctx, indexer, configAgent.Config)
	})
	if err != nil {
		t.Fatalf("create fake manager: %v", err)
	}
	podClient := fakectrlruntimeclient.NewClientBuilder().Build()
	baseClient := &fakeBaseRefClient{sha: "new-base-sha"}
	r := &reconciler{
		pjClient:     fakeManager.GetClient(),
		githubClient: baseClient,
		buildClients: map[string]buildClient{prowapi.DefaultClusterAlias: {Client: podClient}},
		log:          logrus.NewEntry(logrus.New()),
		config:       configAgent.Config,
		totURL:       totServer.URL,
		clock:        clocktesting.NewFakeClock(time.Now()),
	}

	result, err := r.syncTriggeredJob(ctx, pj.DeepCopy())
	if err != nil {
		t.Fatalf("first reconciliation: %v", err)
	}
	if result == nil || result.RequeueAfter == 0 {
		t.Fatalf("first reconciliation should persist the refreshed base and requeue, got %v", result)
	}
	pods := &corev1.PodList{}
	if err := podClient.List(ctx, pods); err != nil {
		t.Fatalf("list pods before second reconciliation: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("created %d pod(s) before persisting the refreshed base", len(pods.Items))
	}

	var persisted prowapi.ProwJob
	key := types.NamespacedName{Namespace: pj.Namespace, Name: pj.Name}
	if err := fakeManager.GetClient().Get(ctx, key, &persisted); err != nil {
		t.Fatalf("get persisted ProwJob: %v", err)
	}
	if persisted.Name != pj.Name || persisted.UID != pj.UID || persisted.Spec.Refs.BaseSHA != "new-base-sha" {
		t.Fatalf("wrong ProwJob persisted: name=%q UID=%q refs=%+v", persisted.Name, persisted.UID, persisted.Spec.Refs)
	}
	if persisted.Spec.Refs.BaseLink != "https://github.com/kubernetes/kubernetes/commit/new-base-sha" {
		t.Errorf("persisted BaseLink = %q", persisted.Spec.Refs.BaseLink)
	}

	if _, err := r.syncTriggeredJob(ctx, &persisted); err != nil {
		t.Fatalf("second reconciliation: %v", err)
	}
	if err := podClient.List(ctx, pods); err != nil {
		t.Fatalf("list pods after second reconciliation: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("second reconciliation created %d pods, want 1", len(pods.Items))
	}
	var podJobSpec string
	for _, env := range pods.Items[0].Spec.Containers[0].Env {
		if env.Name == downwardapi.JobSpecEnv {
			podJobSpec = env.Value
			break
		}
	}
	var spec downwardapi.JobSpec
	if err := json.Unmarshal([]byte(podJobSpec), &spec); err != nil {
		t.Fatalf("decode pod JOB_SPEC: %v", err)
	}
	if spec.Refs == nil || spec.Refs.BaseSHA != "new-base-sha" {
		t.Fatalf("pod JOB_SPEC has stale base metadata: refs=%+v", spec.Refs)
	}
	var started prowapi.ProwJob
	if err := fakeManager.GetClient().Get(ctx, key, &started); err != nil {
		t.Fatalf("get started ProwJob: %v", err)
	}
	if started.Status.State != prowapi.PendingState || started.Spec.Refs.BaseSHA != "new-base-sha" {
		t.Fatalf("started ProwJob has stale vote metadata: state=%s baseSHA=%s", started.Status.State, started.Spec.Refs.BaseSHA)
	}
	if baseClient.calls != 2 {
		t.Errorf("GetRef called %d times, want once for each pod-free reconciliation", baseClient.calls)
	}
}

func TestQueuedPresubmitFailsWhenBaseLookupFails(t *testing.T) {
	ctx := context.Background()
	configAgent := newFakeConfigAgent(t, 0, nil)
	pj := &prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "queued-presubmit", Namespace: "prowjobs"},
		Spec: prowapi.ProwJobSpec{
			Agent: prowapi.KubernetesAgent,
			Job:   "test-e2e",
			Type:  prowapi.PresubmitJob,
			Refs: &prowapi.Refs{
				Org: "kubernetes", Repo: "kubernetes", BaseRef: "main", BaseSHA: "old-base-sha",
				Pulls: []prowapi.Pull{{Number: 42}},
			},
		},
		Status: prowapi.ProwJobStatus{State: prowapi.TriggeredState},
	}
	fakeManager, err := testutil.NewFakeManager(ctx, []runtime.Object{pj}, func(ctx context.Context, indexer ctrlruntimeclient.FieldIndexer) error {
		return setupIndexes(ctx, indexer, configAgent.Config)
	})
	if err != nil {
		t.Fatalf("create fake manager: %v", err)
	}
	podClient := fakectrlruntimeclient.NewClientBuilder().Build()
	r := &reconciler{
		pjClient:     fakeManager.GetClient(),
		githubClient: &fakeBaseRefClient{err: errors.New("GitHub unavailable")},
		buildClients: map[string]buildClient{prowapi.DefaultClusterAlias: {Client: podClient}},
		log:          logrus.NewEntry(logrus.New()),
		config:       configAgent.Config,
	}

	if _, err := r.syncTriggeredJob(ctx, pj.DeepCopy()); err != nil {
		t.Fatalf("reconciliation returned error instead of failing the ProwJob: %v", err)
	}
	var failed prowapi.ProwJob
	key := types.NamespacedName{Namespace: pj.Namespace, Name: pj.Name}
	if err := fakeManager.GetClient().Get(ctx, key, &failed); err != nil {
		t.Fatalf("get failed ProwJob: %v", err)
	}
	if failed.Status.State != prowapi.ErrorState || !failed.Complete() {
		t.Fatalf("ProwJob state=%s complete=%t, want terminal error", failed.Status.State, failed.Complete())
	}
	if !strings.Contains(failed.Status.Description, "GitHub unavailable") {
		t.Errorf("failure description %q does not explain the GitHub lookup error", failed.Status.Description)
	}
	pods := &corev1.PodList{}
	if err := podClient.List(ctx, pods); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Errorf("created %d pod(s) after the base lookup failed", len(pods.Items))
	}
}

type fakeBaseRefClient struct {
	github.Client
	sha              string
	err              error
	calls            int
	owner, repo, ref string
}

func (f *fakeBaseRefClient) GetRef(owner, repo, ref string) (string, error) {
	f.calls++
	f.owner, f.repo, f.ref = owner, repo, ref
	return f.sha, f.err
}
