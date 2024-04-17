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

package kube

const (
	// CreatedByProw is added on resources created by prow.
	// Since resources often live in another cluster/namespace,
	// the k8s garbage collector would immediately delete these
	// resources
	// TODO: Namespace this label.
	CreatedByProw = "created-by-prow"
	// CreatedByTideLabel is added by tide when it triggered a job.
	// TODO: Namespace this label.
	CreatedByTideLabel = "created-by-tide"
	// ProwJobTypeLabel is added in resources created by prow and
	// carries the job type (presubmit, postsubmit, periodic, batch)
	// that the pod is running.
	ProwJobTypeLabel = "prow.k8s.io/type"
	// ProwJobIDLabel is added in resources created by prow and
	// carries the ID of the ProwJob that the pod is fulfilling.
	// We also name resources after the ProwJob that spawned them but
	// this allows for multiple resources to be linked to one
	// ProwJob.
	ProwJobIDLabel = "prow.k8s.io/id"
	// ProwBuildIDLabel is added in resources created by prow and
	// carries the BuildID from a Prow Job's Status.
	ProwBuildIDLabel = "prow.k8s.io/build-id"
	// ProwJobAnnotation is added in resources created by prow and
	// carries the name of the job that the pod is running. Since
	// job names can be arbitrarily long, this is added as
	// an annotation instead of a label.
	ProwJobAnnotation = "prow.k8s.io/job"
	// ContextAnnotation is added in resources created by prow and
	// carries the context of the job that the pod is running. Since
	// job names can be arbitrarily long, this is added as
	// an annotation instead of a label.
	ContextAnnotation = "prow.k8s.io/context"
	// PlankVersionLabel is added in resources created by prow and
	// carries the version of prow that decorated this job.
	PlankVersionLabel = "prow.k8s.io/plank-version"
	// OrgLabel is added in resources created by prow and
	// carries the org associated with the job, eg kubernetes-sigs.
	OrgLabel = "prow.k8s.io/refs.org"
	// RepoLabel is added in resources created by prow and
	// carries the repo associated with the job, eg test-infra
	RepoLabel = "prow.k8s.io/refs.repo"
	// BaseRefLabel is added in resources created by prow and
	// carries the base ref associated with the job, eg main
	BaseRefLabel = "prow.k8s.io/refs.base_ref"
	// PullLabel is added in resources created by prow and
	// carries the PR number associated with the job, eg 321.
	PullLabel = "prow.k8s.io/refs.pull"
	// RetestLabel exposes if the job was created by a re-test request.
	RetestLabel = "prow.k8s.io/retest"
	// IsOptionalLabel is added in resources created by prow and
	// carries the Optional from a Presubmit job.
	IsOptionalLabel = "prow.k8s.io/is-optional"

	// Gerrit related labels that are used by Prow

	// GerritID identifies a gerrit change
	GerritID = "prow.k8s.io/gerrit-id"
	// GerritInstance is the gerrit host url
	GerritInstance = "prow.k8s.io/gerrit-instance"
	// GerritRevision is the SHA of current patchset from a gerrit change
	GerritRevision = "prow.k8s.io/gerrit-revision"
	// GerritPatchset is the numeric ID of the current patchset
	GerritPatchset = "prow.k8s.io/gerrit-patchset"
	// GerritReportLabel is the gerrit label prow will cast vote on, fallback to CodeReview label if unset
	GerritReportLabel = "prow.k8s.io/gerrit-report-label"
)
