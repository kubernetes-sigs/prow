/*
Copyright The Kubernetes Authors.

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

package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/crier/reporters/gcs/util"
	prowio "sigs.k8s.io/prow/pkg/io"
	"sigs.k8s.io/prow/pkg/io/providers"
)

const (
	schedulingInterval   = time.Minute
	maxSchedulingSamples = 32
	maxWorkers           = 200
	maxOccupants         = 2000
	maxSchedulingEvents  = 1000
)

type schedulingResources interface {
	Workers(context.Context, string) (*corev1.NodeList, error)
	Occupants(context.Context, string) (*corev1.PodList, error)
	SchedulingEvents(context.Context, string, *corev1.Pod) (*corev1.EventList, error)
}

type schedulingCollector struct {
	resources schedulingResources
	epoch     string
	mu        sync.Mutex
	attempts  map[string]time.Time
}

type workerOccupancy struct {
	Ready       bool `json:"ready"`
	Schedulable bool `json:"schedulable"`
	Measured    int  `json:"measured"`
	Unmeasured  int  `json:"unmeasured"`
	Unlabelled  int  `json:"unlabelled"`
}

type schedulingSample struct {
	At           time.Time         `json:"at"`
	Workers      []workerOccupancy `json:"workers,omitempty"`
	NodesStatus  string            `json:"nodes_status"`
	PodsStatus   string            `json:"pods_status"`
	EventsStatus string            `json:"events_status"`
	Decisions    map[string]int    `json:"decisions,omitempty"`
}

type schedulingManifest struct {
	Version  int                `json:"version"`
	Observer string             `json:"observer"`
	Start    time.Time          `json:"start"`
	End      time.Time          `json:"end"`
	Final    bool               `json:"final"`
	Gaps     []string           `json:"gaps"`
	Logs     string             `json:"controller_logs"`
	Samples  []schedulingSample `json:"samples"`
}

// EnableSchedulingEvidence enables bounded, public scheduling projections. It is disabled by default.
func (gr *gcsK8sReporter) EnableSchedulingEvidence() error {
	resources, ok := gr.rg.(schedulingResources)
	if !ok {
		return errors.New("scheduling resources unavailable")
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return errors.New("cannot initialize scheduling observer")
	}
	gr.evidence = &schedulingCollector{resources: resources, epoch: hex.EncodeToString(token[:]), attempts: make(map[string]time.Time)}
	return nil
}

func (rg k8sResourceGetter) Workers(ctx context.Context, cluster string) (*corev1.NodeList, error) {
	client, ok := rg.podClientSets[cluster]
	if !ok {
		return nil, errors.New("cluster unavailable")
	}
	return client.Nodes().List(ctx, metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker", Limit: maxWorkers})
}

func (rg k8sResourceGetter) Occupants(ctx context.Context, cluster string) (*corev1.PodList, error) {
	client, ok := rg.podClientSets[cluster]
	if !ok {
		return nil, errors.New("cluster unavailable")
	}
	return client.Pods("").List(ctx, metav1.ListOptions{Limit: maxOccupants})
}

func sourceStatus(err error) string {
	if apierrors.IsForbidden(err) {
		return "forbidden"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	if err != nil {
		return "error"
	}
	return "available"
}

func (rg k8sResourceGetter) SchedulingEvents(ctx context.Context, cluster string, parent *corev1.Pod) (*corev1.EventList, error) {
	client, ok := rg.podClientSets[cluster]
	if !ok {
		return nil, errors.New("cluster unavailable")
	}
	selector := fields.OneTermEqualSelector("involvedObject.uid", string(parent.UID))
	return client.Events(parent.Namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.String(), Limit: maxSchedulingEvents})
}

func addGap(manifest *schedulingManifest, gap string) {
	for _, existing := range manifest.Gaps {
		if existing == gap {
			return
		}
	}
	manifest.Gaps = append(manifest.Gaps, gap)
}

func validSchedulingHistory(manifest *schedulingManifest, start time.Time) bool {
	if manifest.Version != 1 || !manifest.Start.Equal(start) || len(manifest.Samples) > maxSchedulingSamples {
		return false
	}
	for _, gap := range manifest.Gaps {
		switch gap {
		case "history_before_first_observation_unavailable", "observer_restart", "observation_gap", "sample_limit_reached", "sampling_stopped_after_assignment", "parent_forbidden", "parent_cancelled", "parent_error":
		default:
			return false
		}
	}
	statuses := []string{"available", "forbidden", "cancelled", "error", "unavailable", "truncated", "outside_run_window_or_assigned"}
	for _, sample := range manifest.Samples {
		if len(sample.Workers) > maxWorkers || !slices.Contains(statuses, sample.NodesStatus) || !slices.Contains(statuses, sample.PodsStatus) || !slices.Contains(statuses, sample.EventsStatus) {
			return false
		}
		for decision, count := range sample.Decisions {
			switch decision {
			case "scheduler_rejected", "scheduler_assigned", "scale_up_triggered", "scale_up_not_triggered", "scale_up_failed":
			default:
				return false
			}
			if count < 0 || count > maxSchedulingEvents {
				return false
			}
		}
		for _, worker := range sample.Workers {
			if worker.Measured < 0 || worker.Measured > maxOccupants || worker.Unmeasured < 0 || worker.Unmeasured > maxOccupants || worker.Unlabelled < 0 || worker.Unlabelled > maxOccupants {
				return false
			}
		}
	}
	return true
}

func (collector *schedulingCollector) allow(key string, now time.Time) bool {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for name, at := range collector.attempts {
		if now.Sub(at) > time.Hour {
			delete(collector.attempts, name)
		}
	}
	if previous, ok := collector.attempts[key]; ok && now.Sub(previous) < schedulingInterval {
		return false
	}
	if _, exists := collector.attempts[key]; !exists && len(collector.attempts) >= 1024 {
		return false
	}
	collector.attempts[key] = now
	return true
}

func (gr *gcsK8sReporter) collectScheduling(ctx context.Context, log *logrus.Entry, pj *prowv1.ProwJob) bool {
	if gr.dryRun {
		return false
	}
	now := time.Now().UTC()
	if !pj.Complete() && !gr.evidence.allow(pj.Name, now) {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pending, err := gr.schedulingEvidence(ctx, pj, now)
	if err != nil {
		log.Warn("Optional scheduling evidence unavailable; coverage may be incomplete")
	}
	return pending
}

func (gr *gcsK8sReporter) schedulingEvidence(ctx context.Context, pj *prowv1.ProwJob, now time.Time) (bool, error) {
	bucket, directory, err := util.GetJobDestination(gr.cfg, pj)
	if err != nil {
		return true, err
	}
	destination, err := providers.StoragePath(bucket, path.Join(directory, "scheduling-evidence.json"))
	if err != nil {
		return true, err
	}
	manifest := schedulingManifest{Version: 1, Observer: gr.evidence.epoch, Start: pj.CreationTimestamp.Time, End: now, Logs: "unavailable_not_collected"}
	reader, err := gr.opener.Reader(ctx, destination)
	if err == nil {
		decodeErr := json.NewDecoder(io.LimitReader(reader, 2<<20)).Decode(&manifest)
		closeErr := reader.Close()
		if decodeErr != nil {
			return true, decodeErr
		}
		if closeErr != nil {
			return true, closeErr
		}
		if !validSchedulingHistory(&manifest, pj.CreationTimestamp.Time) {
			return true, errors.New("invalid scheduling evidence history")
		}
		if manifest.Observer != gr.evidence.epoch {
			addGap(&manifest, "observer_restart")
		}
	} else if prowio.IsNotExist(err) {
		addGap(&manifest, "history_before_first_observation_unavailable")
	} else {
		return true, err
	}
	manifest.Observer = gr.evidence.epoch
	manifest.Final = pj.Complete()
	manifest.Logs = "unavailable_not_collected"
	manifest.End = now
	if pj.Status.CompletionTime != nil {
		manifest.End = pj.Status.CompletionTime.Time
	}
	if len(manifest.Samples) > 0 && now.Sub(manifest.Samples[len(manifest.Samples)-1].At) > 2*schedulingInterval {
		addGap(&manifest, "observation_gap")
	}
	pending := true
	if len(manifest.Samples) >= maxSchedulingSamples && !pj.Complete() {
		addGap(&manifest, "sample_limit_reached")
		pending = false
	} else {
		pod, podErr := gr.rg.GetPod(ctx, pj.Spec.Cluster, gr.cfg().PodNamespace, pj.Name)
		if podErr != nil {
			addGap(&manifest, "parent_"+sourceStatus(podErr))
		} else {
			if pod.Spec.NodeName != "" {
				pending = false
				if len(manifest.Samples) == 0 {
					return false, nil
				}
				addGap(&manifest, "sampling_stopped_after_assignment")
			}
			if len(manifest.Samples) >= maxSchedulingSamples {
				manifest.Samples = manifest.Samples[:maxSchedulingSamples-1]
				addGap(&manifest, "sample_limit_reached")
			}
			manifest.Samples = append(manifest.Samples, gr.observeScheduling(ctx, pj, pod, now, manifest.End))
		}
	}
	output, err := json.Marshal(manifest)
	if err != nil {
		return pending, err
	}
	quiet := logrus.New()
	quiet.SetOutput(io.Discard)
	return pending, prowio.WriteContent(ctx, logrus.NewEntry(quiet), gr.opener, destination, output, prowio.WriterOptions{PreconditionDoesNotExist: new(false)})
}

func (gr *gcsK8sReporter) observeScheduling(ctx context.Context, pj *prowv1.ProwJob, parent *corev1.Pod, now, end time.Time) schedulingSample {
	sample := schedulingSample{At: now}
	var nodes *corev1.NodeList
	var pods *corev1.PodList
	var err error
	if parent.Spec.NodeName == "" && !now.After(end) {
		nodes, err = gr.evidence.resources.Workers(ctx, pj.Spec.Cluster)
		sample.NodesStatus = sourceStatus(err)
		if nodes == nil && err == nil {
			sample.NodesStatus = "unavailable"
		}
		pods, err = gr.evidence.resources.Occupants(ctx, pj.Spec.Cluster)
		sample.PodsStatus = sourceStatus(err)
		if pods == nil && err == nil {
			sample.PodsStatus = "unavailable"
		}
	} else {
		sample.NodesStatus = "outside_run_window_or_assigned"
		sample.PodsStatus = "outside_run_window_or_assigned"
	}
	if nodes != nil && pods != nil && sample.NodesStatus == "available" && sample.PodsStatus == "available" {
		if nodes.Continue != "" || len(nodes.Items) > maxWorkers {
			sample.NodesStatus = "truncated"
		}
		if pods.Continue != "" || len(pods.Items) > maxOccupants {
			sample.PodsStatus = "truncated"
		}
		occupancy := make(map[string]*workerOccupancy)
		for index, node := range nodes.Items {
			if index >= maxWorkers {
				break
			}
			worker := &workerOccupancy{Schedulable: !node.Spec.Unschedulable}
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady {
					worker.Ready = condition.Status == corev1.ConditionTrue
				}
			}
			occupancy[node.Name] = worker
		}
		for index, pod := range pods.Items {
			if index >= maxOccupants {
				break
			}
			worker := occupancy[pod.Spec.NodeName]
			if worker == nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			switch pod.Labels["pod-scaler.openshift.io/measured"] {
			case "true":
				worker.Measured++
			case "false":
				worker.Unmeasured++
			default:
				worker.Unlabelled++
			}
		}
		for _, worker := range occupancy {
			sample.Workers = append(sample.Workers, *worker)
		}
		sort.Slice(sample.Workers, func(left, right int) bool {
			first, second := sample.Workers[left], sample.Workers[right]
			if first.Ready != second.Ready {
				return !first.Ready
			}
			if first.Schedulable != second.Schedulable {
				return !first.Schedulable
			}
			if first.Measured != second.Measured {
				return first.Measured < second.Measured
			}
			if first.Unmeasured != second.Unmeasured {
				return first.Unmeasured < second.Unmeasured
			}
			return first.Unlabelled < second.Unlabelled
		})
	}
	events, err := gr.evidence.resources.SchedulingEvents(ctx, pj.Spec.Cluster, parent)
	sample.EventsStatus = sourceStatus(err)
	if events == nil && err == nil {
		sample.EventsStatus = "unavailable"
	}
	if err == nil && events != nil {
		sample.Decisions = projectDecisions(events.Items, parent, end)
		if events.Continue != "" || len(events.Items) > maxSchedulingEvents {
			sample.EventsStatus = "truncated"
		}
	}
	return sample
}

func projectDecisions(events []corev1.Event, parent *corev1.Pod, end time.Time) map[string]int {
	decisions := make(map[string]int)
	for index, event := range events {
		if index >= maxSchedulingEvents {
			break
		}
		if parent.UID == "" || event.InvolvedObject.UID != parent.UID || event.InvolvedObject.Kind != "Pod" {
			continue
		}
		at := event.LastTimestamp.Time
		if event.Series != nil {
			at = event.Series.LastObservedTime.Time
		}
		if at.IsZero() {
			at = event.EventTime.Time
		}
		if at.Before(parent.CreationTimestamp.Time) || at.After(end) {
			continue
		}
		scheduler := event.Source.Component == "default-scheduler" || event.ReportingController == "default-scheduler"
		autoscaler := event.Source.Component == "cluster-autoscaler" || event.ReportingController == "cluster-autoscaler"
		switch {
		case scheduler && event.Reason == "FailedScheduling":
			decisions["scheduler_rejected"]++
		case scheduler && event.Reason == "Scheduled":
			decisions["scheduler_assigned"]++
		case autoscaler && event.Reason == "TriggeredScaleUp":
			decisions["scale_up_triggered"]++
		case autoscaler && event.Reason == "NotTriggerScaleUp":
			decisions["scale_up_not_triggered"]++
		case autoscaler && event.Reason == "FailedToScaleUp":
			decisions["scale_up_failed"]++
		}
	}
	return decisions
}
