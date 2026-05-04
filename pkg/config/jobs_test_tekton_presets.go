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

package config

import (
	"testing"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolveTektonPresets(t *testing.T) {
	testCases := []struct {
		name           string
		jobLabels      map[string]string
		spec           *pipelinev1.PipelineRunSpec
		presets        []Preset
		shouldError    bool
		expectedParams int
		expectedWS     int
		expectedSA     string
	}{
		{
			name:      "labels match, merge params",
			jobLabels: map[string]string{"preset-sa": "true"},
			spec:      &pipelinev1.PipelineRunSpec{},
			presets: []Preset{
				{
					Labels:               map[string]string{"preset-sa": "true"},
					TektonServiceAccount: "test-sa",
					TektonParams: []pipelinev1.Param{
						{
							Name: "foo",
							Value: pipelinev1.ParamValue{
								Type:      pipelinev1.ParamTypeString,
								StringVal: "bar",
							},
						},
					},
				},
			},
			expectedParams: 1,
			expectedSA:     "test-sa",
		},
		{
			name:      "labels don't match, skip preset",
			jobLabels: map[string]string{"other": "label"},
			spec:      &pipelinev1.PipelineRunSpec{},
			presets: []Preset{
				{
					Labels: map[string]string{"preset-sa": "true"},
					TektonParams: []pipelinev1.Param{
						{Name: "foo"},
					},
				},
			},
			expectedParams: 0,
		},
		{
			name:      "duplicate param causes error",
			jobLabels: map[string]string{"preset": "true"},
			spec: &pipelinev1.PipelineRunSpec{
				Params: []pipelinev1.Param{
					{Name: "existing"},
				},
			},
			presets: []Preset{
				{
					Labels: map[string]string{"preset": "true"},
					TektonParams: []pipelinev1.Param{
						{Name: "existing"},
					},
				},
			},
			shouldError: true,
		},
		{
			name:      "merge workspaces",
			jobLabels: map[string]string{"preset-ws": "true"},
			spec:      &pipelinev1.PipelineRunSpec{},
			presets: []Preset{
				{
					Labels: map[string]string{"preset-ws": "true"},
					TektonWorkspaces: []pipelinev1.WorkspaceBinding{
						{
							Name: "source",
							VolumeClaimTemplate: &coreapi.PersistentVolumeClaim{
								Spec: coreapi.PersistentVolumeClaimSpec{
									AccessModes: []coreapi.PersistentVolumeAccessMode{coreapi.ReadWriteOnce},
								},
							},
						},
					},
				},
			},
			expectedWS: 1,
		},
		{
			name:      "duplicate workspace causes error",
			jobLabels: map[string]string{"preset": "true"},
			spec: &pipelinev1.PipelineRunSpec{
				Workspaces: []pipelinev1.WorkspaceBinding{
					{Name: "source"},
				},
			},
			presets: []Preset{
				{
					Labels: map[string]string{"preset": "true"},
					TektonWorkspaces: []pipelinev1.WorkspaceBinding{
						{Name: "source"},
					},
				},
			},
			shouldError: true,
		},
		{
			name:      "merge timeout",
			jobLabels: map[string]string{"preset-timeout": "true"},
			spec:      &pipelinev1.PipelineRunSpec{},
			presets: []Preset{
				{
					Labels: map[string]string{"preset-timeout": "true"},
					TektonTimeout: &metav1.Duration{
						Duration: 3600000000000, // 1 hour in nanoseconds
					},
				},
			},
			expectedParams: 0,
		},
		{
			name:      "multiple presets applied",
			jobLabels: map[string]string{"preset-sa": "true", "preset-ws": "true"},
			spec:      &pipelinev1.PipelineRunSpec{},
			presets: []Preset{
				{
					Labels:               map[string]string{"preset-sa": "true"},
					TektonServiceAccount: "test-sa",
				},
				{
					Labels: map[string]string{"preset-ws": "true"},
					TektonWorkspaces: []pipelinev1.WorkspaceBinding{
						{Name: "cache"},
					},
				},
			},
			expectedSA: "test-sa",
			expectedWS: 1,
		},
		{
			name:      "service account conflict",
			jobLabels: map[string]string{"preset": "true"},
			spec: &pipelinev1.PipelineRunSpec{
				TaskRunTemplate: pipelinev1.PipelineTaskRunTemplate{
					ServiceAccountName: "existing-sa",
				},
			},
			presets: []Preset{
				{
					Labels:               map[string]string{"preset": "true"},
					TektonServiceAccount: "different-sa",
				},
			},
			shouldError: true,
		},
		{
			name:      "default preset (no labels) always applied",
			jobLabels: map[string]string{"any": "label"},
			spec:      &pipelinev1.PipelineRunSpec{},
			presets: []Preset{
				{
					Labels: map[string]string{}, // Empty labels = default preset
					TektonParams: []pipelinev1.Param{
						{Name: "default-param"},
					},
				},
			},
			expectedParams: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ResolveTektonPresets("test-job", tc.jobLabels, tc.spec, tc.presets)
			if (err != nil) != tc.shouldError {
				t.Errorf("expected error=%v, got error=%v", tc.shouldError, err)
			}
			if tc.shouldError {
				return
			}
			if len(tc.spec.Params) != tc.expectedParams {
				t.Errorf("expected %d params, got %d", tc.expectedParams, len(tc.spec.Params))
			}
			if len(tc.spec.Workspaces) != tc.expectedWS {
				t.Errorf("expected %d workspaces, got %d", tc.expectedWS, len(tc.spec.Workspaces))
			}
			if tc.expectedSA != "" {
				if tc.spec.TaskRunTemplate.ServiceAccountName != tc.expectedSA {
					t.Errorf("expected SA=%s, got %s", tc.expectedSA, tc.spec.TaskRunTemplate.ServiceAccountName)
				}
			}
		})
	}
}

