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
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

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

	cases := []struct {
		name     string
		start    time.Time
		end      time.Time
		q        string
		cursors  []*githubql.String
		sqs      []searchQuery
		errs     []error
		expected []PullRequest
		err      bool
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &GitHubProvider{}
			var i int
			querier := func(_ context.Context, result interface{}, actual map[string]interface{}, _ string) error {
				expected := map[string]interface{}{
					"query":        githubql.String(tc.q),
					"searchCursor": tc.cursors[i],
				}
				if !equality.Semantic.DeepEqual(expected, actual) {
					t.Errorf("call %d vars do not match:\n%s", i, diff.ObjectReflectDiff(expected, actual))
				}
				ret := result.(*searchQuery)
				err := tc.errs[i]
				sq := tc.sqs[i]
				i++
				if err != nil {
					return err
				}
				*ret = sq
				return nil
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

			if !reflect.DeepEqual(tc.expected, prs) {
				t.Errorf("prs do not match:\n%s", diff.ObjectReflectDiff(tc.expected, prs))
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
			err := client.overwriteProwJobContexts(tc.pr, tc.pjs, logrus.WithField("test", tc.name))
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
