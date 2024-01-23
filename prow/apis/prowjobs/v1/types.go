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
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strings"
	"time"

	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prowgithub "k8s.io/test-infra/prow/github"
)

// ProwJobType specifies how the job is triggered.
type ProwJobType string

// Various job types.
const (
	// PresubmitJob means it runs on unmerged PRs.
	PresubmitJob ProwJobType = "presubmit"
	// PostsubmitJob means it runs on each new commit.
	PostsubmitJob ProwJobType = "postsubmit"
	// Periodic job means it runs on a time-basis, unrelated to git changes.
	PeriodicJob ProwJobType = "periodic"
	// BatchJob tests multiple unmerged PRs at the same time.
	BatchJob ProwJobType = "batch"
)

// ProwJobState specifies whether the job is running
type ProwJobState string

// Various job states.
const (
	// TriggeredState means the job has been created but not yet scheduled.
	TriggeredState ProwJobState = "triggered"
	// PendingState means the job is currently running and we are waiting for it to finish.
	PendingState ProwJobState = "pending"
	// SuccessState means the job completed without error (exit 0)
	SuccessState ProwJobState = "success"
	// FailureState means the job completed with errors (exit non-zero)
	FailureState ProwJobState = "failure"
	// AbortedState means prow killed the job early (new commit pushed, perhaps).
	AbortedState ProwJobState = "aborted"
	// ErrorState means the job could not schedule (bad config, perhaps).
	ErrorState ProwJobState = "error"
)

// GetAllProwJobStates returns all possible job states.
func GetAllProwJobStates() []ProwJobState {
	return []ProwJobState{
		TriggeredState,
		PendingState,
		SuccessState,
		FailureState,
		AbortedState,
		ErrorState}
}

// ProwJobAgent specifies the controller (such as plank or jenkins-agent) that runs the job.
type ProwJobAgent string

const (
	// KubernetesAgent means prow will create a pod to run this job.
	KubernetesAgent ProwJobAgent = "kubernetes"
	// JenkinsAgent means prow will schedule the job on jenkins.
	JenkinsAgent ProwJobAgent = "jenkins"
	// TektonAgent means prow will schedule the job via a tekton PipelineRun CRD resource.
	TektonAgent = "tekton-pipeline"
)

const (
	// DefaultClusterAlias specifies the default cluster key to schedule jobs.
	DefaultClusterAlias = "default"
)

const (
	// StartedStatusFile is the JSON file that stores information about the build
	// at the start of the build. See testgrid/metadata/job.go for more details.
	StartedStatusFile = "started.json"

	// FinishedStatusFile is the JSON file that stores information about the build
	// after its completion. See testgrid/metadata/job.go for more details.
	FinishedStatusFile = "finished.json"

	// ProwJobFile is the JSON file that stores the prowjob information.
	ProwJobFile = "prowjob.json"

	// CloneRecordFile is the JSON file that stores clone records of a prowjob.
	CloneRecordFile = "clone-records.json"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ProwJob contains the spec as well as runtime metadata.
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.spec.job`,description="The name of the job being run"
// +kubebuilder:printcolumn:name="BuildId",type=string,JSONPath=`.status.build_id`,description="The ID of the job being run."
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`,description="The type of job being run."
// +kubebuilder:printcolumn:name="Org",type=string,JSONPath=`.spec.refs.org`,description="The org for which the job is running."
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.refs.repo`,description="The repo for which the job is running."
// +kubebuilder:printcolumn:name="Pulls",type=string,JSONPath=`.spec.refs.pulls[*].number`,description="The pulls for which the job is running."
// +kubebuilder:printcolumn:name="StartTime",type=date,JSONPath=`.status.startTime`,description="When the job started running."
// +kubebuilder:printcolumn:name="CompletionTime",type=date,JSONPath=`.status.completionTime`,description="When the job finished running."
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`,description="The state of the job."
type ProwJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProwJobSpec   `json:"spec,omitempty"`
	Status ProwJobStatus `json:"status,omitempty"`
}

