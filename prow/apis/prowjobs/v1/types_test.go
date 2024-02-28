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

package v1

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	fuzz "github.com/google/gofuzz"
	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
)

func pStr(str string) *string {
	return &str
}

// TODO(mpherman): Add more tests when ProwJobDefaults have more than 1 field
func TestProwJobDefaulting(t *testing.T) {
	var testCases = []struct {
		name     string
		provided *ProwJobDefault
		def      *ProwJobDefault
		expected *ProwJobDefault
	}{
		{
			name:     "nothing provided",
			provided: &ProwJobDefault{},
			def:      &ProwJobDefault{},
			expected: &ProwJobDefault{},
		},
		{
			name: "All provided, no default",
			provided: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "project",
				},
				TenantID: "tenant",
			},
			def: &ProwJobDefault{},
			expected: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "project",
				},
				TenantID: "tenant",
			},
		},
		{
			name: "All provided, no override",
			provided: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "project",
				},
				TenantID: "tenant",
			},
			def: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "default-project",
				},
				TenantID: "default-tenant",
			},
			expected: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "project",
				},
				TenantID: "tenant",
			},
		},
		{
			name:     "Empty provided, no default",
			provided: &ProwJobDefault{},
			def:      &ProwJobDefault{},
			expected: &ProwJobDefault{},
		},
		{
			name:     "Empty provided, use default",
			provided: &ProwJobDefault{},
			def: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "default-project",
				},
				TenantID: "default-tenant",
			},
			expected: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "default-project",
				},
				TenantID: "default-tenant",
			},
		},
		{
			name:     "Nil provided, empty default",
			provided: nil,
			def:      &ProwJobDefault{},
			expected: &ProwJobDefault{},
		},
		{
			name:     "Nil provided, use default",
			provided: nil,
			def: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "default-project",
				},
				TenantID: "default-tenant",
			},
			expected: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "default-project",
				},
				TenantID: "default-tenant",
			},
		},
		{
			name:     "Nil provided, nil default",
			provided: nil,
			def:      nil,
			expected: nil,
		},
		{
			name: "This provided, that default",
			provided: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "project",
				},
			},
			def: &ProwJobDefault{
				TenantID: "default-tenant",
			},
			expected: &ProwJobDefault{
				ResultStoreConfig: &ResultStoreConfig{
					ProjectID: "project",
				},
				TenantID: "default-tenant",
			},
		},
	}
	for _, testCase := range testCases {
		tc := testCase
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := tc.provided.ApplyDefault(tc.def)
			if diff := cmp.Diff(actual, tc.expected, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("expected defaulted config but got diff %v", diff)
			}
		})
	}
}

