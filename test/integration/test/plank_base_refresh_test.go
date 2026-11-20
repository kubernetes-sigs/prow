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

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"
)

func TestQueuedPresubmitRefreshesBaseBeforePodCreation(t *testing.T) {
	ctx := context.Background()
	useFakeGitHubEndpoint(t, ctx)
	client, err := NewClients("", getClusterContext())
	if err != nil {
		t.Fatalf("create Kubernetes client: %v", err)
	}

	jobName := "queued-base-refresh-" + RandomString(t)[:10]
	blockerName := jobName + "-blocker"
	newJob := func(name string, state prowjobv1.ProwJobState) *prowjobv1.ProwJob {
		return &prowjobv1.ProwJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: defaultNamespace,
				Labels: map[string]string{
					kube.ProwJobIDLabel:    name,
					kube.ProwJobTypeLabel:  string(prowjobv1.PresubmitJob),
					kube.ProwJobAnnotation: jobName,
				},
				Annotations: map[string]string{kube.ProwJobAnnotation: jobName},
			},
			Spec: prowjobv1.ProwJobSpec{
				Agent:          prowjobv1.KubernetesAgent,
				Type:           prowjobv1.PresubmitJob,
				Cluster:        "default",
				Namespace:      testpodNamespace,
				Job:            jobName,
				MaxConcurrency: 1,
				Refs: &prowjobv1.Refs{
					Org: "kubernetes", Repo: "kubernetes", RepoLink: "https://github.com/kubernetes/kubernetes",
					BaseRef: "main", BaseSHA: "stale-base-sha",
					Pulls: []prowjobv1.Pull{{Number: 1, SHA: "pull-head-sha"}},
				},
				PodSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "localhost:5001/alpine", Command: []string{"sh", "-c", "echo done"}}}},
			},
			Status: prowjobv1.ProwJobStatus{State: state},
		}
	}

	blocker := newJob(blockerName, prowjobv1.PendingState)
	blocker.Spec.Refs.Pulls[0].Number = 2
	blocker.Spec.Refs.Pulls[0].SHA = "blocker-head-sha"
	target := newJob(jobName, prowjobv1.TriggeredState)
	t.Cleanup(func() {
		for _, pj := range []*prowjobv1.ProwJob{target, blocker} {
			if err := client.Delete(ctx, pj); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("delete ProwJob %s: %v", pj.Name, err)
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: pj.Name, Namespace: testpodNamespace}}
			if err := client.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("delete pod %s: %v", pj.Name, err)
			}
		}
	})

	zeroGrace := int64(0)
	blockerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: blockerName, Namespace: testpodNamespace, Labels: map[string]string{
			kube.CreatedByProw: "true", kube.ProwJobIDLabel: blockerName,
		}},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &zeroGrace,
			Containers: []corev1.Container{{
				Name: "test", Image: "localhost:5001/alpine", Command: []string{"/bin/sleep", "300"},
			}},
		},
	}
	if err := client.Create(ctx, blockerPod); err != nil {
		t.Fatalf("create blocker pod: %v", err)
	}
	if err := client.Create(ctx, blocker); err != nil {
		t.Fatalf("create concurrency blocker: %v", err)
	}
	if err := client.Create(ctx, target); err != nil {
		t.Fatalf("create queued presubmit: %v", err)
	}

	time.Sleep(2 * time.Second)
	var queued prowjobv1.ProwJob
	if err := client.Get(ctx, types.NamespacedName{Namespace: defaultNamespace, Name: target.Name}, &queued); err != nil {
		t.Fatalf("get queued presubmit: %v", err)
	}
	if queued.Spec.Refs.BaseSHA != "stale-base-sha" || queued.Status.State != prowjobv1.TriggeredState {
		t.Fatalf("job did not remain queued with its original SHA: state=%s baseSHA=%s", queued.Status.State, queued.Spec.Refs.BaseSHA)
	}
	queuedPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: testpodNamespace}}
	if err := client.Get(ctx, types.NamespacedName{Namespace: testpodNamespace, Name: target.Name}, queuedPod); err == nil {
		t.Fatal("Plank created a pod while the job was blocked by concurrency")
	} else if !apierrors.IsNotFound(err) {
		t.Fatalf("check for queued pod: %v", err)
	}

	if err := client.Delete(ctx, blocker); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("release concurrency blocker: %v", err)
	}

	if err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, types.NamespacedName{Namespace: defaultNamespace, Name: target.Name}, &queued); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return queued.Spec.Refs.BaseSHA == fakegithub.TestRef, nil
	}); err != nil {
		t.Fatalf("wait for persisted base SHA refresh: %v (last SHA: %s)", err, queued.Spec.Refs.BaseSHA)
	}

	var pod corev1.Pod
	if err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		err := client.Get(ctx, types.NamespacedName{Namespace: testpodNamespace, Name: target.Name}, &pod)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		t.Fatalf("wait for presubmit pod: %v", err)
	}
	var jobSpec string
	for _, env := range pod.Spec.Containers[0].Env {
		if env.Name == downwardapi.JobSpecEnv {
			jobSpec = env.Value
			break
		}
	}
	var spec downwardapi.JobSpec
	if err := json.Unmarshal([]byte(jobSpec), &spec); err != nil {
		t.Fatalf("decode pod JOB_SPEC: %v", err)
	}
	if spec.Refs == nil || spec.Refs.BaseSHA != fakegithub.TestRef {
		t.Fatalf("pod JOB_SPEC has stale base metadata: refs=%+v", spec.Refs)
	}
	t.Logf("queued ProwJob and pod use refreshed base SHA %q", spec.Refs.BaseSHA)
}