// ProwJobSpec configures the details of the prow job.
//
// Details include the podspec, code to clone, the cluster it runs
// any child jobs, concurrency limitations, etc.
type ProwJobSpec struct {
	// Type is the type of job and informs how
	// the jobs is triggered
	// +kubebuilder:validation:Enum=presubmit;postsubmit;periodic;batch
	// +kubebuilder:validation:Required
	Type ProwJobType `json:"type,omitempty"`
	// Agent determines which controller fulfills
	// this specific ProwJobSpec and runs the job
	Agent ProwJobAgent `json:"agent,omitempty"`
	// Cluster is which Kubernetes cluster is used
	// to run the job, only applicable for that
	// specific agent
	Cluster string `json:"cluster,omitempty"`
	// Namespace defines where to create pods/resources.
	Namespace string `json:"namespace,omitempty"`
	// Job is the name of the job
	// +kubebuilder:validation:Required
	Job string `json:"job,omitempty"`
	// Refs is the code under test, determined at
	// runtime by Prow itself
	Refs *Refs `json:"refs,omitempty"`
	// ExtraRefs are auxiliary repositories that
	// need to be cloned, determined from config
	ExtraRefs []Refs `json:"extra_refs,omitempty"`
	// Report determines if the result of this job should
	// be reported (e.g. status on GitHub, message in Slack, etc.)
	Report bool `json:"report,omitempty"`
	// Context is the name of the status context used to
	// report back to GitHub
	Context string `json:"context,omitempty"`
	// RerunCommand is the command a user would write to
	// trigger this job on their pull request
	RerunCommand string `json:"rerun_command,omitempty"`
	// MaxConcurrency restricts the total number of instances
	// of this job that can run in parallel at once. This is
	// a separate mechanism to JobQueueName and the lowest max
	// concurrency is selected from these two.
	// +kubebuilder:validation:Minimum=0
	MaxConcurrency int `json:"max_concurrency,omitempty"`
	// ErrorOnEviction indicates that the ProwJob should be completed and given
	// the ErrorState status if the pod that is executing the job is evicted.
	// If this field is unspecified or false, a new pod will be created to replace
	// the evicted one.
	ErrorOnEviction bool `json:"error_on_eviction,omitempty"`

	// PodSpec provides the basis for running the test under
	// a Kubernetes agent
	PodSpec *corev1.PodSpec `json:"pod_spec,omitempty"`

	// JenkinsSpec holds configuration specific to Jenkins jobs
	JenkinsSpec *JenkinsSpec `json:"jenkins_spec,omitempty"`

	// PipelineRunSpec provides the basis for running the test as
	// a pipeline-crd resource
	// https://github.com/tektoncd/pipeline
	PipelineRunSpec *pipelinev1beta1.PipelineRunSpec `json:"pipeline_run_spec,omitempty"`

	// TektonPipelineRunSpec provides the basis for running the test as
	// a pipeline-crd resource
	// https://github.com/tektoncd/pipeline
	TektonPipelineRunSpec *TektonPipelineRunSpec `json:"tekton_pipeline_run_spec,omitempty"`

	// DecorationConfig holds configuration options for
	// decorating PodSpecs that users provide
	DecorationConfig *DecorationConfig `json:"decoration_config,omitempty"`

	// ReporterConfig holds reporter-specific configuration
	ReporterConfig *ReporterConfig `json:"reporter_config,omitempty"`

	// RerunAuthConfig holds information about which users can rerun the job
	RerunAuthConfig *RerunAuthConfig `json:"rerun_auth_config,omitempty"`

	// Hidden specifies if the Job is considered hidden.
	// Hidden jobs are only shown by deck instances that have the
	// `--hiddenOnly=true` or `--show-hidden=true` flag set.
	// Presubmits and Postsubmits can also be set to hidden by
	// adding their repository in Decks `hidden_repo` setting.
	Hidden bool `json:"hidden,omitempty"`

	// ProwJobDefault holds configuration options provided as defaults
	// in the Prow config
	ProwJobDefault *ProwJobDefault `json:"prowjob_defaults,omitempty"`

	// JobQueueName is an optional field with name of a queue defining
	// max concurrency. When several jobs from the same queue try to run
	// at the same time, the number of them that is actually started is
	// limited by JobQueueCapacities (part of Plank's config). If
	// this field is left undefined inifinite concurrency is assumed.
	// This behaviour may be superseded by MaxConcurrency field, if it
	// is set to a constraining value.
	JobQueueName string `json:"job_queue_name,omitempty"`
}

func (pjs ProwJobSpec) HasPipelineRunSpec() bool {
	if pjs.TektonPipelineRunSpec != nil && pjs.TektonPipelineRunSpec.V1Beta1 != nil {
		return true
	}
	if pjs.PipelineRunSpec != nil {
		return true
	}
	return false
}

func (pjs ProwJobSpec) GetPipelineRunSpec() (*pipelinev1beta1.PipelineRunSpec, error) {
	var found *pipelinev1beta1.PipelineRunSpec
	if pjs.TektonPipelineRunSpec != nil {
		found = pjs.TektonPipelineRunSpec.V1Beta1
	}
	if found == nil && pjs.PipelineRunSpec != nil {
		found = pjs.PipelineRunSpec
	}
	if found == nil {
		return nil, errors.New("pipeline run spec not found")
	}
	return found, nil
}

type GitHubTeamSlug struct {
	Slug string `json:"slug"`
	Org  string `json:"org"`
}

type RerunAuthConfig struct {
	// If AllowAnyone is set to true, any user can rerun the job
	AllowAnyone bool `json:"allow_anyone,omitempty"`
	// GitHubTeams contains IDs of GitHub teams of users who can rerun the job
	// If you know the name of a team and the org it belongs to,
	// you can look up its ID using this command, where the team slug is the hyphenated name:
	// curl -H "Authorization: token <token>" "https://api.github.com/orgs/<org-name>/teams/<team slug>"
	// or, to list all teams in a given org, use
	// curl -H "Authorization: token <token>" "https://api.github.com/orgs/<org-name>/teams"
	GitHubTeamIDs []int `json:"github_team_ids,omitempty"`
	// GitHubTeamSlugs contains slugs and orgs of teams of users who can rerun the job
	GitHubTeamSlugs []GitHubTeamSlug `json:"github_team_slugs,omitempty"`
	// GitHubUsers contains names of individual users who can rerun the job
	GitHubUsers []string `json:"github_users,omitempty"`
	// GitHubOrgs contains names of GitHub organizations whose members can rerun the job
	GitHubOrgs []string `json:"github_orgs,omitempty"`
}

// IsSpecifiedUser returns true if AllowAnyone is set to true or if the given user is
// specified as a permitted GitHubUser
func (rac *RerunAuthConfig) IsAuthorized(org, user string, cli prowgithub.RerunClient) (bool, error) {
	if rac == nil {
		return false, nil
	}
	if rac.AllowAnyone {
		return true, nil
	}
	for _, u := range rac.GitHubUsers {
		if prowgithub.NormLogin(u) == prowgithub.NormLogin(user) {
			return true, nil
		}
	}
	// if there is no client, no token was provided, so we cannot access the teams
	if cli == nil {
		return false, nil
	}
	for _, gho := range rac.GitHubOrgs {
		isOrgMember, err := cli.IsMember(gho, user)
		if err != nil {
			return false, fmt.Errorf("GitHub failed to fetch members of org %v: %w", gho, err)
		}
		if isOrgMember {
			return true, nil
		}
	}
	for _, ght := range rac.GitHubTeamIDs {
		member, err := cli.TeamHasMember(org, ght, user)
		if err != nil {
			return false, fmt.Errorf("GitHub failed to fetch members of team %v, verify that you have the correct team number and access token: %w", ght, err)
		}
		if member {
			return true, nil
		}
	}
	for _, ghts := range rac.GitHubTeamSlugs {
		member, err := cli.TeamBySlugHasMember(ghts.Org, ghts.Slug, user)
		if err != nil {
			return false, fmt.Errorf("GitHub failed to check if team with slug %s has member %s: %w", ghts.Slug, user, err)
		}
		if member {
			return true, nil
		}
	}
	return false, nil
}