func TestDecorationDefaultingDoesntOverwrite(t *testing.T) {
	truth := true
	lies := false

	var testCases = []struct {
		name     string
		provided *DecorationConfig
		// Note: def is a copy of the defaults and may be modified.
		expected func(orig, def *DecorationConfig) *DecorationConfig
	}{
		{
			name:     "nothing provided",
			provided: &DecorationConfig{},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				return def
			},
		},
		{
			name: "timeout provided",
			provided: &DecorationConfig{
				Timeout: &Duration{Duration: 10 * time.Minute},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.Timeout = orig.Timeout
				return def
			},
		},
		{
			name: "grace period provided",
			provided: &DecorationConfig{
				GracePeriod: &Duration{Duration: 10 * time.Hour},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.GracePeriod = orig.GracePeriod
				return def
			},
		},
		{
			name: "utility images provided",
			provided: &DecorationConfig{
				UtilityImages: &UtilityImages{
					CloneRefs:  "clonerefs-special",
					InitUpload: "initupload-special",
					Entrypoint: "entrypoint-special",
					Sidecar:    "sidecar-special",
				},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.UtilityImages = orig.UtilityImages
				return def
			},
		},
		{
			name: "gcs configuration provided",
			provided: &DecorationConfig{
				GCSConfiguration: &GCSConfiguration{
					Bucket:            "bucket-1",
					PathPrefix:        "prefix-2",
					PathStrategy:      PathStrategyExplicit,
					DefaultOrg:        "org2",
					DefaultRepo:       "repo2",
					CompressFileTypes: []string{"txt", "json"},
				},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.GCSConfiguration = orig.GCSConfiguration
				return def
			},
		},
		{
			name: "gcs secret name provided",
			provided: &DecorationConfig{
				GCSCredentialsSecret: pStr("somethingSecret"),
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.GCSCredentialsSecret = orig.GCSCredentialsSecret
				return def
			},
		},
		{
			name: "gcs secret name unset",
			provided: &DecorationConfig{
				GCSCredentialsSecret: pStr(""),
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.GCSCredentialsSecret = orig.GCSCredentialsSecret
				return def
			},
		},
		{
			name: "s3 secret name provided",
			provided: &DecorationConfig{
				S3CredentialsSecret: pStr("overwritten"),
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.S3CredentialsSecret = orig.S3CredentialsSecret
				return def
			},
		},
		{
			name: "s3 secret name unset",
			provided: &DecorationConfig{
				S3CredentialsSecret: pStr(""),
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.S3CredentialsSecret = orig.S3CredentialsSecret
				return def
			},
		},
		{
			name: "default service account name provided",
			provided: &DecorationConfig{
				DefaultServiceAccountName: pStr("gcs-upload-sa"),
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.DefaultServiceAccountName = orig.DefaultServiceAccountName
				return def
			},
		},
		{
			name: "ssh secrets provided",
			provided: &DecorationConfig{
				SSHKeySecrets: []string{"my", "special"},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.SSHKeySecrets = orig.SSHKeySecrets
				return def
			},
		},

		{
			name: "utility images partially provided",
			provided: &DecorationConfig{
				UtilityImages: &UtilityImages{
					CloneRefs:  "clonerefs-special",
					InitUpload: "initupload-special",
				},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.UtilityImages.CloneRefs = orig.UtilityImages.CloneRefs
				def.UtilityImages.InitUpload = orig.UtilityImages.InitUpload
				return def
			},
		},
		{
			name: "gcs configuration partially provided",
			provided: &DecorationConfig{
				GCSConfiguration: &GCSConfiguration{
					Bucket: "bucket-1",
				},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.GCSConfiguration.Bucket = orig.GCSConfiguration.Bucket
				return def
			},
		},
		{
			name: "skip_cloning provided",
			provided: &DecorationConfig{
				SkipCloning: &lies,
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.SkipCloning = orig.SkipCloning
				return def
			},
		},
		{
			name: "ssh host fingerprints provided",
			provided: &DecorationConfig{
				SSHHostFingerprints: []string{"unique", "print"},
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.SSHHostFingerprints = orig.SSHHostFingerprints
				return def
			},
		},
		{
			name: "ingnore interrupts set",
			provided: &DecorationConfig{
				UploadIgnoresInterrupts: &truth,
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.UploadIgnoresInterrupts = orig.UploadIgnoresInterrupts
				return def
			},
		},
		{
			name: "do not ingnore interrupts ",
			provided: &DecorationConfig{
				UploadIgnoresInterrupts: &lies,
			},
			expected: func(orig, def *DecorationConfig) *DecorationConfig {
				def.UploadIgnoresInterrupts = orig.UploadIgnoresInterrupts
				return def
			},
		},
	}

	for _, testCase := range testCases {
		tc := testCase
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defaults := &DecorationConfig{
				Timeout:     &Duration{Duration: 1 * time.Minute},
				GracePeriod: &Duration{Duration: 10 * time.Second},
				UtilityImages: &UtilityImages{
					CloneRefs:  "clonerefs",
					InitUpload: "initupload",
					Entrypoint: "entrypoint",
					Sidecar:    "sidecar",
				},
				GCSConfiguration: &GCSConfiguration{
					Bucket:       "bucket",
					PathPrefix:   "prefix",
					PathStrategy: PathStrategyLegacy,
					DefaultOrg:   "org",
					DefaultRepo:  "repo",
				},
				GCSCredentialsSecret: pStr("secretName"),
				S3CredentialsSecret:  pStr("s3-secret"),
				SSHKeySecrets:        []string{"first", "second"},
				SSHHostFingerprints:  []string{"primero", "segundo"},
				SkipCloning:          &truth,
			}

			expected := tc.expected(tc.provided, defaults)
			actual := tc.provided.ApplyDefault(defaults)
			if diff := cmp.Diff(actual, expected, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("expected defaulted config but got diff %v", diff)
			}
		})
	}
}

