/*
Copyright 2022 The Kubernetes Authors.

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
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/gangway"
	gangwayGoogleClient "sigs.k8s.io/prow/pkg/gangway/client/google"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/test/integration/internal/fakegitserver"
)

// TestGangway makes gRPC calls to gangway.
func TestGangway(t *testing.T) {

	const (
		UidLabel         = "integration-test/uid"
		ProwJobDecorated = `
presubmits:
  - name: trigger-inrepoconfig-presubmit-via-gangway%s
    always_run: false
    decorate: true
    spec:
      containers:
      - image: localhost:5001/alpine
        command:
        - sh
        args:
        - -c
        - |
          set -eu
          echo "hello from trigger-inrepoconfig-presubmit-via-gangway-repo%s"
          cat README.txt
`
	)

	// createGerritRepo creates Gerrit-style Git refs for changes (PRs). The
	// revision is always named "refs/changes/00/123/1" where 123 is the change
	// number (PR number) and 1 is the first revision (version) of the same
	// change.
	CreateRepo1 := createGerritRepo("TestGangway1", fmt.Sprintf(ProwJobDecorated, "1", "1"))

	tests := []struct {
		name          string
		repoSetups    []fakegitserver.RepoSetup
		projectNumber string
		msg           *gangway.CreateJobExecutionRequest
		want          string
	}{
		{
			name: "inrepoconfig-presubmit",
			repoSetups: []fakegitserver.RepoSetup{
				{
					Name:      "some/org/gangway-test-repo-1",
					Script:    CreateRepo1,
					Overwrite: true,
				},
			},
			projectNumber: "123",
			msg: &gangway.CreateJobExecutionRequest{
				JobName:          "trigger-inrepoconfig-presubmit-via-gangway1",
				JobExecutionType: gangway.JobExecutionType_PRESUBMIT,
				// Define where the job definition lives from inrepoconfig.
				Refs: &gangway.Refs{
					Org:      "https://fakegitserver.default/repo/some/org",
					Repo:     "gangway-test-repo-1",
					CloneUri: "https://fakegitserver.default/repo/some/org/gangway-test-repo-1",
					BaseRef:  "master",
					BaseSha:  "f1267354a7bbc5ce7d0458cdf4d0d36e8d35d8b3",
					Pulls: []*gangway.Pull{
						{
							Number: 1,
							Sha:    "458b96a96a74689447530035f5a71c426bacb505",
						},
					},
				},
				PodSpecOptions: &gangway.PodSpecOptions{
					Envs: map[string]string{
						"FOO_VAR": "value-of-foo-var",
					},
					Labels: map[string]string{
						kube.GerritRevision: "123",
					},
					Annotations: map[string]string{
						"foo_annotation": "value-of-foo-annotation",
					},
				},
			},
			want: `hello from trigger-inrepoconfig-presubmit-via-gangway-repo1
this-is-from-repoTestGangway1
`,
		},
		{
			name:          "mainconfig-periodic",
			projectNumber: "123",
			msg: &gangway.CreateJobExecutionRequest{
				JobName:          "trigger-mainconfig-periodic-via-gangway1",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
				PodSpecOptions: &gangway.PodSpecOptions{
					Envs: map[string]string{
						"FOO_VAR": "value-of-foo-var",
					},
					Labels: map[string]string{
						kube.GerritRevision: "123",
					},
					Annotations: map[string]string{
						"foo_annotation": "value-of-foo-annotation",
					},
				},
			},
			want: `hello from main config periodic
`,
		},
	}

	// Ensure that all repos are named uniquely, because otherwise they clobber
	// each other when we create them against fakegitserver. This prevents
	// programmer error when writing new tests.
	allRepoDirs := []string{}
	for _, tt := range tests {
		for _, repoSetup := range tt.repoSetups {

			allRepoDirs = append(allRepoDirs, repoSetup.Name)
		}
	}
	if err := enforceUniqueRepoDirs(allRepoDirs); err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {

			// Set up a connection to gangway.
			c, err := gangwayGoogleClient.NewInsecure(":32000", tt.projectNumber)
			if err != nil {
				t.Fatal(err)
			}

			defer c.Close()

			ctx := context.Background()
			ctx = c.EmbedProjectNumber(ctx)

			// Set up repos on FGS for just this test case.
			fgsClient := fakegitserver.NewClient("http://localhost/fakegitserver", 5*time.Second)
			for _, repoSetup := range tt.repoSetups {
				err := fgsClient.SetupRepo(repoSetup)
				if err != nil {
					t.Fatalf("FGS repo setup failed: %v", err)
				}
			}

			// Create a unique test case ID (UID) for this particular test
			// invocation. This makes it easier to check from this code whether
			// sub actually received the exact same message we just published.
			uid := RandomString(t)
			tt.msg.PodSpecOptions.Labels = make(map[string]string)
			tt.msg.PodSpecOptions.Labels[UidLabel] = uid

			// Use Prow API to create the job through gangway. This is a gRPC
			// call.
			jobExecution, err := c.GRPC.CreateJobExecution(ctx, tt.msg)
			if err != nil {
				t.Fatalf("Failed to create job execution: %v", err)
			}
			fmt.Println(jobExecution)

			// We expect the job to have succeeded.
			timeout := 120 * time.Second
			pollInterval := 500 * time.Millisecond
			expectedStatus := gangway.JobExecutionStatus_SUCCESS

			if err := c.WaitForJobExecutionStatus(ctx, jobExecution.Id, pollInterval, timeout, expectedStatus); err != nil {
				t.Fatal(err)
			} else {
				// Only clean up the ProwJob if it succeeded (save the ProwJob for debugging if it failed).
				cleanup(t, ctx)
			}
		})
	}
}

func TestGangwayBulkJobStatusChange(t *testing.T) {
	tests := []struct {
		name          string
		count         int
		expectedCount int
		expectedErr   error
		bulkMsg       *gangway.BulkJobStatusChangeRequest
		creationMsg   *gangway.CreateJobExecutionRequest
	}{
		{
			name:          "regular",
			count:         3,
			expectedCount: 3,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "large",
			count:         12,
			expectedCount: 12,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "cluster-fail",
			count:         3,
			expectedCount: 0,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				Cluster: "not-a-real-cluster",
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "cluster-success",
			count:         3,
			expectedCount: 3,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				Cluster: "default",
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "refs-fail",
			count:         3,
			expectedCount: 0,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				Refs: &gangway.Refs{
					Org:     "kubernetes",
					Repo:    "test-infra",
					BaseRef: "master",
					BaseSha: "thi5-1s-n0t-a-r34l-sh4",
				},
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-postsubmit",
				JobExecutionType: gangway.JobExecutionType_POSTSUBMIT,
				Refs: &gangway.Refs{
					Org:     "org1",
					Repo:    "repo1",
					BaseRef: "master",
					BaseSha: "thi5-1s-n0t-a-r34l-sh4",
				},
			},
		},
		{
			name:          "refs-success",
			count:         3,
			expectedCount: 3,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				Refs: &gangway.Refs{
					Org:     "org1",
					Repo:    "repo1",
					BaseRef: "master",
					BaseSha: "thi5-1s-n0t-a-r34l-sh4",
				},
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-postsubmit",
				JobExecutionType: gangway.JobExecutionType_POSTSUBMIT,
				Refs: &gangway.Refs{
					Org:     "org1",
					Repo:    "repo1",
					BaseRef: "master",
					BaseSha: "thi5-1s-n0t-a-r34l-sh4",
				},
			},
		},
		{
			name:          "startedBefore-fail",
			count:         3,
			expectedCount: 0,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				StartedBefore: timestamppb.New(time.Now().Add(-time.Hour)),
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "startedBefore-success",
			count:         3,
			expectedCount: 3,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				StartedBefore: timestamppb.New(time.Now().Add(time.Hour)),
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "startedAfter-fail",
			count:         3,
			expectedCount: 0,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				StartedAfter: timestamppb.New(time.Now().Add(time.Hour)),
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "startedAfter-success",
			count:         3,
			expectedCount: 3,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				StartedAfter: timestamppb.New(time.Now().Add(-time.Hour)),
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "startedCombined-fail",
			count:         3,
			expectedCount: 0,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				StartedBefore: timestamppb.New(time.Now().Add(-time.Hour)),
				StartedAfter:  timestamppb.New(time.Now().Add(time.Hour)),
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "startedCombined-success",
			count:         3,
			expectedCount: 3,
			expectedErr:   nil,
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
					Desired: gangway.JobExecutionStatus_ABORTED,
				},
				StartedBefore: timestamppb.New(time.Now().Add(time.Hour)),
				StartedAfter:  timestamppb.New(time.Now().Add(-time.Hour)),
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
		{
			name:          "expected error, no desired status",
			count:         3,
			expectedCount: 0,
			expectedErr:   status.Error(codes.InvalidArgument, "desired status is unspecified"),
			bulkMsg: &gangway.BulkJobStatusChangeRequest{
				JobStatusChange: &gangway.JobStatusChange{
					Current: gangway.JobExecutionStatus_PENDING,
				},
			},
			creationMsg: &gangway.CreateJobExecutionRequest{
				JobName:          "sleep-periodic",
				JobExecutionType: gangway.JobExecutionType_PERIODIC,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			c, err := gangwayGoogleClient.NewInsecure(":32000", "123")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()

			ctx := context.Background()
			ctx = c.EmbedProjectNumber(ctx)
			cleanup(t, ctx)
			for i := 0; i < tt.count; i++ {
				jobExecution, err := c.GRPC.CreateJobExecution(ctx, tt.creationMsg)
				if err != nil {
					t.Fatalf("Failed to create job execution: %v", err)
				}
				fmt.Println(jobExecution)

				// We expect the job to be pending.
				timeout := 120 * time.Second
				pollInterval := 500 * time.Millisecond
				expectedStatus := gangway.JobExecutionStatus_PENDING

				if err := c.WaitForJobExecutionStatus(ctx, jobExecution.Id, pollInterval, timeout, expectedStatus); err != nil {
					t.Fatal(err)
				}
			}

			pjs := getProwJobs(t, ctx)
			for _, pj := range pjs.Items {
				t.Logf("ProwJob: %s,  %s", pj.Name, pj.Status.State)
			}
			t.Logf("Created %d jobs", len(pjs.Items))

			jobsAffected, err := c.GRPC.BulkJobStatusChange(ctx, tt.bulkMsg)
			if tt.expectedErr != nil {
				if !errors.Is(err, tt.expectedErr) {
					t.Fatalf("Expected error %v, got %v", tt.expectedErr, err)
				}
			} else {
				if jobsAffected.Count != int32(tt.expectedCount) {
					t.Fatalf("Expected %d jobs to be affected, got %d", tt.expectedCount, jobsAffected.Count)
				}
				for _, job := range jobsAffected.JobExecutions {
					if job.JobStatus != gangway.JobExecutionStatus_ABORTED {
						t.Fatalf("Expected job status to be %q, got %q", gangway.JobExecutionStatus_ABORTED, job.JobStatus)
					}
					if job.JobType != tt.creationMsg.JobExecutionType {
						t.Fatalf("Expected job type to be %q, got %q", tt.creationMsg.JobExecutionType, job.JobType)
					}
					if job.JobName != tt.creationMsg.JobName {
						t.Fatalf("Expected job name to be %q, got %q", tt.creationMsg.JobName, job.JobName)
					}
				}
			}

			// Clean up all the ProwJobs created in this test.
			cleanup(t, ctx)
		})
	}

}

func cleanup(t *testing.T, ctx context.Context) {
	clusterContext := getClusterContext()
	t.Logf("Creating client for cluster: %s", clusterContext)
	restConfig, err := NewRestConfig("", clusterContext)
	if err != nil {
		t.Fatalf("could not create restConfig: %v", err)
	}
	kubeClient, err := ctrlruntimeclient.New(restConfig, ctrlruntimeclient.Options{})
	if err != nil {
		t.Fatalf("Failed creating clients for cluster %q: %v", clusterContext, err)
	}
	pjList := &prowjobv1.ProwJobList{}
	kubeClient.List(ctx, pjList, ctrlruntimeclient.MatchingLabels{})
	for _, pj := range pjList.Items {
		if err := kubeClient.Delete(ctx, &pj); err != nil {
			t.Logf("Failed cleanup resource %q: %v", pj.Name, err)
		}
	}
}

func getProwJobs(t *testing.T, ctx context.Context) *prowjobv1.ProwJobList {
	clusterContext := getClusterContext()
	restConfig, err := NewRestConfig("", clusterContext)
	if err != nil {
		t.Fatalf("could not create restConfig: %v", err)
	}
	kubeClient, err := ctrlruntimeclient.New(restConfig, ctrlruntimeclient.Options{})
	if err != nil {
		t.Fatalf("Failed creating clients for cluster %q: %v", clusterContext, err)
	}
	pjList := &prowjobv1.ProwJobList{}
	kubeClient.List(ctx, pjList, ctrlruntimeclient.MatchingLabels{})
	return pjList
}
