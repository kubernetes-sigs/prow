package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	prowio "sigs.k8s.io/prow/pkg/io"
	"sigs.k8s.io/prow/pkg/io/fakeopener"
)

type evidenceResources struct {
	testResourceGetter
	nodes     *corev1.NodeList
	pods      *corev1.PodList
	eventList *corev1.EventList
	sourceErr error
	nodesErr  error
	podsErr   error
	eventsErr error
}

func (resources *evidenceResources) Workers(ctx context.Context, _ string) (*corev1.NodeList, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return resources.nodes, errors.Join(resources.sourceErr, resources.nodesErr)
}
func (resources *evidenceResources) Occupants(ctx context.Context, _ string) (*corev1.PodList, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return resources.pods, errors.Join(resources.sourceErr, resources.podsErr)
}
func (resources *evidenceResources) SchedulingEvents(ctx context.Context, _ string, _ *corev1.Pod) (*corev1.EventList, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return resources.eventList, errors.Join(resources.sourceErr, resources.eventsErr)
}

func evidenceFixture() (*gcsK8sReporter, *evidenceResources, *fakeopener.FakeOpener, *prowv1.ProwJob, time.Time) {
	now := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "private-parent", Namespace: "private-namespace", UID: types.UID("private-uid"), CreationTimestamp: metav1.NewTime(now.Add(-time.Minute))}, Status: corev1.PodStatus{Phase: corev1.PodPending}}
	resources := &evidenceResources{testResourceGetter: testResourceGetter{pod: pod, namespace: pod.Namespace, cluster: "build", patchType: types.MergePatchType, patchData: `{"metadata":{"finalizers":["prow.x-k8s.io/gcsk8sreporter"]}}`}, nodes: &corev1.NodeList{}, pods: &corev1.PodList{}, eventList: &corev1.EventList{}}
	opener := &fakeopener.FakeOpener{}
	cfg := fca{c: config.Config{ProwConfig: config.ProwConfig{PodNamespace: pod.Namespace}}}
	reporter := New(cfg.Config, opener, resources, 1, false)
	reporter.evidence = &schedulingCollector{resources: resources, epoch: "observer-one", attempts: make(map[string]time.Time)}
	pj := &prowv1.ProwJob{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, CreationTimestamp: pod.CreationTimestamp}, Spec: prowv1.ProwJobSpec{Type: prowv1.PeriodicJob, Job: "scheduling-example", Agent: prowv1.KubernetesAgent, Cluster: "build", DecorationConfig: &prowv1.DecorationConfig{GCSConfiguration: &prowv1.GCSConfiguration{Bucket: "evidence-bucket"}}}, Status: prowv1.ProwJobStatus{State: prowv1.PendingState, BuildID: "1", PendingTime: &pod.CreationTimestamp}}
	return reporter, resources, opener, pj, now
}

func readEvidence(t *testing.T, opener *fakeopener.FakeOpener) schedulingManifest {
	t.Helper()
	for name, buffer := range opener.Buffer {
		if strings.HasSuffix(name, "/scheduling-evidence.json") {
			var manifest schedulingManifest
			if err := json.Unmarshal(buffer.Bytes(), &manifest); err != nil {
				t.Fatal(err)
			}
			return manifest
		}
	}
	t.Fatal("no evidence uploaded")
	return schedulingManifest{}
}