func TestApplyDefaultsAppliesDefaultsForAllFields(t *testing.T) {
	t.Parallel()
	seed := time.Now().UnixNano()
	// Print the seed so failures can easily be reproduced
	t.Logf("Seed: %d", seed)
	fuzzer := fuzz.NewWithSeed(seed)
	for i := 0; i < 100; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			def := &DecorationConfig{}
			fuzzer.Fuzz(def)

			// Each of those three has its own DeepCopy and in case it is nil,
			// we just call that and return. In order to make this test verify
			// that copying of their fields also works, we have to set them to
			// something non-nil.
			toDefault := &DecorationConfig{
				UtilityImages:    &UtilityImages{},
				Resources:        &Resources{},
				GCSConfiguration: &GCSConfiguration{},
			}
			if def.UtilityImages == nil {
				def.UtilityImages = &UtilityImages{}
			}
			if def.Resources == nil {
				def.Resources = &Resources{}
			}
			if def.GCSConfiguration == nil {
				def.GCSConfiguration = &GCSConfiguration{}
			}
			defaulted := toDefault.ApplyDefault(def)

			if diff := cmp.Diff(def, defaulted); diff != "" {
				t.Errorf("defaulted decoration config didn't get all fields defaulted: %s", diff)
			}
		})
	}
}

func TestSlackConfigApplyDefaultsAppliesDefaultsForAllFields(t *testing.T) {
	t.Parallel()
	seed := time.Now().UnixNano()
	// Print the seed so failures can easily be reproduced
	t.Logf("Seed: %d", seed)
	fuzzer := fuzz.NewWithSeed(seed)
	for i := 0; i < 100; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			def := &SlackReporterConfig{}
			fuzzer.Fuzz(def)

			// Each of those three has its own DeepCopy and in case it is nil,
			// we just call that and return. In order to make this test verify
			// that copying of their fields also works, we have to set them to
			// something non-nil.
			toDefault := &SlackReporterConfig{
				Host:              "",
				Channel:           "",
				JobStatesToReport: nil,
				ReportTemplate:    "",
			}
			defaulted := toDefault.ApplyDefault(def)

			if diff := cmp.Diff(def, defaulted); diff != "" {
				t.Errorf("defaulted decoration config didn't get all fields defaulted: %s", diff)
			}
		})
	}
}

func TestRefsToString(t *testing.T) {
	var tests = []struct {
		name     string
		ref      Refs
		expected string
	}{
		{
			name: "Refs with Pull",
			ref: Refs{
				BaseRef: "master",
				BaseSHA: "deadbeef",
				Pulls: []Pull{
					{
						Number: 123,
						SHA:    "abcd1234",
					},
				},
			},
			expected: "master:deadbeef,123:abcd1234",
		},
		{
			name: "Refs with multiple Pulls",
			ref: Refs{
				BaseRef: "master",
				BaseSHA: "deadbeef",
				Pulls: []Pull{
					{
						Number: 123,
						SHA:    "abcd1234",
					},
					{
						Number: 456,
						SHA:    "dcba4321",
					},
				},
			},
			expected: "master:deadbeef,123:abcd1234,456:dcba4321",
		},
		{
			name: "Refs with BaseRef only",
			ref: Refs{
				BaseRef: "master",
			},
			expected: "master",
		},
		{
			name: "Refs with BaseRef and BaseSHA",
			ref: Refs{
				BaseRef: "master",
				BaseSHA: "deadbeef",
			},
			expected: "master:deadbeef",
		},
	}

	for _, test := range tests {
		actual, expected := test.ref.String(), test.expected
		if actual != expected {
			t.Errorf("%s: got ref string: %s, but expected: %s", test.name, actual, expected)
		}
	}
}