// Validate validates the RerunAuthConfig fields.
func (rac *RerunAuthConfig) Validate() error {
	if rac == nil {
		return nil
	}

	hasAllowList := len(rac.GitHubUsers) > 0 || len(rac.GitHubTeamIDs) > 0 || len(rac.GitHubTeamSlugs) > 0 || len(rac.GitHubOrgs) > 0

	// If an allowlist is specified, the user probably does not intend for anyone to be able to rerun any job.
	if rac.AllowAnyone && hasAllowList {
		return errors.New("allow anyone is set to true and permitted users or groups are specified")
	}

	return nil
}

// IsAllowAnyone checks if anyone can rerun the job.
func (rac *RerunAuthConfig) IsAllowAnyone() bool {
	if rac == nil {
		return false
	}

	return rac.AllowAnyone
}

type ReporterConfig struct {
	ResultStore *ResultStoreReporter `json:"resultstore,omitempty"`
	Slack       *SlackReporterConfig `json:"slack,omitempty"`
}

// TODO: This config is used only for alpha testing and will
// likely move to ProwJobDefaults for flexibility.
type ResultStoreReporter struct {
	// Specifies the ResultStore InvocationAttributes.ProjectId, used
	// for various quota and GUI access control purposes.
	// In practice, it is generally the same as the Google Cloud
	// Project ID or number of the job's GCS storage bucket.
	// Required to write job results to ResultStore.
	ProjectID string `json:"project_id,omitempty"`
}

type SlackReporterConfig struct {
	Host              string         `json:"host,omitempty"`
	Channel           string         `json:"channel,omitempty"`
	JobStatesToReport []ProwJobState `json:"job_states_to_report,omitempty"`
	ReportTemplate    string         `json:"report_template,omitempty"`
	// Report is derived from JobStatesToReport, it's used for differentiating
	// nil from empty slice, as yaml roundtrip by design can't tell the
	// difference when omitempty is supplied.
	// See https://github.com/kubernetes/test-infra/pull/24168 for details
	// Priority-wise, it goes by following order:
	// - `report: true/false`` in job config
	// - `JobStatesToReport: <anything including empty slice>` in job config
	// - `report: true/false`` in global config
	// - `JobStatesToReport:` in global config
	Report *bool `json:"report,omitempty"`
}

// ApplyDefault is called by jobConfig.ApplyDefault(globalConfig)
func (src *SlackReporterConfig) ApplyDefault(def *SlackReporterConfig) *SlackReporterConfig {
	if src == nil && def == nil {
		return nil
	}
	var merged SlackReporterConfig
	if src != nil {
		merged = *src.DeepCopy()
	} else {
		merged = *def.DeepCopy()
	}
	if src == nil || def == nil {
		return &merged
	}

	if merged.Channel == "" {
		merged.Channel = def.Channel
	}
	if merged.Host == "" {
		merged.Host = def.Host
	}
	// Note: `job_states_to_report: []` also results in JobStatesToReport == nil
	if merged.JobStatesToReport == nil {
		merged.JobStatesToReport = def.JobStatesToReport
	}
	if merged.ReportTemplate == "" {
		merged.ReportTemplate = def.ReportTemplate
	}
	if merged.Report == nil {
		merged.Report = def.Report
	}
	return &merged
}

// Duration is a wrapper around time.Duration that parses times in either
// 'integer number of nanoseconds' or 'duration string' formats and serializes
// to 'duration string' format.
// +kubebuilder:validation:Type=string
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &d.Duration); err == nil {
		// b was an integer number of nanoseconds.
		return nil
	}
	// b was not an integer. Assume that it is a duration string.

	var str string
	err := json.Unmarshal(b, &str)
	if err != nil {
		return err
	}

	pd, err := time.ParseDuration(str)
	if err != nil {
		return err
	}
	d.Duration = pd
	return nil
}