func TestMergeTektonPreset(t *testing.T) {
	testCases := []struct {
		name        string
		jobLabels   map[string]string
		spec        *pipelinev1.PipelineRunSpec
		preset      Preset
		shouldError bool
		expectSkip  bool // Preset should be skipped due to label mismatch
	}{
		{
			name:      "label match triggers merge",
			jobLabels: map[string]string{"preset-test": "true"},
			spec:      &pipelinev1.PipelineRunSpec{},
			preset: Preset{
				Labels: map[string]string{"preset-test": "true"},
				TektonParams: []pipelinev1.Param{
					{Name: "test"},
				},
			},
			expectSkip: false,
		},
		{
			name:      "label mismatch skips preset",
			jobLabels: map[string]string{"different": "label"},
			spec:      &pipelinev1.PipelineRunSpec{},
			preset: Preset{
				Labels: map[string]string{"preset-test": "true"},
				TektonParams: []pipelinev1.Param{
					{Name: "test"},
				},
			},
			expectSkip: true,
		},
		{
			name:      "partial label match fails",
			jobLabels: map[string]string{"preset-test": "true"},
			spec:      &pipelinev1.PipelineRunSpec{},
			preset: Preset{
				Labels: map[string]string{
					"preset-test":  "true",
					"preset-other": "true",
				},
				TektonParams: []pipelinev1.Param{
					{Name: "test"},
				},
			},
			expectSkip: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			initialParams := len(tc.spec.Params)
			err := mergeTektonPreset(tc.preset, tc.jobLabels, tc.spec)

			if (err != nil) != tc.shouldError {
				t.Errorf("expected error=%v, got error=%v", tc.shouldError, err)
			}

			if tc.expectSkip {
				if len(tc.spec.Params) != initialParams {
					t.Errorf("expected preset to be skipped, but params changed from %d to %d",
						initialParams, len(tc.spec.Params))
				}
			}
		})
	}
}