func TestRerunAuthConfigValidate(t *testing.T) {
	var testCases = []struct {
		name        string
		config      *RerunAuthConfig
		errExpected bool
	}{
		{
			name:        "disallow all",
			config:      &RerunAuthConfig{AllowAnyone: false},
			errExpected: false,
		},
		{
			name:        "no restrictions",
			config:      &RerunAuthConfig{},
			errExpected: false,
		},
		{
			name:        "allow any",
			config:      &RerunAuthConfig{AllowAnyone: true},
			errExpected: false,
		},
		{
			name:        "restrict orgs",
			config:      &RerunAuthConfig{GitHubOrgs: []string{"istio"}},
			errExpected: false,
		},
		{
			name:        "restrict orgs and users",
			config:      &RerunAuthConfig{GitHubOrgs: []string{"istio", "kubernetes"}, GitHubUsers: []string{"clarketm", "scoobydoo"}},
			errExpected: false,
		},
		{
			name:        "allow any and has restriction",
			config:      &RerunAuthConfig{AllowAnyone: true, GitHubOrgs: []string{"istio"}},
			errExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			if err := tc.config.Validate(); (err != nil) != tc.errExpected {
				t.Errorf("Expected error %v, got %v", tc.errExpected, err)
			}
		})
	}
}

func TestRerunAuthConfigIsAuthorized(t *testing.T) {
	var testCases = []struct {
		name       string
		user       string
		config     *RerunAuthConfig
		authorized bool
	}{
		{
			name:       "authorized - AllowAnyone is true",
			user:       "gumby",
			config:     &RerunAuthConfig{AllowAnyone: true},
			authorized: true,
		},
		{
			name:       "authorized - user in GitHubUsers",
			user:       "gumby",
			config:     &RerunAuthConfig{GitHubUsers: []string{"gumby"}},
			authorized: true,
		},
		{
			name:       "unauthorized - RerunAuthConfig is nil",
			user:       "gumby",
			config:     nil,
			authorized: false,
		},
		{
			name:       "unauthorized - cli is nil",
			user:       "gumby",
			config:     &RerunAuthConfig{},
			authorized: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			if actual, _ := tc.config.IsAuthorized("", tc.user, nil); actual != tc.authorized {
				t.Errorf("Expected %v, got %v", tc.authorized, actual)
			}
		})
	}
}

func TestRerunAuthConfigIsAllowAnyone(t *testing.T) {
	var testCases = []struct {
		name     string
		config   *RerunAuthConfig
		expected bool
	}{
		{
			name:     "AllowAnyone is true",
			config:   &RerunAuthConfig{AllowAnyone: true},
			expected: true,
		},
		{
			name:     "AllowAnyone is false",
			config:   &RerunAuthConfig{AllowAnyone: false},
			expected: false,
		},
		{
			name:     "AllowAnyone is unset",
			config:   &RerunAuthConfig{},
			expected: false,
		},
		{
			name:     "RerunAuthConfig is nil",
			config:   nil,
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			if actual := tc.config.IsAllowAnyone(); actual != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, actual)
			}
		})
	}
}

