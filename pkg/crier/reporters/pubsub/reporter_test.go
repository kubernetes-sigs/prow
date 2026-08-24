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

package pubsub

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/crier/reporters/criercommonlib"
)

const (
	testPubSubProjectName = "test-project"
	testPubSubTopicName   = "test-topic"
	testPubSubRunID       = "test-id"
)

type fca struct {
	sync.Mutex
	c *config.Config
}

func (f *fca) Config() *config.Config {
	f.Lock()
	defer f.Unlock()
	return f.c
}

func TestGenerateMessageFromPJ(t *testing.T) {
	var testcases = []struct {
		name            string
		pj              *prowapi.ProwJob
		jobURLPrefix    string
		expectedMessage *ReportMessage
		expectedError   error
	}{
		// tests with gubernator job URLs
		{
			name: "Prowjob with all information for presubmit jobs should work with no error",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
					URL:   "guber/test1",
				},
				Spec: prowapi.ProwJobSpec{
					Type: prowapi.PresubmitJob,
					Job:  "test1",
					Refs: &prowapi.Refs{
						Pulls: []prowapi.Pull{{Number: 123}},
					},
				},
			},
			jobURLPrefix: "guber/",
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   testPubSubRunID,
				Status:  prowapi.SuccessState,
				URL:     "guber/test1",
				GCSPath: "gs://test1",
				Refs: []prowapi.Refs{
					{
						Pulls: []prowapi.Pull{{Number: 123}},
					},
				},
				JobType: prowapi.PresubmitJob,
				JobName: "test1",
			},
		},
		{
			name: "Prowjob with all information for periodic jobs should work with no error",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
					URL:   "guber/test1",
				},
				Spec: prowapi.ProwJobSpec{
					Type: prowapi.PeriodicJob,
					Job:  "test1",
				},
			},
			jobURLPrefix: "guber/",
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   testPubSubRunID,
				Status:  prowapi.SuccessState,
				URL:     "guber/test1",
				GCSPath: "gs://test1",
				JobType: prowapi.PeriodicJob,
				JobName: "test1",
			},
		},
		{
			name: "Prowjob has no pubsub runID label, should return a message with runid empty",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-runID",
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   "",
				Status:  prowapi.SuccessState,
			},
		},
		{
			name: "Prowjob with all information annotations should work with no error",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Annotations: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
					URL:   "guber/test1",
				},
			},
			jobURLPrefix: "guber/",
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   testPubSubRunID,
				Status:  prowapi.SuccessState,
				URL:     "guber/test1",
				GCSPath: "gs://test1",
			},
		},
		{
			name: "Prowjob has no pubsub runID annotation, should return a message with runid empty",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-runID",
					Annotations: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   "",
				Status:  prowapi.SuccessState,
			},
		},

		// tests with regular job URLs
		{
			name: "Prowjob with all information for presubmit jobs should work with no error",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
					URL:   "https://prow.k8s.io/view/gcs/test1",
				},
				Spec: prowapi.ProwJobSpec{
					Type: prowapi.PresubmitJob,
					Job:  "test1",
					Refs: &prowapi.Refs{
						Pulls: []prowapi.Pull{{Number: 123}},
					},
				},
			},
			jobURLPrefix: "https://prow.k8s.io/view/gcs/",
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   testPubSubRunID,
				Status:  prowapi.SuccessState,
				URL:     "https://prow.k8s.io/view/gcs/test1",
				GCSPath: "gs://test1",
				Refs: []prowapi.Refs{
					{
						Pulls: []prowapi.Pull{{Number: 123}},
					},
				},
				JobType: prowapi.PresubmitJob,
				JobName: "test1",
			},
		},
		{
			name: "Prowjob with all information for periodic jobs should work with no error",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
					URL:   "https://prow.k8s.io/view/gcs/test1",
				},
				Spec: prowapi.ProwJobSpec{
					Type: prowapi.PeriodicJob,
					Job:  "test1",
				},
			},
			jobURLPrefix: "https://prow.k8s.io/view/gcs/",
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   testPubSubRunID,
				Status:  prowapi.SuccessState,
				URL:     "https://prow.k8s.io/view/gcs/test1",
				GCSPath: "gs://test1",
				JobType: prowapi.PeriodicJob,
				JobName: "test1",
			},
		},
		{
			name: "Prowjob with all information annotations should work with no error",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Annotations: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
					URL:   "https://prow.k8s.io/view/gcs/test1",
				},
			},
			jobURLPrefix: "https://prow.k8s.io/view/gcs/",
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   testPubSubRunID,
				Status:  prowapi.SuccessState,
				URL:     "https://prow.k8s.io/view/gcs/test1",
				GCSPath: "gs://test1",
			},
		},
		{
			name: "Status message",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Annotations: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State:       prowapi.SuccessState,
					URL:         "https://prow.k8s.io/view/gcs/test1",
					Description: "this job went great",
				},
			},
			jobURLPrefix: "https://prow.k8s.io/view/gcs/",
			expectedMessage: &ReportMessage{
				Project: testPubSubProjectName,
				Topic:   testPubSubTopicName,
				RunID:   testPubSubRunID,
				Status:  prowapi.SuccessState,
				URL:     "https://prow.k8s.io/view/gcs/test1",
				GCSPath: "gs://test1",
				Message: "this job went great",
			},
		},
	}

	for _, tc := range testcases {
		fca := &fca{
			c: &config.Config{
				ProwConfig: config.ProwConfig{
					Plank: config.Plank{
						JobURLPrefixConfig: map[string]string{"*": tc.jobURLPrefix},
					},
				},
			},
		}

		c := &Client{
			config: fca.Config,
		}

		m := c.generateMessageFromPJ(tc.pj)

		if !reflect.DeepEqual(m, tc.expectedMessage) {
			t.Errorf("Unexpected result from test: %s.\nExpected: %v\nGot: %v",
				tc.name, tc.expectedMessage, m)
		}
	}
}

