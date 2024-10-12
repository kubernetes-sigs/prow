/*
Copyright 2021 The Kubernetes Authors.

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
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/yaml"
)

var (
	DefaultID = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "defaultid",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job: "Default TenantID",
			ProwJobDefault: &prowjobv1.ProwJobDefault{
				TenantID: config.DefaultTenantID,
			},
			Namespace: testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
	NoID = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "noid",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job:            "No TenantID",
			ProwJobDefault: &prowjobv1.ProwJobDefault{},
			Namespace:      testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
	NoDefault = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "nodefault",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job:       "No ProwJobDefault",
			Namespace: testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
	DefaultIDHidden = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "defaulthidden",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job: "Default TenantID and Hidden",
			ProwJobDefault: &prowjobv1.ProwJobDefault{
				TenantID: config.DefaultTenantID,
			},
			Hidden:    true,
			Namespace: testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
	NoIDHidden = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "nohiddenid",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job:            "No TenantID and Hidden",
			ProwJobDefault: &prowjobv1.ProwJobDefault{},
			Hidden:         true,
			Namespace:      testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
	NoDefaultHidden = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "nodefaulthidden",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job:       "No ProwJobDefault and Hidden",
			Hidden:    true,
			Namespace: testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
	ID = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "id",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job: "TenantID and hidden",
			ProwJobDefault: &prowjobv1.ProwJobDefault{
				TenantID: "tester",
			},
			Namespace: testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
	IDHidden = prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{
			Name:      "idhidden",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				"created-by-prow": "true",
			},
		},
		Spec: prowjobv1.ProwJobSpec{
			Job: "Default TenantID and Hidden",
			ProwJobDefault: &prowjobv1.ProwJobDefault{
				TenantID: "tester",
			},
			Hidden:    true,
			Namespace: testpodNamespace,
		},
		Status: prowjobv1.ProwJobStatus{
			State:     prowjobv1.TriggeredState,
			StartTime: v1.NewTime(time.Now()),
		},
	}
)

func populateProwJobs(t *testing.T, prowjobs *prowjobv1.ProwJobList, kubeClient ctrlruntimeclient.Client, ctx context.Context) {
	if len(prowjobs.Items) > 0 {
		for _, prowjob := range prowjobs.Items {
			t.Logf("Creating prowjob: %s", prowjob.Name)

			if err := kubeClient.Create(ctx, &prowjob); err != nil {
				t.Fatalf("Failed creating prowjob: %v", err)
			}
			t.Logf("Finished creating prowjob: %s", prowjob.Name)
		}
	}
}

func getCleanupProwJobsFunc(prowjobs *prowjobv1.ProwJobList, kubeClient ctrlruntimeclient.Client, ctx context.Context) func() {
	return func() {
		for _, prowjob := range prowjobs.Items {
			kubeClient.Delete(ctx, &prowjob)
		}
	}
}

func getSpecs(pjs *prowjobv1.ProwJobList) []prowjobv1.ProwJobSpec {
	res := []prowjobv1.ProwJobSpec{}
	for _, pj := range pjs.Items {
		res = append(res, pj.Spec)
	}
	return res
}

func TestDeck(t *testing.T) {
	t.Parallel()

	resp, err := http.Get("http://localhost/deck")
	if err != nil {
		t.Fatalf("Failed getting deck front end %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected response status code %d, got %d, ", http.StatusOK, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed getting deck body response content %v", err)
	}
	if got, want := string(body), "<title>Prow Status</title>"; !strings.Contains(got, want) {
		firstLines := strings.Join(strings.SplitN(strings.TrimSpace(got), "\n", 30), "\n")
		t.Fatalf("Expected content %q not found in body %s [......]", want, firstLines)
	}
}

func TestDeckTenantIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		prowjobs     *prowjobv1.ProwJobList
		expected     *prowjobv1.ProwJobList
		unexpected   *prowjobv1.ProwJobList
		deckInstance string
	}{
		{
			name:         "deck-tenanted",
			prowjobs:     &prowjobv1.ProwJobList{Items: []prowjobv1.ProwJob{DefaultID, NoID, NoDefault, DefaultIDHidden, NoIDHidden, NoDefaultHidden, ID, IDHidden}},
			expected:     &prowjobv1.ProwJobList{Items: []prowjobv1.ProwJob{ID, IDHidden}},
			unexpected:   &prowjobv1.ProwJobList{Items: []prowjobv1.ProwJob{DefaultID, NoID, NoDefault, DefaultIDHidden, NoIDHidden, NoDefaultHidden}},
			deckInstance: "deck-tenanted",
		},
		{
			name:         "public-deck",
			prowjobs:     &prowjobv1.ProwJobList{Items: []prowjobv1.ProwJob{DefaultID, NoID, NoDefault, DefaultIDHidden, NoIDHidden, NoDefaultHidden, ID, IDHidden}},
			expected:     &prowjobv1.ProwJobList{Items: []prowjobv1.ProwJob{DefaultID, NoID, NoDefault}},
			unexpected:   &prowjobv1.ProwJobList{Items: []prowjobv1.ProwJob{ID, IDHidden, DefaultIDHidden, NoIDHidden, NoDefaultHidden}},
			deckInstance: "deck",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			//Give them new names to prevent conflict
			name := RandomString(t)
			prowjobs := renamePJs(tt.prowjobs, name)
			expected := renamePJs(tt.expected, name)
			unexpected := renamePJs(tt.unexpected, name)

			clusterContext := getClusterContext()
			t.Logf("Creating client for cluster: %s", clusterContext)
			kubeClient, err := NewClients("", clusterContext)
			if err != nil {
				t.Fatalf("Failed creating clients for cluster %q: %v", clusterContext, err)
			}
			ctx := context.Background()

			populateProwJobs(t, &prowjobs, kubeClient, ctx)
			t.Cleanup(getCleanupProwJobsFunc(&prowjobs, kubeClient, ctx))

			// Scrape Deck instance for jobs. It may be that the instance isn't
			// ready immediately by the time this test starts executing, so use
			// a retry mechanism.
			var body []byte
			timeout := 180 * time.Second
			pollInterval := 1 * time.Second
			endpoint := fmt.Sprintf("http://localhost/%s/prowjobs.js", tt.deckInstance)
			// Every time "scraper" below returns "false, nil" we retry it.
			scraper := func(ctx context.Context) (bool, error) {
				resp, err := http.Get(endpoint)
				if err != nil {
					t.Logf("Failed running GET request against %q: %v", endpoint, err)
					return false, nil
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Logf("Expected response status code %d, got %d, ", http.StatusOK, resp.StatusCode)
					return false, nil
				}
				body, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Logf("Failed getting deck body response content %v", err)
					return false, nil
				}

				got := prowjobv1.ProwJobList{}
				if err = yaml.Unmarshal(body, &got); err != nil {
					t.Logf("Failed unmarshal prowjobs %v", err)
					return false, nil
				}

				// Deck may take some number of seconds before it can return a
				// non-empty body. If we detect an empty body, retry early to
				// avoid spamming logs with "want vs got" messages below.
				if string(body) == "{\"items\":[]}" {
					t.Logf("endpoint %q returned empty result", endpoint)
					return false, nil
				}

				// It is conceivable that Deck could be aware of a partial set
				// of the jobs that we created in this test. Retry here as well.
				if allExpected := expectedPJsInDeck(&expected, &got); !allExpected {
					t.Logf("Not all expected jobs are present.\nwant: %v\n got:%v", expected, got)
					return false, nil
				}
				if unexpectedFound := unexpectedPJsInDeck(&unexpected, &got); unexpectedFound {
					t.Logf("Unexpected jobs found.\nwant: %v\n got:%v", expected, got)
					return false, nil
				}

				// Everything checks out, so no need to scrape any more.
				t.Logf("endpoint %q (finally) matched test expectations; scraping finished", endpoint)
				return true, nil
			}
			if waitErr := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, scraper); waitErr != nil {
				// If waitErr is not nil, it means we timed out waiting for this
				// test to succeed.
				t.Errorf("Timed out while scraping %q", endpoint)
				t.Fatal(waitErr)
			}
		})
	}
}

func TestRerun(t *testing.T) {
	t.Parallel()
	const rerunJobConfigFile = "rerun-test.yaml"

	jobName := "rerun-test-job-" + RandomString(t)
	prowJobSelector := labels.SelectorFromSet(map[string]string{kube.ProwJobAnnotation: jobName})

	var rerunJobConfigTemplate = `periodics:
- interval: 1h
  name: %s
  spec:
    containers:
    - command:
      - echo
      args:
      - "Hello World!"
      image: localhost:5001/alpine
  labels:
    foo: "%s"`

	clusterContext := getClusterContext()
	t.Logf("Creating client for cluster: %s", clusterContext)
	kubeClient, err := NewClients("", clusterContext)
	if err != nil {
		t.Fatalf("Failed creating clients for cluster %q: %v", clusterContext, err)
	}

	ctx := context.Background()

	redeployJobConfig := func(jobConfig string) {
		if err := updateJobConfig(ctx, kubeClient, rerunJobConfigFile, []byte(jobConfig)); err != nil {
			t.Fatalf("Failed update job config: %v", err)
		}

		// Restart Horologium and Deck to ensure they see the updated ConfigMap contents.
		if err := rolloutDeployment(t, ctx, kubeClient, "horologium"); err != nil {
			t.Fatalf("Failed rolling out Horologium: %v", err)
		}
		if err := rolloutDeployment(t, ctx, kubeClient, "deck"); err != nil {
			t.Fatalf("Failed rolling out Deck: %v", err)
		}
	}

	// Deploy the initial template with "foo" as the label.
	redeployJobConfig(fmt.Sprintf(rerunJobConfigTemplate, jobName, "foo"))

	t.Cleanup(func() {
		if err := updateJobConfig(ctx, kubeClient, rerunJobConfigFile, []byte{}); err != nil {
			t.Logf("ERROR CLEANUP: %v", err)
		}
		// Prevent horologium from immediately creating the "missing" ProwJob after the
		// DeleteAll call further down, because horologium still runs with the last
		// non-empty configuration (foo=bar).
		if err := rolloutDeployment(t, ctx, kubeClient, "horologium"); err != nil {
			t.Logf("Failed rolling out Horologium: %v", err)
		}
		if err := kubeClient.DeleteAllOf(ctx, &prowjobv1.ProwJob{}, &ctrlruntimeclient.DeleteAllOfOptions{
			ListOptions: ctrlruntimeclient.ListOptions{
				Namespace:     defaultNamespace,
				LabelSelector: prowJobSelector,
			},
		}); err != nil {
			t.Logf("ERROR CLEANUP: %v", err)
		}
	})

	getLatestJob := func(t *testing.T, jobName string, lastRun *v1.Time) *prowjobv1.ProwJob {
		var res *prowjobv1.ProwJob
		if err := wait.PollUntilContextTimeout(ctx, time.Second, 90*time.Second, true, func(ctx context.Context) (bool, error) {
			pjs := &prowjobv1.ProwJobList{}
			err := kubeClient.List(ctx, pjs, &ctrlruntimeclient.ListOptions{
				LabelSelector: prowJobSelector,
				Namespace:     defaultNamespace,
			})
			if err != nil {
				return false, fmt.Errorf("failed listing prow jobs: %w", err)
			}

			sort.Slice(pjs.Items, func(i, j int) bool {
				createdi := pjs.Items[i].CreationTimestamp
				createdj := pjs.Items[j].CreationTimestamp
				return createdj.Before(&createdi)
			})

			if len(pjs.Items) > 0 {
				if lastRun != nil && pjs.Items[0].CreationTimestamp.Before(lastRun) {
					return false, nil
				}
				res = &pjs.Items[0]
			}

			return res != nil, nil
		}); err != nil {
			t.Fatalf("Failed waiting for job %q: %v", jobName, err)
		}
		return res
	}

	// Wait for the first job to be created by horologium.
	initialJob := getLatestJob(t, jobName, nil)

	// Update the job configuration with a new label.
	redeployJobConfig(fmt.Sprintf(rerunJobConfigTemplate, jobName, "bar"))

	// Rerun the job using the latest config.
	rerunJob(t, ctx, initialJob.Name, "latest")

	// Wait until the desired ProwJob shows up.
	latestJob := getLatestJob(t, jobName, &initialJob.CreationTimestamp)
	if latestJob.Labels["foo"] != "bar" {
		t.Fatalf("Failed waiting for ProwJob %q using latest config with foo=bar.", jobName)
	}

	// Prevent Deck from being too fast and recreating the new job in the same second
	// as the previous one.
	time.Sleep(1 * time.Second)

	// Deck scheduled job from latest configuration, rerun with "original"
	// should still go with original configuration.
	rerunJob(t, ctx, initialJob.Name, "original")

	originalJob := getLatestJob(t, jobName, &latestJob.CreationTimestamp)
	if originalJob.Labels["foo"] != "foo" {
		t.Fatalf("Failed waiting for ProwJob %q using original config with foo=foo.", jobName)
	}
}

func rerunJob(t *testing.T, ctx context.Context, jobName string, mode string) {
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://localhost/rerun?mode=%v&prowjob=%v", mode, jobName), nil)
	if err != nil {
		t.Fatalf("Could not generate a request %v", err)
	}

	// Deck might not be fully ready yet, so we must retry.
	if err := wait.PollUntilContextTimeout(ctx, time.Second, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, fmt.Errorf("could not make post request: %w", err)
		}
		defer res.Body.Close()

		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Logf("Failed to read response body: %v", err)
			return false, nil
		}
		t.Logf("Response body: %s", string(body))

		return res.StatusCode == http.StatusOK, nil
	}); err != nil {
		t.Fatalf("Failed to rerun job %q with %s config: %v", jobName, mode, err)
	}
}

func renamePJs(pjs *prowjobv1.ProwJobList, name string) prowjobv1.ProwJobList {
	res := prowjobv1.ProwJobList{Items: []prowjobv1.ProwJob{}}
	for _, pj := range pjs.Items {
		renamed := pj.DeepCopy()
		renamed.ObjectMeta.Name = pj.ObjectMeta.Name + name
		res.Items = append(res.Items, *renamed)
	}
	return res
}

func expectedPJsInDeck(pjs *prowjobv1.ProwJobList, deck *prowjobv1.ProwJobList) bool {
	for _, expected := range getSpecs(pjs) {
		found := false
		for _, spec := range getSpecs(deck) {
			if diff := cmp.Diff(expected, spec); diff == "" {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func unexpectedPJsInDeck(pjs *prowjobv1.ProwJobList, deck *prowjobv1.ProwJobList) bool {
	for _, unexpected := range getSpecs(pjs) {
		for _, spec := range getSpecs(deck) {
			if diff := cmp.Diff(unexpected, spec); diff == "" {
				return true
			}
		}
	}
	return false
}