func TestParsePath(t *testing.T) {
	type args struct {
		bucket string
	}
	tests := []struct {
		name                string
		args                args
		wantStorageProvider string
		wantBucket          string
		wantFullPath        string
		wantPath            string
		wantErr             string
	}{
		{
			name: "valid gcs bucket",
			args: args{
				bucket: "prow-artifacts",
			},
			wantStorageProvider: "gs",
			wantBucket:          "prow-artifacts",
			wantFullPath:        "prow-artifacts",
			wantPath:            "",
		},
		{
			name: "valid gcs bucket with storage provider prefix",
			args: args{
				bucket: "gs://prow-artifacts",
			},
			wantStorageProvider: "gs",
			wantBucket:          "prow-artifacts",
			wantFullPath:        "prow-artifacts",
			wantPath:            "",
		},
		{
			name: "valid gcs bucket with multiple separator with storage provider prefix",
			args: args{
				bucket: "gs://my-floppy-backup/a://doom2.wad.006",
			},
			wantStorageProvider: "gs",
			wantBucket:          "my-floppy-backup",
			wantFullPath:        "my-floppy-backup/a://doom2.wad.006",
			wantPath:            "/a://doom2.wad.006",
		},
		{
			name: "valid s3 bucket with storage provider prefix",
			args: args{
				bucket: "s3://prow-artifacts",
			},
			wantStorageProvider: "s3",
			wantBucket:          "prow-artifacts",
			wantFullPath:        "prow-artifacts",
			wantPath:            "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prowPath, err := ParsePath(tt.args.bucket)
			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tt.wantErr {
				t.Errorf("ParsePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if prowPath.StorageProvider() != tt.wantStorageProvider {
				t.Errorf("ParsePath() gotStorageProvider = %v, wantStorageProvider %v", prowPath.StorageProvider(), tt.wantStorageProvider)
			}
			if got, want := prowPath.Bucket(), tt.wantBucket; got != want {
				t.Errorf("ParsePath() gotBucket = %v, wantBucket %v", got, want)
			}
			if got, want := prowPath.FullPath(), tt.wantFullPath; got != want {
				t.Errorf("ParsePath() gotFullPath = %v, wantFullPath %v", got, want)
			}
			if got, want := prowPath.Path, tt.wantPath; got != want {
				t.Errorf("ParsePath() gotPath = %v, wantPath %v", got, want)
			}
		})
	}
}

