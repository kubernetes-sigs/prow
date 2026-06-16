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
	"maps"
	"mime"
	"net/url"
	"strings"
	"time"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prowgithub "sigs.k8s.io/prow/pkg/github"
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
	// SchedulingState means the job has been created and it is waiting to be scheduled.
	SchedulingState ProwJobState = "scheduling"
	// TriggeredState means the job has been scheduled but it is not running yet.
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
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the ProwJob spec
	// +optional
	Spec ProwJobSpec `json:"spec,omitempty"`
	// status is the ProwJob status
	// +optional
	Status ProwJobStatus `json:"status,omitempty"`
}

// ProwJobSpec configures the details of the prow job.
//
// Details include the podspec, code to clone, the cluster it runs
// any child jobs, concurrency limitations, etc.
type ProwJobSpec struct {
	// type is the type of job and informs how
	// the jobs is triggered
	// +kubebuilder:validation:Enum=presubmit;postsubmit;periodic;batch
	// +required
	Type ProwJobType `json:"type,omitempty"`
	// agent determines which controller fulfills
	// this specific ProwJobSpec and runs the job
	// +optional
	Agent ProwJobAgent `json:"agent,omitempty"`
	// cluster is which Kubernetes cluster is used
	// to run the job, only applicable for that
	// specific agent
	// +optional
	Cluster string `json:"cluster,omitempty"`
	// namespace defines where to create pods/resources.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// job is the name of the job
	// +required
	Job string `json:"job,omitempty"`
	// refs is the code under test, determined at
	// runtime by Prow itself
	// +optional
	Refs *Refs `json:"refs,omitempty"`
	// extra_refs are auxiliary repositories that
	// need to be cloned, determined from config
	// +optional
	ExtraRefs []Refs `json:"extra_refs,omitempty"`
	// report determines if the result of this job should
	// be reported (e.g. status on GitHub, message in Slack, etc.)
	// +optional
	Report bool `json:"report,omitempty"`
	// context is the name of the status context used to
	// report back to GitHub
	// +optional
	Context string `json:"context,omitempty"`
	// rerun_command is the command a user would write to
	// trigger this job on their pull request
	// +optional
	RerunCommand string `json:"rerun_command,omitempty"`
	// max_concurrency restricts the total number of instances
	// of this job that can run in parallel at once. This is
	// a separate mechanism to JobQueueName and the lowest max
	// concurrency is selected from these two.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxConcurrency int `json:"max_concurrency,omitempty"`
	// error_on_eviction indicates that the ProwJob should be completed and given
	// the ErrorState status if the pod that is executing the job is evicted.
	// If this field is unspecified or false, a new pod will be created to replace
	// the evicted one.
	// +optional
	ErrorOnEviction bool `json:"error_on_eviction,omitempty"`

	// pod_spec provides the basis for running the test under
	// a Kubernetes agent
	// +optional
	PodSpec *corev1.PodSpec `json:"pod_spec,omitempty"`

	// jenkins_spec holds configuration specific to Jenkins jobs
	// +optional
	JenkinsSpec *JenkinsSpec `json:"jenkins_spec,omitempty"`

	// pipeline_run_spec provides the basis for running the test as
	// a pipeline-crd resource
	// https://github.com/tektoncd/pipeline
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:XPreserveUnknownFields
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	PipelineRunSpec *pipelinev1.PipelineRunSpec `json:"pipeline_run_spec,omitempty"`

	// tekton_pipeline_run_spec provides the basis for running the test as
	// a pipeline-crd resource
	// https://github.com/tektoncd/pipeline
	// +optional
	TektonPipelineRunSpec *TektonPipelineRunSpec `json:"tekton_pipeline_run_spec,omitempty"`

	// decoration_config holds configuration options for
	// decorating PodSpecs that users provide
	// +optional
	DecorationConfig *DecorationConfig `json:"decoration_config,omitempty"`

	// reporter_config holds reporter-specific configuration
	// +optional
	ReporterConfig *ReporterConfig `json:"reporter_config,omitempty"`

	// rerun_auth_config holds information about which users can rerun the job
	// +optional
	RerunAuthConfig *RerunAuthConfig `json:"rerun_auth_config,omitempty"`

	// hidden specifies if the Job is considered hidden.
	// Hidden jobs are only shown by deck instances that have the
	// `--hiddenOnly=true` or `--show-hidden=true` flag set.
	// Presubmits and Postsubmits can also be set to hidden by
	// adding their repository in Decks `hidden_repo` setting.
	// +optional
	Hidden bool `json:"hidden,omitempty"`

	// prowjob_defaults holds configuration options provided as defaults
	// in the Prow config
	// +optional
	ProwJobDefault *ProwJobDefault `json:"prowjob_defaults,omitempty"`

	// job_queue_name is an optional field with name of a queue defining
	// max concurrency. When several jobs from the same queue try to run
	// at the same time, the number of them that is actually started is
	// limited by JobQueueCapacities (part of Plank's config). If
	// this field is left undefined infinite concurrency is assumed.
	// This behaviour may be superseded by MaxConcurrency field, if it
	// is set to a constraining value.
	// +optional
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

func (pjs ProwJobSpec) GetPipelineRunSpec() (*pipelinev1.PipelineRunSpec, error) {
	var found *pipelinev1.PipelineRunSpec
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
	// slug is the team slug
	// +required
	Slug string `json:"slug"`
	// org is the GitHub organization
	// +required
	Org string `json:"org"`
}

type RerunAuthConfig struct {
	// allow_anyone if set to true, any user can rerun the job
	// +optional
	AllowAnyone bool `json:"allow_anyone,omitempty"`
	// github_team_ids contains IDs of GitHub teams of users who can rerun the job
	// If you know the name of a team and the org it belongs to,
	// you can look up its ID using this command, where the team slug is the hyphenated name:
	// curl -H "Authorization: token <token>" "https://api.github.com/orgs/<org-name>/teams/<team slug>"
	// or, to list all teams in a given org, use
	// curl -H "Authorization: token <token>" "https://api.github.com/orgs/<org-name>/teams"
	// +optional
	GitHubTeamIDs []int `json:"github_team_ids,omitempty"`
	// github_team_slugs contains slugs and orgs of teams of users who can rerun the job
	// +optional
	GitHubTeamSlugs []GitHubTeamSlug `json:"github_team_slugs,omitempty"`
	// github_users contains names of individual users who can rerun the job
	// +optional
	GitHubUsers []string `json:"github_users,omitempty"`
	// github_orgs contains names of GitHub organizations whose members can rerun the job
	// +optional
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
	// slack is the Slack reporter configuration
	// +optional
	Slack *SlackReporterConfig `json:"slack,omitempty"`
}

type SlackReporterConfig struct {
	// host is the Slack host
	// +optional
	Host string `json:"host,omitempty"`
	// channel is the Slack channel
	// +optional
	Channel string `json:"channel,omitempty"`
	// job_states_to_report lists the job states to report
	// +optional
	JobStatesToReport []ProwJobState `json:"job_states_to_report,omitempty"`
	// report_template is the template for the report
	// +optional
	ReportTemplate string `json:"report_template,omitempty"`
	// report is derived from JobStatesToReport, it's used for differentiating
	// nil from empty slice, as yaml roundtrip by design can't tell the
	// difference when omitempty is supplied.
	// See https://github.com/kubernetes/test-infra/pull/24168 for details
	// Priority-wise, it goes by following order:
	// - `report: true/false`` in job config
	// - `JobStatesToReport: <anything including empty slice>` in job config
	// - `report: true/false`` in global config
	// - `JobStatesToReport:` in global config
	// +optional
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
	// +optional
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
	// resultstore_config specifies parameters for ResultStore
	// +optional
	ResultStoreConfig *ResultStoreConfig `json:"resultstore_config,omitempty"`
	// tenant_id is the tenant identifier
	// +optional
	TenantID string `json:"tenant_id,omitempty"`
}

// ResultStoreConfig specifies parameters for uploading results to
// the ResultStore service.
type ResultStoreConfig struct {
	// project_id specifies the ResultStore InvocationAttributes.ProjectID, used
	// for various quota and GUI access control purposes.
	// In practice, it is generally the same as the Google Cloud Project ID or
	// number of the job's GCS storage bucket.
	// Required to upload results to ResultStore.
	// +optional
	ProjectID string `json:"project_id,omitempty"`
}

// DecorationConfig specifies how to augment pods.
//
// This is primarily used to provide automatic integration with gubernator
// and testgrid.
type DecorationConfig struct {
	// timeout is how long the pod utilities will wait
	// before aborting a job with SIGINT.
	// +optional
	Timeout *Duration `json:"timeout,omitempty"`
	// grace_period is how long the pod utilities will wait
	// after sending SIGINT to send SIGKILL when aborting
	// a job. Only applicable if decorating the PodSpec.
	// +optional
	GracePeriod *Duration `json:"grace_period,omitempty"`

	// utility_images holds pull specs for utility container
	// images used to decorate a PodSpec.
	// +optional
	UtilityImages *UtilityImages `json:"utility_images,omitempty"`
	// resources holds resource requests and limits for utility
	// containers used to decorate a PodSpec.
	// +optional
	Resources *Resources `json:"resources,omitempty"`
	// gcs_configuration holds options for pushing logs and
	// artifacts to GCS from a job.
	// +optional
	GCSConfiguration *GCSConfiguration `json:"gcs_configuration,omitempty"`
	// gcs_credentials_secret is the name of the Kubernetes secret
	// that holds GCS push credentials.
	// +optional
	GCSCredentialsSecret *string `json:"gcs_credentials_secret,omitempty"`
	// s3_credentials_secret is the name of the Kubernetes secret
	// that holds blob storage push credentials.
	// +optional
	S3CredentialsSecret *string `json:"s3_credentials_secret,omitempty"`
	// default_service_account_name is the name of the Kubernetes service account
	// that should be used by the pod if one is not specified in the podspec.
	// +optional
	DefaultServiceAccountName *string `json:"default_service_account_name,omitempty"`
	// ssh_key_secrets are the names of Kubernetes secrets that contain
	// SSK keys which should be used during the cloning process.
	// +optional
	SSHKeySecrets []string `json:"ssh_key_secrets,omitempty"`
	// ssh_host_fingerprints are the fingerprints of known SSH hosts
	// that the cloning process can trust.
	// Create with ssh-keyscan [-t rsa] host
	// +optional
	SSHHostFingerprints []string `json:"ssh_host_fingerprints,omitempty"`
	// blobless_fetch tells Prow to avoid fetching objects when cloning using
	// the --filter=blob:none flag.
	// +optional
	BloblessFetch *bool `json:"blobless_fetch,omitempty"`
	// sparse_checkout_files limits the working tree to only the listed paths.
	// Accepts the same patterns as git sparse-checkout set (file names,
	// directory names, gitignore-style globs). When set, clonerefs performs a
	// sparse checkout instead of a full clone. Applied to the primary ref for
	// presubmit/postsubmit jobs. Not applied to extra refs.
	// +optional
	SparseCheckoutFiles []string `json:"sparse_checkout_files,omitempty"`
	// skip_cloning determines if we should clone source code in the
	// initcontainers for jobs that specify refs
	// +optional
	SkipCloning *bool `json:"skip_cloning,omitempty"`
	// cookiefile_secret is the name of a kubernetes secret that contains
	// a git http.cookiefile, which should be used during the cloning process.
	// +optional
	CookiefileSecret *string `json:"cookiefile_secret,omitempty"`
	// oauth_token_secret is a Kubernetes secret that contains the OAuth token,
	// which is going to be used for fetching a private repository.
	// +optional
	OauthTokenSecret *OauthTokenSecret `json:"oauth_token_secret,omitempty"`
	// github_api_endpoints are the endpoints of GitHub APIs.
	// +optional
	GitHubAPIEndpoints []string `json:"github_api_endpoints,omitempty"`
	// github_app_id is the ID of GitHub App, which is going to be used for fetching a private
	// repository.
	// +optional
	GitHubAppID string `json:"github_app_id,omitempty"`
	// github_app_private_key_secret is a Kubernetes secret that contains the GitHub App private key,
	// which is going to be used for fetching a private repository.
	// +optional
	GitHubAppPrivateKeySecret *GitHubAppPrivateKeySecret `json:"github_app_private_key_secret,omitempty"`

	// censor_secrets enables censoring output logs and artifacts.
	// +optional
	CensorSecrets *bool `json:"censor_secrets,omitempty"`

	// censoring_options exposes options for censoring output logs and artifacts.
	// +optional
	CensoringOptions *CensoringOptions `json:"censoring_options,omitempty"`

	// upload_ignores_interrupts causes sidecar to ignore interrupts for the upload process in
	// hope that the test process exits cleanly before starting an upload.
	// +optional
	UploadIgnoresInterrupts *bool `json:"upload_ignores_interrupts,omitempty"`

	// set_limit_equals_memory_request sets memory limit equal to request.
	// +optional
	SetLimitEqualsMemoryRequest *bool `json:"set_limit_equals_memory_request,omitempty"`
	// default_memory_request is the default requested memory on a test container.
	// If SetLimitEqualsMemoryRequest is also true then the Limit will also be
	// set the same as this request. Could be overridden by memory request
	// defined explicitly on prowjob.
	// +optional
	DefaultMemoryRequest *resource.Quantity `json:"default_memory_request,omitempty"`

	// scheduling_options define the configuration for fields required for pod scheduling.
	// These fields directly modify the way how pods can be scheduled giving the operator
	// ability to run workloads on designated node.
	// If these fields are already present in the pod definition, they will be ignored.
	// +optional
	SchedulingOptions *SchedulingOptions `json:"scheduling_options,omitempty"`

	// pod_pending_timeout defines how long the controller will wait to perform garbage
	// collection on pending pods. Specific for OrgRepo or Cluster. If not set, it has a fallback inside plank field.
	// +optional
	PodPendingTimeout *metav1.Duration `json:"pod_pending_timeout,omitempty"`
	// pod_running_timeout defines how long the controller will wait to abort a prowjob pod
	// stuck in running state. Specific for OrgRepo or Cluster. If not set, it has a fallback inside plank field.
	// +optional
	PodRunningTimeout *metav1.Duration `json:"pod_running_timeout,omitempty"`
	// pod_unscheduled_timeout defines how long the controller will wait to abort a prowjob
	// stuck in an unscheduled state. Specific for OrgRepo or Cluster. If not set, it has a fallback inside plank field.
	// +optional
	PodUnscheduledTimeout *metav1.Duration `json:"pod_unscheduled_timeout,omitempty"`

	// run_as_user defines UID for process in all containers running in a Pod.
	// This field will not override the existing ProwJob's PodSecurityContext.
	// Equivalent to PodSecurityContext's RunAsUser
	// +optional
	RunAsUser *int64 `json:"run_as_user,omitempty"`
	// run_as_group defines GID of process in all containers running in a Pod.
	// This field will not override the existing ProwJob's PodSecurityContext.
	// Equivalent to PodSecurityContext's RunAsGroup
	// +optional
	RunAsGroup *int64 `json:"run_as_group,omitempty"`
	// fs_group defines special supplemental group ID used in all containers in a Pod.
	// This allows to change the ownership of particular volumes by kubelet.
	// This field will not override the existing ProwJob's PodSecurityContext.
	// Equivalent to PodSecurityContext's FsGroup
	// +optional
	FsGroup *int64 `json:"fs_group,omitempty"`
}

type CensoringOptions struct {
	// censoring_concurrency is the maximum number of goroutines that should be censoring
	// artifacts and logs at any time. If unset, defaults to 10.
	// +optional
	CensoringConcurrency *int64 `json:"censoring_concurrency,omitempty"`
	// censoring_buffer_size is the size in bytes of the buffer allocated for every file
	// being censored. We want to keep as little of the file in memory as possible in
	// order for censoring to be reasonably performant in space. However, to guarantee
	// that we censor every instance of every secret, our buffer size must be at least
	// two times larger than the largest secret we are about to censor. While that size
	// is the smallest possible buffer we could use, if the secrets being censored are
	// small, censoring will not be performant as the number of I/O actions per file
	// would increase. If unset, defaults to 10MiB.
	// +optional
	CensoringBufferSize *int `json:"censoring_buffer_size,omitempty"`

	// include_directories are directories which should have their content censored. If
	// present, only content in these directories will be censored. Entries in this list
	// are relative to $ARTIFACTS and are parsed with the go-zglob library, allowing for
	// globbed matches.
	// +optional
	IncludeDirectories []string `json:"include_directories,omitempty"`

	// exclude_directories are directories which should not have their content censored. If
	// present, content in these directories will not be censored even if the directory also
	// matches a glob in IncludeDirectories. Entries in this list are relative to $ARTIFACTS,
	// and are parsed with the go-zglob library, allowing for globbed matches.
	// +optional
	ExcludeDirectories []string `json:"exclude_directories,omitempty"`

	// minimum_secret_length is the minimum length a secret must have to be censored.
	// Secrets shorter than this length will not be censored. If unset, defaults to 0
	// (all secrets are censored regardless of length).
	// +optional
	MinimumSecretLength *int `json:"minimum_secret_length,omitempty"`
}

type SchedulingOptions struct {
	// affinity is the Pod Affinity configuration applied to the ProwJob's pod.
	// Equivalent to PodSpec's Affinity
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// tolerations define list of tolerable taints applied to the ProwJob's pod.
	// Equivalent to PodSpec's Tolerations
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
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

	if merged.MinimumSecretLength == nil {
		merged.MinimumSecretLength = def.MinimumSecretLength
	}
	return &merged
}

// ApplyDefault applies the defaults for SchedulingOptions decorations. If a field has a zero value,
// it replaces that with the value set in def.
func (g *SchedulingOptions) ApplyDefault(def *SchedulingOptions) *SchedulingOptions {
	if g == nil && def == nil {
		return nil
	}
	var merged SchedulingOptions
	if g != nil {
		merged = *g.DeepCopy()
	} else {
		merged = *def.DeepCopy()
	}
	if g == nil || def == nil {
		return &merged
	}

	if merged.Affinity == nil {
		merged.Affinity = def.Affinity
	}

	if merged.Tolerations == nil {
		merged.Tolerations = def.Tolerations
	}

	return &merged
}

// Resources holds resource requests and limits for
// containers used to decorate a PodSpec
type Resources struct {
	// clonerefs is the resource requirements for the clonerefs utility
	// +optional
	CloneRefs *corev1.ResourceRequirements `json:"clonerefs,omitempty"`
	// initupload is the resource requirements for the initupload utility
	// +optional
	InitUpload *corev1.ResourceRequirements `json:"initupload,omitempty"`
	// place_entrypoint is the resource requirements for the place_entrypoint utility
	// +optional
	PlaceEntrypoint *corev1.ResourceRequirements `json:"place_entrypoint,omitempty"`
	// sidecar is the resource requirements for the sidecar utility
	// +optional
	Sidecar *corev1.ResourceRequirements `json:"sidecar,omitempty"`
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
	// name is the name of a kubernetes secret.
	// +optional
	Name string `json:"name,omitempty"`
	// key is the key of the corresponding kubernetes secret that
	// holds the value of the OAuth token.
	// +optional
	Key string `json:"key,omitempty"`
}

// GitHubAppPrivateKeySecret holds the information of the GitHub App private key's secret name and key.
type GitHubAppPrivateKeySecret struct {
	// name is the name of a kubernetes secret.
	// +optional
	Name string `json:"name,omitempty"`
	// key is the key of the corresponding kubernetes secret that
	// holds the value of the GitHub App private key.
	// +optional
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
	if merged.ResultStoreConfig == nil {
		merged.ResultStoreConfig = def.ResultStoreConfig
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
	merged.SchedulingOptions = merged.SchedulingOptions.ApplyDefault(def.SchedulingOptions)

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

	if merged.BloblessFetch == nil {
		merged.BloblessFetch = def.BloblessFetch
	}
	if len(merged.SparseCheckoutFiles) == 0 {
		merged.SparseCheckoutFiles = def.SparseCheckoutFiles
	}
	if merged.SchedulingOptions == nil {
		merged.SchedulingOptions = def.SchedulingOptions
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
	// clonerefs is the pull spec used for the clonerefs utility
	// +optional
	CloneRefs string `json:"clonerefs,omitempty"`
	// initupload is the pull spec used for the initupload utility
	// +optional
	InitUpload string `json:"initupload,omitempty"`
	// entrypoint is the pull spec used for the entrypoint utility
	// +optional
	Entrypoint string `json:"entrypoint,omitempty"`
	// sidecar is the pull spec used for the sidecar utility
	// +optional
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
	// bucket is the bucket to upload to, it can be:
	// * a GCS bucket: with gs:// prefix
	// * a S3 bucket: with s3:// prefix
	// * a GCS bucket: without a prefix (deprecated, it's discouraged to use Bucket without prefix please add the gs:// prefix)
	// +optional
	Bucket string `json:"bucket,omitempty"`
	// path_prefix is an optional path that follows the
	// bucket name and comes before any structure
	// +optional
	PathPrefix string `json:"path_prefix,omitempty"`
	// path_strategy dictates how the org and repo are used
	// when calculating the full path to an artifact in GCS
	// +optional
	PathStrategy string `json:"path_strategy,omitempty"`
	// default_org is omitted from GCS paths when using the
	// legacy or simple strategy
	// +optional
	DefaultOrg string `json:"default_org,omitempty"`
	// default_repo is omitted from GCS paths when using the
	// legacy or simple strategy
	// +optional
	DefaultRepo string `json:"default_repo,omitempty"`
	// mediaTypes holds additional extension media types to add to Go's
	// builtin's and the local system's defaults.  This maps extensions
	// to media types, for example: MediaTypes["log"] = "text/plain"
	// +optional
	MediaTypes map[string]string `json:"mediaTypes,omitempty"`
	// job_url_prefix holds the baseURL under which the jobs output can be viewed.
	// If unset, this will be derived based on org/repo from the job_url_prefix_config.
	// +optional
	JobURLPrefix string `json:"job_url_prefix,omitempty"`

	// local_output_dir specifies a directory where files should be copied INSTEAD of uploading to blob storage.
	// This option is useful for testing jobs that use the pod-utilities without actually uploading.
	// +optional
	LocalOutputDir string `json:"local_output_dir,omitempty"`
	// compress_file_types specify file types that should be gzipped prior to upload.
	// Matching files will be compressed prior to upload, and the content-encoding on these files will be set to gzip.
	// GCS will transcode these gzipped files transparently when viewing. See: https://cloud.google.com/storage/docs/transcoding
	// Example: "txt", "json"
	// Use "*" for all
	// +optional
	CompressFileTypes []string `json:"compress_file_types,omitempty"`
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

	maps.Copy(merged.MediaTypes, def.MediaTypes)

	if merged.JobURLPrefix == "" {
		merged.JobURLPrefix = def.JobURLPrefix
	}

	if merged.LocalOutputDir == "" {
		merged.LocalOutputDir = def.LocalOutputDir
	}
	if merged.CompressFileTypes == nil {
		merged.CompressFileTypes = def.CompressFileTypes
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
	// startTime is equal to the creation time of the ProwJob
	// +optional
	StartTime metav1.Time `json:"startTime,omitempty"`
	// pendingTime is the timestamp for when the job moved from triggered to pending
	// +optional
	PendingTime *metav1.Time `json:"pendingTime,omitempty"`
	// completionTime is the timestamp for when the job goes to a final state
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// state is the current state of the prow job
	// +kubebuilder:validation:Enum=scheduling;triggered;pending;success;failure;aborted;error
	// +required
	State ProwJobState `json:"state,omitempty"`
	// description is a human-readable description of the job status
	// +optional
	Description string `json:"description,omitempty"`
	// url is the URL where the job results can be viewed
	// +optional
	URL string `json:"url,omitempty"`

	// pod_revival_count applies only to ProwJobs fulfilled by
	// plank. This field shows the amount of times the
	// Pod was recreated due to an unexpected stop.
	// +optional
	PodRevivalCount int `json:"pod_revival_count,omitempty"`
	// pod_name applies only to ProwJobs fulfilled by
	// plank. This field should always be the same as
	// the ProwJob.ObjectMeta.Name field.
	// +optional
	PodName string `json:"pod_name,omitempty"`

	// build_id is the build identifier vended either by tot
	// or the snowflake library for this job and used as an
	// identifier for grouping artifacts in GCS for views in
	// TestGrid and Gubernator. Idenitifiers vended by tot
	// are monotonically increasing whereas identifiers vended
	// by the snowflake library are not.
	// +optional
	BuildID string `json:"build_id,omitempty"`

	// jenkins_build_id applies only to ProwJobs fulfilled
	// by the jenkins-operator. This field is the build
	// identifier that Jenkins gave to the build for this
	// ProwJob.
	// +optional
	JenkinsBuildID string `json:"jenkins_build_id,omitempty"`

	// prev_report_states stores the previous reported prowjob state per reporter
	// So crier won't make duplicated report attempt
	// +optional
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
	// number is the pull request number
	// +required
	Number int `json:"number"`
	// author is the author of the pull request
	// +required
	Author string `json:"author"`
	// sha is the SHA of the pull request head
	// +required
	SHA string `json:"sha"`
	// title is the title of the pull request
	// +optional
	Title string `json:"title,omitempty"`

	// ref is git ref can be checked out for a change
	// for example,
	// github: pull/123/head
	// gerrit: refs/changes/00/123/1
	// +optional
	Ref string `json:"ref,omitempty"`
	// head_ref is the git ref (branch name) of the proposed change.  This can be more human-readable than just
	// a PR #, and some tools want this metadata to help associate the work with a pull request (e.g. some code
	// scanning services, or chromatic.com).
	// +optional
	HeadRef string `json:"head_ref,omitempty"`
	// link links to the pull request itself.
	// +optional
	Link string `json:"link,omitempty"`
	// commit_link links to the commit identified by the SHA.
	// +optional
	CommitLink string `json:"commit_link,omitempty"`
	// author_link links to the author of the pull request.
	// +optional
	AuthorLink string `json:"author_link,omitempty"`
}

// Refs describes how the repo was constructed.
type Refs struct {
	// org is something like kubernetes or k8s.io
	// +required
	Org string `json:"org"`
	// repo is something like test-infra
	// +required
	Repo string `json:"repo"`
	// repo_link links to the source for Repo.
	// +optional
	RepoLink string `json:"repo_link,omitempty"`

	// base_ref is the base git ref of the pull request
	// +optional
	BaseRef string `json:"base_ref,omitempty"`
	// base_sha is the base git SHA of the pull request
	// +optional
	BaseSHA string `json:"base_sha,omitempty"`
	// base_link is a link to the commit identified by BaseSHA.
	// +optional
	BaseLink string `json:"base_link,omitempty"`

	// pulls is the list of pull requests associated with the ref
	// +optional
	Pulls []Pull `json:"pulls,omitempty"`

	// path_alias is the location under <root-dir>/src
	// where this repository is cloned. If this is not
	// set, <root-dir>/src/github.com/org/repo will be
	// used as the default.
	// +optional
	PathAlias string `json:"path_alias,omitempty"`

	// workdir defines if the location of the cloned
	// repository will be used as the default working
	// directory.
	// +optional
	WorkDir bool `json:"workdir,omitempty"`

	// auxiliary indicates that the repository really only provides
	// auxiliary files for the job and is not the main repository that is
	// being tested.
	//
	// This is relevant when determining which version to record in a
	// periodic job's started.json file: the first repository where
	// Auxiliary is false or unset is considered the main repository
	// and determines the version.
	//
	// In presubmit jobs the version always comes from the repository
	// for which the job is defined.
	// +optional
	Auxiliary bool `json:"auxiliary,omitempty"`

	// clone_uri is the URI that is used to clone the
	// repository. If unset, will default to
	// `https://github.com/org/repo.git`.
	// +optional
	CloneURI string `json:"clone_uri,omitempty"`
	// skip_submodules determines if submodules should be
	// cloned when the job is run. Defaults to false.
	// +optional
	SkipSubmodules bool `json:"skip_submodules,omitempty"`
	// clone_depth is the depth of the clone that will be used.
	// A depth of zero will do a full clone.
	// +optional
	CloneDepth int `json:"clone_depth,omitempty"`
	// skip_fetch_head tells prow to avoid a git fetch <remote> call.
	// Multiheaded repos may need to not make this call.
	// The git fetch <remote> <BaseRef> call occurs regardless.
	// +optional
	SkipFetchHead bool `json:"skip_fetch_head,omitempty"`
	// blobless_fetch tells prow to avoid fetching objects when cloning
	// using the --filter=blob:none flag. If unspecified, defaults to
	// DecorationConfig.BloblessFetch.
	// +optional
	BloblessFetch *bool `json:"blobless_fetch,omitempty"`
	// sparse_checkout_files limits the working tree to only the listed paths.
	// Accepts the same patterns as git sparse-checkout set: file names,
	// directory names, and gitignore-style globs (e.g. "Makefile",
	// "pkg/operator", "config/**/*.yaml").
	//
	// When set, clonerefs will:
	//   1. run git sparse-checkout init to enable sparse mode
	//   2. run git fetch with --depth 1 --filter=blob:none --no-tags
	//   3. run git sparse-checkout set <paths> before checkout
	//
	// Only the blobs needed for the requested paths are downloaded.
	// +optional
	SparseCheckoutFiles []string `json:"sparse_checkout_files,omitempty"`
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
	// github_branch_source_job indicates if the job is generated by the
	// https://go.cloudbees.com/docs/plugins/github-branch-source/#github-branch-source plugin
	// +optional
	GitHubBranchSourceJob bool `json:"github_branch_source_job,omitempty"`
}

// TektonPipelineRunSpec is optional parameters for Tekton pipeline jobs.
type TektonPipelineRunSpec struct {
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:XPreserveUnknownFields
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	V1Beta1 *pipelinev1.PipelineRunSpec `json:"v1beta1,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ProwJobList is a list of ProwJob resources
type ProwJobList struct {
	// +optional
	metav1.TypeMeta `json:",inline"`
	// +required
	metav1.ListMeta `json:"metadata"`

	// +required
	Items []ProwJob `json:"items"`
}