func TestShouldReport(t *testing.T) {
	var testcases = []struct {
		name           string
		pj             *prowapi.ProwJob
		expectedResult bool
	}{
		{
			name: "Prowjob with all pubsub information labels should return",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedResult: true,
		},
		{
			name: "Prowjob has no pubsub project label, should not report",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-project",
					Labels: map[string]string{
						PubSubTopicLabel: testPubSubTopicName,
						PubSubRunIDLabel: testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedResult: false,
		},
		{
			name: "Prowjob has no pubsub topic label, should not report",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-topic",
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedResult: false,
		},
		{
			name: "Prowjob with all pubsub information annotations should return",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Annotations: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedResult: true,
		},
		{
			name: "Prowjob has no pubsub project annotation, should not report",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-project",
					Annotations: map[string]string{
						PubSubTopicLabel: testPubSubTopicName,
						PubSubRunIDLabel: testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedResult: false,
		},
		{
			name: "Prowjob has no pubsub topic annotation, should not report",
			pj: &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-topic",
					Annotations: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
				Status: prowapi.ProwJobStatus{
					State: prowapi.SuccessState,
				},
			},
			expectedResult: false,
		},
	}

	var fakeConfigAgent fca
	c := NewReporter(fakeConfigAgent.Config)

	for _, tc := range testcases {
		r := c.ShouldReport(context.Background(), logrus.NewEntry(logrus.StandardLogger()), tc.pj)

		if r != tc.expectedResult {
			t.Errorf("Unexpected result from test: %s.\nExpected: %v\nGot: %v",
				tc.name, tc.expectedResult, r)
		}
	}
}

// newTestReporter returns a reporter that talks to an in-memory Pub/Sub server
// and the server itself so tests can set up topics.
func newTestReporter(t *testing.T, opts ...pstest.ServerReactorOption) (*Client, *pstest.Server) {
	t.Helper()
	srv := pstest.NewServer(opts...)
	t.Cleanup(func() { srv.Close() })
	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial fake pubsub server: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	reporter := NewReporter(nil)
	reporter.newPubSubClient = func(ctx context.Context, project string) (*pubsub.Client, error) {
		return pubsub.NewClient(ctx, project, option.WithGRPCConn(conn))
	}
	return reporter, srv
}

func TestReport(t *testing.T) {
	topicName := "projects/" + testPubSubProjectName + "/topics/" + testPubSubTopicName
	testcases := []struct {
		name          string
		createTopic   bool
		serverOpts    []pstest.ServerReactorOption
		wantErr       bool
		wantUserError bool
	}{
		{
			name:        "existing topic receives the report",
			createTopic: true,
		},
		{
			name:          "missing topic is a user error",
			wantErr:       true,
			wantUserError: true,
		},
		{
			name:        "publish failure on an existing topic is not a user error",
			createTopic: true,
			serverOpts:  []pstest.ServerReactorOption{pstest.WithErrorInjection("Publish", codes.PermissionDenied, "injected")},
			wantErr:     true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			reporter, srv := newTestReporter(t, tc.serverOpts...)
			if tc.createTopic {
				if _, err := srv.GServer.CreateTopic(context.Background(), &pubsubpb.Topic{Name: topicName}); err != nil {
					t.Fatalf("create topic: %v", err)
				}
			}
			pj := &prowapi.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						PubSubProjectLabel: testPubSubProjectName,
						PubSubTopicLabel:   testPubSubTopicName,
						PubSubRunIDLabel:   testPubSubRunID,
					},
				},
			}

			_, _, err := reporter.Report(context.Background(), logrus.NewEntry(logrus.StandardLogger()), pj)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Report() error = %v, wantErr %t", err, tc.wantErr)
			}
			if got := criercommonlib.IsUserError(err); got != tc.wantUserError {
				t.Errorf("IsUserError(Report() error) = %t, want %t", got, tc.wantUserError)
			}

			if tc.wantErr {
				return
			}
			var got []*ReportMessage
			for _, m := range srv.Messages() {
				var rm ReportMessage
				if err := json.Unmarshal(m.Data, &rm); err != nil {
					t.Fatalf("unmarshal published message: %v", err)
				}
				got = append(got, &rm)
			}
			want := []*ReportMessage{reporter.generateMessageFromPJ(pj)}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("published messages differ (-want +got):\n%s", diff)
			}
		})
	}
}
