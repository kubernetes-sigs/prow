/*
Copyright 2018 The Kubernetes Authors.

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

package metadata

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"

	k8sreporter "k8s.io/test-infra/prow/crier/reporters/gcs/kubernetes"
	"k8s.io/test-infra/prow/spyglass/api"
	"k8s.io/test-infra/prow/spyglass/lenses"
	"k8s.io/test-infra/prow/spyglass/lenses/fake"
)

type FakeArtifact = fake.Artifact

// TestCheckTimestamps checks the way in which the started.json and
// finished.json files affect the view. For example, a negative duration should
// result in a warning for the user.
func TestCheckTimestamps(t *testing.T) {
	startedJson := &FakeArtifact{
		Path:    "started.json",
		Content: []byte(`{"timestamp":1676610469}`),
	}
	// This timestamp is *after* the one in startedJson. This is the happy path.
	finishedJsonNormal := &FakeArtifact{
		Path:    "finished.json",
		Content: []byte(`{"timestamp":1676611469,"passed":true,"result":"SUCCESS"}`),
	}
	// This timestamp is *before* the one in startedJson.
	finishedJsonNegative := &FakeArtifact{
		Path:    "finished.json",
		Content: []byte(`{"timestamp":1671827322,"passed":true,"result":"SUCCESS"}`),
	}
	// NOTE: We cannot check for human-readable timestamps because the timezone
	// can differ from a local execution of this test (e.g., PST) versus the
	// timezone used in CI (e.g., UTC). So we make sure to avoid
	// timezone-dependent strings in these test cases.
	testCases := []struct {
		name               string
		artifacts          []api.Artifact
		expectedSubstrings []string
		err                error
	}{
		{
			name: "regular (positive) duration",
			artifacts: []api.Artifact{
				startedJson, finishedJsonNormal,
			},
			expectedSubstrings: []string{`Test started`, `after 16m40s`, `more info`},
			err:                nil,
		},
		{
			name: "negative duration triggers user-facing warning",
			artifacts: []api.Artifact{
				startedJson, finishedJsonNegative,
			},
			expectedSubstrings: []string{`WARNING: The elapsed duration (-1328h39m7s) is negative. This can be caused by another process outside of Prow writing into the finished.json file. The file currently has a completion time of`},
			err:                nil,
		},
	}
	for _, tc := range testCases {
		lens, err := lenses.GetLens("metadata")
		if tc.err != err {
			t.Errorf("%s expected error %v but got error %v", tc.name, tc.err, err)
			continue
		}
		if tc.err == nil && lens == nil {
			t.Fatal("Expected lens 'metadata' but got nil.")
		}
		got := lens.Body(tc.artifacts, "", "", nil, config.Spyglass{})
		for _, expectedSubstring := range tc.expectedSubstrings {
			if !strings.Contains(got, expectedSubstring) {
				t.Errorf("%s: failed to find expected substring %v in %v", tc.name, expectedSubstring, got)
			}
		}
	}
}

func TestFlattenMetadata(t *testing.T) {
	tests := []struct {
		name        string
		metadata    map[string]interface{}
		expectedMap map[string]string
	}{
		{
			name:        "Empty map",
			metadata:    map[string]interface{}{},
			expectedMap: map[string]string{},
		},
		{
			name: "Test metadata",
			metadata: map[string]interface{}{
				"field1": "value1",
				"field2": "value2",
				"field3": "value3",
			},
			expectedMap: map[string]string{
				"field1": "value1",
				"field2": "value2",
				"field3": "value3",
			},
		},
		{
			name: "Test metadata with non-strings",
			metadata: map[string]interface{}{
				"field1": "value1",
				"field2": 2,
				"field3": true,
				"field4": "value4",
			},
			expectedMap: map[string]string{
				"field1": "value1",
				"field4": "value4",
			},
		},
		{
			name: "Test nested metadata",
			metadata: map[string]interface{}{
				"field1": "value1",
				"field2": "value2",
				"field3": map[string]interface{}{
					"nest1-field1": "nest1-value1",
					"nest1-field2": "nest1-value2",
					"nest1-field3": map[string]interface{}{
						"nest2-field1": "nest2-value1",
						"nest2-field2": "nest2-value2",
					},
				},
				"field4": "value4",
			},
			expectedMap: map[string]string{
				"field1":                           "value1",
				"field2":                           "value2",
				"field3.nest1-field1":              "nest1-value1",
				"field3.nest1-field2":              "nest1-value2",
				"field3.nest1-field3.nest2-field1": "nest2-value1",
				"field3.nest1-field3.nest2-field2": "nest2-value2",
				"field4":                           "value4",
			},
		},
	}

	lens := Lens{}
	for _, test := range tests {
		flattenedMetadata := lens.flattenMetadata(test.metadata)
		if !reflect.DeepEqual(flattenedMetadata, test.expectedMap) {
			t.Errorf("%s: resulting map did not match expected map: %v", test.name, cmp.Diff(flattenedMetadata, test.expectedMap))
		}
	}
}

func TestHintFromPodInfo(t *testing.T) {
	tests := []struct {
		name     string
		info     k8sreporter.PodReport
		expected string
	}{
		{
			name: "normal failed run has no output",
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodFailed,
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Terminated: &v1.ContainerStateTerminated{
										ExitCode: 1,
										Reason:   "Completed",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:     "stuck images are reported by name",
			expected: `The test container could not start because it could not pull "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master". Check your images. Full message: "rpc error: code = Unknown desc"`,
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason:  "ImagePullBackOff",
										Message: "rpc error: code = Unknown desc",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:     "stuck images are reported by name - errimagepull",
			expected: `The test container could not start because it could not pull "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master". Check your images. Full message: "rpc error: code = Unknown desc"`,
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason:  "ErrImagePull",
										Message: "rpc error: code = Unknown desc",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:     "stuck volumes are reported by name",
			expected: `The pod could not start because it could not mount the volume "some-volume": secrets "no-such-secret" not found`,
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								VolumeMounts: []v1.VolumeMount{
									{
										Name:      "some-volume",
										MountPath: "/mnt/some-volume",
									},
								},
							},
						},
						Volumes: []v1.Volume{
							{
								Name: "some-volume",
								VolumeSource: v1.VolumeSource{
									Secret: &v1.SecretVolumeSource{
										SecretName: "no-such-secret",
									},
								},
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason: "ContainerCreating",
									},
								},
							},
						},
					},
				},
				Events: []v1.Event{
					{
						Type:    "Warning",
						Reason:  "FailedMount",
						Message: `MountVolume.SetUp failed for volume "some-volume" : secrets "no-such-secret" not found`,
					},
				},
			},
		},
		{
			name:     "pod scheduled to an illegal node is reported",
			expected: "The job could not start because it was scheduled to a node that does not satisfy its NodeSelector",
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase:  v1.PodFailed,
						Reason: "MatchNodeSelector",
					},
				},
			},
		},
		{
			name:     "pod that could not be scheduled is reported",
			expected: "There are no nodes that your pod can schedule to - check your requests, tolerations, and node selectors (0/3 nodes are available: 3 node(s) didn't match node selector.)",
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
					},
				},
				Events: []v1.Event{
					{
						Type:    "Warning",
						Reason:  "FailedScheduling",
						Message: "0/3 nodes are available: 3 node(s) didn't match node selector.",
					},
				},
			},
		},
		{
			name:     "apparent node failure is reported as such",
			expected: "The job may have executed on an unhealthy node. Contact your prow maintainers with a link to this page or check the detailed pod information.",
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason: "ContainerCreating",
									},
								},
							},
						},
					},
				},
				Events: []v1.Event{
					{
						Type:   "Warning",
						Reason: "FailedCreatePodSandbox",
					},
				},
			},
		},
		{
			name:     "init container failed to start",
			expected: "Init container initupload not ready: (state: terminated, reason: \"Error\", message: \"failed fetching oauth2 token\")",
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
						InitContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "initupload",
								Ready: false,
								State: v1.ContainerState{
									Terminated: &v1.ContainerStateTerminated{
										Reason:  "Error",
										Message: "failed fetching oauth2 token",
									},
								},
							},
						},
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason: "PodInitializing",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:     "init container running but not ready",
			expected: "Init container initupload not ready: (state: running)",
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
						InitContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "initupload",
								Ready: false,
								State: v1.ContainerState{
									Running: &v1.ContainerStateRunning{},
								},
							},
						},
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason: "PodInitializing",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:     "multiple init containers failed to start",
			expected: "Init container entrypoint not ready: (state: waiting, reason: \"PodInitializing\", message: \"\")\nInit container initupload not ready: (state: terminated, reason: \"Error\", message: \"failed fetching oauth2 token\")",
			info: k8sreporter.PodReport{
				Pod: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "8ef160fc-46b6-11ea-a907-1a9873703b03",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
							},
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
						InitContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "entrypoint",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason:  "PodInitializing",
										Message: "",
									},
								},
							},
							{
								Name:  "initupload",
								Ready: false,
								State: v1.ContainerState{
									Terminated: &v1.ContainerStateTerminated{
										Reason:  "Error",
										Message: "failed fetching oauth2 token",
									},
								},
							},
						},
						ContainerStatuses: []v1.ContainerStatus{
							{
								Name:  "test",
								Image: "gcr.io/k8s-staging-test-infra/kubekins-e2e:latest-master",
								Ready: false,
								State: v1.ContainerState{
									Waiting: &v1.ContainerStateWaiting{
										Reason: "PodInitializing",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.info)
			if err != nil {
				t.Fatalf("Unexpected failed to marshal pod to JSON (this wasn't even part of the test!): %v", err)
			}
			result := hintFromPodInfo(b)
			if result != tc.expected {
				t.Errorf("Expected hint %q, but got %q", tc.expected, result)
			}
		})
	}
}

func TestHintFromProwJob(t *testing.T) {
	tests := []struct {
		name            string
		expected        string
		expectedErrored bool
		pj              prowv1.ProwJob
	}{
		{
			name:            "errored job has its description reported",
			expected:        "Job execution failed: this is the description",
			expectedErrored: true,
			pj: prowv1.ProwJob{
				Status: prowv1.ProwJobStatus{
					State:       prowv1.ErrorState,
					Description: "this is the description",
				},
			},
		},
		{
			name:     "failed prowjob reports nothing",
			expected: "",
			pj: prowv1.ProwJob{
				Status: prowv1.ProwJobStatus{
					State:       prowv1.FailureState,
					Description: "this is another description",
				},
			},
		},
		{
			name:     "aborted prowjob reports nothing",
			expected: "",
			pj: prowv1.ProwJob{
				Status: prowv1.ProwJobStatus{
					State:       prowv1.AbortedState,
					Description: "this is another description",
				},
			},
		},
		{
			name:     "successful prowjob reports nothing",
			expected: "",
			pj: prowv1.ProwJob{
				Status: prowv1.ProwJobStatus{
					State:       prowv1.SuccessState,
					Description: "this is another description",
				},
			},
		},
		{
			name:     "pending prowjob reports nothing",
			expected: "",
			pj: prowv1.ProwJob{
				Status: prowv1.ProwJobStatus{
					State:       prowv1.PendingState,
					Description: "this is another description",
				},
			},
		},
		{
			name:     "triggered prowjob reports nothing",
			expected: "",
			pj: prowv1.ProwJob{
				Status: prowv1.ProwJobStatus{
					State:       prowv1.TriggeredState,
					Description: "this is another description",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.pj)
			if err != nil {
				t.Fatalf("Unexpected failed to marshal prowjob to JSON (this wasn't even part of the test!): %v", err)
			}
			result, errored := hintFromProwJob(b)
			if result != tc.expected {
				t.Errorf("Expected hint %q, but got %q", tc.expected, result)
			}
			if errored != tc.expectedErrored {
				t.Errorf("Expected errored to be %t, but got %t", tc.expectedErrored, errored)
			}
		})
	}
}