func (d *Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// ProwJobDefault is used for Prowjob fields we want to set as defaults
// in Prow config
type ProwJobDefault struct {
	TenantID string `json:"tenant_id,omitempty"`
}

// DecorationConfig specifies how to augment pods.
//
// This is primarily used to provide automatic integration with gubernator
// and testgrid.
type DecorationConfig struct {
	// Timeout is how long the pod utilities will wait
	// before aborting a job with SIGINT.
	Timeout *Duration `json:"timeout,omitempty"`
	// GracePeriod is how long the pod utilities will wait
	// after sending SIGINT to send SIGKILL when aborting
	// a job. Only applicable if decorating the PodSpec.
	GracePeriod *Duration `json:"grace_period,omitempty"`

	// UtilityImages holds pull specs for utility container
	// images used to decorate a PodSpec.
	UtilityImages *UtilityImages `json:"utility_images,omitempty"`
	// Resources holds resource requests and limits for utility
	// containers used to decorate a PodSpec.
	Resources *Resources `json:"resources,omitempty"`
	// GCSConfiguration holds options for pushing logs and
	// artifacts to GCS from a job.
	GCSConfiguration *GCSConfiguration `json:"gcs_configuration,omitempty"`
	// GCSCredentialsSecret is the name of the Kubernetes secret
	// that holds GCS push credentials.
	GCSCredentialsSecret *string `json:"gcs_credentials_secret,omitempty"`
	// S3CredentialsSecret is the name of the Kubernetes secret
	// that holds blob storage push credentials.
	S3CredentialsSecret *string `json:"s3_credentials_secret,omitempty"`
	// DefaultServiceAccountName is the name of the Kubernetes service account
	// that should be used by the pod if one is not specified in the podspec.
	DefaultServiceAccountName *string `json:"default_service_account_name,omitempty"`
	// SSHKeySecrets are the names of Kubernetes secrets that contain
	// SSK keys which should be used during the cloning process.
	SSHKeySecrets []string `json:"ssh_key_secrets,omitempty"`
	// SSHHostFingerprints are the fingerprints of known SSH hosts
	// that the cloning process can trust.
	// Create with ssh-keyscan [-t rsa] host
	SSHHostFingerprints []string `json:"ssh_host_fingerprints,omitempty"`
	// SkipCloning determines if we should clone source code in the
	// initcontainers for jobs that specify refs
	SkipCloning *bool `json:"skip_cloning,omitempty"`
	// CookieFileSecret is the name of a kubernetes secret that contains
	// a git http.cookiefile, which should be used during the cloning process.
	CookiefileSecret *string `json:"cookiefile_secret,omitempty"`
	// OauthTokenSecret is a Kubernetes secret that contains the OAuth token,
	// which is going to be used for fetching a private repository.
	OauthTokenSecret *OauthTokenSecret `json:"oauth_token_secret,omitempty"`
	// GitHubAPIEndpoints are the endpoints of GitHub APIs.
	GitHubAPIEndpoints []string `json:"github_api_endpoints,omitempty"`
	// GitHubAppID is the ID of GitHub App, which is going to be used for fetching a private
	// repository.
	GitHubAppID string `json:"github_app_id,omitempty"`
	// GitHubAppPrivateKeySecret is a Kubernetes secret that contains the GitHub App private key,
	// which is going to be used for fetching a private repository.
	GitHubAppPrivateKeySecret *GitHubAppPrivateKeySecret `json:"github_app_private_key_secret,omitempty"`

	// CensorSecrets enables censoring output logs and artifacts.
	CensorSecrets *bool `json:"censor_secrets,omitempty"`

	// CensoringOptions exposes options for censoring output logs and artifacts.
	CensoringOptions *CensoringOptions `json:"censoring_options,omitempty"`

	// UploadIgnoresInterrupts causes sidecar to ignore interrupts for the upload process in
	// hope that the test process exits cleanly before starting an upload.
	UploadIgnoresInterrupts *bool `json:"upload_ignores_interrupts,omitempty"`

	// SetLimitEqualsMemoryRequest sets memory limit equal to request.
	SetLimitEqualsMemoryRequest *bool `json:"set_limit_equals_memory_request,omitempty"`
	// DefaultMemoryRequest is the default requested memory on a test container.
	// If SetLimitEqualsMemoryRequest is also true then the Limit will also be
	// set the same as this request. Could be overridden by memory request
	// defined explicitly on prowjob.
	DefaultMemoryRequest *resource.Quantity `json:"default_memory_request,omitempty"`

	// PodPendingTimeout defines how long the controller will wait to perform garbage
	// collection on pending pods. Specific for OrgRepo or Cluster. If not set, it has a fallback inside plank field.
	PodPendingTimeout *metav1.Duration `json:"pod_pending_timeout,omitempty"`
	// PodRunningTimeout defines how long the controller will wait to abort a prowjob pod
	// stuck in running state. Specific for OrgRepo or Cluster. If not set, it has a fallback inside plank field.
	PodRunningTimeout *metav1.Duration `json:"pod_running_timeout,omitempty"`
	// PodUnscheduledTimeout defines how long the controller will wait to abort a prowjob
	// stuck in an unscheduled state. Specific for OrgRepo or Cluster. If not set, it has a fallback inside plank field.
	PodUnscheduledTimeout *metav1.Duration `json:"pod_unscheduled_timeout,omitempty"`

	// RunAsUser defines UID for process in all containers running in a Pod.
	// This field will not override the existing ProwJob's PodSecurityContext.
	// Equivalent to PodSecurityContext's RunAsUser
	RunAsUser *int64 `json:"run_as_user,omitempty"`
	// RunAsGroup defines GID of process in all containers running in a Pod.
	// This field will not override the existing ProwJob's PodSecurityContext.
	// Equivalent to PodSecurityContext's RunAsGroup
	RunAsGroup *int64 `json:"run_as_group,omitempty"`
	// FsGroup defines special supplemental group ID used in all containers in a Pod.
	// This allows to change the ownership of particular volumes by kubelet.
	// This field will not override the existing ProwJob's PodSecurityContext.
	// Equivalent to PodSecurityContext's FsGroup
	FsGroup *int64 `json:"fs_group,omitempty"`
}

type CensoringOptions struct {
	// CensoringConcurrency is the maximum number of goroutines that should be censoring
	// artifacts and logs at any time. If unset, defaults to 10.
	CensoringConcurrency *int64 `json:"censoring_concurrency,omitempty"`
	// CensoringBufferSize is the size in bytes of the buffer allocated for every file
	// being censored. We want to keep as little of the file in memory as possible in
	// order for censoring to be reasonably performant in space. However, to guarantee
	// that we censor every instance of every secret, our buffer size must be at least
	// two times larger than the largest secret we are about to censor. While that size
	// is the smallest possible buffer we could use, if the secrets being censored are
	// small, censoring will not be performant as the number of I/O actions per file
	// would increase. If unset, defaults to 10MiB.
	CensoringBufferSize *int `json:"censoring_buffer_size,omitempty"`

	// IncludeDirectories are directories which should have their content censored. If
	// present, only content in these directories will be censored. Entries in this list
	// are relative to $ARTIFACTS and are parsed with the go-zglob library, allowing for
	// globbed matches.
	IncludeDirectories []string `json:"include_directories,omitempty"`

	// ExcludeDirectories are directories which should not have their content censored. If
	// present, content in these directories will not be censored even if the directory also
	// matches a glob in IncludeDirectories. Entries in this list are relative to $ARTIFACTS,
	// and are parsed with the go-zglob library, allowing for globbed matches.
	ExcludeDirectories []string `json:"exclude_directories,omitempty"`
}

// ApplyDefault applies the defaults for CensoringOptions decorations. If a field has a zero value,
// it replaces that with the value set in def.
func (g *CensoringOptions) ApplyDefault(def *CensoringOptions) *CensoringOptions {
	if g == nil && def == nil {
		return nil
	}
	var merged CensoringOptions
	if g != nil {
		merged = *g.DeepCopy()
	} else {
		merged = *def.DeepCopy()
	}
	if g == nil || def == nil {
		return &merged
	}

	if merged.CensoringConcurrency == nil {
		merged.CensoringConcurrency = def.CensoringConcurrency
	}

	if merged.CensoringBufferSize == nil {
		merged.CensoringBufferSize = def.CensoringBufferSize
	}

	if merged.IncludeDirectories == nil {
		merged.IncludeDirectories = def.IncludeDirectories
	}

	if merged.ExcludeDirectories == nil {
		merged.ExcludeDirectories = def.ExcludeDirectories
	}
	return &merged
}

// Resources holds resource requests and limits for
// containers used to decorate a PodSpec
type Resources struct {
	CloneRefs       *corev1.ResourceRequirements `json:"clonerefs,omitempty"`
	InitUpload      *corev1.ResourceRequirements `json:"initupload,omitempty"`
	PlaceEntrypoint *corev1.ResourceRequirements `json:"place_entrypoint,omitempty"`
	Sidecar         *corev1.ResourceRequirements `json:"sidecar,omitempty"`
}

// ApplyDefault applies the defaults for the resource decorations. If a field has a zero value,
// it replaces that with the value set in def.
func (u *Resources) ApplyDefault(def *Resources) *Resources {
	if u == nil {
		return def
	} else if def == nil {
		return u
	}

	merged := *u
	if merged.CloneRefs == nil {
		merged.CloneRefs = def.CloneRefs
	}
	if merged.InitUpload == nil {
		merged.InitUpload = def.InitUpload
	}
	if merged.PlaceEntrypoint == nil {
		merged.PlaceEntrypoint = def.PlaceEntrypoint
	}
	if merged.Sidecar == nil {
		merged.Sidecar = def.Sidecar
	}
	return &merged
}

// OauthTokenSecret holds the information of the oauth token's secret name and key.
type OauthTokenSecret struct {
	// Name is the name of a kubernetes secret.
	Name string `json:"name,omitempty"`
	// Key is the key of the corresponding kubernetes secret that
	// holds the value of the OAuth token.
	Key string `json:"key,omitempty"`
}

// GitHubAppPrivateKeySecret holds the information of the GitHub App private key's secret name and key.
type GitHubAppPrivateKeySecret struct {
	// Name is the name of a kubernetes secret.
	Name string `json:"name,omitempty"`
	// Key is the key of the corresponding kubernetes secret that
	// holds the value of the GitHub App private key.
	Key string `json:"key,omitempty"`
}

func (d *ProwJobDefault) ApplyDefault(def *ProwJobDefault) *ProwJobDefault {
	if d == nil && def == nil {
		return nil
	}
	var merged ProwJobDefault
	if d != nil {
		merged = *d.DeepCopy()
	} else {
		merged = *def.DeepCopy()
	}
	if d == nil || def == nil {
		return &merged
	}
	if merged.TenantID == "" {
		merged.TenantID = def.TenantID
	}

	return &merged
}

// ApplyDefault applies the defaults for the ProwJob decoration. If a field has a zero value, it
// replaces that with the value set in def.
func (d *DecorationConfig) ApplyDefault(def *DecorationConfig) *DecorationConfig {
	if d == nil && def == nil {
		return nil
	}
	var merged DecorationConfig
	if d != nil {
		merged = *d.DeepCopy()
	} else {
		merged = *def.DeepCopy()
	}
	if d == nil || def == nil {
		return &merged
	}
	merged.UtilityImages = merged.UtilityImages.ApplyDefault(def.UtilityImages)
	merged.Resources = merged.Resources.ApplyDefault(def.Resources)
	merged.GCSConfiguration = merged.GCSConfiguration.ApplyDefault(def.GCSConfiguration)
	merged.CensoringOptions = merged.CensoringOptions.ApplyDefault(def.CensoringOptions)

	if merged.Timeout == nil {
		merged.Timeout = def.Timeout
	}
	if merged.GracePeriod == nil {
		merged.GracePeriod = def.GracePeriod
	}
	if merged.GCSCredentialsSecret == nil {
		merged.GCSCredentialsSecret = def.GCSCredentialsSecret
	}
	if merged.S3CredentialsSecret == nil {
		merged.S3CredentialsSecret = def.S3CredentialsSecret
	}
	if merged.DefaultServiceAccountName == nil {
		merged.DefaultServiceAccountName = def.DefaultServiceAccountName
	}
	if len(merged.SSHKeySecrets) == 0 {
		merged.SSHKeySecrets = def.SSHKeySecrets
	}
	if len(merged.SSHHostFingerprints) == 0 {
		merged.SSHHostFingerprints = def.SSHHostFingerprints
	}
	if merged.SkipCloning == nil {
		merged.SkipCloning = def.SkipCloning
	}
	if merged.CookiefileSecret == nil {
		merged.CookiefileSecret = def.CookiefileSecret
	}
	if merged.OauthTokenSecret == nil {
		merged.OauthTokenSecret = def.OauthTokenSecret
	}
	if len(merged.GitHubAPIEndpoints) == 0 {
		merged.GitHubAPIEndpoints = def.GitHubAPIEndpoints
	}
	if merged.GitHubAppID == "" {
		merged.GitHubAppID = def.GitHubAppID
	}
	if merged.GitHubAppPrivateKeySecret == nil {
		merged.GitHubAppPrivateKeySecret = def.GitHubAppPrivateKeySecret
	}
	if merged.CensorSecrets == nil {
		merged.CensorSecrets = def.CensorSecrets
	}

	if merged.UploadIgnoresInterrupts == nil {
		merged.UploadIgnoresInterrupts = def.UploadIgnoresInterrupts
	}

	if merged.SetLimitEqualsMemoryRequest == nil {
		merged.SetLimitEqualsMemoryRequest = def.SetLimitEqualsMemoryRequest
	}

	if merged.DefaultMemoryRequest == nil {
		merged.DefaultMemoryRequest = def.DefaultMemoryRequest
	}

	if merged.PodPendingTimeout == nil {
		merged.PodPendingTimeout = def.PodPendingTimeout
	}

	if merged.PodRunningTimeout == nil {
		merged.PodRunningTimeout = def.PodRunningTimeout
	}

	if merged.PodUnscheduledTimeout == nil {
		merged.PodUnscheduledTimeout = def.PodUnscheduledTimeout
	}

	if merged.RunAsUser == nil {
		merged.RunAsUser = def.RunAsUser
	}

	if merged.RunAsGroup == nil {
		merged.RunAsGroup = def.RunAsGroup
	}

	if merged.FsGroup == nil {
		merged.FsGroup = def.FsGroup
	}
	return &merged
}

// Validate ensures all the values set in the DecorationConfig are valid.
func (d *DecorationConfig) Validate() error {
	if d.UtilityImages == nil {
		return errors.New("utility image config is not specified")
	}
	var missing []string
	if d.UtilityImages.CloneRefs == "" {
		missing = append(missing, "clonerefs")
	}
	if d.UtilityImages.InitUpload == "" {
		missing = append(missing, "initupload")
	}
	if d.UtilityImages.Entrypoint == "" {
		missing = append(missing, "entrypoint")
	}
	if d.UtilityImages.Sidecar == "" {
		missing = append(missing, "sidecar")
	}
	if len(missing) > 0 {
		return fmt.Errorf("the following utility images are not specified: %q", missing)
	}

	if d.GCSConfiguration == nil {
		return errors.New("GCS upload configuration is not specified")
	}
	// Intentionally allow d.GCSCredentialsSecret and d.S3CredentialsSecret to
	// be unset in which case we assume GCS permissions are provided by GKE
	// Workload Identity: https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity

	if err := d.GCSConfiguration.Validate(); err != nil {
		return fmt.Errorf("GCS configuration is invalid: %w", err)
	}
	if d.OauthTokenSecret != nil && len(d.SSHKeySecrets) > 0 {
		return errors.New("both OAuth token and SSH key secrets are specified")
	}
	return nil
}

func (d *Duration) Get() time.Duration {
	if d == nil {
		return 0
	}
	return d.Duration
}

// UtilityImages holds pull specs for the utility images
// to be used for a job
type UtilityImages struct {
	// CloneRefs is the pull spec used for the clonerefs utility
	CloneRefs string `json:"clonerefs,omitempty"`
	// InitUpload is the pull spec used for the initupload utility
	InitUpload string `json:"initupload,omitempty"`
	// Entrypoint is the pull spec used for the entrypoint utility
	Entrypoint string `json:"entrypoint,omitempty"`
	// sidecar is the pull spec used for the sidecar utility
	Sidecar string `json:"sidecar,omitempty"`
}

// ApplyDefault applies the defaults for the UtilityImages decorations. If a field has a zero value,
// it replaces that with the value set in def.
func (u *UtilityImages) ApplyDefault(def *UtilityImages) *UtilityImages {
	if u == nil {
		return def
	} else if def == nil {
		return u
	}

	merged := *u.DeepCopy()
	if merged.CloneRefs == "" {
		merged.CloneRefs = def.CloneRefs
	}
	if merged.InitUpload == "" {
		merged.InitUpload = def.InitUpload
	}
	if merged.Entrypoint == "" {
		merged.Entrypoint = def.Entrypoint
	}
	if merged.Sidecar == "" {
		merged.Sidecar = def.Sidecar
	}
	return &merged
}

// PathStrategy specifies minutia about how to construct the url.
// Usually consumed by gubernator/testgrid.
const (
	PathStrategyLegacy   = "legacy"
	PathStrategySingle   = "single"
	PathStrategyExplicit = "explicit"
)

// GCSConfiguration holds options for pushing logs and
// artifacts to GCS from a job.
type GCSConfiguration struct {
	// Bucket is the bucket to upload to, it can be:
	// * a GCS bucket: with gs:// prefix
	// * a S3 bucket: with s3:// prefix
	// * a GCS bucket: without a prefix (deprecated, it's discouraged to use Bucket without prefix please add the gs:// prefix)
	Bucket string `json:"bucket,omitempty"`
	// PathPrefix is an optional path that follows the
	// bucket name and comes before any structure
	PathPrefix string `json:"path_prefix,omitempty"`
	// PathStrategy dictates how the org and repo are used
	// when calculating the full path to an artifact in GCS
	PathStrategy string `json:"path_strategy,omitempty"`
	// DefaultOrg is omitted from GCS paths when using the
	// legacy or simple strategy
	DefaultOrg string `json:"default_org,omitempty"`
	// DefaultRepo is omitted from GCS paths when using the
	// legacy or simple strategy
	DefaultRepo string `json:"default_repo,omitempty"`
	// MediaTypes holds additional extension media types to add to Go's
	// builtin's and the local system's defaults.  This maps extensions
	// to media types, for example: MediaTypes["log"] = "text/plain"
	MediaTypes map[string]string `json:"mediaTypes,omitempty"`
	// JobURLPrefix holds the baseURL under which the jobs output can be viewed.
	// If unset, this will be derived based on org/repo from the job_url_prefix_config.
	JobURLPrefix string `json:"job_url_prefix,omitempty"`

	// LocalOutputDir specifies a directory where files should be copied INSTEAD of uploading to blob storage.
	// This option is useful for testing jobs that use the pod-utilities without actually uploading.
	LocalOutputDir string `json:"local_output_dir,omitempty"`
}

// ApplyDefault applies the defaults for GCSConfiguration decorations. If a field has a zero value,
// it replaces that with the value set in def.
func (g *GCSConfiguration) ApplyDefault(def *GCSConfiguration) *GCSConfiguration {
	if g == nil && def == nil {
		return nil
	}
	var merged GCSConfiguration
	if g != nil {
		merged = *g.DeepCopy()
	} else {
		merged = *def.DeepCopy()
	}
	if g == nil || def == nil {
		return &merged
	}

	if merged.Bucket == "" {
		merged.Bucket = def.Bucket
	}
	if merged.PathPrefix == "" {
		merged.PathPrefix = def.PathPrefix
	}
	if merged.PathStrategy == "" {
		merged.PathStrategy = def.PathStrategy
	}
	if merged.DefaultOrg == "" {
		merged.DefaultOrg = def.DefaultOrg
	}
	if merged.DefaultRepo == "" {
		merged.DefaultRepo = def.DefaultRepo
	}

	if merged.MediaTypes == nil && len(def.MediaTypes) > 0 {
		merged.MediaTypes = map[string]string{}
	}

	for extension, mediaType := range def.MediaTypes {
		merged.MediaTypes[extension] = mediaType
	}

	if merged.JobURLPrefix == "" {
		merged.JobURLPrefix = def.JobURLPrefix
	}

	if merged.LocalOutputDir == "" {
		merged.LocalOutputDir = def.LocalOutputDir
	}
	return &merged
}

// Validate ensures all the values set in the GCSConfiguration are valid.
func (g *GCSConfiguration) Validate() error {
	if _, err := ParsePath(g.Bucket); err != nil {
		return err
	}
	for _, mediaType := range g.MediaTypes {
		if _, _, err := mime.ParseMediaType(mediaType); err != nil {
			return fmt.Errorf("invalid extension media type %q: %w", mediaType, err)
		}
	}
	if g.PathStrategy != PathStrategyLegacy && g.PathStrategy != PathStrategyExplicit && g.PathStrategy != PathStrategySingle {
		return fmt.Errorf("gcs_path_strategy must be one of %q, %q, or %q", PathStrategyLegacy, PathStrategyExplicit, PathStrategySingle)
	}
	if g.PathStrategy != PathStrategyExplicit && (g.DefaultOrg == "" || g.DefaultRepo == "") {
		return fmt.Errorf("default org and repo must be provided for GCS strategy %q", g.PathStrategy)
	}
	return nil
}

type ProwPath url.URL

func (pp ProwPath) StorageProvider() string {
	return pp.Scheme
}

func (pp ProwPath) Bucket() string {
	return pp.Host
}

func (pp ProwPath) BucketWithScheme() string {
	return fmt.Sprintf("%s://%s", pp.StorageProvider(), pp.Bucket())
}

func (pp ProwPath) FullPath() string {
	return pp.Host + pp.Path
}

func (pp *ProwPath) String() string {
	return (*url.URL)(pp).String()
}

// ParsePath tries to extract the ProwPath from, e.g.:
// * <bucket-name> (storageProvider gs)
// * <storage-provider>://<bucket-name>
func ParsePath(bucket string) (*ProwPath, error) {
	// default to GCS if no storage-provider is specified
	if !strings.Contains(bucket, "://") {
		bucket = "gs://" + bucket
	}
	parsedBucket, err := url.Parse(bucket)
	if err != nil {
		return nil, fmt.Errorf("path %q has invalid format, expected either <bucket-name>[/<path>] or <storage-provider>://<bucket-name>[/<path>]", bucket)
	}
	pp := ProwPath(*parsedBucket)
	return &pp, nil
}

// ProwJobStatus provides runtime metadata, such as when it finished, whether it is running, etc.
type ProwJobStatus struct {
	// StartTime is equal to the creation time of the ProwJob
	StartTime metav1.Time `json:"startTime,omitempty"`
	// PendingTime is the timestamp for when the job moved from triggered to pending
	PendingTime *metav1.Time `json:"pendingTime,omitempty"`
	// CompletionTime is the timestamp for when the job goes to a final state
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// +kubebuilder:validation:Enum=triggered;pending;success;failure;aborted;error
	// +kubebuilder:validation:Required
	State       ProwJobState `json:"state,omitempty"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`

	// PodName applies only to ProwJobs fulfilled by
	// plank. This field should always be the same as
	// the ProwJob.ObjectMeta.Name field.
	PodName string `json:"pod_name,omitempty"`

	// BuildID is the build identifier vended either by tot
	// or the snowflake library for this job and used as an
	// identifier for grouping artifacts in GCS for views in
	// TestGrid and Gubernator. Idenitifiers vended by tot
	// are monotonically increasing whereas identifiers vended
	// by the snowflake library are not.
	BuildID string `json:"build_id,omitempty"`

	// JenkinsBuildID applies only to ProwJobs fulfilled
	// by the jenkins-operator. This field is the build
	// identifier that Jenkins gave to the build for this
	// ProwJob.
	JenkinsBuildID string `json:"jenkins_build_id,omitempty"`

	// PrevReportStates stores the previous reported prowjob state per reporter
	// So crier won't make duplicated report attempt
	PrevReportStates map[string]ProwJobState `json:"prev_report_states,omitempty"`
}

// Complete returns true if the prow job has finished
func (j *ProwJob) Complete() bool {
	// TODO(fejta): support a timeout?
	return j.Status.CompletionTime != nil
}

// SetComplete marks the job as completed (at time now).
func (j *ProwJob) SetComplete() {
	j.Status.CompletionTime = new(metav1.Time)
	*j.Status.CompletionTime = metav1.Now()
}

// ClusterAlias specifies the key in the clusters map to use.
//
// This allows scheduling a prow job somewhere aside from the default build cluster.
func (j *ProwJob) ClusterAlias() string {
	if j.Spec.Cluster == "" {
		return DefaultClusterAlias
	}
	return j.Spec.Cluster
}

// Pull describes a pull request at a particular point in time.
type Pull struct {
	Number int    `json:"number"`
	Author string `json:"author"`
	SHA    string `json:"sha"`
	Title  string `json:"title,omitempty"`

	// Ref is git ref can be checked out for a change
	// for example,
	// github: pull/123/head
	// gerrit: refs/changes/00/123/1
	Ref string `json:"ref,omitempty"`
	// HeadRef is the git ref (branch name) of the proposed change.  This can be more human-readable than just
	// a PR #, and some tools want this metadata to help associate the work with a pull request (e.g. some code
	// scanning services, or chromatic.com).
	HeadRef string `json:"head_ref,omitempty"`
	// Link links to the pull request itself.
	Link string `json:"link,omitempty"`
	// CommitLink links to the commit identified by the SHA.
	CommitLink string `json:"commit_link,omitempty"`
	// AuthorLink links to the author of the pull request.
	AuthorLink string `json:"author_link,omitempty"`
}

// Refs describes how the repo was constructed.
type Refs struct {
	// Org is something like kubernetes or k8s.io
	Org string `json:"org"`
	// Repo is something like test-infra
	Repo string `json:"repo"`
	// RepoLink links to the source for Repo.
	RepoLink string `json:"repo_link,omitempty"`

	BaseRef string `json:"base_ref,omitempty"`
	BaseSHA string `json:"base_sha,omitempty"`
	// BaseLink is a link to the commit identified by BaseSHA.
	BaseLink string `json:"base_link,omitempty"`

	Pulls []Pull `json:"pulls,omitempty"`

	// PathAlias is the location under <root-dir>/src
	// where this repository is cloned. If this is not
	// set, <root-dir>/src/github.com/org/repo will be
	// used as the default.
	PathAlias string `json:"path_alias,omitempty"`

	// WorkDir defines if the location of the cloned
	// repository will be used as the default working
	// directory.
	WorkDir bool `json:"workdir,omitempty"`

	// CloneURI is the URI that is used to clone the
	// repository. If unset, will default to
	// `https://github.com/org/repo.git`.
	CloneURI string `json:"clone_uri,omitempty"`
	// SkipSubmodules determines if submodules should be
	// cloned when the job is run. Defaults to false.
	SkipSubmodules bool `json:"skip_submodules,omitempty"`
	// CloneDepth is the depth of the clone that will be used.
	// A depth of zero will do a full clone.
	CloneDepth int `json:"clone_depth,omitempty"`
	// SkipFetchHead tells prow to avoid a git fetch <remote> call.
	// Multiheaded repos may need to not make this call.
	// The git fetch <remote> <BaseRef> call occurs regardless.
	SkipFetchHead bool `json:"skip_fetch_head,omitempty"`
}

func (r Refs) String() string {
	rs := []string{}
	if r.BaseSHA != "" {
		rs = append(rs, fmt.Sprintf("%s:%s", r.BaseRef, r.BaseSHA))
	} else {
		rs = append(rs, r.BaseRef)
	}

	for _, pull := range r.Pulls {
		ref := fmt.Sprintf("%d:%s", pull.Number, pull.SHA)

		if pull.Ref != "" {
			ref = fmt.Sprintf("%s:%s", ref, pull.Ref)
		}

		rs = append(rs, ref)
	}
	return strings.Join(rs, ",")
}

func (r Refs) OrgRepoString() string {
	if r.Repo != "" {
		return r.Org + "/" + r.Repo
	}
	return r.Org
}

// JenkinsSpec is optional parameters for Jenkins jobs.
// Currently, the only parameter supported is for telling
// jenkins-operator that the job is generated by the https://go.cloudbees.com/docs/plugins/github-branch-source/#github-branch-source plugin
type JenkinsSpec struct {
	GitHubBranchSourceJob bool `json:"github_branch_source_job,omitempty"`
}

// TektonPipelineRunSpec is optional parameters for Tekton pipeline jobs.
type TektonPipelineRunSpec struct {
	V1Beta1 *pipelinev1beta1.PipelineRunSpec `json:"v1beta1,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ProwJobList is a list of ProwJob resources
type ProwJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []ProwJob `json:"items"`
}