func TestSchedulingEvidence(t *testing.T) {
	for _, test := range []struct {
		name                                                            string
		restart, complete, assigned, capped, uploadFailure, readFailure bool
	}{
		{name: "periodic pending observations"},
		{name: "restart preserves history and records gap", restart: true},
		{name: "never started scheduling timeout finalizes evidence", complete: true},
		{name: "assignment stops sampling", assigned: true},
		{name: "bounded observations", capped: true},
		{name: "upload error", uploadFailure: true},
		{name: "read error", readFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter, resources, opener, pj, now := evidenceFixture()
			pending, err := reporter.schedulingEvidence(context.Background(), pj, now)
			if err != nil || !pending {
				t.Fatalf("initial pending=%v err=%v", pending, err)
			}
			if test.restart {
				reporter.evidence.epoch = "observer-two"
			}
			if test.complete {
				pj.Status.State = prowv1.ErrorState
				completed := metav1.NewTime(now.Add(time.Minute))
				pj.Status.CompletionTime = &completed
			}
			if test.assigned {
				resources.pod.Spec.NodeName = "private-worker"
			}
			if test.uploadFailure {
				opener.WriteError = errors.New("private-storage-error")
			}
			if test.readFailure {
				opener.ReadError = errors.New("private-read-error")
			}
			if test.capped {
				for index := 1; index < maxSchedulingSamples; index++ {
					if _, err := reporter.schedulingEvidence(context.Background(), pj, now.Add(time.Duration(index)*time.Minute)); err != nil {
						t.Fatal(err)
					}
				}
			}
			pending, err = reporter.schedulingEvidence(context.Background(), pj, now.Add(40*time.Minute))
			if test.uploadFailure || test.readFailure {
				if err == nil {
					t.Fatal("expected storage error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			manifest := readEvidence(t, opener)
			if manifest.Final != test.complete {
				t.Fatal("incorrect final status")
			}
			if test.assigned || test.capped {
				if pending {
					t.Fatal("expected sampling to stop")
				}
			}
			if test.restart && !strings.Contains(strings.Join(manifest.Gaps, ","), "observer_restart") {
				t.Fatal("missing restart gap")
			}
			if !strings.Contains(strings.Join(manifest.Gaps, ","), "observation_gap") {
				t.Fatal("missing observation gap")
			}
			if len(manifest.Samples) > maxSchedulingSamples {
				t.Fatal("unbounded samples")
			}
			if manifest.Logs != "unavailable_not_collected" {
				t.Fatal("logs coverage overstated")
			}
		})
	}
}

func TestObserveScheduling(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		truncated bool
		want      string
	}{
		{name: "allowlisted occupancy and decisions", want: "available"},
		{name: "forbidden", err: apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "private-pod", errors.New("private-api-error")), want: "forbidden"},
		{name: "failure", err: errors.New("private-api-error"), want: "error"},
		{name: "cancelled", err: context.Canceled, want: "cancelled"},
		{name: "partial page", truncated: true, want: "truncated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter, resources, _, pj, now := evidenceFixture()
			resources.sourceErr = test.err
			resources.nodes.Items = []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "private-worker", Annotations: map[string]string{"private": "private-annotation"}}, Spec: corev1.NodeSpec{ProviderID: "private-provider"}}}
			resources.pods.Items = []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "private-pod", Namespace: "private-customer", Labels: map[string]string{"pod-scaler.openshift.io/measured": "false"}}, Spec: corev1.PodSpec{NodeName: "private-worker", Containers: []corev1.Container{{Image: "private-image", Env: []corev1.EnvVar{{Name: "TOKEN", Value: "private-token"}}}}}}}
			resources.eventList.Items = []corev1.Event{{InvolvedObject: corev1.ObjectReference{UID: resources.pod.UID, Kind: "Pod"}, Source: corev1.EventSource{Component: "cluster-autoscaler"}, Reason: "NotTriggerScaleUp", Message: "private-message", LastTimestamp: metav1.NewTime(now)}}
			if test.truncated {
				resources.nodes.Continue = "private-token"
				resources.pods.Continue = "private-token"
				resources.eventList.Continue = "private-token"
			}
			sample := reporter.observeScheduling(context.Background(), pj, resources.pod, now, now)
			if sample.NodesStatus != test.want || sample.PodsStatus != test.want || sample.EventsStatus != test.want {
				t.Fatalf("unexpected statuses: %+v", sample)
			}
			if test.err == nil && (len(sample.Workers) != 1 || sample.Workers[0].Unmeasured != 1 || sample.Decisions["scale_up_not_triggered"] != 1) {
				t.Fatalf("incorrect projection: %+v", sample)
			}
			data, err := json.Marshal(sample)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "private") {
				t.Fatalf("private input leaked: %s", data)
			}
		})
	}
}

