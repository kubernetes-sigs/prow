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

package tide

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/go-test/deep"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/apimachinery/pkg/util/sets"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/tide/blockers"
)

func TestExpectedStatus(t *testing.T) {
	mergeLabel := "tide/merge-method-merge"
	squashLabel := "tide/merge-method-squash"
	neededLabelsWithAlt := []string{"need-1", "need-2", "need-a-very-super-duper-extra-not-short-at-all-label-name,need-3"}
	neededLabels := []string{"need-1", "need-2", "need-a-very-super-duper-extra-not-short-at-all-label-name"}
	forbiddenLabels := []string{"forbidden-1", "forbidden-2"}
	testcases := []struct {
		name string

		baseref               string
		branchAllowList       []string
		branchDenyList        []string
		sameBranchReqs        bool
		labels                []string
		author                string
		firstQueryAuthor      string
		secondQueryAuthor     string
		milestone             string
		contexts              []Context
		checkRuns             []CheckRun
		inPool                bool
		blocks                []int
		prowJobs              []runtime.Object
		requiredContexts      []string
		mergeConflicts        bool
		displayAllTideQueries bool
		additionalTideQueries []config.TideQuery
		hasApprovingReview    bool
		singleQuery           bool

		state string
		desc  string
	}{
		{
			name:   "in pool",
			inPool: true,

			state: github.StatusSuccess,
			desc:  statusInPool,
		},
		{
			name:              "check truncation of label list",
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs need-1, need-2 labels."),
		},
		{
			name:              "check truncation of label list is not excessive",
			labels:            append([]string{}, neededLabels[:2]...),
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs need-a-very-super-duper-extra-not-short-at-all-label-name or need-3 label."),
		},
		{
			name:   "check multiple /tide labels result in conflict message",
			labels: append(append([]string{}, neededLabels...), mergeLabel, squashLabel),
			state:  github.StatusError,
			desc:   fmt.Sprintf(statusNotInPool, " PR has conflicting merge method override labels"),
		},
		{
			name:              "has forbidden labels",
			labels:            append(append([]string{}, neededLabels...), forbiddenLabels...),
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Should not have forbidden-1, forbidden-2 labels."),
		},
		{
			name:              "has one forbidden label",
			labels:            append(append([]string{}, neededLabels...), forbiddenLabels[0]),
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Should not have forbidden-1 label."),
		},
		{
			name:              "only mention one requirement class",
			labels:            append(append([]string{}, neededLabels[1:]...), forbiddenLabels[0]),
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs need-1 label."),
		},
		{
			name:                  "mention all possible queries when opted in",
			labels:                append(append([]string{}, neededLabels[1:]...), forbiddenLabels[0]),
			author:                "batman",
			firstQueryAuthor:      "batman",
			secondQueryAuthor:     "batman",
			milestone:             "v1.0",
			inPool:                false,
			displayAllTideQueries: true,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs need-1 label OR Needs 1, 2, 3, 4, 5, 6, 7 labels."),
		},
		{
			name:                  "displayAllTideQueries but only one query",
			labels:                append(append([]string{}, neededLabels[1:]...), forbiddenLabels[0]),
			author:                "batman",
			firstQueryAuthor:      "batman",
			secondQueryAuthor:     "batman",
			milestone:             "v1.0",
			inPool:                false,
			displayAllTideQueries: true,
			singleQuery:           true,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs need-1 label."),
		},
		{
			name:                  "displayAllTideQueries when there is no matching query",
			baseref:               "bad",
			branchDenyList:        []string{"bad"},
			sameBranchReqs:        true,
			labels:                append(append([]string{}, neededLabels[1:]...), forbiddenLabels[0]),
			author:                "batman",
			firstQueryAuthor:      "batman",
			secondQueryAuthor:     "batman",
			milestone:             "v1.0",
			inPool:                false,
			displayAllTideQueries: true,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " No Tide query for branch bad found."),
		},
		{
			name:           "displayAllTideQueries shows only queries matching the branch",
			baseref:        "main",
			branchDenyList: []string{"main"},
			sameBranchReqs: true,
			additionalTideQueries: []config.TideQuery{{
				Orgs:   []string{""},
				Labels: []string{"good-to-go"},
			}},
			labels:                append(append([]string{}, neededLabels[1:]...), forbiddenLabels[0]),
			author:                "batman",
			firstQueryAuthor:      "batman",
			secondQueryAuthor:     "batman",
			milestone:             "v1.0",
			inPool:                false,
			displayAllTideQueries: true,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs good-to-go label."),
		},
		{
			name:           "against excluded branch",
			baseref:        "bad",
			branchDenyList: []string{"bad"},
			sameBranchReqs: true,
			labels:         neededLabels,
			inPool:         false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Merging to branch bad is forbidden."),
		},
		{
			name:            "not against included branch",
			baseref:         "bad",
			branchAllowList: []string{"good"},
			sameBranchReqs:  true,
			labels:          neededLabels,
			inPool:          false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Merging to branch bad is forbidden."),
		},
		{
			name:              "choose query for correct branch",
			baseref:           "bad",
			branchAllowList:   []string{"good"},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			labels:            neededLabels,
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs 1, 2, 3, 4, 5, 6, 7 labels."),
		},
		{
			name:              "tides own context failed but is ignored",
			labels:            neededLabels,
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			contexts:          []Context{{Context: githubql.String(statusContext), State: githubql.StatusStateError}},
			inPool:            false,

			state: github.StatusSuccess,
			desc:  statusInPool,
		},
		{
			name:              "single bad context",
			labels:            neededLabels,
			contexts:          []Context{{Context: githubql.String("job-name"), State: githubql.StatusStateError}},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Job job-name has not succeeded."),
		},
		{
			name:              "single bad checkrun",
			labels:            neededLabels,
			checkRuns:         []CheckRun{{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)}},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Job job-name has not succeeded."),
		},
		{
			name:              "single good checkrun",
			labels:            neededLabels,
			checkRuns:         []CheckRun{{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)}},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            true,

			state: github.StatusSuccess,
			desc:  statusInPool,
		},
		{
			name:   "multiple good checkruns",
			labels: neededLabels,
			checkRuns: []CheckRun{
				{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
				{Name: githubql.String("another-job"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
			},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            true,

			state: github.StatusSuccess,
			desc:  statusInPool,
		},
		{
			name:   "mix of good and bad checkruns",
			labels: neededLabels,
			checkRuns: []CheckRun{
				{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
				{Name: githubql.String("another-job"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Job another-job has not succeeded."),
		},
		{
			name:   "mix of good status contexts and checkruns",
			labels: neededLabels,
			checkRuns: []CheckRun{
				{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
			},
			contexts: []Context{
				{Context: githubql.String("other-job-name"), State: githubql.StatusStateSuccess},
			},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            true,

			state: github.StatusSuccess,
			desc:  statusInPool,
		},
		{
			name:   "mix of bad status contexts and checkruns",
			labels: neededLabels,
			checkRuns: []CheckRun{
				{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			contexts: []Context{
				{Context: githubql.String("other-job-name"), State: githubql.StatusStateFailure},
			},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Jobs job-name, other-job-name have not succeeded."),
		},
		{
			name:   "good context, bad checkrun",
			labels: neededLabels,
			checkRuns: []CheckRun{
				{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			contexts: []Context{
				{Context: githubql.String("other-job-name"), State: githubql.StatusStateSuccess},
			},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Job job-name has not succeeded."),
		},
		{
			name:   "bad context, good checkrun",
			labels: neededLabels,
			checkRuns: []CheckRun{
				{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
			},
			contexts: []Context{
				{Context: githubql.String("other-job-name"), State: githubql.StatusStateFailure},
			},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Job other-job-name has not succeeded."),
		},
		{
			name:              "multiple bad contexts",
			labels:            neededLabels,
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			contexts: []Context{
				{Context: githubql.String("job-name"), State: githubql.StatusStateError},
				{Context: githubql.String("other-job-name"), State: githubql.StatusStateError},
			},
			inPool: false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Jobs job-name, other-job-name have not succeeded."),
		},
		{
			name:              "multiple bad checkruns",
			labels:            neededLabels,
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			checkRuns: []CheckRun{
				{Name: githubql.String("job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
				{Name: githubql.String("other-job-name"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			inPool: false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Jobs job-name, other-job-name have not succeeded."),
		},
		{
			name:              "wrong author",
			labels:            neededLabels,
			author:            "robin",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			contexts:          []Context{{Context: githubql.String("job-name"), State: githubql.StatusStateSuccess}},
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Must be by author batman."),
		},
		{
			name:              "wrong author; use lowest diff",
			labels:            neededLabels,
			author:            "robin",
			firstQueryAuthor:  "penguin",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			contexts:          []Context{{Context: githubql.String("job-name"), State: githubql.StatusStateSuccess}},
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Must be by author penguin."),
		},
		{
			name:              "wrong milestone",
			labels:            neededLabels,
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.1",
			contexts:          []Context{{Context: githubql.String("job-name"), State: githubql.StatusStateSuccess}},
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Must be in milestone v1.0."),
		},
		{
			name:              "not in pool, but all requirements are met",
			labels:            neededLabels,
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusSuccess,
			desc:  statusInPool,
		},
		{
			name:              "not in pool, but all requirements are met, including a successful third-party context",
			labels:            neededLabels,
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			contexts:          []Context{{Context: githubql.String("job-name"), State: githubql.StatusStateSuccess}},
			inPool:            false,

			state: github.StatusSuccess,
			desc:  statusInPool,
		},
		{
			name:              "check that min diff query is used",
			labels:            []string{"3", "4", "5", "6", "7"},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,

			state: github.StatusPending,
			desc:  fmt.Sprintf(statusNotInPool, " Needs 1, 2 labels."),
		},
		{
			name:              "check that blockers take precedence over other queries",
			labels:            []string{"3", "4", "5", "6", "7"},
			author:            "batman",
			firstQueryAuthor:  "batman",
			secondQueryAuthor: "batman",
			milestone:         "v1.0",
			inPool:            false,
			blocks:            []int{1, 2},

			state: github.StatusError,
			desc:  fmt.Sprintf(statusNotInPool, " Merging is blocked by issues 1, 2."),
		},
		{
			name:             "missing passing up-to-date context",
			inPool:           true,
			baseref:          "baseref",
			requiredContexts: []string{"foo", "bar"},
			prowJobs: []runtime.Object{
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "123"},
					Spec: prowapi.ProwJobSpec{
						Context: "foo",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.SuccessState,
					},
				},
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "1234"},
					Spec: prowapi.ProwJobSpec{
						Context: "bar",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.PendingState,
					},
				},
			},

			state: github.StatusPending,
			desc:  "Not mergeable. Retesting: bar",
		},
		{
			name:             "missing passing up-to-date contexts",
			inPool:           true,
			baseref:          "baseref",
			requiredContexts: []string{"foo", "bar", "baz"},
			prowJobs: []runtime.Object{
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "123"},
					Spec: prowapi.ProwJobSpec{
						Context: "foo",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.SuccessState,
					},
				},
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "1234"},
					Spec: prowapi.ProwJobSpec{
						Context: "bar",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.PendingState,
					},
				},
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "12345"},
					Spec: prowapi.ProwJobSpec{
						Context: "baz",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.PendingState,
					},
				},
			},

			state: github.StatusPending,
			desc:  "Not mergeable. Retesting: bar baz",
		},
		{
			name:             "missing passing up-to-date contexts with different ordering",
			inPool:           true,
			baseref:          "baseref",
			requiredContexts: []string{"foo", "bar", "baz"},
			prowJobs: []runtime.Object{
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "123"},
					Spec: prowapi.ProwJobSpec{
						Context: "foo",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.SuccessState,
					},
				},
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "1234"},
					Spec: prowapi.ProwJobSpec{
						Context: "baz",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.PendingState,
					},
				},
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "12345"},
					Spec: prowapi.ProwJobSpec{
						Context: "bar",
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.PendingState,
					},
				},
			},

			state: github.StatusPending,
			desc:  "Not mergeable. Retesting: bar baz",
		},
		{
			name:    "long list of not up-to-date contexts results in shortened message",
			inPool:  true,
			baseref: "baseref",
			requiredContexts: []string{
				strings.Repeat("very-long-context", 8),
				strings.Repeat("also-long-content", 8),
			},
			prowJobs: []runtime.Object{
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "123"},
					Spec: prowapi.ProwJobSpec{
						Context: strings.Repeat("very-long-context", 8),
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.PendingState,
					},
				},
				&prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "1234"},
					Spec: prowapi.ProwJobSpec{
						Context: strings.Repeat("also-long-content", 8),
						Refs: &prowapi.Refs{
							BaseSHA: "baseref",
							Pulls:   []prowapi.Pull{{SHA: "head"}},
						},
						Type: prowapi.PresubmitJob,
					},
					Status: prowapi.ProwJobStatus{
						State: prowapi.PendingState,
					},
				},
			},

			state: github.StatusPending,
			desc:  "Not mergeable. Retesting 2 jobs.",
		},
		{
			name:           "mergeconflicts",
			inPool:         true,
			mergeConflicts: true,
			state:          github.StatusError,
			desc:           "Not mergeable. PR has a merge conflict.",
		},
		{
			name:                  "Missing approving review",
			additionalTideQueries: []config.TideQuery{{Orgs: []string{""}, ReviewApprovedRequired: true}},
			inPool:                false,

			state: github.StatusPending,
			desc:  "Not mergeable. PullRequest is missing sufficient approving GitHub review(s)",
		},
		{
			name:                  "Required approving review is present",
			additionalTideQueries: []config.TideQuery{{Orgs: []string{""}, ReviewApprovedRequired: true}},
			inPool:                false,
			hasApprovingReview:    true,

			state: github.StatusSuccess,
			desc:  "In merge pool.",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			secondQuery := config.TideQuery{
				Orgs:      []string{""},
				Labels:    []string{"1", "2", "3", "4", "5", "6", "7"}, // lots of requirements
				Author:    tc.secondQueryAuthor,
				Milestone: "v1.0",
			}
			if tc.sameBranchReqs {
				secondQuery.ExcludedBranches = tc.branchDenyList
				secondQuery.IncludedBranches = tc.branchAllowList
			}
			queries := config.TideQueries{
				config.TideQuery{
					Orgs:             []string{""},
					ExcludedBranches: tc.branchDenyList,
					IncludedBranches: tc.branchAllowList,
					Labels:           neededLabelsWithAlt,
					MissingLabels:    forbiddenLabels,
					Author:           tc.firstQueryAuthor,
					Milestone:        "v1.0",
				},
				secondQuery,
			}
			if tc.singleQuery {
				queries = config.TideQueries{queries[0]}
			}
			queries = append(queries, tc.additionalTideQueries...)
			queriesByRepo := queries.QueryMap()
			var pr PullRequest
			pr.BaseRef = struct {
				Name   githubql.String
				Prefix githubql.String
			}{
				Name: githubql.String(tc.baseref),
			}
			for _, label := range tc.labels {
				pr.Labels.Nodes = append(
					pr.Labels.Nodes,
					struct{ Name githubql.String }{Name: githubql.String(label)},
				)
			}
			pr.HeadRefOID = githubql.String("head")
			var checkRunNodes []CheckRunNode
			for _, checkRun := range tc.checkRuns {
				checkRunNodes = append(checkRunNodes, CheckRunNode{CheckRun: checkRun})
			}
			pr.Commits.Nodes = append(
				pr.Commits.Nodes,
				struct{ Commit Commit }{
					Commit: Commit{
						Status: struct{ Contexts []Context }{
							Contexts: tc.contexts,
						},
						OID: githubql.String("head"),
						StatusCheckRollup: StatusCheckRollup{
							Contexts: StatusCheckRollupContext{
								Nodes: checkRunNodes,
							},
						},
					},
				},
			)
			pr.Author = struct {
				Login githubql.String
			}{githubql.String(tc.author)}
			if tc.milestone != "" {
				pr.Milestone = &Milestone{githubql.String(tc.milestone)}
			}
			if tc.mergeConflicts {
				pr.Mergeable = githubql.MergeableStateConflicting
			}
			if tc.hasApprovingReview {
				pr.ReviewDecision = githubql.PullRequestReviewDecisionApproved
			}
			var pool map[string]CodeReviewCommon
			if tc.inPool {
				pool = map[string]CodeReviewCommon{"#0": {}}
			}
			blocks := blockers.Blockers{
				Repo: map[blockers.OrgRepo][]blockers.Blocker{},
			}
			var items []blockers.Blocker
			for _, block := range tc.blocks {
				items = append(items, blockers.Blocker{Number: block})
			}
			blocks.Repo[blockers.OrgRepo{Org: "", Repo: ""}] = items

			ca := &config.Agent{}
			ca.Set(&config.Config{ProwConfig: config.ProwConfig{Tide: config.Tide{
				TideGitHubConfig: config.TideGitHubConfig{
					DisplayAllQueriesInStatus: tc.displayAllTideQueries,
					MergeLabel:                mergeLabel,
					SquashLabel:               squashLabel,
				}}}})
			mmc := newMergeChecker(ca.Config, &fgc{})

			ctx := context.Background()
			mgr := newFakeManager(t, ctx, tc.prowJobs...)

			sc, err := newStatusController(
				ctx,
				logrus.NewEntry(logrus.StandardLogger()),
				nil,
				mgr,
				nil,
				ca.Config,
				nil,
				"",
				mmc,
				false,
				&statusUpdate{
					dontUpdateStatus: &threadSafePRSet{},
					newPoolPending:   make(chan bool),
				},
			)
			if err != nil {
				t.Fatalf("failed to get statusController: %v", err)
			}
			ccg := func() (contextChecker, error) {
				return &config.TideContextPolicy{RequiredContexts: tc.requiredContexts}, nil
			}
			state, desc, err := sc.expectedStatus(sc.logger, queriesByRepo, CodeReviewCommonFromPullRequest(&pr), pool, ccg, blocks, tc.baseref)
			if err != nil {
				t.Fatalf("error calling expectedStatus(): %v", err)
			}
			if state != tc.state {
				t.Errorf("Expected status state %q, but got %q.", string(tc.state), string(state))
			}
			if desc != tc.desc {
				t.Errorf("Expected status description %q, but got %q.", tc.desc, desc)
			}
		})
	}
}

