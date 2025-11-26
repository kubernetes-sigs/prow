/*
Copyright 2022 The Kubernetes Authors.

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

package checker

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/plugins/testfreeze/checker/checkerfakes"
)

func TestInTestFreeze(t *testing.T) {
	t.Parallel()

	errTest := errors.New("")

	releaseBranch := func(v string) *plumbing.Reference {
		return plumbing.NewReferenceFromStrings("refs/heads/release-"+v, "")
	}

	tag := func(v string) *plumbing.Reference {
		return plumbing.NewReferenceFromStrings("refs/tags/"+v, "")
	}

	testTime := metav1.Now()

	for _, tc := range []struct {
		name    string
		prepare func(*checkerfakes.FakeChecker)
		assert  func(*Result, error)
	}{
		{
			name: "success no test freeze",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.ListRefsReturns([]*plumbing.Reference{
					tag("wrong"),       // unable to parse this tag, but don't error
					releaseBranch("1"), // unable to parse this branch, but don't error
					releaseBranch("1.18"),
					tag("1.18.0"),
					releaseBranch("1.23"),
					tag("1.23.0"),
					releaseBranch("1.22"),
					tag("1.22.0"),
				}, nil)
			},
			assert: func(res *Result, err error) {
				assert.False(t, res.InTestFreeze)
				assert.Equal(t, "release-1.23", res.Branch)
				assert.Equal(t, "v1.23.0", res.Tag)
				assert.Empty(t, res.LastFastForward)
				assert.Nil(t, err)
			},
		},
		{
			name: "success in test freeze",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.ListRefsReturns([]*plumbing.Reference{
					releaseBranch("1.18"),
					releaseBranch("1.24"),
					releaseBranch("1.22"),
					tag("1.18.0"),
					tag("1.22.0"),
				}, nil)
				mock.UnmarshalProwJobsReturns(&v1.ProwJobList{
					Items: []v1.ProwJob{
						{
							Spec: v1.ProwJobSpec{Job: jobName},
							Status: v1.ProwJobStatus{
								State:          v1.SuccessState,
								CompletionTime: &testTime,
							},
						},
					},
				}, nil)
			},
			assert: func(res *Result, err error) {
				assert.True(t, res.InTestFreeze)
				assert.Equal(t, "release-1.24", res.Branch)
				assert.Equal(t, "v1.24.0", res.Tag)
				assert.Equal(t, "v1.24.0", res.Tag)
				assert.Equal(t, testTime.Format(time.UnixDate), res.LastFastForward)
				assert.Nil(t, err)
			},
		},
		{
			name: "error no latest release branch found",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.ListRefsReturns([]*plumbing.Reference{
					tag("1.22.0"),
				}, nil)
			},
			assert: func(res *Result, err error) {
				assert.Nil(t, res)
				assert.NotNil(t, err)
			},
		},
		{
			name: "error on list refs",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.ListRefsReturns(nil, errTest)
			},
			assert: func(res *Result, err error) {
				assert.Nil(t, res)
				assert.NotNil(t, err)
			},
		},
		{
			name: "error on HttpGet",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.ListRefsReturns([]*plumbing.Reference{
					releaseBranch("1.18"),
					releaseBranch("1.24"),
					releaseBranch("1.22"),
					tag("1.18.0"),
					tag("1.22.0"),
				}, nil)
				mock.HttpGetReturns(nil, errTest)
			},
			assert: func(res *Result, err error) {
				assert.True(t, res.InTestFreeze)
				assert.Equal(t, "release-1.24", res.Branch)
				assert.Equal(t, "v1.24.0", res.Tag)
				assert.Equal(t, "v1.24.0", res.Tag)
				assert.Equal(t, unknownTime, res.LastFastForward)
				assert.Nil(t, err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &checkerfakes.FakeChecker{}
			tc.prepare(mock)

			sut := New(logrus.NewEntry(logrus.StandardLogger()))
			sut.checker = mock

			res, err := sut.InTestFreeze()

			tc.assert(res, err)
		})
	}
}

func TestInCodeFreeze(t *testing.T) {
	t.Parallel()

	errTest := errors.New("test error")

	for _, tc := range []struct {
		name    string
		branch  string
		prepare func(*checkerfakes.FakeChecker)
		assert  func(bool, error)
	}{
		{
			name:   "success branch is excluded (code freeze active)",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				prowConfig := &ProwConfig{}
				prowConfig.Tide.Queries = []struct {
					Repos            []string `yaml:"repos"`
					ExcludedBranches []string `yaml:"excludedBranches"`
				}{
					{
						Repos:            []string{"kubernetes/kubernetes"},
						ExcludedBranches: []string{"release-1.34"},
					},
				}
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns([]byte{}, nil)
				mock.UnmarshalProwConfigReturns(prowConfig, nil)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.True(t, inCodeFreeze)
				assert.Nil(t, err)
			},
		},
		{
			name:   "success branch not excluded (code freeze not active)",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				prowConfig := &ProwConfig{}
				prowConfig.Tide.Queries = []struct {
					Repos            []string `yaml:"repos"`
					ExcludedBranches []string `yaml:"excludedBranches"`
				}{
					{
						Repos:            []string{"kubernetes/kubernetes"},
						ExcludedBranches: []string{"release-1.33"},
					},
				}
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns([]byte{}, nil)
				mock.UnmarshalProwConfigReturns(prowConfig, nil)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.False(t, inCodeFreeze)
				assert.Nil(t, err)
			},
		},
		{
			name:   "success multiple queries, branch excluded in one",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				prowConfig := &ProwConfig{}
				prowConfig.Tide.Queries = []struct {
					Repos            []string `yaml:"repos"`
					ExcludedBranches []string `yaml:"excludedBranches"`
				}{
					{
						Repos:            []string{"kubernetes/test-infra"},
						ExcludedBranches: []string{"release-1.34"},
					},
					{
						Repos:            []string{"kubernetes/kubernetes"},
						ExcludedBranches: []string{"release-1.34"},
					},
				}
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns([]byte{}, nil)
				mock.UnmarshalProwConfigReturns(prowConfig, nil)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.True(t, inCodeFreeze)
				assert.Nil(t, err)
			},
		},
		{
			name:   "success branch excluded for different repo only",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				prowConfig := &ProwConfig{}
				prowConfig.Tide.Queries = []struct {
					Repos            []string `yaml:"repos"`
					ExcludedBranches []string `yaml:"excludedBranches"`
				}{
					{
						Repos:            []string{"kubernetes/test-infra"},
						ExcludedBranches: []string{"release-1.34"},
					},
					{
						Repos:            []string{"kubernetes/kubernetes"},
						ExcludedBranches: []string{"release-1.33"},
					},
				}
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns([]byte{}, nil)
				mock.UnmarshalProwConfigReturns(prowConfig, nil)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.False(t, inCodeFreeze)
				assert.Nil(t, err)
			},
		},
		{
			name:   "success empty tide queries",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				prowConfig := &ProwConfig{}
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns([]byte{}, nil)
				mock.UnmarshalProwConfigReturns(prowConfig, nil)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.False(t, inCodeFreeze)
				assert.Nil(t, err)
			},
		},
		{
			name:   "error on HttpGet",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.HttpGetReturns(nil, errTest)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.False(t, inCodeFreeze)
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "get prow config")
			},
		},
		{
			name:   "error on ReadAllBody",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns(nil, errTest)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.False(t, inCodeFreeze)
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "read response body")
			},
		},
		{
			name:   "error on UnmarshalProwConfig",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns([]byte{}, nil)
				mock.UnmarshalProwConfigReturns(nil, errTest)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.False(t, inCodeFreeze)
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "unmarshal prow config")
			},
		},
		{
			name:   "error on type assertion",
			branch: "release-1.34",
			prepare: func(mock *checkerfakes.FakeChecker) {
				mock.HttpGetReturns(&http.Response{
					Body: io.NopCloser(strings.NewReader("")),
				}, nil)
				mock.ReadAllBodyReturns([]byte{}, nil)
				// Return a string instead of *ProwConfig
				mock.UnmarshalProwConfigReturns("wrong type", nil)
			},
			assert: func(inCodeFreeze bool, err error) {
				assert.False(t, inCodeFreeze)
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "failed to type assert prow config")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &checkerfakes.FakeChecker{}
			tc.prepare(mock)

			sut := New(logrus.NewEntry(logrus.StandardLogger()))
			sut.checker = mock

			inCodeFreeze, err := sut.inCodeFreeze(tc.branch)

			tc.assert(inCodeFreeze, err)
		})
	}
}