func useFakeGitHubEndpoint(t *testing.T, ctx context.Context) {
	t.Helper()

	restConfig, err := NewRestConfig("", getClusterContext())
	if err != nil {
		t.Fatalf("create REST config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		t.Fatalf("create Kubernetes clientset: %v", err)
	}
	deployments := clientset.AppsV1().Deployments(defaultNamespace)
	deployment, err := deployments.Get(ctx, "prow-controller-manager", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get prow-controller-manager deployment: %v", err)
	}
	originalArgs := append([]string(nil), deployment.Spec.Template.Spec.Containers[0].Args...)
	deployment.Spec.Template.Spec.Containers[0].Args = setGitHubEndpoint(deployment.Spec.Template.Spec.Containers[0].Args, "http://fakeghserver")
	updated, err := deployments.Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("configure fake GitHub endpoint: %v", err)
	}

	t.Cleanup(func() {
		deployment, err := deployments.Get(ctx, "prow-controller-manager", metav1.GetOptions{})
		if err != nil {
			t.Errorf("get prow-controller-manager deployment for restore: %v", err)
			return
		}
		deployment.Spec.Template.Spec.Containers[0].Args = originalArgs
		restored, err := deployments.Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			t.Errorf("restore prow-controller-manager args: %v", err)
			return
		}
		waitForProwControllerManagerRollout(t, ctx, clientset, restored.Generation)
	})
	waitForProwControllerManagerRollout(t, ctx, clientset, updated.Generation)
}

func setGitHubEndpoint(args []string, endpoint string) []string {
	for i, arg := range args {
		if strings.HasPrefix(arg, "--github-endpoint=") {
			args[i] = "--github-endpoint=" + endpoint
			return args
		}
	}
	return append(args, "--github-endpoint="+endpoint)
}

func waitForProwControllerManagerRollout(t *testing.T, ctx context.Context, clientset *kubernetes.Clientset, generation int64) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(ctx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		deployment, err := clientset.AppsV1().Deployments(defaultNamespace).Get(ctx, "prow-controller-manager", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		desired := int32(1)
		if deployment.Spec.Replicas != nil {
			desired = *deployment.Spec.Replicas
		}
		return deployment.Status.ObservedGeneration >= generation && deployment.Status.UpdatedReplicas == desired && deployment.Status.ReadyReplicas == desired, nil
	}); err != nil {
		t.Fatalf("wait for prow-controller-manager rollout: %v", err)
	}
}