func TestProjectDecisions(t *testing.T) {
	for _, test := range []struct {
		name, uid, reason, component string
		offset                       time.Duration
		series                       bool
		want                         map[string]int
	}{
		{name: "matching UID and window", uid: "private-uid", reason: "FailedScheduling", component: "default-scheduler", want: map[string]int{"scheduler_rejected": 1}},
		{name: "other parent", uid: "other", reason: "FailedScheduling", component: "default-scheduler", want: map[string]int{}},
		{name: "before creation", uid: "private-uid", reason: "FailedScheduling", component: "default-scheduler", offset: -2 * time.Minute, want: map[string]int{}},
		{name: "after completion", uid: "private-uid", reason: "TriggeredScaleUp", component: "cluster-autoscaler", offset: time.Minute, want: map[string]int{}},
		{name: "unknown reason", uid: "private-uid", reason: "private-reason", component: "cluster-autoscaler", want: map[string]int{}},
		{name: "unknown producer", uid: "private-uid", reason: "TriggeredScaleUp", component: "private-controller", want: map[string]int{}},
		{name: "series timestamp", uid: "private-uid", reason: "TriggeredScaleUp", component: "cluster-autoscaler", series: true, want: map[string]int{"scale_up_triggered": 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, resources, _, _, now := evidenceFixture()
			event := corev1.Event{InvolvedObject: corev1.ObjectReference{UID: types.UID(test.uid), Kind: "Pod"}, Source: corev1.EventSource{Component: test.component}, Reason: test.reason, LastTimestamp: metav1.NewTime(now.Add(test.offset))}
			if test.series {
				event.Series = &corev1.EventSeries{LastObservedTime: metav1.NewMicroTime(now)}
			}
			if diff := cmp.Diff(test.want, projectDecisions([]corev1.Event{event}, resources.pod, now)); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestSchedulingOptionalFailure(t *testing.T) {
	for _, test := range []struct {
		name                       string
		complete, disabled, dryRun bool
	}{
		{name: "pending requeues without started containers"},
		{name: "optout unchanged", disabled: true},
		{name: "dryrun unchanged", dryRun: true},
		{name: "baseline upload and finalizer release survive optional failure", complete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter, resources, opener, pj, _ := evidenceFixture()
			opener.ReadError = errors.New("private-api-error")
			reporter.dryRun = test.dryRun
			if test.disabled {
				reporter.evidence = nil
			}
			if test.complete {
				pj.Status.State = prowv1.ErrorState
				pj.SetComplete()
				resources.pod.Finalizers = []string{"prow.x-k8s.io/gcsk8sreporter"}
				resources.patchData = `{"metadata":{"finalizers":null}}`
			}
			result, err := reporter.report(context.Background(), logrus.NewEntry(logrus.New()), pj)
			if err != nil {
				t.Fatal(err)
			}
			if !test.complete && !test.disabled && !test.dryRun && (result == nil || result.RequeueAfter != schedulingInterval) {
				t.Fatal("missing bounded requeue")
			}
			if (test.disabled || test.dryRun || test.complete) && result != nil {
				t.Fatal("unexpected requeue")
			}
			if test.complete {
				found := false
				for name := range opener.Buffer {
					if strings.HasSuffix(name, "/podinfo.json") {
						found = true
					}
				}
				if !found {
					t.Fatal("baseline artifact missing")
				}
			}
		})
	}
}

func TestSchedulingAllow(t *testing.T) {
	collector := &schedulingCollector{attempts: make(map[string]time.Time)}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		offset time.Duration
		want   bool
	}{
		{name: "first attempt", want: true}, {name: "immediate event does not sample", want: false}, {name: "next interval", offset: time.Minute, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := collector.allow("job", now.Add(test.offset)); got != test.want {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}

func TestSchedulingPersistedHistoryRecovery(t *testing.T) {
	for _, failure := range []string{"none", "read", "write"} {
		t.Run(failure, func(t *testing.T) {
			reporter, resources, opener, pj, now := evidenceFixture()
			if _, err := reporter.schedulingEvidence(context.Background(), pj, now); err != nil {
				t.Fatal(err)
			}
			before := readEvidence(t, opener)
			stored := make(map[string]string)
			for name, buffer := range opener.Buffer {
				stored[name] = buffer.String()
			}
			restarted := New(reporter.cfg, opener, resources, 1, false)
			if err := restarted.EnableSchedulingEvidence(); err != nil {
				t.Fatal(err)
			}
			if failure != "none" {
				if failure == "read" {
					opener.ReadError = errors.New("storage read unavailable")
				} else {
					opener.WriteError = errors.New("storage write unavailable")
				}
				if _, err := restarted.schedulingEvidence(context.Background(), pj, now.Add(time.Minute)); err == nil {
					t.Fatal("expected storage failure")
				}
				for name, content := range stored {
					if got := opener.Buffer[name].String(); got != content {
						t.Fatal("failed collection changed persisted history")
					}
				}
				opener.ReadError, opener.WriteError = nil, nil
			}
			if _, err := restarted.schedulingEvidence(context.Background(), pj, now.Add(3*time.Minute)); err != nil {
				t.Fatal(err)
			}
			after := readEvidence(t, opener)
			if len(after.Samples) != len(before.Samples)+1 {
				t.Fatalf("history length: got %d, want %d", len(after.Samples), len(before.Samples)+1)
			}
			if diff := cmp.Diff(before.Samples, after.Samples[:len(before.Samples)]); diff != "" {
				t.Fatalf("persisted samples changed (-want +got): %s", diff)
			}
			if after.Observer != restarted.evidence.epoch || after.Observer == before.Observer || !after.Start.Equal(before.Start) {
				t.Fatal("restart did not retain start and replace observer")
			}
			for _, gap := range []string{"observer_restart", "observation_gap", "history_before_first_observation_unavailable"} {
				if !strings.Contains(strings.Join(after.Gaps, ","), gap) {
					t.Fatalf("missing gap %q", gap)
				}
			}
		})
	}
}

type orderedEvidenceResources struct {
	*evidenceResources
	calls *[]string
}

func (resources *orderedEvidenceResources) PatchPod(ctx context.Context, cluster, namespace, name string, patchType types.PatchType, data []byte) error {
	*resources.calls = append(*resources.calls, "remove finalizer")
	return resources.testResourceGetter.PatchPod(ctx, cluster, namespace, name, patchType, data)
}

type orderedEvidenceOpener struct {
	*fakeopener.FakeOpener
	calls              *[]string
	failOptionalUpload bool
}

func (opener *orderedEvidenceOpener) Reader(ctx context.Context, destination string) (prowio.ReadCloser, error) {
	*opener.calls = append(*opener.calls, "optional read")
	return opener.FakeOpener.Reader(ctx, destination)
}

func (opener *orderedEvidenceOpener) Writer(ctx context.Context, destination string, opts ...prowio.WriterOptions) (prowio.WriteCloser, error) {
	if strings.HasSuffix(destination, "/podinfo.json") {
		*opener.calls = append(*opener.calls, "baseline upload")
	}
	if strings.HasSuffix(destination, "/scheduling-evidence.json") && opener.failOptionalUpload {
		*opener.calls = append(*opener.calls, "optional upload")
		return nil, errors.New("optional upload failure")
	}
	return opener.FakeOpener.Writer(ctx, destination, opts...)
}

func TestSchedulingFinalizerOrdering(t *testing.T) {
	for _, failure := range []string{"optional read", "optional API", "optional upload", "baseline upload", "finalizer patch"} {
		t.Run(failure, func(t *testing.T) {
			reporter, resources, opener, pj, _ := evidenceFixture()
			pj.Status.State = prowv1.ErrorState
			pj.SetComplete()
			resources.pod.Finalizers = []string{"prow.x-k8s.io/gcsk8sreporter"}
			resources.patchData = `{"metadata":{"finalizers":null}}`
			calls := []string{}
			reporter.rg = &orderedEvidenceResources{evidenceResources: resources, calls: &calls}
			reporter.opener = &orderedEvidenceOpener{FakeOpener: opener, calls: &calls, failOptionalUpload: failure == "optional upload"}
			switch failure {
			case "optional read":
				opener.ReadError = errors.New("optional storage failure")
			case "optional API":
				resources.sourceErr = errors.New("optional API failure")
			case "baseline upload":
				opener.WriteError = errors.New("baseline storage failure")
			case "finalizer patch":
				resources.patchErr = errors.New("patch failure")
			}
			result, err := reporter.report(context.Background(), logrus.NewEntry(logrus.New()), pj)
			wantError := failure == "baseline upload" || failure == "finalizer patch"
			if (err != nil) != wantError || result != nil {
				t.Fatalf("result=%v err=%v, wantError=%v", result, err, wantError)
			}
			want := []string{"baseline upload", "remove finalizer", "optional read"}
			if failure == "optional upload" {
				want = append(want, "optional upload")
			}
			if failure == "baseline upload" {
				want = []string{"baseline upload", "optional read"}
			}
			if diff := cmp.Diff(want, calls); diff != "" {
				t.Fatalf("operation order/count (-want +got): %s", diff)
			}
		})
	}
}

func TestSchedulingProductionLists(t *testing.T) {
	for _, test := range []struct {
		name, resourcePath, label, field, kind string
		limit                                  int
	}{
		{name: "workers", resourcePath: "/api/v1/nodes", label: "node-role.kubernetes.io/worker", kind: "NodeList", limit: maxWorkers},
		{name: "occupants across namespaces", resourcePath: "/api/v1/pods", kind: "PodList", limit: maxOccupants},
		{name: "parent events", resourcePath: "/api/v1/namespaces/private-namespace/events", field: "involvedObject.uid=private-uid", kind: "EventList", limit: maxSchedulingEvents},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := make(chan struct{}, 2)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				select {
				case calls <- struct{}{}:
				default:
					t.Error("unexpected additional list request")
				}
				query := request.URL.Query()
				if request.Method != http.MethodGet || request.URL.Path != test.resourcePath || query.Get("labelSelector") != test.label || query.Get("fieldSelector") != test.field || query.Get("limit") != strconv.Itoa(test.limit) || query.Get("continue") != "" {
					t.Errorf("unexpected list request: %s %s", request.Method, request.URL)
				}
				writer.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(writer).Encode(map[string]interface{}{"apiVersion": "v1", "kind": test.kind, "metadata": map[string]string{"continue": "next-page"}, "items": []interface{}{}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := coreclient.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			getter := NewK8sResourceGetter(map[string]coreclient.CoreV1Interface{"build": client})
			_, resources, _, _, _ := evidenceFixture()
			var continuation string
			switch test.kind {
			case "NodeList":
				list, listErr := getter.Workers(context.Background(), "build")
				err = listErr
				if list != nil {
					continuation = list.Continue
				}
			case "PodList":
				list, listErr := getter.Occupants(context.Background(), "build")
				err = listErr
				if list != nil {
					continuation = list.Continue
				}
			case "EventList":
				list, listErr := getter.SchedulingEvents(context.Background(), "build", resources.pod)
				err = listErr
				if list != nil {
					continuation = list.Continue
				}
			}
			if err != nil || len(calls) != 1 || continuation != "next-page" {
				t.Fatalf("calls=%d continuation=%q err=%v", len(calls), continuation, err)
			}
		})
	}
}

func TestSchedulingMixedSourceFailures(t *testing.T) {
	for _, test := range []struct {
		source  string
		missing bool
	}{
		{source: "nodes"}, {source: "pods"}, {source: "events"},
		{source: "nodes", missing: true}, {source: "pods", missing: true}, {source: "events", missing: true},
	} {
		t.Run(test.source+"/missing="+strconv.FormatBool(test.missing), func(t *testing.T) {
			source := test.source
			reporter, resources, _, pj, now := evidenceFixture()
			failure := apierrors.NewForbidden(schema.GroupResource{Resource: source}, "", errors.New("denied"))
			want := map[string]string{"nodes": "available", "pods": "available", "events": "available"}
			want[source] = "forbidden"
			switch source {
			case "nodes":
				resources.nodesErr = failure
			case "pods":
				resources.podsErr = failure
			case "events":
				resources.eventsErr = failure
			}
			if test.missing {
				resources.nodesErr, resources.podsErr, resources.eventsErr = nil, nil, nil
				want[source] = "unavailable"
				switch source {
				case "nodes":
					resources.nodes = nil
				case "pods":
					resources.pods = nil
				case "events":
					resources.eventList = nil
				}
			}
			sample := reporter.observeScheduling(context.Background(), pj, resources.pod, now, now)
			got := map[string]string{"nodes": sample.NodesStatus, "pods": sample.PodsStatus, "events": sample.EventsStatus}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestSchedulingAssignmentDecisionRefresh(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		t.Run("assigned="+strconv.FormatBool(assigned), func(t *testing.T) {
			reporter, resources, opener, pj, now := evidenceFixture()
			resources.nodes.Items = []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker"}}}
			if _, err := reporter.schedulingEvidence(context.Background(), pj, now); err != nil {
				t.Fatal(err)
			}
			initial := readEvidence(t, opener)
			if len(initial.Samples) != 1 || len(initial.Samples[0].Workers) != 1 {
				t.Fatal("missing initial unassigned occupancy")
			}
			if assigned {
				resources.pod.Spec.NodeName = "worker"
			}
			resources.eventList.Items = []corev1.Event{{InvolvedObject: corev1.ObjectReference{UID: resources.pod.UID, Kind: "Pod"}, Source: corev1.EventSource{Component: "cluster-autoscaler"}, Reason: "TriggeredScaleUp", LastTimestamp: metav1.NewTime(now.Add(time.Minute))}}
			if _, err := reporter.schedulingEvidence(context.Background(), pj, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			intermediate := readEvidence(t, opener)
			if len(intermediate.Samples) != 2 || intermediate.Samples[1].Decisions["scale_up_triggered"] != 1 {
				t.Fatal("missing decision refresh")
			}
			pj.Status.State = prowv1.SuccessState
			completed := metav1.NewTime(now.Add(2 * time.Minute))
			pj.Status.CompletionTime = &completed
			resources.eventList.Items = append(resources.eventList.Items, corev1.Event{InvolvedObject: corev1.ObjectReference{UID: resources.pod.UID, Kind: "Pod"}, Source: corev1.EventSource{Component: "default-scheduler"}, Reason: "Scheduled", LastTimestamp: completed}, corev1.Event{InvolvedObject: corev1.ObjectReference{UID: resources.pod.UID, Kind: "Pod"}, Source: corev1.EventSource{Component: "cluster-autoscaler"}, Reason: "FailedToScaleUp", LastTimestamp: metav1.NewTime(now.Add(4 * time.Minute))})
			if _, err := reporter.schedulingEvidence(context.Background(), pj, now.Add(5*time.Minute)); err != nil {
				t.Fatal(err)
			}
			final := readEvidence(t, opener)
			if !final.Final || !final.End.Equal(completed.Time) || len(final.Samples) != 3 {
				t.Fatalf("incorrect terminal manifest: %+v", final)
			}
			if diff := cmp.Diff(initial.Samples, final.Samples[:1]); diff != "" {
				t.Fatal(diff)
			}
			last := final.Samples[2]
			if diff := cmp.Diff(map[string]int{"scale_up_triggered": 1, "scheduler_assigned": 1}, last.Decisions); diff != "" {
				t.Fatal(diff)
			}
			for index, sample := range final.Samples {
				if index == 0 || (index == 1 && !assigned) {
					continue
				}
				if len(sample.Workers) != 0 || sample.NodesStatus != "outside_run_window_or_assigned" || sample.PodsStatus != "outside_run_window_or_assigned" {
					t.Fatalf("out-of-window or assigned occupancy: %+v", sample)
				}
			}
		})
	}
}

func TestEnableSchedulingEvidence(t *testing.T) {
	for _, test := range []struct {
		name        string
		unsupported bool
	}{
		{name: "explicit initialization"}, {name: "unsupported resource getter", unsupported: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter, _, _, _, _ := evidenceFixture()
			reporter.evidence = nil
			if test.unsupported {
				reporter.rg = testResourceGetter{}
			}
			err := reporter.EnableSchedulingEvidence()
			if (err != nil) != test.unsupported {
				t.Fatalf("unexpected enablement error: %v", err)
			}
			if !test.unsupported && (reporter.evidence == nil || len(reporter.evidence.epoch) != 32) {
				t.Fatal("missing random observer epoch")
			}
		})
	}
}

func TestSchedulingCancellation(t *testing.T) {
	reporter, _, opener, pj, now := evidenceFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reporter.schedulingEvidence(ctx, pj, now); err != nil {
		t.Fatal(err)
	}
	manifest := readEvidence(t, opener)
	if sample := manifest.Samples[0]; sample.NodesStatus != "cancelled" || sample.PodsStatus != "cancelled" || sample.EventsStatus != "cancelled" {
		t.Fatalf("cancellation not projected: %+v", sample)
	}
}
