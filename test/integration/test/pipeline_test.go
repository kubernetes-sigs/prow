/*
Copyright 2025 The Kubernetes Authors.

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
	"fmt"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

func TestPipelineController(t *testing.T) {
	t.Parallel()

	clusterContext := getClusterContext()
	t.Logf("Creating client for cluster: %s", clusterContext)

	kubeClient, err := NewClients("", clusterContext)
	if err != nil {
		t.Fatalf("Failed creating clients for cluster %q: %v", clusterContext, err)
	}

	tests := []struct {
		name          string
		prowJobState  prowjobv1.ProwJobState
		expectSuccess bool
	}{
		{
			name:          "successful-pipelinerun",
			prowJobState:  prowjobv1.TriggeredState,
			expectSuccess: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			jobName := fmt.Sprintf("pl-%s", RandomString(t)[:10])

			t.Logf("Creating ProwJob: %s", jobName)
			prowjob := prowjobv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{
					Name:      jobName,
					Namespace: defaultNamespace,
					Labels: map[string]string{
						"created-by-prow":  "true",
						"prow.k8s.io/type": "presubmit",
						"prow.k8s.io/job":  jobName,
					},
					Annotations: map[string]string{
						"prow.k8s.io/job": jobName,
					},
				},
				Spec: prowjobv1.ProwJobSpec{
					Type:      prowjobv1.PresubmitJob,
					Agent:     prowjobv1.TektonAgent,
					Cluster:   "default",
					Namespace: "test-pods",
					Job:       jobName,
					Refs: &prowjobv1.Refs{
						Org:     "fake-org",
						Repo:    "fake-repo",
						BaseRef: "master",
						BaseSHA: "49e0442008f963eb77963213b85fb53c345e0632",
						Pulls: []prowjobv1.Pull{
							{
								Author: "fake_author",
								Number: 1,
								SHA:    uuid.New().String(),
							},
						},
					},
					PipelineRunSpec: &pipelinev1.PipelineRunSpec{
						PipelineSpec: &pipelinev1.PipelineSpec{
							Tasks: []pipelinev1.PipelineTask{
								{
									Name: "test",
									TaskRef: &pipelinev1.TaskRef{
										Name: "test-task",
									},
								},
							},
						},
					},
				},
				Status: prowjobv1.ProwJobStatus{
					State:     tt.prowJobState,
					StartTime: v1.NewTime(time.Now()),
				},
			}

			t.Cleanup(func() {
				if err := kubeClient.Delete(ctx, &prowjob); err != nil {
					t.Logf("Failed cleanup prowjob %q: %v", prowjob.Name, err)
				}
			})

			t.Logf("Creating prowjob: %s", jobName)
			if err := kubeClient.Create(ctx, &prowjob); err != nil {
				t.Fatalf("Failed creating prowjob: %v", err)
			}
			t.Logf("Finished creating prowjob: %s", jobName)

			// Wait for pipeline controller to create PipelineRun
			t.Logf("Waiting for PipelineRun to be created by pipeline controller")
			var pipelineRun pipelinev1.PipelineRun
			if err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				pipelineRunList := &pipelinev1.PipelineRunList{}
				if err := kubeClient.List(ctx, pipelineRunList, &ctrlruntimeclient.ListOptions{
					Namespace: "test-pods",
				}); err != nil {
					return false, fmt.Errorf("failed listing pipelineruns: %w", err)
				}

				for _, pr := range pipelineRunList.Items {
					if pr.Labels["prow.k8s.io/id"] == jobName {
						pipelineRun = pr
						t.Logf("Found PipelineRun: %s", pr.Name)
						return true, nil
					}
				}
				return false, nil
			}); err != nil {
				t.Fatalf("Failed waiting for PipelineRun to be created: %v", err)
			}

			// Verify PipelineRun has correct labels
			if pipelineRun.Labels["prow.k8s.io/job"] != jobName {
				t.Errorf("PipelineRun has incorrect job label: got %q, want %q",
					pipelineRun.Labels["prow.k8s.io/job"], jobName)
			}

			if pipelineRun.Labels["prow.k8s.io/type"] != "presubmit" {
				t.Errorf("PipelineRun has incorrect type label: got %q, want %q",
					pipelineRun.Labels["prow.k8s.io/type"], "presubmit")
			}

			// Verify PipelineRun is in the correct namespace
			if pipelineRun.Namespace != "test-pods" {
				t.Errorf("PipelineRun is in incorrect namespace: got %q, want %q",
					pipelineRun.Namespace, "test-pods")
			}

			t.Logf("Pipeline controller successfully created PipelineRun: %s", pipelineRun.Name)
		})
	}
}

func TestPipelineControllerConcurrentJobs(t *testing.T) {
	t.Parallel()

	clusterContext := getClusterContext()
	t.Logf("Creating client for cluster: %s", clusterContext)

	kubeClient, err := NewClients("", clusterContext)
	if err != nil {
		t.Fatalf("Failed creating clients for cluster %q: %v", clusterContext, err)
	}

	ctx := context.Background()
	numJobs := 5

	t.Logf("Creating %d concurrent ProwJobs", numJobs)
	jobNames := make([]string, numJobs)

	for i := 0; i < numJobs; i++ {
		// Use shorter job name to stay under 63 character Kubernetes label limit
		jobName := fmt.Sprintf("plc-%d-%s", i, RandomString(t)[:8])
		jobNames[i] = jobName

		prowjob := prowjobv1.ProwJob{
			ObjectMeta: v1.ObjectMeta{
				Name:      jobName,
				Namespace: defaultNamespace,
				Labels: map[string]string{
					"created-by-prow":  "true",
					"prow.k8s.io/type": "periodic",
					"prow.k8s.io/job":  jobName,
				},
				Annotations: map[string]string{
					"prow.k8s.io/job": jobName,
				},
			},
			Spec: prowjobv1.ProwJobSpec{
				Type:      prowjobv1.PeriodicJob,
				Agent:     prowjobv1.TektonAgent,
				Cluster:   "default",
				Namespace: "test-pods",
				Job:       jobName,
				PipelineRunSpec: &pipelinev1.PipelineRunSpec{
					PipelineSpec: &pipelinev1.PipelineSpec{
						Tasks: []pipelinev1.PipelineTask{
							{
								Name: "test",
								TaskRef: &pipelinev1.TaskRef{
									Name: "test-task",
								},
							},
						},
					},
				},
			},
			Status: prowjobv1.ProwJobStatus{
				State:     prowjobv1.TriggeredState,
				StartTime: v1.NewTime(time.Now()),
			},
		}

		t.Cleanup(func() {
			if err := kubeClient.Delete(ctx, &prowjob); err != nil {
				t.Logf("Failed cleanup prowjob %q: %v", prowjob.Name, err)
			}
		})

		if err := kubeClient.Create(ctx, &prowjob); err != nil {
			t.Fatalf("Failed creating prowjob %d: %v", i, err)
		}
		t.Logf("Created prowjob %d: %s", i, jobName)
	}

	// Wait for all PipelineRuns to be created
	t.Logf("Waiting for %d PipelineRuns to be created", numJobs)
	createdCount := 0
	if err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		pipelineRunList := &pipelinev1.PipelineRunList{}
		if err := kubeClient.List(ctx, pipelineRunList, &ctrlruntimeclient.ListOptions{
			Namespace: "test-pods",
		}); err != nil {
			return false, fmt.Errorf("failed listing pipelineruns: %w", err)
		}

		createdCount = 0
		for _, jobName := range jobNames {
			for _, pr := range pipelineRunList.Items {
				if pr.Labels["prow.k8s.io/id"] == jobName {
					createdCount++
					break
				}
			}
		}

		t.Logf("PipelineRuns created: %d/%d", createdCount, numJobs)
		return createdCount == numJobs, nil
	}); err != nil {
		t.Fatalf("Failed waiting for all PipelineRuns to be created: got %d/%d, error: %v",
			createdCount, numJobs, err)
	}

	t.Logf("Successfully verified pipeline controller created %d concurrent PipelineRuns", numJobs)
}
