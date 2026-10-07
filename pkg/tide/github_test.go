/*
Copyright 2019 The Kubernetes Authors.

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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/git/types"
	"sigs.k8s.io/prow/pkg/github"
)

func TestSearch(t *testing.T) {
	const q = "random search string"
	now := time.Now()
	earlier := now.Add(-5 * time.Hour)
	makePRs := func(numbers ...int) []PullRequest {
		var prs []PullRequest
		for _, n := range numbers {
			prs = append(prs, PullRequest{Number: githubql.Int(n)})
		}
		return prs
	}
	makeQuery := func(more bool, cursor string, numbers ...int) searchQuery {
		var sq searchQuery
		sq.Search.PageInfo.HasNextPage = githubql.Boolean(more)
		sq.Search.PageInfo.EndCursor = githubql.String(cursor)
		for _, pr := range makePRs(numbers...) {
			sq.Search.Nodes = append(sq.Search.Nodes, PRNode{pr})
		}
		return sq
	}

	gatewayErr := github.NewGraphQLServerError
	resourceErr := errors.New("Resource limits for this query exceeded.")

	cases := []struct {
		name    string
		start   time.Time
		end     time.Time
		q       string
		cursors []*githubql.String
		// pageSizes is the expected searchPageSize per call. If nil,
		// maxSearchPageSize is expected for every call.
		pageSizes []int
		sqs       []searchQuery
		errs      []error
		expected  []PullRequest
		err       bool
		wantErr   error
	}{
		{
			name:    "single page works",
			start:   earlier,
			end:     now,
			q:       datedQuery(q, earlier, now),
			cursors: []*githubql.String{nil},
			sqs: []searchQuery{
				makeQuery(false, "", 1, 2),
			},
			errs:     []error{nil},
			expected: makePRs(1, 2),
		},
		{
			name:    "fail on first page",
			start:   earlier,
			end:     now,
			q:       datedQuery(q, earlier, now),
			cursors: []*githubql.String{nil},
			sqs: []searchQuery{
				{},
			},
			errs: []error{errors.New("injected error")},
			err:  true,
		},
		{
			name:    "set minimum start time",
			start:   time.Time{},
			end:     now,
			q:       datedQuery(q, floor(time.Time{}), now),
			cursors: []*githubql.String{nil},
			sqs: []searchQuery{
				makeQuery(false, "", 1, 2),
			},
			errs:     []error{nil},
			expected: makePRs(1, 2),
		},
		{
			name:  "can handle multiple pages of results",
			start: earlier,
			end:   now,
			q:     datedQuery(q, earlier, now),
			cursors: []*githubql.String{
				nil,
				githubql.NewString("first"),
				githubql.NewString("second"),
			},
			sqs: []searchQuery{
				makeQuery(true, "first", 1, 2),
				makeQuery(true, "second", 3, 4),
				makeQuery(false, "", 5, 6),
			},
			errs:     []error{nil, nil, nil},
			expected: makePRs(1, 2, 3, 4, 5, 6),
		},
		{
			name:  "return partial results on later page failure",
			start: earlier,
			end:   now,
			q:     datedQuery(q, earlier, now),
			cursors: []*githubql.String{
				nil,
				githubql.NewString("first"),
			},
			sqs: []searchQuery{
				makeQuery(true, "first", 1, 2),
				{},
			},
			errs:     []error{nil, errors.New("second page error")},
			expected: makePRs(1, 2),
			err:      true,
		},
		{
			name:      "non-timeout error is not retried with a smaller page",
			start:     earlier,
			end:       now,
			q:         datedQuery(q, earlier, now),
			cursors:   []*githubql.String{nil},
			pageSizes: []int{37},
			sqs:       []searchQuery{{}},
			errs:      []error{gatewayErr(http.StatusServiceUnavailable)},
			err:       true,
		},
		{
			name:      "502 on first page is retried with a smaller page",
			start:     earlier,
			end:       now,
			q:         datedQuery(q, earlier, now),
			cursors:   []*githubql.String{nil, nil},
			pageSizes: []int{37, 18},
			sqs: []searchQuery{
				{},
				makeQuery(false, "", 1, 2),
			},
			errs:     []error{gatewayErr(http.StatusBadGateway), nil},
			expected: makePRs(1, 2),
		},
		{
			name:  "504 on a later page retries the same cursor and keeps the smaller page",
			start: earlier,
			end:   now,
			q:     datedQuery(q, earlier, now),
			cursors: []*githubql.String{
				nil,
				githubql.NewString("first"),
				githubql.NewString("first"),
				githubql.NewString("second"),
			},
			pageSizes: []int{37, 37, 18, 18},
			sqs: []searchQuery{
				makeQuery(true, "first", 1, 2),
				{},
				makeQuery(true, "second", 3, 4),
				makeQuery(false, "", 5, 6),
			},
			errs:     []error{nil, gatewayErr(http.StatusGatewayTimeout), nil, nil},
			expected: makePRs(1, 2, 3, 4, 5, 6),
		},
		{
			name:      "resource limit on first page is retried with a smaller page",
			start:     earlier,
			end:       now,
			q:         datedQuery(q, earlier, now),
			cursors:   []*githubql.String{nil, nil},
			pageSizes: []int{37, 18},
			sqs: []searchQuery{
				makeQuery(false, "", 999),
				makeQuery(false, "", 1, 2),
			},
			errs:     []error{resourceErr, nil},
			expected: makePRs(1, 2),
		},
		{
			name:  "resource limit on later page retries the same cursor and keeps the smaller page",
			start: earlier,
			end:   now,
			q:     datedQuery(q, earlier, now),
			cursors: []*githubql.String{
				nil,
				githubql.NewString("first"),
				githubql.NewString("first"),
				githubql.NewString("second"),
			},
			pageSizes: []int{37, 37, 18, 18},
			sqs: []searchQuery{
				makeQuery(true, "first", 1, 2),
				makeQuery(false, "", 999),
				makeQuery(true, "second", 3, 4),
				makeQuery(false, "", 5, 6),
			},
			errs:     []error{nil, resourceErr, nil, nil},
			expected: makePRs(1, 2, 3, 4, 5, 6),
		},
		{
			name:  "persistent resource limit preserves earlier pages and stops at the minimum size",
			start: earlier,
			end:   now,
			q:     datedQuery(q, earlier, now),
			cursors: []*githubql.String{
				nil,
				githubql.NewString("first"),
				githubql.NewString("first"),
				githubql.NewString("first"),
				githubql.NewString("first"),
			},
			pageSizes: []int{37, 37, 18, 9, 5},
			sqs: []searchQuery{
				makeQuery(true, "first", 1, 2),
				{}, {}, {}, {},
			},
			errs:     []error{nil, resourceErr, resourceErr, resourceErr, resourceErr},
			expected: makePRs(1, 2),
			err:      true,
			wantErr:  resourceErr,
		},
		{
			name:      "rate limit is not retried with a smaller page",
			start:     earlier,
			end:       now,
			q:         datedQuery(q, earlier, now),
			cursors:   []*githubql.String{nil},
			pageSizes: []int{37},
			sqs:       []searchQuery{{}},
			errs:      []error{errors.New("API rate limit exceeded")},
			err:       true,
		},
		{
			name:      "gives up after shrinking to the minimum page size",
			start:     earlier,
			end:       now,
			q:         datedQuery(q, earlier, now),
			cursors:   []*githubql.String{nil, nil, nil, nil},
			pageSizes: []int{37, 18, 9, 5},
			sqs:       []searchQuery{{}, {}, {}, {}},
			errs: []error{
				gatewayErr(http.StatusBadGateway),
				gatewayErr(http.StatusGatewayTimeout),
				gatewayErr(http.StatusBadGateway),
				gatewayErr(http.StatusBadGateway),
			},
			err: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &GitHubProvider{}
			var i int
			querier := func(ctx context.Context, result interface{}, actual map[string]interface{}, _ string) error {
				if i >= len(tc.cursors) {
					t.Fatalf("unexpected call %d", i)
				}
				pageSize := maxSearchPageSize
				if tc.pageSizes != nil {
					pageSize = tc.pageSizes[i]
				}
				if want, got := pageSize > minSearchPageSize, github.CallerHandlesGatewayTimeouts(ctx); want != got {
					t.Errorf("call %d (page size %d): expected caller-handled gateway timeouts=%t, got %t", i, pageSize, want, got)
				}
				expected := map[string]interface{}{
					"query":          githubql.String(tc.q),
					"searchCursor":   tc.cursors[i],
					"searchPageSize": githubql.Int(pageSize),
				}
				if !equality.Semantic.DeepEqual(expected, actual) {
					t.Errorf("call %d vars do not match:\n%s", i, diff.Diff(expected, actual))
				}
				ret := result.(*searchQuery)
				err := tc.errs[i]
				sq := tc.sqs[i]
				i++
				*ret = sq
				return err
			}
			prs, err := client.search(querier, logrus.WithField("test", tc.name), q, tc.start, tc.end, "")
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive expected error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("expected error wrapping %v, got %v", tc.wantErr, err)
			}

			if !reflect.DeepEqual(tc.expected, prs) {
				t.Errorf("prs do not match:\n%s", diff.Diff(tc.expected, prs))
			}
			if i != len(tc.cursors) {
				t.Errorf("expected %d queries, got %d", len(tc.cursors), i)
			}
		})
	}
}

func TestPrepareMergeDetails(t *testing.T) {
	pr := PullRequest{
		Number:     githubql.Int(1),
		Mergeable:  githubql.MergeableStateMergeable,
		HeadRefOID: githubql.String("SHA"),
		Title:      "my commit title",
		Body:       "my commit body",
	}

	testCases := []struct {
		name        string
		tpl         config.TideMergeCommitTemplate
		pr          PullRequest
		mergeMethod types.PullRequestMergeType
		expected    github.MergeDetails
	}{{
		name:        "No commit template",
		tpl:         config.TideMergeCommitTemplate{},
		pr:          pr,
		mergeMethod: "merge",
		expected: github.MergeDetails{
			SHA:         "SHA",
			MergeMethod: "merge",
		},
	}, {
		name: "No commit template fields",
		tpl: config.TideMergeCommitTemplate{
			Title: nil,
			Body:  nil,
		},
		pr:          pr,
		mergeMethod: "merge",
		expected: github.MergeDetails{
			SHA:         "SHA",
			MergeMethod: "merge",
		},
	}, {
		name: "Static commit template",
		tpl: config.TideMergeCommitTemplate{
			Title: getTemplate("CommitTitle", "static title"),
			Body:  getTemplate("CommitBody", "static body"),
		},
		pr:          pr,
		mergeMethod: "merge",
		expected: github.MergeDetails{
			SHA:           "SHA",
			MergeMethod:   "merge",
			CommitTitle:   "static title",
			CommitMessage: "static body",
		},
	}, {
		name: "Commit template uses PullRequest fields",
		tpl: config.TideMergeCommitTemplate{
			Title: getTemplate("CommitTitle", "{{ .Number }}: {{ .Title }}"),
			Body:  getTemplate("CommitBody", "{{ .HeadRefOID }} - {{ .Body }}"),
		},
		pr:          pr,
		mergeMethod: "merge",
		expected: github.MergeDetails{
			SHA:           "SHA",
			MergeMethod:   "merge",
			CommitTitle:   "1: my commit title",
			CommitMessage: "SHA - my commit body",
		},
	}, {
		name: "Commit template uses nonexistent fields",
		tpl: config.TideMergeCommitTemplate{
			Title: getTemplate("CommitTitle", "{{ .Hello }}"),
			Body:  getTemplate("CommitBody", "{{ .World }}"),
		},
		pr:          pr,
		mergeMethod: "merge",
		expected: github.MergeDetails{
			SHA:         "SHA",
			MergeMethod: "merge",
		},
	}}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfgAgent := &config.Agent{}
			cfgAgent.Set(cfg)
			provider := &GitHubProvider{
				cfg:    cfgAgent.Config,
				ghc:    &fgc{},
				logger: logrus.WithContext(context.Background()),
			}

			actual := provider.prepareMergeDetails(test.tpl, *CodeReviewCommonFromPullRequest(&test.pr), test.mergeMethod)

			if !reflect.DeepEqual(actual, test.expected) {
				t.Errorf("Case %s failed: expected %+v, got %+v", test.name, test.expected, actual)
			}
		})
	}
}

func TestHeadContexts(t *testing.T) {
	type commitContext struct {
		// one context per commit for testing
		context string
		sha     string
	}

	win := "win"
	lose := "lose"
	headSHA := "head"
	testCases := []struct {
		name                string
		commitContexts      []commitContext
		expectAPICall       bool
		expectChecksAPICall bool
	}{
		{
			name: "first commit is head",
			commitContexts: []commitContext{
				{context: win, sha: headSHA},
				{context: lose, sha: "other"},
				{context: lose, sha: "sha"},
			},
		},
		{
			name: "last commit is head",
			commitContexts: []commitContext{
				{context: lose, sha: "shaaa"},
				{context: lose, sha: "other"},
				{context: win, sha: headSHA},
			},
		},
		{
			name: "no commit is head, falling back to v3 api and getting context via status api",
			commitContexts: []commitContext{
				{context: lose, sha: "shaaa"},
				{context: lose, sha: "other"},
				{context: lose, sha: "sha"},
			},
			expectAPICall: true,
		},
		{
			name: "no commit is head, falling back to v3 api and getting context via checks api",
			commitContexts: []commitContext{
				{context: lose, sha: "shaaa"},
				{context: lose, sha: "other"},
				{context: lose, sha: "sha"},
			},
			expectAPICall:       true,
			expectChecksAPICall: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Running test case %q", tc.name)
			fgc := &fgc{}
			if !tc.expectChecksAPICall {
				fgc.combinedStatus = map[string]string{win: string(githubql.StatusStateSuccess)}
			} else {
				fgc.checkRuns = &github.CheckRunList{CheckRuns: []github.CheckRun{
					{Name: win, Status: "completed", Conclusion: "neutral"},
				}}
			}
			if tc.expectAPICall {
				fgc.expectedSHA = headSHA
			}
			provider := &GitHubProvider{
				ghc:    fgc,
				logger: logrus.WithField("component", "tide"),
			}
			pr := &PullRequest{HeadRefOID: githubql.String(headSHA)}
			for _, ctx := range tc.commitContexts {
				commit := Commit{
					Status: struct{ Contexts []Context }{
						Contexts: []Context{
							{
								Context: githubql.String(ctx.context),
							},
						},
					},
					OID: githubql.String(ctx.sha),
				}
				pr.Commits.Nodes = append(pr.Commits.Nodes, struct{ Commit Commit }{commit})
			}

			contexts, err := provider.headContexts(CodeReviewCommonFromPullRequest(pr))
			if err != nil {
				t.Fatalf("Unexpected error from headContexts: %v", err)
			}
			if len(contexts) != 1 || string(contexts[0].Context) != win {
				t.Errorf("Expected exactly 1 %q context, but got: %#v", win, contexts)
			}
		})
	}
}

func TestGetProwJobsForPRs(t *testing.T) {
	t.Parallel()

	batchJobSuccess := prowapi.ProwJob{
		Spec: prowapi.ProwJobSpec{
			Context: "fooContext",
			Type:    prowapi.BatchJob,
			Refs: &prowapi.Refs{
				Pulls: []prowapi.Pull{
					{Number: 1, SHA: "fooRef"},
					{Number: 2, SHA: "fooRef"},
				},
			},
		},
		Status: prowapi.ProwJobStatus{
			State: prowapi.SuccessState,
		},
	}
	batchJobPending := *batchJobSuccess.DeepCopy()
	batchJobPending.Status.State = prowapi.PendingState

	presubmitJobSuccess := prowapi.ProwJob{
		Spec: prowapi.ProwJobSpec{
			Context: "fooContext",
			Type:    prowapi.PresubmitJob,
			Refs: &prowapi.Refs{
				Pulls: []prowapi.Pull{
					{Number: 1, SHA: "fooRef"},
				},
			},
		},
		Status: prowapi.ProwJobStatus{
			State: prowapi.SuccessState,
		},
	}
	presubmitJobPending := *presubmitJobSuccess.DeepCopy()
	presubmitJobPending.Status.State = prowapi.PendingState
	presubmitJobPR2Success := *presubmitJobSuccess.DeepCopy()
	presubmitJobPR2Success.Spec.Refs.Pulls = []prowapi.Pull{
		{Number: 2, SHA: "fooRef"},
	}
	presubmitJobPR2Pending := *presubmitJobPR2Success.DeepCopy()
	presubmitJobPR2Pending.Status.State = prowapi.PendingState

	testCases := []struct {
		name string
		prs  []CodeReviewCommon
		pjs  []prowapi.ProwJob

		expected map[int]prowJobsByContext
	}{
		{
			name:     "no PRs, no prow jobs",
			expected: map[int]prowJobsByContext{},
		},
		{
			name: "PR but no prow jobs",
			prs: []CodeReviewCommon{
				{Number: 1, HeadRefOID: "fooRef"},
			},
			expected: map[int]prowJobsByContext{},
		},
		{
			name: "no matching refs",
			prs: []CodeReviewCommon{
				{Number: 1, HeadRefOID: "barRef"},
			},
			pjs: []prowapi.ProwJob{
				*batchJobSuccess.DeepCopy(),
				*presubmitJobPending.DeepCopy(),
			},
			expected: map[int]prowJobsByContext{},
		},
		{
			name: "One PR with matching ref and one PR left pool",
			prs: []CodeReviewCommon{
				{Number: 1, HeadRefOID: "fooRef"},
			},
			pjs: []prowapi.ProwJob{
				*batchJobSuccess.DeepCopy(),
				*presubmitJobPending.DeepCopy(),
			},
			expected: map[int]prowJobsByContext{
				1: {
					successfulBatchJob:   map[string]prowapi.ProwJob{},
					pendingPresubmitJobs: map[string]bool{"fooContext": true},
				},
			},
		},
		{
			name: "matching refs, presubmit jobs successful",
			prs: []CodeReviewCommon{
				{Number: 1, HeadRefOID: "fooRef"},
				{Number: 2, HeadRefOID: "fooRef"},
			},
			pjs: []prowapi.ProwJob{
				*batchJobSuccess.DeepCopy(),
				*presubmitJobSuccess.DeepCopy(),
				*presubmitJobPR2Success.DeepCopy(),
			},
			expected: map[int]prowJobsByContext{
				1: {
					successfulBatchJob:   map[string]prowapi.ProwJob{"fooContext": *batchJobSuccess.DeepCopy()},
					pendingPresubmitJobs: map[string]bool{},
				},
				2: {
					successfulBatchJob:   map[string]prowapi.ProwJob{"fooContext": *batchJobSuccess.DeepCopy()},
					pendingPresubmitJobs: map[string]bool{},
				},
			},
		},
		{
			name: "matching refs, all jobs pending",
			prs: []CodeReviewCommon{
				{Number: 1, HeadRefOID: "fooRef"},
				{Number: 2, HeadRefOID: "fooRef"},
			},
			pjs: []prowapi.ProwJob{
				*batchJobPending.DeepCopy(),
				*presubmitJobPending.DeepCopy(),
				*presubmitJobPR2Pending.DeepCopy(),
			},
			expected: map[int]prowJobsByContext{
				1: {
					successfulBatchJob:   map[string]prowapi.ProwJob{},
					pendingPresubmitJobs: map[string]bool{"fooContext": true},
				},
				2: {
					successfulBatchJob:   map[string]prowapi.ProwJob{},
					pendingPresubmitJobs: map[string]bool{"fooContext": true},
				},
			},
		},
		{
			name: "multiple PRs with matching refs for successful batch and pending presubmit/successful jobs",
			prs: []CodeReviewCommon{
				{Number: 1, HeadRefOID: "fooRef"},
				{Number: 2, HeadRefOID: "fooRef"},
			},
			pjs: []prowapi.ProwJob{
				*batchJobSuccess.DeepCopy(),
				*presubmitJobPending.DeepCopy(),
				*presubmitJobPR2Success.DeepCopy(),
			},
			expected: map[int]prowJobsByContext{
				1: {
					successfulBatchJob:   map[string]prowapi.ProwJob{"fooContext": *batchJobSuccess.DeepCopy()},
					pendingPresubmitJobs: map[string]bool{"fooContext": true},
				},
				2: {
					successfulBatchJob:   map[string]prowapi.ProwJob{"fooContext": *batchJobSuccess.DeepCopy()},
					pendingPresubmitJobs: map[string]bool{},
				},
			},
		},
		{
			name: "multiple PRs with matching refs for successful batch and pending presubmit jobs",
			prs: []CodeReviewCommon{
				{Number: 1, HeadRefOID: "fooRef"},
				{Number: 2, HeadRefOID: "fooRef"},
			},
			pjs: []prowapi.ProwJob{
				*batchJobSuccess.DeepCopy(),
				*presubmitJobPending.DeepCopy(),
				*presubmitJobPR2Pending.DeepCopy(),
			},
			expected: map[int]prowJobsByContext{
				1: {
					successfulBatchJob:   map[string]prowapi.ProwJob{"fooContext": *batchJobSuccess.DeepCopy()},
					pendingPresubmitJobs: map[string]bool{"fooContext": true},
				},
				2: {
					successfulBatchJob:   map[string]prowapi.ProwJob{"fooContext": *batchJobSuccess.DeepCopy()},
					pendingPresubmitJobs: map[string]bool{"fooContext": true},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := getProwJobsForPRs(tc.prs, tc.pjs)
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("expected %+v, got %+v", tc.expected, actual)
			}
		})
	}
}

func TestOverwriteProwJobContexts(t *testing.T) {
	t.Parallel()

	batchJobFooRefSuccess := prowapi.ProwJob{
		Spec: prowapi.ProwJobSpec{
			Context: "fooContext",
			Type:    prowapi.BatchJob,
			Refs: &prowapi.Refs{
				BaseSHA: "fooBaseRef",
				Pulls: []prowapi.Pull{
					{Number: 1, SHA: "fooRef"},
					{Number: 2, SHA: "fooRef"},
				},
			},
		},
		Status: prowapi.ProwJobStatus{
			Description: "Job succeeded.",
			State:       prowapi.SuccessState,
			URL:         "https://prow.foo.bar/foobar",
		},
	}
	batchJobBarRefSuccess := *batchJobFooRefSuccess.DeepCopy()
	batchJobBarRefSuccess.Spec.Context = "barContext"

	testCases := []struct {
		name string
		pr   CodeReviewCommon
		pjs  prowJobsByContext

		expectedOverwrittenContexts []string
	}{
		{
			name: "no pending presubmit job",
			pr:   CodeReviewCommon{Org: "foo", Repo: "bar", Number: 1, HeadRefOID: "fooRef"},
			pjs: prowJobsByContext{
				successfulBatchJob: map[string]prowapi.ProwJob{"fooContext": *batchJobFooRefSuccess.DeepCopy()},
			},
			expectedOverwrittenContexts: []string{},
		},
		{
			name: "pending presubmit job with matching context",
			pr:   CodeReviewCommon{Org: "foo", Repo: "bar", Number: 1, HeadRefOID: "fooRef"},
			pjs: prowJobsByContext{
				successfulBatchJob:   map[string]prowapi.ProwJob{"fooContext": *batchJobFooRefSuccess.DeepCopy()},
				pendingPresubmitJobs: map[string]bool{"fooContext": true},
			},
			expectedOverwrittenContexts: []string{"fooContext"},
		},
		{
			name: "pending presubmit job without matching context",
			pr:   CodeReviewCommon{Org: "foo", Repo: "bar", Number: 1, HeadRefOID: "fooRef"},
			pjs: prowJobsByContext{
				successfulBatchJob:   map[string]prowapi.ProwJob{"barContext": *batchJobBarRefSuccess.DeepCopy()},
				pendingPresubmitJobs: map[string]bool{"fooContext": true},
			},
			expectedOverwrittenContexts: []string{},
		},
		{
			name: "pending presubmit job with multiple matching contexts",
			pr:   CodeReviewCommon{Org: "foo", Repo: "bar", Number: 1, HeadRefOID: "fooRef"},
			pjs: prowJobsByContext{
				successfulBatchJob: map[string]prowapi.ProwJob{
					"fooContext": *batchJobFooRefSuccess.DeepCopy(),
					"barContext": *batchJobBarRefSuccess.DeepCopy(),
				},
				pendingPresubmitJobs: map[string]bool{"fooContext": true, "barContext": true},
			},
			expectedOverwrittenContexts: []string{"fooContext", "barContext"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fgc{}
			client := &GitHubProvider{
				ghc: ghc,
			}
			err := client.overwriteProwJobContextsWithStatusSuccess(tc.pr, tc.pjs, logrus.WithField("test", tc.name))
			if err != nil {
				t.Fatalf("failed to set status: %v", err)
			}
			switch len(tc.expectedOverwrittenContexts) {
			case 0:
				if ghc.setStatus {
					t.Errorf("expected CreateStatusApiCall: false, got CreateStatusApiCall: %t", ghc.setStatus)
				}
			default:
				if !ghc.setStatus {
					t.Errorf("expected CreateStatusApiCall: true, got CreateStatusApiCall: %t", ghc.setStatus)
				}
			}
			for _, expectedContext := range tc.expectedOverwrittenContexts {
				batchJob := tc.pjs.successfulBatchJob[expectedContext]
				githubStatus := ghc.statuses[tc.pr.Org+"/"+tc.pr.Repo+"/"+tc.pr.HeadRefOID+"/"+expectedContext]
				expectedStatus := github.Status{
					State:       github.StatusSuccess,
					Description: config.ContextDescriptionWithBaseSha(batchJob.Status.Description, batchJob.Spec.Refs.BaseSHA),
					Context:     expectedContext,
					TargetURL:   batchJob.Status.URL,
				}
				if githubStatus != expectedStatus {
					t.Errorf("expected GitHub Status: %+v, got GitHub Status: %+v", expectedStatus, githubStatus)
				}
			}

		})
	}
}

func TestDeleteReportIssueComment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		issueComments []github.IssueComment

		expectedIssueComments []github.IssueComment
	}{
		{
			name: "no test report issue comment",
			issueComments: []github.IssueComment{
				{ID: 1, User: github.User{Login: "foo-bot"}, Body: "foo-body"},
				{ID: 2, User: github.User{Login: "bar"}, Body: "bar-body"},
			},
			expectedIssueComments: []github.IssueComment{
				{ID: 1, User: github.User{Login: "foo-bot"}, Body: "foo-body"},
				{ID: 2, User: github.User{Login: "bar"}, Body: "bar-body"},
			},
		},
		{
			name: "report issue comment from bot user exists",
			issueComments: []github.IssueComment{
				{ID: 1, User: github.User{Login: "foo-bot"}, Body: "foo-body\n<!-- test report -->\n"},
				{ID: 2, User: github.User{Login: "bar"}, Body: "bar-body"},
			},
			expectedIssueComments: []github.IssueComment{
				{ID: 2, User: github.User{Login: "bar"}, Body: "bar-body"},
			},
		},
		{
			name: "report issue comment from non-bot user exists",
			issueComments: []github.IssueComment{
				{ID: 1, User: github.User{Login: "foo"}, Body: "foo-body\n<!-- test report -->\n"},
				{ID: 2, User: github.User{Login: "bar"}, Body: "bar-body"},
			},
			expectedIssueComments: []github.IssueComment{
				{ID: 1, User: github.User{Login: "foo"}, Body: "foo-body\n<!-- test report -->\n"},
				{ID: 2, User: github.User{Login: "bar"}, Body: "bar-body"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fgc{
				issueComments: map[int][]github.IssueComment{1: tc.issueComments},
			}
			client := &GitHubProvider{
				ghc: ghc,
			}
			err := client.deleteReportIssueComment(CodeReviewCommon{Number: 1}, logrus.WithField("test", tc.name))
			if err != nil {
				t.Fatalf("failed delete report issue comment: %v", err)
			}
			if !slices.Equal(ghc.issueComments[1], tc.expectedIssueComments) {
				t.Errorf("expected issue comments: %+v, got issue comments: %+v", tc.expectedIssueComments, ghc.issueComments)
			}
		})
	}
}

// nginxErrorPage mimics the body GitHub returns alongside gateway errors.
func nginxErrorPage(code int) string {
	return fmt.Sprintf("<html>\r\n<head><title>%[1]d %[2]s</title></head>\r\n<body>\r\n<center><h1>%[1]d %[2]s</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n", code, http.StatusText(code))
}

// newTestGitHubClient returns a real prow GitHub client whose GraphQL and REST
// endpoints point at server.
func newTestGitHubClient(t *testing.T, server *httptest.Server) github.Client {
	t.Helper()
	_, _, ghc, err := github.NewClientFromOptions(logrus.Fields{}, github.ClientOptions{
		Censor:          func(b []byte) []byte { return b },
		GetToken:        func() []byte { return []byte("token") },
		GraphqlEndpoint: server.URL,
		Bases:           []string{server.URL},
		// Keep the client's GraphQL retry backoff short.
		InitialDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create GitHub client: %v", err)
	}
	return ghc
}

// TestIsGatewayTimeoutMatchesGraphQLClientErrors checks that isGatewayTimeout
// recognizes the errors the real GraphQL client returns, i.e. that the typed
// server error survives the GraphQL library and net/http.
func TestIsGatewayTimeoutMatchesGraphQLClientErrors(t *testing.T) {
	for _, tc := range []struct {
		code int
		want bool
	}{
		{code: http.StatusBadGateway, want: true},
		{code: http.StatusGatewayTimeout, want: true},
		{code: http.StatusInternalServerError, want: false},
	} {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(tc.code)
				fmt.Fprint(w, nginxErrorPage(tc.code))
			}))
			defer server.Close()
			ghc := newTestGitHubClient(t, server)

			// Same opt-out search() uses, so the client returns the 502/504
			// immediately instead of retrying with backoff.
			ctx := github.WithCallerHandledGatewayTimeouts(context.Background())
			vars := map[string]interface{}{
				"query":          githubql.String("is:pr"),
				"searchCursor":   (*githubql.String)(nil),
				"searchPageSize": githubql.Int(maxSearchPageSize),
			}
			err := ghc.QueryWithGitHubAppsSupport(ctx, &searchQuery{}, vars, "")
			if err == nil {
				t.Fatal("expected an error from the GraphQL client")
			}
			if got := isGatewayTimeout(err); got != tc.want {
				t.Errorf("isGatewayTimeout(%q) = %t, want %t; has the GraphQL library changed its error format?", err, got, tc.want)
			}
			if calls != 1 {
				t.Errorf("expected exactly 1 request, got %d", calls)
			}
		})
	}
}

// TestSearchShrinksPageOnGraphQLResourceLimit exercises resource errors returned
// with HTTP 200 through the real GitHub and GraphQL clients, including partial
// data that must be discarded before retrying the page.
func TestSearchShrinksPageOnGraphQLResourceLimit(t *testing.T) {
	var pageSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				SearchCursor   *string `json:"searchCursor"`
				SearchPageSize int     `json:"searchPageSize"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode GraphQL request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.Variables.SearchCursor != nil {
			t.Errorf("expected first-page cursor, got %q", *req.Variables.SearchCursor)
		}
		pageSizes = append(pageSizes, req.Variables.SearchPageSize)
		w.Header().Set("Content-Type", "application/json")
		if req.Variables.SearchPageSize > 18 {
			fmt.Fprint(w, `{"data":{"search":{"nodes":[{"number":999}]}},"errors":[{"message":"Resource limits for this query exceeded."}]}`)
			return
		}
		fmt.Fprint(w, `{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":1},{"number":2}]}}}`)
	}))
	defer server.Close()
	ghc := newTestGitHubClient(t, server)

	prs, err := (&GitHubProvider{}).search(ghc.QueryWithGitHubAppsSupport, logrus.WithField("test", t.Name()), "is:pr", time.Time{}, time.Now(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var numbers []int
	for _, pr := range prs {
		numbers = append(numbers, int(pr.Number))
	}
	if diff := cmp.Diff([]int{1, 2}, numbers); diff != "" {
		t.Errorf("unexpected PRs (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{37, 18}, pageSizes); diff != "" {
		t.Errorf("unexpected page sizes (-want +got):\n%s", diff)
	}
}

// TestSearchShrinksPageAgainstGitHubServer runs search() end to end through the
// real GitHub and GraphQL clients against a server that, like GitHub, cannot
// resolve pages larger than a threshold in time.
func TestSearchShrinksPageAgainstGitHubServer(t *testing.T) {
	const (
		maxResolvablePageSize = 18
		totalPRs              = 40
	)
	type gqlRequest struct {
		Query     string `json:"query"`
		Variables struct {
			SearchCursor   *string `json:"searchCursor"`
			SearchPageSize int     `json:"searchPageSize"`
		} `json:"variables"`
	}
	var pageSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode GraphQL request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !strings.Contains(req.Query, "$searchPageSize:Int!") || !strings.Contains(req.Query, "first: $searchPageSize") {
			t.Errorf("query does not declare and use $searchPageSize: %s", req.Query)
		}
		size := req.Variables.SearchPageSize
		pageSizes = append(pageSizes, size)
		if size > maxResolvablePageSize {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, nginxErrorPage(http.StatusBadGateway))
			return
		}
		offset := 0
		if req.Variables.SearchCursor != nil {
			offset, _ = strconv.Atoi(*req.Variables.SearchCursor)
		}
		end := min(offset+size, totalPRs)
		var nodes []map[string]interface{}
		for n := offset + 1; n <= end; n++ {
			nodes = append(nodes, map[string]interface{}{"number": n})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"rateLimit": map[string]interface{}{"cost": 1, "remaining": 5000},
				"search": map[string]interface{}{
					"pageInfo": map[string]interface{}{"hasNextPage": end < totalPRs, "endCursor": strconv.Itoa(end)},
					"nodes":    nodes,
				},
			},
		})
	}))
	defer server.Close()
	ghc := newTestGitHubClient(t, server)

	prs, err := (&GitHubProvider{}).search(ghc.QueryWithGitHubAppsSupport, logrus.WithField("test", t.Name()), "is:pr", time.Time{}, time.Now(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != totalPRs {
		t.Fatalf("expected %d PRs, got %d", totalPRs, len(prs))
	}
	for i, pr := range prs {
		if int(pr.Number) != i+1 {
			t.Fatalf("PRs skipped or duplicated: position %d has PR %d", i, pr.Number)
		}
	}
	// One 502 at the default size, then the shrunk size for every page; the
	// client must not have retried the 502 itself.
	if diff := cmp.Diff([]int{maxSearchPageSize, 18, 18, 18}, pageSizes); diff != "" {
		t.Errorf("unexpected page sizes requested (-want +got):\n%s", diff)
	}
}

// TestSearchGivesUpAgainstFailingGitHubServer bounds the number of requests
// search() makes when every page times out: one per page size while shrinking,
// then the client's own retries at the minimum size.
func TestSearchGivesUpAgainstFailingGitHubServer(t *testing.T) {
	var pageSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				SearchPageSize int `json:"searchPageSize"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode GraphQL request: %v", err)
		}
		pageSizes = append(pageSizes, req.Variables.SearchPageSize)
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, nginxErrorPage(http.StatusBadGateway))
	}))
	defer server.Close()
	ghc := newTestGitHubClient(t, server)

	_, err := (&GitHubProvider{}).search(ghc.QueryWithGitHubAppsSupport, logrus.WithField("test", t.Name()), "is:pr", time.Time{}, time.Now(), "")
	if !isGatewayTimeout(err) {
		t.Errorf("expected a gateway timeout error, got %v", err)
	}
	if diff := cmp.Diff([]int{maxSearchPageSize, 18, 9, minSearchPageSize, minSearchPageSize, minSearchPageSize}, pageSizes); diff != "" {
		t.Errorf("unexpected page sizes requested (-want +got):\n%s", diff)
	}
}
