/*
Copyright 2026 The Kubernetes Authors.

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

package spyglass

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"

	pkgio "sigs.k8s.io/prow/pkg/io"
)

// scriptedIterator returns errs in order, then one object, then io.EOF.
type scriptedIterator struct {
	errs  []error
	calls int
}

func (it *scriptedIterator) Next(context.Context) (pkgio.ObjectAttributes, error) {
	it.calls++
	if it.calls <= len(it.errs) {
		return pkgio.ObjectAttributes{}, it.errs[it.calls-1]
	}
	if it.calls == len(it.errs)+1 {
		return pkgio.ObjectAttributes{Name: "logs/job/1/build-log.txt"}, nil
	}
	return pkgio.ObjectAttributes{}, io.EOF
}

type scriptedOpener struct {
	pkgio.Opener
	it *scriptedIterator
}

func (o scriptedOpener) Iterator(context.Context, string, string) (pkgio.ObjectIterator, error) {
	return o.it, nil
}

func TestArtifactsDoesNotRetryPermanentErrors(t *testing.T) {
	transient := errors.New("connection reset by peer")
	for _, tc := range []struct {
		name          string
		errs          []error
		expectedCalls int
		expectedErr   bool
	}{
		{
			name: "missing bucket fails without retrying",
			// The error seen in production.
			errs:          []error{fmt.Errorf("%w: %w", storage.ErrBucketNotExist, &googleapi.Error{Code: http.StatusNotFound})},
			expectedCalls: 1,
			expectedErr:   true,
		},
		{
			name:          "permission denied fails without retrying",
			errs:          []error{&googleapi.Error{Code: http.StatusForbidden, Message: "Access denied."}},
			expectedCalls: 1,
			expectedErr:   true,
		},
		{
			name:          "missing object fails without retrying",
			errs:          []error{storage.ErrObjectNotExist},
			expectedCalls: 1,
			expectedErr:   true,
		},
		{
			name:          "transient errors are still retried",
			errs:          []error{transient, transient},
			expectedCalls: 4, // two errors, one object, EOF
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := &scriptedIterator{errs: tc.errs}
			af := NewStorageArtifactFetcher(scriptedOpener{it: it}, createConfigGetter("test-bucket"), false)
			arts, err := af.artifacts(context.Background(), "gs://test-bucket/logs/job/1")
			if (err != nil) != tc.expectedErr {
				t.Fatalf("expected error=%t, got %v", tc.expectedErr, err)
			}
			if it.calls != tc.expectedCalls {
				t.Errorf("expected %d calls to Next, got %d", tc.expectedCalls, it.calls)
			}
			if !tc.expectedErr && (len(arts) != 1 || arts[0] != "build-log.txt") {
				t.Errorf("expected [build-log.txt], got %v", arts)
			}
		})
	}
}
