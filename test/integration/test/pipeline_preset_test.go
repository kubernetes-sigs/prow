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
	"path/filepath"
	"slices"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
)

// loadTestPresets loads the presets from the test config directory
func loadTestPresets(t *testing.T) []config.Preset {
	t.Helper()

	// Get the path to the presets config file
	configPath := filepath.Join("..", "config", "prow", "jobs", "presets.yaml")
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		t.Fatalf("Failed to get absolute path for presets: %v", err)
	}

	// Load the config
	cfg, err := config.LoadStrict(absPath, "", nil, "")
	if err != nil {
		t.Fatalf("Failed to load presets config: %v", err)
	}

	return cfg.Presets
}

// TestPipelinePresets tests that Tekton presets are correctly applied to ProwJobs
// and propagated to the resulting PipelineRuns created by the pipeline controller.
func TestPipelinePresets(t *testing.T) {
	t.Parallel()

	clusterContext := getClusterContext()
	t.Logf("Creating client for cluster: %s", clusterContext)

	// Load presets from config
	presets := loadTestPresets(t)

	kubeClient, err := NewClients("", clusterContext)
	if err != nil {
		t.Fatalf("Failed creating clients for cluster %q: %v", clusterContext, err)
	}

	tests := []struct {
		name                   string
		labels                 map[string]string
		jobParams              []pipelinev1.Param
		jobWorkspaces          []pipelinev1.WorkspaceBinding
		expectedServiceAccount string
		expectedParamNames     []string
		expectedWorkspaceNames []string
		expectedTimeout        *v1.Duration
	}{
		{
			name: "preset-service-account-applied",
			labels: map[string]string{
				"preset-tekton-service-account": "true",
			},
			expectedServiceAccount: "preset-test-sa",
		},
		{
			name: "preset-params-applied",
			labels: map[string]string{
				"preset-tekton-params": "true",
			},
			expectedParamNames: []string{"preset-version", "preset-environment"},
		},
		{
			name: "preset-workspace-applied",
			labels: map[string]string{
				"preset-tekton-workspace": "true",
			},
			expectedWorkspaceNames: []string{"preset-cache"},
		},
		{
			name: "preset-params-merged-with-job-params",
			labels: map[string]string{
				"preset-tekton-params": "true",
			},
			jobParams: []pipelinev1.Param{
				{
					Name: "job-specific-param",
					Value: pipelinev1.ParamValue{
						Type:      pipelinev1.ParamTypeString,
						StringVal: "job-value",
					},
				},
			},
			// Should have both preset params + job param = 3 total
			expectedParamNames: []string{"preset-version", "preset-environment", "job-specific-param"},
		},
		{
			name: "multiple-presets-applied",
			labels: map[string]string{
				"preset-tekton-service-account": "true",
				"preset-tekton-params":          "true",
				"preset-tekton-workspace":       "true",
			},
			expectedServiceAccount: "preset-test-sa",
			expectedParamNames:     []string{"preset-version", "preset-environment"},
			expectedWorkspaceNames: []string{"preset-cache"},
		},
		{
			name: "full-preset-with-timeout",
			labels: map[string]string{
				"preset-tekton-full": "true",
			},
			expectedServiceAccount: "full-preset-sa",
			expectedParamNames:     []string{"full-preset-param"},
			expectedWorkspaceNames: []string{"full-preset-workspace"},
			expectedTimeout:        &v1.Duration{Duration: 30 * time.Minute},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			jobName := fmt.Sprintf("preset-%s", RandomString(t)[:10])

			t.Logf("Creating ProwJob with presets: %s", jobName)

			// Build PipelineRunSpec with job-specific params/workspaces if provided
			pipelineRunSpec := &pipelinev1.PipelineRunSpec{
				PipelineSpec: &pipelinev1.PipelineSpec{
					Tasks: []pipelinev1.PipelineTask{
						{
							Name: "test-task",
							TaskRef: &pipelinev1.TaskRef{
								Name: "test-task",
							},
						},
					},
				},
			}

			if len(tt.jobParams) > 0 {
				pipelineRunSpec.Params = tt.jobParams
			}

			if len(tt.jobWorkspaces) > 0 {
				pipelineRunSpec.Workspaces = tt.jobWorkspaces
			}

			// Apply presets to the PipelineRunSpec (simulating config loading behavior)
			if err := config.ResolveTektonPresets(jobName, tt.labels, pipelineRunSpec, presets); err != nil {
				t.Fatalf("Failed to resolve Tekton presets: %v", err)
			}

			// Debug: log what was applied by presets
			t.Logf("After applying presets:")
			t.Logf("  ServiceAccount: %q", pipelineRunSpec.TaskRunTemplate.ServiceAccountName)
			t.Logf("  Params: %d", len(pipelineRunSpec.Params))
			t.Logf("  Workspaces: %d", len(pipelineRunSpec.Workspaces))

			prowjob := prowjobv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{
					Name:      jobName,
					Namespace: defaultNamespace,
					Labels:    tt.labels,
				},
				Spec: prowjobv1.ProwJobSpec{
					Type:      prowjobv1.PresubmitJob,
					Agent:     prowjobv1.TektonAgent,
					Cluster:   "default",
					Namespace: "test-pods",
					Job:       jobName,
					Refs: &prowjobv1.Refs{
						Org:     "test-org",
						Repo:    "test-repo",
						BaseRef: "main",
						BaseSHA: "abc123",
						Pulls: []prowjobv1.Pull{
							{
								Author: "test_author",
								Number: 1,
								SHA:    uuid.New().String(),
							},
						},
					},
					PipelineRunSpec: pipelineRunSpec,
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

			// Verify service account from preset
			if tt.expectedServiceAccount != "" {
				actualSA := pipelineRun.Spec.TaskRunTemplate.ServiceAccountName
				if actualSA != tt.expectedServiceAccount {
					t.Errorf("Service account mismatch: got %q, want %q",
						actualSA, tt.expectedServiceAccount)
				} else {
					t.Logf("✓ Service account correctly applied: %s", actualSA)
				}
			}

			// Verify params from preset
			if len(tt.expectedParamNames) > 0 {
				actualParamNames := make([]string, len(pipelineRun.Spec.Params))
				for i, p := range pipelineRun.Spec.Params {
					actualParamNames[i] = p.Name
				}

				for _, expectedParam := range tt.expectedParamNames {
					if !slices.Contains(actualParamNames, expectedParam) {
						t.Errorf("Expected param %q not found in PipelineRun. Available params: %v",
							expectedParam, actualParamNames)
					} else {
						t.Logf("✓ Param correctly applied: %s", expectedParam)
					}
				}
			}

			// Verify workspaces from preset
			if len(tt.expectedWorkspaceNames) > 0 {
				actualWorkspaceNames := make([]string, len(pipelineRun.Spec.Workspaces))
				for i, w := range pipelineRun.Spec.Workspaces {
					actualWorkspaceNames[i] = w.Name
				}

				for _, expectedWorkspace := range tt.expectedWorkspaceNames {
					if !slices.Contains(actualWorkspaceNames, expectedWorkspace) {
						t.Errorf("Expected workspace %q not found in PipelineRun. Available workspaces: %v",
							expectedWorkspace, actualWorkspaceNames)
					} else {
						t.Logf("✓ Workspace correctly applied: %s", expectedWorkspace)
					}
				}
			}

			// Verify timeout from preset
			if tt.expectedTimeout != nil {
				if pipelineRun.Spec.Timeouts == nil || pipelineRun.Spec.Timeouts.Pipeline == nil {
					t.Errorf("Expected timeout to be set, but Timeouts is nil")
				} else if pipelineRun.Spec.Timeouts.Pipeline.Duration != tt.expectedTimeout.Duration {
					t.Errorf("Timeout mismatch: got %v, want %v",
						pipelineRun.Spec.Timeouts.Pipeline.Duration, tt.expectedTimeout.Duration)
				} else {
					t.Logf("✓ Timeout correctly applied: %s", pipelineRun.Spec.Timeouts.Pipeline.Duration)
				}
			}

			t.Logf("✅ All preset validations passed for test: %s", tt.name)
		})
	}
}