func TestRequirementDiff(t *testing.T) {
	testCases := []struct {
		name                   string
		prLabels               []string
		prAuthor               string
		prMilestone            *Milestone
		prBaseBranch           string
		prContexts             []Context
		prCheckRuns            []CheckRun
		reviewDecision         githubql.PullRequestReviewDecision
		mergeStateStatus       string
		queryLabels            []string
		queryForbiddenLabels   []string
		queryAuthor            string
		queryMilestone         string
		queryExcludedBranches  []string
		queryIncludedBranches  []string
		reviewApprovedRequired bool
		expectedDiff           int
		expectedDescContains   string
	}{
		// MergeStateStatus tests
		{
			name:                   "Blocked merge state with changes requested",
			reviewDecision:         githubql.PullRequestReviewDecisionChangesRequested,
			mergeStateStatus:       "BLOCKED",
			reviewApprovedRequired: false,
			expectedDiff:           100,
			expectedDescContains:   "Blocked by GitHub (branch rulesets or protection)",
		},
		{
			name:                   "Blocked merge state with specific review decision, review required",
			reviewDecision:         githubql.PullRequestReviewDecisionChangesRequested,
			mergeStateStatus:       "BLOCKED",
			reviewApprovedRequired: true,
			expectedDiff:           150,
			expectedDescContains:   "PullRequest is missing sufficient approving GitHub review(s)",
		},
		{
			name:                   "Blocked merge state without specific review decision",
			mergeStateStatus:       "BLOCKED",
			reviewApprovedRequired: false,
			expectedDiff:           100,
			expectedDescContains:   "Blocked by GitHub (branch rulesets or protection)",
		},
		{
			name:                   "Clean merge state allows merge",
			mergeStateStatus:       "CLEAN",
			reviewApprovedRequired: false,
			expectedDiff:           0,
			expectedDescContains:   "",
		},
		{
			name:                   "Missing approval when required",
			reviewDecision:         "",
			reviewApprovedRequired: true,
			expectedDiff:           50,
			expectedDescContains:   "missing sufficient approving",
		},
		{
			name:                   "Approved review passes",
			reviewDecision:         githubql.PullRequestReviewDecisionApproved,
			reviewApprovedRequired: true,
			expectedDiff:           0,
			expectedDescContains:   "",
		},
		{
			name:                   "No review decision without requirement passes",
			reviewDecision:         "",
			reviewApprovedRequired: false,
			expectedDiff:           0,
			expectedDescContains:   "",
		},
		// Branch filtering tests
		{
			name:                  "Branch in excluded list",
			prBaseBranch:          "release-1.0",
			queryExcludedBranches: []string{"release-1.0", "release-2.0"},
			expectedDiff:          2000,
			expectedDescContains:  "Merging to branch release-1.0 is forbidden",
		},
		{
			name:                  "Branch not in excluded list",
			prBaseBranch:          "main",
			queryExcludedBranches: []string{"release-1.0", "release-2.0"},
			expectedDiff:          0,
			expectedDescContains:  "",
		},
		{
			name:                  "Branch not in included list",
			prBaseBranch:          "feature-branch",
			queryIncludedBranches: []string{"main", "develop"},
			expectedDiff:          2000,
			expectedDescContains:  "Merging to branch feature-branch is forbidden",
		},
		{
			name:                  "Branch in included list",
			prBaseBranch:          "main",
			queryIncludedBranches: []string{"main", "develop"},
			expectedDiff:          0,
			expectedDescContains:  "",
		},
		// Author tests
		{
			name:                 "Matching author",
			prAuthor:             "alice",
			queryAuthor:          "alice",
			expectedDiff:         0,
			expectedDescContains: "",
		},
		{
			name:                 "Non-matching author",
			prAuthor:             "bob",
			queryAuthor:          "alice",
			expectedDiff:         1000,
			expectedDescContains: "Must be by author alice",
		},
		{
			name:                 "No author requirement",
			prAuthor:             "anyone",
			queryAuthor:          "",
			expectedDiff:         0,
			expectedDescContains: "",
		},
		// Milestone tests
		{
			name:                 "Matching milestone",
			prMilestone:          &Milestone{Title: githubql.String("v1.0")},
			queryMilestone:       "v1.0",
			expectedDiff:         0,
			expectedDescContains: "",
		},
		{
			name:                 "Non-matching milestone",
			prMilestone:          &Milestone{Title: githubql.String("v1.1")},
			queryMilestone:       "v1.0",
			expectedDiff:         100,
			expectedDescContains: "Must be in milestone v1.0",
		},
		{
			name:                 "PR has no milestone but query requires one",
			prMilestone:          nil,
			queryMilestone:       "v1.0",
			expectedDiff:         100,
			expectedDescContains: "Must be in milestone v1.0",
		},
		{
			name:                 "No milestone requirement",
			prMilestone:          &Milestone{Title: githubql.String("v1.0")},
			queryMilestone:       "",
			expectedDiff:         0,
			expectedDescContains: "",
		},
		// Label tests
		{
			name:                 "All required labels present",
			prLabels:             []string{"lgtm", "approved"},
			queryLabels:          []string{"lgtm", "approved"},
			expectedDiff:         0,
			expectedDescContains: "",
		},
		{
			name:                 "Missing one required label",
			prLabels:             []string{"lgtm"},
			queryLabels:          []string{"lgtm", "approved"},
			expectedDiff:         1,
			expectedDescContains: "Needs approved label",
		},
		{
			name:                 "Missing multiple required labels",
			prLabels:             []string{},
			queryLabels:          []string{"lgtm", "approved"},
			expectedDiff:         2,
			expectedDescContains: "Needs approved, lgtm labels",
		},
		{
			name:                 "Has forbidden label",
			prLabels:             []string{"lgtm", "do-not-merge"},
			queryForbiddenLabels: []string{"do-not-merge"},
			expectedDiff:         1,
			expectedDescContains: "Should not have do-not-merge label",
		},
		{
			name:                 "Has multiple forbidden labels",
			prLabels:             []string{"lgtm", "do-not-merge", "hold"},
			queryForbiddenLabels: []string{"do-not-merge", "hold"},
			expectedDiff:         2,
			expectedDescContains: "Should not have do-not-merge, hold labels",
		},
		{
			name:                 "Alternative labels (one of several)",
			prLabels:             []string{"approved"},
			queryLabels:          []string{"lgtm,approved"},
			expectedDiff:         0,
			expectedDescContains: "",
		},
		{
			name:                 "Alternative labels with none present",
			prLabels:             []string{},
			queryLabels:          []string{"lgtm,approved"},
			expectedDiff:         1,
			expectedDescContains: "Needs lgtm or approved label",
		},
		// Context tests
		{
			name: "All contexts successful",
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateSuccess},
				{Context: githubql.String("ci/build"), State: githubql.StatusStateSuccess},
			},
			expectedDiff:         0,
			expectedDescContains: "",
		},
		{
			name: "One failed context",
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
			},
			expectedDiff:         1,
			expectedDescContains: "Job ci/test has not succeeded",
		},
		{
			name: "Multiple failed contexts",
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
				{Context: githubql.String("ci/build"), State: githubql.StatusStateError},
			},
			expectedDiff:         2,
			expectedDescContains: "Jobs ci/build, ci/test have not succeeded",
		},
		{
			name: "Pending context counts as failed",
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStatePending},
			},
			expectedDiff:         1,
			expectedDescContains: "Job ci/test has not succeeded",
		},
		{
			name: "Successful checkrun",
			prCheckRuns: []CheckRun{
				{Name: githubql.String("test-job"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
			},
			expectedDiff:         0,
			expectedDescContains: "",
		},
		{
			name: "Failed checkrun",
			prCheckRuns: []CheckRun{
				{Name: githubql.String("test-job"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			expectedDiff:         1,
			expectedDescContains: "Job test-job has not succeeded",
		},
		// Priority ordering test: branch takes precedence
		{
			name:                  "Branch mismatch takes precedence over other issues",
			prBaseBranch:          "forbidden-branch",
			queryExcludedBranches: []string{"forbidden-branch"},
			prAuthor:              "wrong-author",
			queryAuthor:           "correct-author",
			prLabels:              []string{},
			queryLabels:           []string{"lgtm"},
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
			},
			expectedDiff:         3002, // 2000 (branch) + 1000 (author) + 1 (label) + 1 (context)
			expectedDescContains: "Merging to branch forbidden-branch is forbidden",
		},
		// Author takes precedence over labels and contexts
		{
			name:        "Author mismatch takes precedence over labels and contexts",
			prAuthor:    "wrong-author",
			queryAuthor: "correct-author",
			prLabels:    []string{},
			queryLabels: []string{"lgtm"},
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
			},
			expectedDiff:         1002, // 1000 (author) + 1 (label) + 1 (context)
			expectedDescContains: "Must be by author correct-author",
		},
		// Milestone takes precedence over labels and contexts
		{
			name:           "Milestone mismatch takes precedence over labels and contexts",
			prMilestone:    &Milestone{Title: githubql.String("v1.1")},
			queryMilestone: "v1.0",
			prLabels:       []string{},
			queryLabels:    []string{"lgtm"},
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
			},
			expectedDiff:         102, // 100 (milestone) + 1 (label) + 1 (context)
			expectedDescContains: "Must be in milestone v1.0",
		},
		// Labels take precedence over contexts
		{
			name:        "Missing labels take precedence over failed contexts",
			prLabels:    []string{},
			queryLabels: []string{"lgtm"},
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
			},
			expectedDiff:         2, // 1 (label) + 1 (context)
			expectedDescContains: "Needs lgtm label",
		},
		// Forbidden labels take precedence over contexts
		{
			name:                 "Forbidden labels take precedence over failed contexts",
			prLabels:             []string{"do-not-merge"},
			queryForbiddenLabels: []string{"do-not-merge"},
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
			},
			expectedDiff:         2, // 1 (forbidden label) + 1 (context)
			expectedDescContains: "Should not have do-not-merge label",
		},
		// All aspects satisfied - perfect PR
		{
			name:                   "Complex: All requirements satisfied across all areas",
			prBaseBranch:           "main",
			queryIncludedBranches:  []string{"main", "develop"},
			prAuthor:               "approved-contributor",
			queryAuthor:            "approved-contributor",
			prMilestone:            &Milestone{Title: githubql.String("v1.0")},
			queryMilestone:         "v1.0",
			prLabels:               []string{"lgtm", "approved", "size/small"},
			queryLabels:            []string{"lgtm", "approved"},
			queryForbiddenLabels:   []string{"do-not-merge", "hold"},
			reviewDecision:         githubql.PullRequestReviewDecisionApproved,
			reviewApprovedRequired: true,
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateSuccess},
				{Context: githubql.String("ci/lint"), State: githubql.StatusStateSuccess},
			},
			prCheckRuns: []CheckRun{
				{Name: githubql.String("security-scan"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
				{Name: githubql.String("e2e-tests"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
			},
			expectedDiff:         0,
			expectedDescContains: "",
		},
		// All aspects with branch issue
		{
			name:                   "Complex: Multiple issues across all areas with branch taking precedence",
			prBaseBranch:           "release-1.0",
			queryExcludedBranches:  []string{"release-1.0"},
			prAuthor:               "unauthorized-user",
			queryAuthor:            "authorized-user",
			prMilestone:            &Milestone{Title: githubql.String("v2.0")},
			queryMilestone:         "v1.0",
			prLabels:               []string{"do-not-merge"},
			queryLabels:            []string{"lgtm", "approved"},
			queryForbiddenLabels:   []string{"do-not-merge"},
			reviewDecision:         githubql.PullRequestReviewDecisionChangesRequested,
			reviewApprovedRequired: true,
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateFailure},
				{Context: githubql.String("ci/lint"), State: githubql.StatusStateError},
			},
			prCheckRuns: []CheckRun{
				{Name: githubql.String("security-scan"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			mergeStateStatus: "BLOCKED",
			// 2000 (branch) + 1000 (author) + 100 (milestone) + 2 (missing labels) + 1 (forbidden label) + 100 (blocked) + 50 (review) + 3 (contexts: 2 status + 1 checkrun)
			expectedDiff:         3256,
			expectedDescContains: "Merging to branch release-1.0 is forbidden",
		},
		// All aspects with author issue
		{
			name:                   "Complex: Multiple issues across all areas with author taking precedence",
			prBaseBranch:           "main",
			queryIncludedBranches:  []string{"main", "develop"},
			prAuthor:               "external-contributor",
			queryAuthor:            "core-team",
			prMilestone:            nil,
			queryMilestone:         "v1.5",
			prLabels:               []string{"help-wanted", "needs-rebase", "wip"},
			queryLabels:            []string{"lgtm", "approved", "size/small"},
			queryForbiddenLabels:   []string{"wip", "needs-rebase"},
			reviewDecision:         githubql.PullRequestReviewDecisionApproved,
			reviewApprovedRequired: true,
			prContexts: []Context{
				{Context: githubql.String("ci/build"), State: githubql.StatusStatePending},
				{Context: githubql.String("ci/unit-tests"), State: githubql.StatusStateSuccess},
			},
			prCheckRuns: []CheckRun{
				{Name: githubql.String("integration-tests"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
				{Name: githubql.String("e2e-tests"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			mergeStateStatus: "CLEAN",
			// 0 (branch OK) + 1000 (author) + 100 (milestone) + 3 (missing labels) + 2 (forbidden labels) + 0 (not blocked) + 2 (failed contexts: ci/build pending + e2e-tests failed)
			expectedDiff:         1107,
			expectedDescContains: "Must be by author core-team",
		},
		// All aspects with milestone issue
		{
			name:                   "Complex: Branch and author OK, other issues present with milestone precedence",
			prBaseBranch:           "develop",
			queryIncludedBranches:  []string{"main", "develop"},
			prAuthor:               "approved-dev",
			queryAuthor:            "approved-dev",
			prMilestone:            &Milestone{Title: githubql.String("backlog")},
			queryMilestone:         "sprint-23",
			prLabels:               []string{"lgtm", "hold"},
			queryLabels:            []string{"lgtm", "approved"},
			queryForbiddenLabels:   []string{"hold", "blocked"},
			reviewDecision:         githubql.PullRequestReviewDecisionChangesRequested,
			reviewApprovedRequired: false,
			prContexts: []Context{
				{Context: githubql.String("ci/verify"), State: githubql.StatusStateSuccess},
			},
			prCheckRuns: []CheckRun{
				{Name: githubql.String("code-coverage"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateFailure)},
			},
			mergeStateStatus: "BLOCKED",
			// 0 (branch OK) + 0 (author OK) + 100 (milestone) + 1 (missing approved label) + 1 (forbidden hold label) + 100 (blocked) + 1 (failed checkrun)
			expectedDiff:         203,
			expectedDescContains: "Must be in milestone sprint-23",
		},
		//  All aspects with changes requested issue
		{
			name:                   "Complex: All requirements met but changes requested blocks merge",
			prBaseBranch:           "main",
			queryIncludedBranches:  []string{"main", "develop"},
			prAuthor:               "core-maintainer",
			queryAuthor:            "core-maintainer",
			prMilestone:            &Milestone{Title: githubql.String("v2.0")},
			queryMilestone:         "v2.0",
			prLabels:               []string{"lgtm", "approved", "ready-to-merge"},
			queryLabels:            []string{"lgtm", "approved"},
			queryForbiddenLabels:   []string{"do-not-merge", "hold"},
			reviewDecision:         githubql.PullRequestReviewDecisionChangesRequested,
			reviewApprovedRequired: false,
			prContexts: []Context{
				{Context: githubql.String("ci/test"), State: githubql.StatusStateSuccess},
				{Context: githubql.String("ci/lint"), State: githubql.StatusStateSuccess},
				{Context: githubql.String("ci/build"), State: githubql.StatusStateSuccess},
			},
			prCheckRuns: []CheckRun{
				{Name: githubql.String("security-scan"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
				{Name: githubql.String("e2e-tests"), Status: githubql.String(githubql.CheckStatusStateCompleted), Conclusion: githubql.String(githubql.StatusStateSuccess)},
			},
			mergeStateStatus: "BLOCKED",
			// 0 (branch OK) + 0 (author OK) + 0 (milestone OK) + 0 (labels OK) + 100 (blocked) + 50(review) + 0 (contexts OK)
			expectedDiff:         100,
			expectedDescContains: "Blocked by GitHub (branch rulesets or protection)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pr := &PullRequest{
				ReviewDecision:   tc.reviewDecision,
				MergeStateStatus: githubql.String(tc.mergeStateStatus),
				HeadRefOID:       githubql.String("abc123"),
			}

			if tc.prBaseBranch != "" {
				pr.BaseRef = struct {
					Name   githubql.String
					Prefix githubql.String
				}{
					Name: githubql.String(tc.prBaseBranch),
				}
			}

			if tc.prAuthor != "" {
				pr.Author = struct {
					Login githubql.String
				}{
					Login: githubql.String(tc.prAuthor),
				}
			}

			pr.Milestone = tc.prMilestone

			for _, label := range tc.prLabels {
				pr.Labels.Nodes = append(pr.Labels.Nodes, struct{ Name githubql.String }{Name: githubql.String(label)})
			}

			if len(tc.prContexts) > 0 || len(tc.prCheckRuns) > 0 {
				var checkRunNodes []CheckRunNode
				for _, cr := range tc.prCheckRuns {
					checkRunNodes = append(checkRunNodes, CheckRunNode{CheckRun: cr})
				}
				pr.Commits.Nodes = append(pr.Commits.Nodes, struct{ Commit Commit }{
					Commit: Commit{
						OID: githubql.String("abc123"),
						Status: struct{ Contexts []Context }{
							Contexts: tc.prContexts,
						},
						StatusCheckRollup: StatusCheckRollup{
							Contexts: StatusCheckRollupContext{
								Nodes: checkRunNodes,
							},
						},
					},
				})
			}

			query := &config.TideQuery{
				Labels:                 tc.queryLabels,
				MissingLabels:          tc.queryForbiddenLabels,
				Author:                 tc.queryAuthor,
				Milestone:              tc.queryMilestone,
				ExcludedBranches:       tc.queryExcludedBranches,
				IncludedBranches:       tc.queryIncludedBranches,
				ReviewApprovedRequired: tc.reviewApprovedRequired,
			}

			cc := &config.TideContextPolicy{}

			desc, diff := requirementDiff(pr, query, cc)

			if diff != tc.expectedDiff {
				t.Errorf("Expected diff %d, but got %d", tc.expectedDiff, diff)
			}

			if tc.expectedDescContains != "" {
				if !strings.Contains(desc, tc.expectedDescContains) {
					t.Errorf("Expected description to contain %q, but got %q", tc.expectedDescContains, desc)
				}
			} else if desc != "" {
				t.Errorf("Expected empty description, but got %q", desc)
			}
		})
	}
}

func TestSetStatuses(t *testing.T) {
	statusNotInPoolEmpty := fmt.Sprintf(statusNotInPool, "")
	testcases := []struct {
		name string

		inPool          bool
		hasContext      bool
		inDontSetStatus bool
		state           githubql.StatusState
		desc            string

		shouldSet bool
	}{
		{
			name: "in pool with proper context",

			inPool:     true,
			hasContext: true,
			state:      githubql.StatusStateSuccess,
			desc:       statusInPool,

			shouldSet: false,
		},
		{
			name: "in pool without context",

			inPool:     true,
			hasContext: false,

			shouldSet: true,
		},
		{
			name: "in pool with improper context",

			inPool:     true,
			hasContext: true,
			state:      githubql.StatusStateSuccess,
			desc:       statusNotInPoolEmpty,

			shouldSet: true,
		},
		{
			name: "in pool with wrong state",

			inPool:     true,
			hasContext: true,
			state:      githubql.StatusStatePending,
			desc:       statusInPool,

			shouldSet: true,
		},
		{
			name: "in pool with wrong state but set to not update status",

			inPool:          true,
			hasContext:      true,
			inDontSetStatus: true,
			state:           githubql.StatusStatePending,
			desc:            statusInPool,

			shouldSet: false,
		},
		{
			name: "not in pool with proper context",

			inPool:     false,
			hasContext: true,
			state:      githubql.StatusStatePending,
			desc:       statusNotInPoolEmpty,

			shouldSet: false,
		},
		{
			name: "not in pool with improper context",

			inPool:     false,
			hasContext: true,
			state:      githubql.StatusStatePending,
			desc:       statusInPool,

			shouldSet: true,
		},
		{
			name: "not in pool with no context",

			inPool:     false,
			hasContext: false,

			shouldSet: true,
		},
	}
	for _, tc := range testcases {
		var pr PullRequest
		pr.Commits.Nodes = []struct{ Commit Commit }{{}}
		if tc.hasContext {
			pr.Commits.Nodes[0].Commit.Status.Contexts = []Context{
				{
					Context:     githubql.String(statusContext),
					State:       tc.state,
					Description: githubql.String(tc.desc),
				},
			}
		}
		crc := CodeReviewCommonFromPullRequest(&pr)
		pool := make(map[string]CodeReviewCommon)
		if tc.inPool {
			pool[prKey(crc)] = *crc
		}
		fc := &fgc{
			refs: map[string]string{"/ heads/": "SHA"},
		}
		ca := &config.Agent{}
		ca.Set(&config.Config{})
		// setStatuses logs instead of returning errors.
		// Construct a logger to watch for errors to be printed.
		log := logrus.WithField("component", "tide")
		initialLog, err := log.String()
		if err != nil {
			t.Fatalf("Failed to get log output before testing: %v", err)
		}

		ctx := context.Background()
		mgr := newFakeManager(t, ctx)
		mmc := newMergeChecker(ca.Config, fc)
		sc, err := newStatusController(
			ctx,
			log,
			fc,
			mgr,
			nil,
			ca.Config,
			nil,
			"",
			mmc,
			false,
			&statusUpdate{
				dontUpdateStatus: &threadSafePRSet{},
				newPoolPending:   make(chan bool),
			},
		)
		if err != nil {
			t.Fatalf("failed to get statusController: %v", err)
		}
		if tc.inDontSetStatus {
			sc.dontUpdateStatus = &threadSafePRSet{data: map[pullRequestIdentifier]struct{}{{}: {}}}
		}
		sc.setStatuses([]CodeReviewCommon{*crc}, pool, blockers.Blockers{}, nil, nil)
		if str, err := log.String(); err != nil {
			t.Fatalf("For case %s: failed to get log output: %v", tc.name, err)
		} else if str != initialLog {
			t.Errorf("For case %s: error setting status: %s", tc.name, str)
		}
		if tc.shouldSet && !fc.setStatus {
			t.Errorf("For case %s: should set but didn't", tc.name)
		} else if !tc.shouldSet && fc.setStatus {
			t.Errorf("For case %s: should not set but did", tc.name)
		}
	}
}

func TestTargetUrl(t *testing.T) {
	testcases := []struct {
		name   string
		pr     *PullRequest
		config config.Tide

		expectedURL string
	}{
		{
			name:        "no config",
			pr:          &PullRequest{},
			config:      config.Tide{},
			expectedURL: "",
		},
		{
			name:        "tide overview config",
			pr:          &PullRequest{},
			config:      config.Tide{TideGitHubConfig: config.TideGitHubConfig{TargetURLs: map[string]string{"*": "tide.com"}}},
			expectedURL: "tide.com",
		},
		{
			name:        "PR dashboard config and overview config",
			pr:          &PullRequest{},
			config:      config.Tide{TideGitHubConfig: config.TideGitHubConfig{TargetURLs: map[string]string{"*": "tide.com"}, PRStatusBaseURLs: map[string]string{"*": "pr.status.com"}}},
			expectedURL: "tide.com",
		},
		{
			name: "PR dashboard config",
			pr: &PullRequest{
				Author: struct {
					Login githubql.String
				}{Login: githubql.String("author")},
				Repository: struct {
					Name          githubql.String
					NameWithOwner githubql.String
					Owner         struct {
						Login githubql.String
					}
				}{NameWithOwner: githubql.String("org/repo")},
				HeadRefName: "head",
			},
			config:      config.Tide{TideGitHubConfig: config.TideGitHubConfig{PRStatusBaseURLs: map[string]string{"*": "pr.status.com"}}},
			expectedURL: "pr.status.com?query=is%3Apr+repo%3Aorg%2Frepo+author%3Aauthor+head%3Ahead",
		},
		{
			name: "generate link by default config",
			pr: &PullRequest{
				Author: struct {
					Login githubql.String
				}{Login: githubql.String("author")},
				Repository: struct {
					Name          githubql.String
					NameWithOwner githubql.String
					Owner         struct {
						Login githubql.String
					}
				}{
					Owner:         struct{ Login githubql.String }{Login: githubql.String("testOrg")},
					Name:          githubql.String("testRepo"),
					NameWithOwner: githubql.String("testOrg/testRepo"),
				},
				HeadRefName: "head",
			},
			config:      config.Tide{TideGitHubConfig: config.TideGitHubConfig{PRStatusBaseURLs: map[string]string{"*": "default.pr.status.com"}}},
			expectedURL: "default.pr.status.com?query=is%3Apr+repo%3AtestOrg%2FtestRepo+author%3Aauthor+head%3Ahead",
		},
		{
			name: "generate link by org config",
			pr: &PullRequest{
				Author: struct {
					Login githubql.String
				}{Login: githubql.String("author")},
				Repository: struct {
					Name          githubql.String
					NameWithOwner githubql.String
					Owner         struct {
						Login githubql.String
					}
				}{
					Owner:         struct{ Login githubql.String }{Login: githubql.String("testOrg")},
					Name:          githubql.String("testRepo"),
					NameWithOwner: githubql.String("testOrg/testRepo"),
				},
				HeadRefName: "head",
			},
			config: config.Tide{TideGitHubConfig: config.TideGitHubConfig{PRStatusBaseURLs: map[string]string{
				"*":       "default.pr.status.com",
				"testOrg": "byorg.pr.status.com"},
			}},
			expectedURL: "byorg.pr.status.com?query=is%3Apr+repo%3AtestOrg%2FtestRepo+author%3Aauthor+head%3Ahead",
		},
		{
			name: "generate link by repo config",
			pr: &PullRequest{
				Author: struct {
					Login githubql.String
				}{Login: githubql.String("author")},
				Repository: struct {
					Name          githubql.String
					NameWithOwner githubql.String
					Owner         struct {
						Login githubql.String
					}
				}{
					Owner:         struct{ Login githubql.String }{Login: githubql.String("testOrg")},
					Name:          githubql.String("testRepo"),
					NameWithOwner: githubql.String("testOrg/testRepo"),
				},
				HeadRefName: "head",
			},
			config: config.Tide{TideGitHubConfig: config.TideGitHubConfig{PRStatusBaseURLs: map[string]string{
				"*":                "default.pr.status.com",
				"testOrg":          "byorg.pr.status.com",
				"testOrg/testRepo": "byrepo.pr.status.com"},
			}},
			expectedURL: "byrepo.pr.status.com?query=is%3Apr+repo%3AtestOrg%2FtestRepo+author%3Aauthor+head%3Ahead",
		},
	}

	for _, tc := range testcases {
		log := logrus.WithField("controller", "status-update")
		c := &config.Config{ProwConfig: config.ProwConfig{Tide: tc.config}}
		if actual, expected := targetURL(c, CodeReviewCommonFromPullRequest(tc.pr), log), tc.expectedURL; actual != expected {
			t.Errorf("%s: expected target URL %s but got %s", tc.name, expected, actual)
		}
	}
}

func TestOpenPRsQuery(t *testing.T) {
	orgs := []string{"org", "kuber"}
	repos := []string{"k8s/k8s", "k8s/t-i"}
	exceptions := map[string]sets.Set[string]{
		"org":            sets.New[string]("org/repo1", "org/repo2"),
		"irrelevant-org": sets.New[string]("irrelevant-org/repo1", "irrelevant-org/repo2"),
	}

	queriesByOrg := openPRsQueries(orgs, repos, exceptions)
	expectedQueriesByOrg := map[string]string{
		"org":   `-repo:"org/repo1" -repo:"org/repo2" archived:false is:pr org:"org" sort:updated-asc state:open`,
		"kuber": `archived:false is:pr org:"kuber" sort:updated-asc state:open`,
		"k8s":   ` archived:false is:pr repo:"k8s/k8s" repo:"k8s/t-i" sort:updated-asc state:open`,
	}
	for org, query := range queriesByOrg {
		// This is produced from a map so the result is not deterministic. Work around by using
		// the fact that the parameters are space split and do a space split, sort, space join.
		split := strings.Split(query, " ")
		sort.Strings(split)
		queriesByOrg[org] = strings.Join(split, " ")
	}

	if diff := cmp.Diff(queriesByOrg, expectedQueriesByOrg); diff != "" {
		t.Errorf("actual queries differ from expected: %s", diff)
	}
}

func TestIndexFuncPassingJobs(t *testing.T) {
	testCases := []struct {
		name     string
		pj       *prowapi.ProwJob
		expected []string
	}{
		{
			name: "Jobs that are not presubmit or batch are ignored",
			pj:   getProwJob(prowapi.PeriodicJob, "org", "", "repo", "baseSHA", prowapi.SuccessState, []prowapi.Pull{{SHA: "head"}}),
		},
		{
			name: "Non-Passing jobs are ignored",
			pj:   getProwJob(prowapi.PresubmitJob, "org", "repo", "", "baseSHA", prowapi.FailureState, []prowapi.Pull{{SHA: "head"}}),
		},
		{
			name:     "Indexkey is returned for presubmit job",
			pj:       getProwJob(prowapi.PresubmitJob, "org", "repo", "", "baseSHA", prowapi.SuccessState, []prowapi.Pull{{SHA: "head"}}),
			expected: []string{"org/repo@baseSHA+head"},
		},
		{
			name:     "Indexkeys are returned for batch job",
			pj:       getProwJob(prowapi.BatchJob, "org", "repo", "", "baseSHA", prowapi.SuccessState, []prowapi.Pull{{SHA: "head"}, {SHA: "head-2"}}),
			expected: []string{"org/repo@baseSHA+head", "org/repo@baseSHA+head-2"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var results []string
			results = append(results, indexFuncPassingJobs(tc.pj)...)
			if diff := deep.Equal(tc.expected, results); diff != nil {
				t.Errorf("expected does not match result, diff: %v", diff)
			}
		})
	}
}

func TestSetStatusRespectsRequiredContexts(t *testing.T) {
	var pr PullRequest
	pr.Commits.Nodes = []struct{ Commit Commit }{{}}
	pr.Repository.NameWithOwner = githubql.String("org/repo")
	pr.Number = githubql.Int(2)
	requiredContexts := map[string][]string{"org/repo#2": {"foo", "bar"}}

	fghc := &fgc{
		refs: map[string]string{"/ heads/": "SHA"},
	}
	log := logrus.WithField("component", "tide")
	initialLog, err := log.String()
	if err != nil {
		t.Fatalf("Failed to get log output before testing: %v", err)
	}

	ca := &config.Agent{}
	ca.Set(&config.Config{})

	ctx := context.Background()
	mgr := newFakeManager(t, ctx)

	sc := &statusController{
		logger:   log,
		ghc:      fghc,
		config:   ca.Config,
		pjClient: mgr.GetClient(),
		ghProvider: &GitHubProvider{
			ghc:          fghc,
			mergeChecker: newMergeChecker(ca.Config, fghc),
		},
		statusUpdate: &statusUpdate{
			dontUpdateStatus: &threadSafePRSet{},
			newPoolPending:   make(chan bool),
		},
	}
	crc := CodeReviewCommonFromPullRequest(&pr)
	pool := map[string]CodeReviewCommon{prKey(crc): *crc}
	sc.setStatuses([]CodeReviewCommon{*crc}, pool, blockers.Blockers{}, nil, requiredContexts)
	if str, err := log.String(); err != nil {
		t.Fatalf("Failed to get log output: %v", err)
	} else if str != initialLog {
		t.Errorf("Error setting status: %s", str)
	}

	if n := len(fghc.statuses); n != 1 {
		t.Fatalf("expected exactly one status to be set, got %d", n)
	}

	expectedDescription := "Not mergeable. Retesting: bar foo"
	val, exists := fghc.statuses["///tide"]
	if !exists {
		t.Fatal("Status didn't get set")
	}
	if val.Description != expectedDescription {
		t.Errorf("Expected description to be %q, was %q", expectedDescription, val.Description)
	}
}

func TestNewBaseSHAGetter(t *testing.T) {
	org, repo, branch := "org", "repo", "branch"
	testCases := []struct {
		name     string
		baseSHAs map[string]string
		ghc      githubClient

		expectedSHA string
		expectErr   bool
	}{
		{
			name:        "Default to content of baseSHAs map",
			baseSHAs:    map[string]string{"org/repo:branch": "123"},
			expectedSHA: "123",
		},
		{
			name:        "BaseSHAs map has no entry, ask GitHub",
			baseSHAs:    map[string]string{},
			ghc:         &fgc{refs: map[string]string{"org/repo heads/branch": "SHA"}},
			expectedSHA: "SHA",
		},
		{
			name:      "Error is returned",
			baseSHAs:  map[string]string{},
			ghc:       &fgc{err: errors.New("some-failure")},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := newBaseSHAGetter(tc.baseSHAs, tc.ghc, org, repo, branch)()
			if err != nil && !tc.expectErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.expectErr {
				return
			}
			if result != tc.expectedSHA {
				t.Errorf("expected %q, got %q", tc.expectedSHA, result)
			}
			if val := tc.baseSHAs[org+"/"+repo+":"+branch]; val != tc.expectedSHA {
				t.Errorf("baseSHA in the map (%q) does not match expected(%q)", val, tc.expectedSHA)
			}
		})
	}
}

func TestStatusControllerSearch(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		prs          map[string][]PullRequest
		usesAppsAuth bool

		expected []CodeReviewCommon
	}{
		{
			name: "Apps auth: Query gets split by org",
			prs: map[string][]PullRequest{
				"org-a": {{Number: githubql.Int(1)}},
				"org-b": {{Number: githubql.Int(2)}},
			},
			usesAppsAuth: true,
			expected: []CodeReviewCommon{
				*CodeReviewCommonFromPullRequest(&PullRequest{Number: 1}),
				*CodeReviewCommonFromPullRequest(&PullRequest{Number: 2}),
			},
		},
		{
			name: "No apps auth: Query remains unsplit",
			prs: map[string][]PullRequest{
				"": {{Number: githubql.Int(1)}, {Number: githubql.Int(2)}},
			},
			usesAppsAuth: false,
			expected: []CodeReviewCommon{
				*CodeReviewCommonFromPullRequest(&PullRequest{Number: 1}),
				*CodeReviewCommonFromPullRequest(&PullRequest{Number: 2}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fgc{prs: tc.prs}
			cfg := func() *config.Config {
				return &config.Config{ProwConfig: config.ProwConfig{Tide: config.Tide{
					TideGitHubConfig: config.TideGitHubConfig{Queries: config.TideQueries{{Orgs: []string{"org-a", "org-b"}}}}}}}
			}
			ctx := context.Background()
			mgr := newFakeManager(t, ctx)
			sc, err := newStatusController(
				ctx,
				logrus.WithField("tc", tc),
				ghc,
				mgr,
				nil,
				cfg,
				nil,
				"",
				nil,
				tc.usesAppsAuth,
				&statusUpdate{
					dontUpdateStatus: &threadSafePRSet{},
					newPoolPending:   make(chan bool),
				},
			)
			if err != nil {
				t.Fatalf("failed to construct status controller: %v", err)
			}

			result := sc.search()
			if diff := cmp.Diff(result, tc.expected, cmpopts.SortSlices(func(a, b CodeReviewCommon) bool { return a.Number < b.Number })); diff != "" {
				t.Errorf("result differs from expected: %s", diff)
			}
		})
	}
}
