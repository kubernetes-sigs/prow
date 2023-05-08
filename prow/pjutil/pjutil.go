/*
Copyright 2017 The Kubernetes Authors.

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

// Package pjutil contains helpers for working with ProwJobs.
package pjutil

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"strconv"

	uuid "github.com/google/uuid"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/gcsupload"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pod-utils/decorate"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
)

// NewProwJob initializes a ProwJob out of a ProwJobSpec.
func NewProwJob(spec prowapi.ProwJobSpec, extraLabels, extraAnnotations map[string]string) prowapi.ProwJob {
	labels, annotations := decorate.LabelsAndAnnotationsForSpec(spec, extraLabels, extraAnnotations)
	specCopy := spec.DeepCopy()
	setReportDefault(specCopy)
	uuidV1 := uuid.New()

	return prowapi.ProwJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "prow.k8s.io/v1",
			Kind:       "ProwJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        uuidV1.String(),
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: *specCopy,
		Status: prowapi.ProwJobStatus{
			StartTime: metav1.Now(),
			State:     prowapi.TriggeredState,
		},
	}
}

// setReportDefault sets Slack to false when states to report is an empty slice.
//
// `omitempty` is required for fields that are optional, otherwise strict prowjob CRD
// validation will reject prowjob without the field.
// For `ReporterConfig.Slack.JobStatesToReport`, it's certainly not a required field
// for all prowjobs.
// However, when omitempty is presented, `JobStatesToReport: []` roundtrip into
// `JobStatesToReport: nil`, which prevents a prowjob from overriding global Slack
// report configuration(ref: https://github.com/kubernetes/test-infra/issues/22888#issuecomment-881513368).
// Use a boolean instead so that we can use `omitempty`.
// `report: false` has highest priority when it comes to decide whether
// this job should report or not. `false` strictly means not, which could be
// resulted from either of the following config:
// - `job_state_to_report: []`
// - `report: false`
// `report: true` also depends on other conditions, such as channel name etc.
func setReportDefault(spec *prowapi.ProwJobSpec) {
	if spec.ReporterConfig == nil || spec.ReporterConfig.Slack == nil {
		return
	}
	// `job_states_to_report: []` means false
	if spec.ReporterConfig.Slack.JobStatesToReport != nil && len(spec.ReporterConfig.Slack.JobStatesToReport) == 0 {
		spec.ReporterConfig.Slack.Report = boolPtr(false)
	} else {
		spec.ReporterConfig.Slack.Report = boolPtr(true)
	}
}

func createRefs(pr github.PullRequest, baseSHA string) prowapi.Refs {
	org := pr.Base.Repo.Owner.Login
	repo := pr.Base.Repo.Name
	repoLink := pr.Base.Repo.HTMLURL
	number := pr.Number
	return prowapi.Refs{
		Org:      org,
		Repo:     repo,
		RepoLink: repoLink,
		BaseRef:  pr.Base.Ref,
		BaseSHA:  baseSHA,
		BaseLink: fmt.Sprintf("%s/commit/%s", repoLink, baseSHA),
		Pulls: []prowapi.Pull{
			{
				Number:     number,
				Author:     pr.User.Login,
				SHA:        pr.Head.SHA,
				HeadRef:    pr.Head.Ref,
				Title:      pr.Title,
				Link:       pr.HTMLURL,
				AuthorLink: pr.User.HTMLURL,
				CommitLink: fmt.Sprintf("%s/pull/%d/commits/%s", repoLink, number, pr.Head.SHA),
			},
		},
	}
}

// NewPresubmit converts a config.Presubmit into a prowapi.ProwJob.
// The prowapi.Refs are configured correctly per the pr, baseSHA.
// The eventGUID becomes a github.EventGUID label.
func NewPresubmit(pr github.PullRequest, baseSHA string, job config.Presubmit, eventGUID string, additionalLabels map[string]string) prowapi.ProwJob {
	refs := createRefs(pr, baseSHA)
	labels := make(map[string]string)
	for k, v := range job.Labels {
		labels[k] = v
	}
	for k, v := range additionalLabels {
		labels[k] = v
	}
	labels[github.EventGUID] = eventGUID
	labels[kube.IsOptionalLabel] = strconv.FormatBool(job.Optional)
	annotations := make(map[string]string)
	for k, v := range job.Annotations {
		annotations[k] = v
	}
	return NewProwJob(PresubmitSpec(job, refs), labels, annotations)
}

// PresubmitSpec initializes a ProwJobSpec for a given presubmit job.
func PresubmitSpec(p config.Presubmit, refs prowapi.Refs) prowapi.ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	pjs.Type = prowapi.PresubmitJob
	pjs.Context = p.Context
	pjs.Report = !p.SkipReport
	pjs.RerunCommand = p.RerunCommand
	if p.JenkinsSpec != nil {
		pjs.JenkinsSpec = &prowapi.JenkinsSpec{
			GitHubBranchSourceJob: p.JenkinsSpec.GitHubBranchSourceJob,
		}
	}
	pjs.Refs = CompletePrimaryRefs(refs, p.JobBase)

	return pjs
}

// PostsubmitSpec initializes a ProwJobSpec for a given postsubmit job.
func PostsubmitSpec(p config.Postsubmit, refs prowapi.Refs) prowapi.ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	pjs.Type = prowapi.PostsubmitJob
	pjs.Context = p.Context
	pjs.Report = !p.SkipReport
	pjs.Refs = CompletePrimaryRefs(refs, p.JobBase)
	if p.JenkinsSpec != nil {
		pjs.JenkinsSpec = &prowapi.JenkinsSpec{
			GitHubBranchSourceJob: p.JenkinsSpec.GitHubBranchSourceJob,
		}
	}

	return pjs
}

// PeriodicSpec initializes a ProwJobSpec for a given periodic job.
func PeriodicSpec(p config.Periodic) prowapi.ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	// It is currently not possible to disable reporting for individual periodics.
	pjs.Report = true
	pjs.Type = prowapi.PeriodicJob

	return pjs
}

// BatchSpec initializes a ProwJobSpec for a given batch job and ref spec.
func BatchSpec(p config.Presubmit, refs prowapi.Refs) prowapi.ProwJobSpec {
	pjs := specFromJobBase(p.JobBase)
	pjs.Type = prowapi.BatchJob
	pjs.Context = p.Context
	pjs.Refs = CompletePrimaryRefs(refs, p.JobBase)

	return pjs
}

func specFromJobBase(jb config.JobBase) prowapi.ProwJobSpec {
	var namespace string
	if jb.Namespace != nil {
		namespace = *jb.Namespace
	}
	return prowapi.ProwJobSpec{
		Job:             jb.Name,
		Agent:           prowapi.ProwJobAgent(jb.Agent),
		Cluster:         jb.Cluster,
		Namespace:       namespace,
		MaxConcurrency:  jb.MaxConcurrency,
		ErrorOnEviction: jb.ErrorOnEviction,

		ExtraRefs:        jb.ExtraRefs,
		DecorationConfig: jb.DecorationConfig,

		PodSpec:               jb.Spec,
		PipelineRunSpec:       jb.PipelineRunSpec,
		TektonPipelineRunSpec: jb.TektonPipelineRunSpec,

		ReporterConfig:  jb.ReporterConfig,
		RerunAuthConfig: jb.RerunAuthConfig,
		Hidden:          jb.Hidden,
		ProwJobDefault:  jb.ProwJobDefault,
		JobQueueName:    jb.JobQueueName,
	}
}

func CompletePrimaryRefs(refs prowapi.Refs, jb config.JobBase) *prowapi.Refs {
	if jb.PathAlias != "" {
		refs.PathAlias = jb.PathAlias
	}
	if jb.CloneURI != "" {
		refs.CloneURI = jb.CloneURI
	}
	if jb.SkipSubmodules {
		refs.SkipSubmodules = jb.SkipSubmodules
	}
	if jb.CloneDepth > 0 {
		refs.CloneDepth = jb.CloneDepth
	}
	if jb.SkipFetchHead {
		refs.SkipFetchHead = jb.SkipFetchHead
	}
	return &refs
}

// PartitionActive separates the provided prowjobs into pending and triggered
// and returns them inside channels so that they can be consumed in parallel
// by different goroutines. Complete prowjobs are filtered out. Controller
// loops need to handle pending jobs first so they can conform to maximum
// concurrency requirements that different jobs may have.
func PartitionActive(pjs []prowapi.ProwJob) (pending, triggered, aborted chan prowapi.ProwJob) {
	// Size channels correctly.
	pendingCount, triggeredCount, abortedCount := 0, 0, 0
	for _, pj := range pjs {
		switch pj.Status.State {
		case prowapi.PendingState:
			pendingCount++
		case prowapi.TriggeredState:
			triggeredCount++
		case prowapi.AbortedState:
			abortedCount++
		}
	}
	pending = make(chan prowapi.ProwJob, pendingCount)
	triggered = make(chan prowapi.ProwJob, triggeredCount)
	aborted = make(chan prowapi.ProwJob, abortedCount)

	// Partition the jobs into the two separate channels.
	for _, pj := range pjs {
		switch pj.Status.State {
		case prowapi.PendingState:
			pending <- pj
		case prowapi.TriggeredState:
			triggered <- pj
		case prowapi.AbortedState:
			if !pj.Complete() {
				aborted <- pj
			}
		}
	}
	close(pending)
	close(triggered)
	close(aborted)
	return pending, triggered, aborted
}

// GetLatestProwJobs filters through the provided prowjobs and returns
// a map of jobType jobs to their latest prowjobs.
func GetLatestProwJobs(pjs []prowapi.ProwJob, jobType prowapi.ProwJobType) map[string]prowapi.ProwJob {
	latestJobs := make(map[string]prowapi.ProwJob)
	for _, j := range pjs {
		if j.Spec.Type != jobType {
			continue
		}
		name := j.Spec.Job
		if j.Status.StartTime.After(latestJobs[name].Status.StartTime.Time) {
			latestJobs[name] = j
		}
	}
	return latestJobs
}

// ProwJobFields extracts logrus fields from a prowjob useful for logging.
func ProwJobFields(pj *prowapi.ProwJob) logrus.Fields {
	fields := make(logrus.Fields)
	fields["name"] = pj.ObjectMeta.Name
	fields["job"] = pj.Spec.Job
	fields["type"] = pj.Spec.Type
	fields["state"] = pj.Status.State
	if len(pj.ObjectMeta.Labels[github.EventGUID]) > 0 {
		fields[github.EventGUID] = pj.ObjectMeta.Labels[github.EventGUID]
	}
	if pj.Spec.Refs != nil && len(pj.Spec.Refs.Pulls) == 1 {
		fields[github.PrLogField] = pj.Spec.Refs.Pulls[0].Number
		fields[github.RepoLogField] = pj.Spec.Refs.Repo
		fields[github.OrgLogField] = pj.Spec.Refs.Org
	}
	if pj.Spec.JenkinsSpec != nil {
		fields["github_based_job"] = pj.Spec.JenkinsSpec.GitHubBranchSourceJob
	}

	return fields
}

// JobURL returns the expected URL for ProwJobStatus.
//
// TODO(fejta): consider moving default JobURLTemplate and JobURLPrefix out of plank
func JobURL(plank config.Plank, pj prowapi.ProwJob, log *logrus.Entry) (string, error) {
	if pj.Spec.DecorationConfig != nil && plank.GetJobURLPrefix(&pj) != "" {
		spec := downwardapi.NewJobSpec(pj.Spec, pj.Status.BuildID, pj.Name)
		gcsConfig := pj.Spec.DecorationConfig.GCSConfiguration
		_, gcsPath, _ := gcsupload.PathsForJob(gcsConfig, &spec, "")

		prefix, _ := url.Parse(plank.GetJobURLPrefix(&pj))

		prowPath, err := prowapi.ParsePath(gcsConfig.Bucket)
		if err != nil {
			return "", fmt.Errorf("calculating joburl: %w", err)
		}

		// Final path will be, e.g.:
		// prefix.Scheme + prefix.Host + prefix.Path + storageProvider + bucketName         + gcsPath
		// https://prow.k8s.io/view/                 + gs/             + kubernetes-jenkins + pr-logs/pull/kubernetes-sigs_cluster-api-provider-openstack/541/pull-cluster-api-provider-openstack-test/1247344427123347459
		if plank.JobURLPrefixDisableAppendStorageProvider {
			prefix.Path = path.Join(prefix.Path, prowPath.FullPath(), gcsPath)
		} else {
			prefix.Path = path.Join(prefix.Path, prowPath.StorageProvider(), prowPath.FullPath(), gcsPath)
		}
		return prefix.String(), nil
	}
	var b bytes.Buffer
	if err := plank.JobURLTemplate.Execute(&b, &pj); err != nil {
		log.WithFields(ProwJobFields(&pj)).Errorf("error executing URL template: %v", err)
	} else {
		return b.String(), nil
	}
	return "", nil
}

// ClusterToCtx converts the prow job's cluster to a cluster context
func ClusterToCtx(cluster string) string {
	if cluster == kube.InClusterContext {
		return kube.DefaultClusterAlias
	}
	return cluster
}

func boolPtr(b bool) *bool {
	return &b
}
