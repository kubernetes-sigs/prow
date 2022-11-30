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

package subscriber

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cloud.google.com/go/pubsub"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pjutil"
)

const (
	ProwEventType          = "prow.k8s.io/pubsub.EventType"
	PeriodicProwJobEvent   = "prow.k8s.io/pubsub.PeriodicProwJobEvent"
	PresubmitProwJobEvent  = "prow.k8s.io/pubsub.PresubmitProwJobEvent"
	PostsubmitProwJobEvent = "prow.k8s.io/pubsub.PostsubmitProwJobEvent"
)

// Ensure interface is intact. I.e., this declaration ensures that the type
// "*config.Config" implements the "prowCfgClient" interface. See
// https://golang.org/doc/faq#guarantee_satisfies_interface.
var _ prowCfgClient = (*config.Config)(nil)

// prowCfgClient is a subset of all the various behaviors that the
// "*config.Config" type implements, which we will test here.
type prowCfgClient interface {
	AllPeriodics() []config.Periodic
	GetPresubmitsStatic(identifier string) []config.Presubmit
	GetPostsubmitsStatic(identifier string) []config.Postsubmit
}

// ProwJobEvent contains the minimum information required to start a ProwJob.
type ProwJobEvent struct {
	Name string `json:"name"`
	// Refs are used by presubmit and postsubmit jobs supplying baseSHA and SHA
	Refs        *v1.Refs          `json:"refs,omitempty"`
	Envs        map[string]string `json:"envs,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// FromPayload set the ProwJobEvent from the PubSub message payload.
func (pe *ProwJobEvent) FromPayload(data []byte) error {
	if err := json.Unmarshal(data, pe); err != nil {
		return err
	}
	return nil
}

// ToMessage generates a PubSub Message from a ProwJobEvent.
func (pe *ProwJobEvent) ToMessage() (*pubsub.Message, error) {
	return pe.ToMessageOfType(PeriodicProwJobEvent)
}

// ToMessage generates a PubSub Message from a ProwJobEvent.
func (pe *ProwJobEvent) ToMessageOfType(t string) (*pubsub.Message, error) {
	data, err := json.Marshal(pe)
	if err != nil {
		return nil, err
	}
	message := pubsub.Message{
		Data: data,
		Attributes: map[string]string{
			ProwEventType: t,
		},
	}
	return &message, nil
}

// ProwJobClient mostly for testing.
type ProwJobClient interface {
	Create(context.Context, *prowapi.ProwJob, metav1.CreateOptions) (*prowapi.ProwJob, error)
}

// Subscriber handles Pub/Sub subscriptions, update metrics,
// validates them using Prow Configuration and
// use a ProwJobClient to create Prow Jobs.
type Subscriber struct {
	ConfigAgent              *config.Agent
	Metrics                  *Metrics
	ProwJobClient            ProwJobClient
	Reporter                 reportClient
	InRepoConfigCacheHandler *config.InRepoConfigCacheHandler
}

type messageInterface interface {
	getAttributes() map[string]string
	getPayload() []byte
	getID() string
	ack()
	nack()
}

type reportClient interface {
	Report(ctx context.Context, log *logrus.Entry, pj *prowapi.ProwJob) ([]*prowapi.ProwJob, *reconcile.Result, error)
	ShouldReport(ctx context.Context, log *logrus.Entry, pj *prowapi.ProwJob) bool
}

type pubSubMessage struct {
	pubsub.Message
}

func (m *pubSubMessage) getAttributes() map[string]string {
	return m.Attributes
}

func (m *pubSubMessage) getPayload() []byte {
	return m.Data
}

func (m *pubSubMessage) getID() string {
	return m.ID
}

func (m *pubSubMessage) ack() {
	m.Message.Ack()
}
func (m *pubSubMessage) nack() {
	m.Message.Nack()
}

// jobHandler handles job type specific logic
type jobHandler interface {
	getProwJobSpec(cfg prowCfgClient, pc *config.InRepoConfigCacheHandler, pe ProwJobEvent) (jobSpec *v1.ProwJobSpec, labels map[string]string, annotations map[string]string, err error)
}

// periodicJobHandler implements jobHandler
type periodicJobHandler struct{}

func (peh *periodicJobHandler) getProwJobSpec(cfg prowCfgClient, pc *config.InRepoConfigCacheHandler, pe ProwJobEvent) (jobSpec *v1.ProwJobSpec, labels map[string]string, annotations map[string]string, err error) {
	var periodicJob *config.Periodic
	// TODO(chaodaiG): do we want to support inrepoconfig when
	// https://github.com/kubernetes/test-infra/issues/21729 is done?
	for _, job := range cfg.AllPeriodics() {
		if job.Name == pe.Name {
			// Directly followed by break, so this is ok
			// nolint: exportloopref
			periodicJob = &job
			break
		}
	}
	if periodicJob == nil {
		err = fmt.Errorf("failed to find associated periodic job %q", pe.Name)
		return
	}

	spec := pjutil.PeriodicSpec(*periodicJob)
	jobSpec = &spec
	labels, annotations = periodicJob.Labels, periodicJob.Annotations
	return
}

// presubmitJobHandler implements jobHandler
type presubmitJobHandler struct {
}

func (prh *presubmitJobHandler) getProwJobSpec(cfg prowCfgClient, pc *config.InRepoConfigCacheHandler, pe ProwJobEvent) (jobSpec *v1.ProwJobSpec, labels map[string]string, annotations map[string]string, err error) {
	// presubmit jobs require Refs and Refs.Pulls to be set
	refs := pe.Refs
	if refs == nil {
		err = errors.New("Refs must be supplied")
		return
	}
	if len(refs.Org) == 0 {
		err = errors.New("org must be supplied")
		return
	}
	if len(refs.Repo) == 0 {
		err = errors.New("repo must be supplied")
		return
	}
	if len(refs.Pulls) == 0 {
		err = errors.New("at least 1 Pulls is required")
		return
	}
	if len(refs.BaseSHA) == 0 {
		err = errors.New("baseSHA must be supplied")
		return
	}
	if len(refs.BaseRef) == 0 {
		err = errors.New("baseRef must be supplied")
		return
	}

	var presubmitJob *config.Presubmit
	org, repo, branch := refs.Org, refs.Repo, refs.BaseRef
	orgRepo := org + "/" + repo
	// Add "https://" prefix to orgRepo if this is a gerrit job.
	// (Unfortunately gerrit jobs use the full repo URL as the identifier.)
	prefix := "https://"
	if pe.Labels[kube.GerritRevision] != "" && !strings.HasPrefix(orgRepo, prefix) {
		orgRepo = prefix + orgRepo
	}
	baseSHAGetter := func() (string, error) {
		return refs.BaseSHA, nil
	}
	var headSHAGetters []func() (string, error)
	for _, pull := range refs.Pulls {
		pull := pull
		headSHAGetters = append(headSHAGetters, func() (string, error) {
			return pull.SHA, nil
		})
	}

	logger := logrus.WithFields(logrus.Fields{"org": org, "repo": repo, "branch": branch, "orgRepo": orgRepo})
	// Get presubmits from Config alone.
	presubmits := cfg.GetPresubmitsStatic(orgRepo)
	// If InRepoConfigCache is provided, then it means that we also want to fetch
	// from an inrepoconfig.
	if pc != nil {
		logger.Debug("Getting prow jobs.")
		var presubmitsWithInrepoconfig []config.Presubmit
		var err error
		presubmitsWithInrepoconfig, err = pc.GetPresubmits(orgRepo, baseSHAGetter, headSHAGetters...)
		if err != nil {
			logger.WithError(err).Info("Failed to get presubmits")
		} else {
			logger.WithField("static-jobs", len(presubmits)).WithField("jobs-with-inrepoconfig", len(presubmitsWithInrepoconfig)).Debug("Jobs found.")
			// Overwrite presubmits. This is safe because pc.GetPresubmits()
			// itself calls cfg.GetPresubmitsStatic() and adds to it all the
			// presubmits found in the inrepoconfig.
			presubmits = presubmitsWithInrepoconfig
		}
	}

	for _, job := range presubmits {
		job := job
		if !job.CouldRun(branch) { // filter out jobs that are not branch matching
			continue
		}
		if job.Name == pe.Name {
			if presubmitJob != nil {
				err = fmt.Errorf("%s matches multiple prow jobs from orgRepo %q", pe.Name, orgRepo)
				return
			}
			presubmitJob = &job
		}
	}
	// This also captures the case where fetching jobs from inrepoconfig failed.
	// However doesn't not distinguish between this case and a wrong prow job name.
	if presubmitJob == nil {
		err = fmt.Errorf("failed to find associated presubmit job %q from orgRepo %q", pe.Name, orgRepo)
		return
	}

	spec := pjutil.PresubmitSpec(*presubmitJob, *refs)
	jobSpec, labels, annotations = &spec, presubmitJob.Labels, presubmitJob.Annotations
	return
}

// postsubmitJobHandler implements jobHandler
type postsubmitJobHandler struct {
}

func (poh *postsubmitJobHandler) getProwJobSpec(cfg prowCfgClient, pc *config.InRepoConfigCacheHandler, pe ProwJobEvent) (jobSpec *v1.ProwJobSpec, labels map[string]string, annotations map[string]string, err error) {
	// postsubmit jobs require Refs to be set
	refs := pe.Refs
	if refs == nil {
		err = errors.New("refs must be supplied")
		return
	}
	if len(refs.Org) == 0 {
		err = errors.New("org must be supplied")
		return
	}
	if len(refs.Repo) == 0 {
		err = errors.New("repo must be supplied")
		return
	}
	if len(refs.BaseSHA) == 0 {
		err = errors.New("baseSHA must be supplied")
		return
	}
	if len(refs.BaseRef) == 0 {
		err = errors.New("baseRef must be supplied")
		return
	}

	var postsubmitJob *config.Postsubmit
	org, repo, branch := refs.Org, refs.Repo, refs.BaseRef
	orgRepo := org + "/" + repo
	// Add "https://" prefix to orgRepo if this is a gerrit job.
	// (Unfortunately gerrit jobs use the full repo URL as the identifier.)
	prefix := "https://"
	if pe.Labels[kube.GerritRevision] != "" && !strings.HasPrefix(orgRepo, prefix) {
		orgRepo = prefix + orgRepo
	}
	baseSHAGetter := func() (string, error) {
		return refs.BaseSHA, nil
	}

	logger := logrus.WithFields(logrus.Fields{"org": org, "repo": repo, "branch": branch, "orgRepo": orgRepo})
	postsubmits := cfg.GetPostsubmitsStatic(orgRepo)
	if pc != nil {
		logger.Debug("Getting prow jobs.")
		var postsubmitsWithInrepoconfig []config.Postsubmit
		var err error
		postsubmitsWithInrepoconfig, err = pc.GetPostsubmits(orgRepo, baseSHAGetter)
		if err != nil {
			logger.WithError(err).Info("Failed to get postsubmits from inrepoconfig")
		} else {
			logger.WithField("static-jobs", len(postsubmits)).WithField("jobs-with-inrepoconfig", len(postsubmitsWithInrepoconfig)).Debug("Jobs found.")
			postsubmits = postsubmitsWithInrepoconfig
		}
	}

	for _, job := range postsubmits {
		job := job
		if !job.CouldRun(branch) { // filter out jobs that are not branch matching
			continue
		}
		if job.Name == pe.Name {
			if postsubmitJob != nil {
				return nil, nil, nil, fmt.Errorf("%s matches multiple prow jobs from orgRepo %q", pe.Name, orgRepo)
			}
			postsubmitJob = &job
		}
	}
	// This also captures the case where fetching jobs from inrepoconfig failed.
	// However doesn't not distinguish between this case and a wrong prow job name.
	if postsubmitJob == nil {
		err = fmt.Errorf("failed to find associated postsubmit job %q from orgRepo %q", pe.Name, orgRepo)
		return
	}

	spec := pjutil.PostsubmitSpec(*postsubmitJob, *refs)
	jobSpec, labels, annotations = &spec, postsubmitJob.Labels, postsubmitJob.Annotations
	return
}

func extractFromAttribute(attrs map[string]string, key string) (string, error) {
	value, ok := attrs[key]
	if !ok {
		return "", fmt.Errorf("unable to find %q from the attributes", key)
	}
	return value, nil
}

func (s *Subscriber) handleMessage(msg messageInterface, subscription string, allowedClusters []string) error {
	// Observed message payload drifts to the payload from another pubsub
	// message and couldn't figure out why. Extracts the values ahead of time
	// and hopefully could mitigate this issue for now.
	// TODO(chaodaiG): remove these once figured out the root cause of data race.
	msgID := msg.getID()
	msgPayload := msg.getPayload()
	msgAttributes := msg.getAttributes()

	l := logrus.WithFields(logrus.Fields{
		"pubsub-subscription": subscription,
		"pubsub-id":           msgID})
	s.Metrics.MessageCounter.With(prometheus.Labels{subscriptionLabel: subscription}).Inc()

	l.WithField("payload", string(msgPayload)).Debug("Received message")
	eType, err := extractFromAttribute(msgAttributes, ProwEventType)
	if err != nil {
		l.WithError(err).Error("failed to read message")
		s.Metrics.ErrorCounter.With(prometheus.Labels{
			subscriptionLabel: subscription,
			errorTypeLabel:    "malformed-message",
		}).Inc()
		return err
	}

	var jh jobHandler
	switch eType {
	case PeriodicProwJobEvent:
		jh = &periodicJobHandler{}
	case PresubmitProwJobEvent:
		jh = &presubmitJobHandler{}
	case PostsubmitProwJobEvent:
		jh = &postsubmitJobHandler{}
	default:
		l.WithField("type", eType).Info("Unsupported event type")
		s.Metrics.ErrorCounter.With(prometheus.Labels{
			subscriptionLabel: subscription,
			errorTypeLabel:    "unsupported-event-type",
		}).Inc()
		return fmt.Errorf("unsupported event type: %s", eType)
	}
	if err = s.handleProwJob(l, jh, msgPayload, subscription, eType, allowedClusters); err != nil {
		l.WithError(err).Info("failed to create Prow Job")
		s.Metrics.ErrorCounter.With(prometheus.Labels{
			subscriptionLabel: subscription,
			// This should be the only case prow operator should pay more
			// attention too, because errors here are more likely caused by
			// prow. (There are exceptions, which we can iterate slightly later)
			errorTypeLabel: "failed-handle-prowjob",
		}).Inc()
	}

	// TODO(chaodaiG): debugging purpose, remove once done debugging.
	l.WithField("payload", string(msg.getPayload())).WithField("post-id", msg.getID()).Debug("Finished handling message")
	return err
}

func tryGetCloneURIAndHost(pe ProwJobEvent) (cloneURI, host string) {
	refs := pe.Refs
	if refs == nil {
		return "", ""
	}
	if len(refs.Org) == 0 {
		return "", ""
	}
	if len(refs.Repo) == 0 {
		return "", ""
	}

	// If the Refs struct already has a populated CloneURI field, use that
	// instead.
	if refs.CloneURI != "" {
		if strings.HasPrefix(refs.Org, "http") {
			return refs.CloneURI, refs.Org
		}
		return refs.CloneURI, ""
	}

	org, repo := refs.Org, refs.Repo
	orgRepo := org + "/" + repo
	// Add "https://" prefix to orgRepo if this is a gerrit job.
	// (Unfortunately gerrit jobs use the full repo URL as the identifier.)
	prefix := "https://"
	if pe.Labels[kube.GerritRevision] != "" && !strings.HasPrefix(orgRepo, prefix) {
		orgRepo = prefix + orgRepo
	}
	return orgRepo, org
}

func (s *Subscriber) handleProwJob(l *logrus.Entry, jh jobHandler, msgPayload []byte, subscription, eType string, allowedClusters []string) error {

	var pe ProwJobEvent
	var prowJob prowapi.ProwJob

	if err := pe.FromPayload(msgPayload); err != nil {
		return err
	}

	reportProwJob := func(pj *prowapi.ProwJob, state v1.ProwJobState, err error) {
		pj.Status.State = state
		pj.Status.Description = "Successfully triggered prowjob."
		if err != nil {
			pj.Status.Description = fmt.Sprintf("Failed creating prowjob: %v", err)
		}
		if s.Reporter.ShouldReport(context.TODO(), l, pj) {
			if _, _, err := s.Reporter.Report(context.TODO(), l, pj); err != nil {
				l.WithError(err).Warning("Failed to report status.")
			}
		}
	}

	reportProwJobFailure := func(pj *prowapi.ProwJob, err error) {
		reportProwJob(pj, prowapi.ErrorState, err)
	}

	reportProwJobTriggered := func(pj *prowapi.ProwJob) {
		reportProwJob(pj, prowapi.TriggeredState, nil)
	}

	// Normalize job name
	pe.Name = strings.TrimSpace(pe.Name)

	prowJobSpec, labels, annotations, err := jh.getProwJobSpec(s.ConfigAgent.Config(), s.InRepoConfigCacheHandler, pe)
	if err != nil {
		// These are user errors, i.e. missing fields, requested prowjob doesn't exist etc.
		// These errors are already surfaced to user via pubsub two lines below.
		l.WithError(err).WithField("name", pe.Name).Info("Failed getting prowjob spec")
		prowJob = pjutil.NewProwJob(prowapi.ProwJobSpec{}, nil, pe.Annotations)
		reportProwJobFailure(&prowJob, err)
		return err
	}
	if prowJobSpec == nil {
		return fmt.Errorf("failed getting prowjob spec") // This should not happen
	}

	// deny job that runs on not allowed cluster
	var clusterIsAllowed bool
	for _, allowedCluster := range allowedClusters {
		if allowedCluster == "*" || allowedCluster == prowJobSpec.Cluster {
			clusterIsAllowed = true
			break
		}
	}
	// This is a user error, not sure whether we want to return error here.
	if !clusterIsAllowed {
		err := fmt.Errorf("cluster %s is not allowed. Can be fixed by defining this cluster under pubsub_triggers -> allowed_clusters", prowJobSpec.Cluster)
		l.WithField("cluster", prowJobSpec.Cluster).Warn("cluster not allowed")
		prowJob = pjutil.NewProwJob(*prowJobSpec, nil, pe.Annotations)
		reportProwJobFailure(&prowJob, err)
		return err
	}

	// Adds / Updates Labels from prow job event
	if labels == nil { // Could be nil if the job doesn't have label
		labels = make(map[string]string)
	}
	for k, v := range pe.Labels {
		labels[k] = v
	}
	if annotations == nil {
		annotations = make(map[string]string)
	}
	for k, v := range pe.Annotations {
		annotations[k] = v
	}

	// Adds annotations
	prowJob = pjutil.NewProwJob(*prowJobSpec, labels, annotations)
	// Adds / Updates Environments to containers
	if prowJob.Spec.PodSpec != nil {
		for i, c := range prowJob.Spec.PodSpec.Containers {
			for k, v := range pe.Envs {
				c.Env = append(c.Env, coreapi.EnvVar{Name: k, Value: v})
			}
			prowJob.Spec.PodSpec.Containers[i].Env = c.Env
		}
	}

	if _, err := s.ProwJobClient.Create(context.TODO(), &prowJob, metav1.CreateOptions{}); err != nil {
		l.WithError(err).Errorf("failed to create job %q as %q", pe.Name, prowJob.Name)
		reportProwJobFailure(&prowJob, err)
		return err
	}
	l.WithFields(logrus.Fields{
		"job":  pe.Name,
		"name": prowJob.Name,
	}).Info("Job created.")
	reportProwJobTriggered(&prowJob)
	return nil
}