func TestProwJobSpec_HasPipelineRunSpec(t *testing.T) {
	type fields struct {
		PipelineRunSpec       *pipelinev1beta1.PipelineRunSpec
		TektonPipelineRunSpec *TektonPipelineRunSpec
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{{
		name: "none set",
		want: false,
	}, {
		name: "PipelineRunSpec set",
		fields: fields{
			PipelineRunSpec: &pipelinev1beta1.PipelineRunSpec{},
		},
		want: true,
	}, {
		name: "TektonPipelineRunSpec set",
		fields: fields{
			TektonPipelineRunSpec: &TektonPipelineRunSpec{},
		},
		want: false,
	}, {
		name: "TektonPipelineRunSpec.V1VBeta1 set",
		fields: fields{
			TektonPipelineRunSpec: &TektonPipelineRunSpec{
				V1Beta1: &pipelinev1beta1.PipelineRunSpec{},
			},
		},
		want: true,
	}, {
		name: "both set",
		fields: fields{
			PipelineRunSpec: &pipelinev1beta1.PipelineRunSpec{},
			TektonPipelineRunSpec: &TektonPipelineRunSpec{
				V1Beta1: &pipelinev1beta1.PipelineRunSpec{},
			},
		},
		want: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pjs := ProwJobSpec{
				PipelineRunSpec:       tt.fields.PipelineRunSpec,
				TektonPipelineRunSpec: tt.fields.TektonPipelineRunSpec,
			}
			if got := pjs.HasPipelineRunSpec(); got != tt.want {
				t.Errorf("ProwJobSpec.HasPipelineRunSpec() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProwJobSpec_GetPipelineRunSpec(t *testing.T) {
	type fields struct {
		PipelineRunSpec       *pipelinev1beta1.PipelineRunSpec
		TektonPipelineRunSpec *TektonPipelineRunSpec
	}
	tests := []struct {
		name    string
		fields  fields
		want    *pipelinev1beta1.PipelineRunSpec
		wantErr bool
	}{
		{
			name: "none set",
			fields: fields{
				PipelineRunSpec:       nil,
				TektonPipelineRunSpec: nil,
			},
			wantErr: true,
		},
		{
			name: "only PipelineRunSpec set",
			fields: fields{
				PipelineRunSpec: &pipelinev1beta1.PipelineRunSpec{
					ServiceAccountName: "robot",
					PipelineSpec: &pipelinev1beta1.PipelineSpec{
						Tasks: []pipelinev1beta1.PipelineTask{{Name: "implicit git resource", TaskRef: &pipelinev1beta1.TaskRef{Name: "abc"}}},
					},
				},
				TektonPipelineRunSpec: nil,
			},
			want: &pipelinev1beta1.PipelineRunSpec{
				ServiceAccountName: "robot",
				PipelineSpec: &pipelinev1beta1.PipelineSpec{
					Tasks: []pipelinev1beta1.PipelineTask{{Name: "implicit git resource", TaskRef: &pipelinev1beta1.TaskRef{Name: "abc"}}},
				},
			},
		},
		{
			name: "only TektonPipelineRunSpec set",
			fields: fields{
				PipelineRunSpec: nil,
				TektonPipelineRunSpec: &TektonPipelineRunSpec{
					V1Beta1: &pipelinev1beta1.PipelineRunSpec{
						ServiceAccountName: "robot",
						PipelineSpec: &pipelinev1beta1.PipelineSpec{
							Tasks: []pipelinev1beta1.PipelineTask{{Name: "implicit git resource", TaskRef: &pipelinev1beta1.TaskRef{Name: "abc"}}},
						},
					},
				},
			},
			want: &pipelinev1beta1.PipelineRunSpec{
				ServiceAccountName: "robot",
				PipelineSpec: &pipelinev1beta1.PipelineSpec{
					Tasks: []pipelinev1beta1.PipelineTask{{Name: "implicit git resource", TaskRef: &pipelinev1beta1.TaskRef{Name: "abc"}}},
				},
			},
		},
		{
			name: "PipelineRunSpec and TektonPipelineRunSpec set",
			fields: fields{
				PipelineRunSpec: &pipelinev1beta1.PipelineRunSpec{
					ServiceAccountName: "robot",
					PipelineSpec: &pipelinev1beta1.PipelineSpec{
						Tasks: []pipelinev1beta1.PipelineTask{{Name: "implicit git resource", TaskRef: &pipelinev1beta1.TaskRef{Name: "abc"}}},
					},
				},
				TektonPipelineRunSpec: &TektonPipelineRunSpec{
					V1Beta1: &pipelinev1beta1.PipelineRunSpec{
						ServiceAccountName: "robot",
						PipelineSpec: &pipelinev1beta1.PipelineSpec{
							Tasks: []pipelinev1beta1.PipelineTask{{Name: "implicit git resource", TaskRef: &pipelinev1beta1.TaskRef{Name: "def"}}},
						},
					},
				},
			},
			want: &pipelinev1beta1.PipelineRunSpec{
				ServiceAccountName: "robot",
				PipelineSpec: &pipelinev1beta1.PipelineSpec{
					Tasks: []pipelinev1beta1.PipelineTask{{Name: "implicit git resource", TaskRef: &pipelinev1beta1.TaskRef{Name: "def"}}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pjs := ProwJobSpec{
				PipelineRunSpec:       tt.fields.PipelineRunSpec,
				TektonPipelineRunSpec: tt.fields.TektonPipelineRunSpec,
			}
			got, err := pjs.GetPipelineRunSpec()
			if (err != nil) != tt.wantErr {
				t.Errorf("ProwJobSpec.GetPipelineRunSpec() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ProwJobSpec.GetPipelineRunSpec() = %v, want %v", got, tt.want)
			}
		})
	}
}
