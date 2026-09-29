/*
Copyright 2016 The Kubernetes Authors.

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

package trigger

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	clienttesting "k8s.io/client-go/testing"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/client/clientset/versioned/fake"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/plugins"
)

func issueLabels(labels ...string) []string {
	var ls []string
	for _, label := range labels {
		ls = append(ls, fmt.Sprintf("org/repo#0:%s", label))
	}
	return ls
}

const shouldNotAddComment = "<none>"

type fakeCommentPruner struct {
	called bool
}

func (cp *fakeCommentPruner) PruneComments(shouldPrune func(github.IssueComment) bool) {
	cp.called = true
}

type testcase struct {
	name string

	Author         string
	PRAuthor       string
	Body           string
	State          string
	IsPR           bool
	Branch         string
	ShouldBuild    bool
	AddedLabels    []string
	RemovedLabels  []string
	StartsExactly  string
	Presubmits     map[string][]config.Presubmit
	IssueLabels    []string
	IgnoreOkToTest bool
	AddedComment   string
	PruneHelp      bool
}

func TestHandleGenericComment(t *testing.T) {
	helpComment := "The following commands are available to trigger required jobs:\n```\n/test jib\n```\n```\n/test job\n```\n\n"
	helpTestAllWithJobsComment := fmt.Sprintf("Use `/test all` to run the following jobs that were automatically triggered:%s\n\n", "\n```\njob\n```")
	var testcases = []testcase{
		{
			name: "Not a PR.",

			Author:      "trusted-member",
			Body:        "/ok-to-test",
			State:       "open",
			IsPR:        false,
			ShouldBuild: false,
		},
		{
			name: "Closed PR.",

			Author:      "trusted-member",
			Body:        "/ok-to-test",
			State:       "closed",
			IsPR:        true,
			ShouldBuild: false,
		},
		{
			name: "Comment by a bot.",

			Author:      "k8s-ci-robot",
			Body:        "/ok-to-test",
			State:       "open",
			IsPR:        true,
			ShouldBuild: false,
		},
		{
			name: "Irrelevant comment leads to no action.",

			Author:      "trusted-member",
			Body:        "Nice weather outside, right?",
			State:       "open",
			IsPR:        true,
			ShouldBuild: false,
		},
		{
			name: "Non-trusted member's ok to test.",

			Author:      "untrusted-member",
			Body:        "/ok-to-test",
			State:       "open",
			IsPR:        true,
			ShouldBuild: false,
		},
		{
			name:        "accept /test from non-trusted member if PR author is trusted",
			Author:      "untrusted-member",
			PRAuthor:    "trusted-member",
			Body:        "/test all",
			State:       "open",
			IsPR:        true,
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name:        "reject /test from non-trusted member when PR author is untrusted",
			Author:      "untrusted-member",
			PRAuthor:    "untrusted-member",
			Body:        "/test all",
			State:       "open",
			IsPR:        true,
			ShouldBuild: false,
		},
		{
			name: `Non-trusted member after "/ok-to-test".`,

			Author:      "untrusted-member",
			Body:        "/test all",
			State:       "open",
			IsPR:        true,
			ShouldBuild: true,
			PruneHelp:   true,
			IssueLabels: issueLabels(labels.OkToTest),
		},
		{
			name: `Non-trusted member after "/ok-to-test", needs-ok-to-test label wasn't deleted.`,

			Author:        "untrusted-member",
			Body:          "/test all",
			State:         "open",
			IsPR:          true,
			ShouldBuild:   true,
			PruneHelp:     true,
			IssueLabels:   issueLabels(labels.NeedsOkToTest, labels.OkToTest),
			RemovedLabels: issueLabels(labels.NeedsOkToTest),
		},
		{
			name: "Trusted member's ok to test, IgnoreOkToTest",

			Author:         "trusted-member",
			Body:           "/ok-to-test",
			State:          "open",
			IsPR:           true,
			ShouldBuild:    false,
			IgnoreOkToTest: true,
		},
		{
			name: "Trusted member's ok to test",

			Author:      "trusted-member",
			Body:        "looks great, thanks!\n/ok-to-test",
			State:       "open",
			IsPR:        true,
			ShouldBuild: true,
			AddedLabels: issueLabels(labels.OkToTest),
		},
		{
			name: "Trusted member's ok to test, trailing space.",

			Author:      "trusted-member",
			Body:        "looks great, thanks!\n/ok-to-test \r",
			State:       "open",
			IsPR:        true,
			ShouldBuild: true,
			AddedLabels: issueLabels(labels.OkToTest),
		},
		{
			name: "Trusted member's not ok to test.",

			Author:      "trusted-member",
			Body:        "not /ok-to-test",
			State:       "open",
			IsPR:        true,
			ShouldBuild: false,
		},
		{
			name: "Trusted member's test this.",

			Author:      "trusted-member",
			Body:        "/test all",
			State:       "open",
			IsPR:        true,
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name: "Wrong branch",

			Author:      "trusted-member",
			Body:        "/test all",
			State:       "open",
			IsPR:        true,
			Branch:      "other",
			ShouldBuild: false,
		},
		{
			name: "Retest with one running and one failed",

			Author:        "trusted-member",
			Body:          "/retest",
			State:         "open",
			IsPR:          true,
			ShouldBuild:   true,
			StartsExactly: "pull-jib",
			PruneHelp:     true,
		},
		{
			name: "Retest with one running and one failed, trailing space.",

			Author:        "trusted-member",
			Body:          "/retest \r",
			State:         "open",
			IsPR:          true,
			ShouldBuild:   true,
			StartsExactly: "pull-jib",
			PruneHelp:     true,
		},
		{
			name:   "test of silly regex job",
			Author: "trusted-member",
			Body:   "Nice weather outside, right?",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Brancher: config.Brancher{Branches: []string{"master"}},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      "Nice weather outside, right?",
						RerunCommand: "Nice weather outside, right?",
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jab",

			// We only add/remove help comments for things that look like the
			// normal triggers.
			PruneHelp: false,
		},
		{
			name: "needs-ok-to-test label is removed when no presubmit runs by default",

			Author:      "trusted-member",
			Body:        "/ok-to-test",
			State:       "open",
			IsPR:        true,
			ShouldBuild: false,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: false,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test (?:.*? )?job(?: .*?)?$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						AlwaysRun: false,
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			IssueLabels:   issueLabels(labels.NeedsOkToTest),
			AddedLabels:   issueLabels(labels.OkToTest),
			RemovedLabels: issueLabels(labels.NeedsOkToTest),
		},
		{
			name:   "Wrong branch w/ SkipReport",
			Author: "trusted-member",
			Body:   "/test all",
			Branch: "other",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							SkipReport: true,
							Context:    "pull-job",
						},
						Trigger:      `(?m)^/test (?:.*? )?job(?: .*?)?$`,
						RerunCommand: `/test job`,
						Brancher:     config.Brancher{Branches: []string{"master"}},
					},
				},
			},
		},
		{
			name:   "Retest of run_if_changed job that hasn't run. Changes require job",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							SkipReport: true,
							Context:    "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jab",
			PruneHelp:     true,
		},
		{
			name:   "Retest of skip_if_only_changed job that hasn't run. Changes require job",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							SkipReport: true,
							Context:    "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jab",
			PruneHelp:     true,
		},
		{
			name:   "Retest of run_if_changed job that failed. Changes require job",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jib",
			PruneHelp:     true,
		},
		{
			name:   "Retest of skip_if_only_changed job that failed. Changes require job",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jib",
			PruneHelp:     true,
		},
		{
			name:   "/test of run_if_changed job that has passed",
			Author: "trusted-member",
			Body:   "/test jub",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `(?m)^/test (?:.*? )?jub(?: .*?)?$`,
						RerunCommand: `/test jub`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jub",
			PruneHelp:     true,
		},
		{
			name:   "/test of skip_if_only_changed job that has passed",
			Author: "trusted-member",
			Body:   "/test jub",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `(?m)^/test (?:.*? )?jub(?: .*?)?$`,
						RerunCommand: `/test jub`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jub",
			PruneHelp:     true,
		},
		{
			name:   "Retest triggers failed job",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name:   "Retest triggers failed job that is optional",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
						Optional:     true,
					},
				},
			},
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name:   "Retest-Required doesn't triggers failed job",
			Author: "trusted-member",
			Body:   "/retest-required",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name:   "Retest-Required doesn't trigger failed job that is optional",
			Author: "trusted-member",
			Body:   "/retest-required",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
						Optional:     true,
					},
				},
			},
			PruneHelp: true,
		},
		{
			name:   "Test-Manual-Required triggers missing required manual job",
			Author: "trusted-member",
			Body:   "/test-manual-required",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jab",
			PruneHelp:     true,
		},
		{
			name:   "Test-Manual-Required doesn't trigger missing optional job",
			Author: "trusted-member",
			Body:   "/test-manual-required",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Optional: true,
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
			PruneHelp: true,
		},
		{
			name:   "Test-Manual-Required does not trigger always_run or conditional jobs",
			Author: "trusted-member",
			Body:   "/test-manual-required",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "always-run",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-always-run",
						},
						Trigger:      `(?m)^/test (?:.*? )?always-run(?: .*?)?$`,
						RerunCommand: `/test always-run`,
					},
					{
						JobBase: config.JobBase{
							Name: "conditional",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{RunIfChanged: "CHANGED"},
						Reporter: config.Reporter{
							Context: "pull-conditional",
						},
						Trigger:      `(?m)^/test (?:.*? )?conditional(?: .*?)?$`,
						RerunCommand: `/test conditional`,
					},
				},
			},
			ShouldBuild: false,
			PruneHelp:   true,
		},
		{
			name:   "Retest of run_if_changed job that failed. Changes do not require the job",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name:   "Retest of skip_if_only_changed job that failed. Changes do not require the job",
			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name:   "Run if changed job triggered by /ok-to-test",
			Author: "trusted-member",
			Body:   "/ok-to-test",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jab",
			IssueLabels:   issueLabels(labels.NeedsOkToTest),
			AddedLabels:   issueLabels(labels.OkToTest),
			RemovedLabels: issueLabels(labels.NeedsOkToTest),
		},
		{
			name:   "Run if non-skipped job triggered by /ok-to-test",
			Author: "trusted-member",
			Body:   "/ok-to-test",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jab",
			IssueLabels:   issueLabels(labels.NeedsOkToTest),
			AddedLabels:   issueLabels(labels.OkToTest),
			RemovedLabels: issueLabels(labels.NeedsOkToTest),
		},
		{
			name:   "/test of branch-sharded job",
			Author: "trusted-member",
			Body:   "/test jab",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Brancher: config.Brancher{Branches: []string{"master"}},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Brancher: config.Brancher{Branches: []string{"release"}},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jab",
			PruneHelp:     true,
		},
		{
			name:   "branch-sharded job. no shard matches base branch",
			Author: "trusted-member",
			Branch: "branch",
			Body:   "/test jab",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Brancher: config.Brancher{Branches: []string{"master"}},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Brancher: config.Brancher{Branches: []string{"release"}},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `(?m)^/test (?:.*? )?jab(?: .*?)?$`,
						RerunCommand: `/test jab`,
					},
				},
			},
		},
		{
			name: "/retest of RunIfChanged job that doesn't need to run and hasn't run",

			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jeb",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jeb",
						},
						Trigger:      `(?m)^/test (?:.*? )?jeb(?: .*?)?$`,
						RerunCommand: `/test jeb`,
					},
				},
			},
			PruneHelp: true,
		},
		{
			name: "/retest of SkipIfOnlyChanged job that doesn't need to run and hasn't run",

			Author: "trusted-member",
			Body:   "/retest",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jeb",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jeb",
						},
						Trigger:      `(?m)^/test (?:.*? )?jeb(?: .*?)?$`,
						RerunCommand: `/test jeb`,
					},
				},
			},
			PruneHelp: true,
		},
		{
			name: "explicit /test for RunIfChanged job that doesn't need to run",

			Author: "trusted-member",
			Body:   "/test pull-jeb",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jeb",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jeb",
						},
						Trigger:      `(?m)^/test (?:.*? )?jeb(?: .*?)?$`,
						RerunCommand: `/test jeb`,
					},
				},
			},
			ShouldBuild: false,
		},
		{
			name: "explicit /test for SkipIfOnlyChanged job that doesn't need to run",

			Author: "trusted-member",
			Body:   "/test pull-jeb",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jeb",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jeb",
						},
						Trigger:      `(?m)^/test (?:.*? )?jeb(?: .*?)?$`,
						RerunCommand: `/test jeb`,
					},
				},
			},
			ShouldBuild: false,
		},
		{
			name:   "/test all of run_if_changed job that has passed and needs to run",
			Author: "trusted-member",
			Body:   "/test all",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `(?m)^/test (?:.*? )?jub(?: .*?)?$`,
						RerunCommand: `/test jub`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jub",
			PruneHelp:     true,
		},
		{
			name:   "/test all of skip_if_only_changed job that has passed and needs to run",
			Author: "trusted-member",
			Body:   "/test all",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `(?m)^/test (?:.*? )?jub(?: .*?)?$`,
						RerunCommand: `/test jub`,
					},
				},
			},
			ShouldBuild:   true,
			StartsExactly: "pull-jub",
			PruneHelp:     true,
		},
		{
			name:   "/test all of run_if_changed job that has passed and doesn't need to run",
			Author: "trusted-member",
			Body:   "/test all",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							RunIfChanged: "CHANGED2",
						},
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `(?m)^/test (?:.*? )?jub(?: .*?)?$`,
						RerunCommand: `/test jub`,
					},
				},
			},
		},
		{
			name:   "/test all of skip_if_only_changed job that has passed and doesn't need to run",
			Author: "trusted-member",
			Body:   "/test all",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						RegexpChangeMatcher: config.RegexpChangeMatcher{
							SkipIfOnlyChanged: "CHANGED",
						},
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `(?m)^/test (?:.*? )?jub(?: .*?)?$`,
						RerunCommand: `/test jub`,
					},
				},
			},
		},
		{
			name:        "accept /test all from trusted user",
			Author:      "trusted-member",
			PRAuthor:    "trusted-member",
			Body:        "/test all",
			State:       "open",
			IsPR:        true,
			ShouldBuild: true,
			PruneHelp:   true,
		},
		{
			name:        `Non-trusted member after "/lgtm" and "/approve"`,
			Author:      "untrusted-member",
			PRAuthor:    "untrusted-member",
			Body:        "/retest",
			State:       "open",
			IsPR:        true,
			ShouldBuild: false,
			IssueLabels: issueLabels(labels.LGTM, labels.Approved),
		},
		{
			name:   `help command "/test ?" lists available presubmits`,
			Author: "trusted-member",
			Body:   "/test ?",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test (?:.*? )?job(?: .*?)?$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			AddedComment: helpComment + "Use `/test all` to run all jobs.",
		},
		{
			name:   `ignore "/test ?" in code block`,
			Author: "trusted-member",
			Body:   produceCodeBlock("/test ?", false),
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test (?:.*? )?job(?: .*?)?$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			AddedComment: shouldNotAddComment,
		},
		{
			name:   `help command "/test ?" uses unique RerunCommand field of presubmits`,
			Author: "trusted-member",
			Body:   "/test ?",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `/rerun_command`,
						RerunCommand: `/rerun_command`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `/command_foo`,
						RerunCommand: `/command_foo`,
					},
					{
						JobBase: config.JobBase{
							Name: "jab",
						},
						Reporter: config.Reporter{
							Context: "pull-jab",
						},
						Trigger:      `/rerun_command`,
						RerunCommand: `/rerun_command`,
					},
				},
			},
			AddedComment: "@trusted-member: The following commands are available to trigger required jobs:\n" +
				"```\n/command_foo\n```\n```\n/rerun_command\n```\n\n" +
				"Use `/test all` to run all jobs.",
		},
		{
			name:         "/test with no target results in a help message",
			Author:       "trusted-member",
			Body:         "/test",
			State:        "open",
			IsPR:         true,
			AddedComment: pjutil.TestWithoutTargetNote + helpComment + helpTestAllWithJobsComment,
		},
		{
			name:         "ignore `/test` with no target in a code block",
			Author:       "trusted-member",
			Body:         "```\n/test\n```",
			State:        "open",
			IsPR:         true,
			AddedComment: shouldNotAddComment,
		},
		{
			name:         "ignore `/test` with no target in a tilde code block",
			Author:       "trusted-member",
			Body:         "~~~\n/test\n~~~",
			State:        "open",
			IsPR:         true,
			AddedComment: shouldNotAddComment,
		},
		{
			name:         "/test with no target but ? in the next line results in an invalid test command message",
			Author:       "trusted-member",
			Body:         "/test \r\n?",
			State:        "open",
			IsPR:         true,
			AddedComment: pjutil.TestWithoutTargetNote + helpComment + helpTestAllWithJobsComment,
		},
		{
			name:         "/retest with trailing words results in a help message",
			Author:       "trusted-member",
			Body:         "/retest FOO",
			State:        "open",
			IsPR:         true,
			AddedComment: pjutil.RetestWithTargetNote + helpComment + helpTestAllWithJobsComment,
		},
		{
			name:         "/retest with trailing words in a code block",
			Author:       "trusted-member",
			Body:         produceCodeBlock("/retest FOO", false),
			State:        "open",
			IsPR:         true,
			AddedComment: shouldNotAddComment,
		},
		{
			name:          "/retest without target but with lines following it, is valid",
			Author:        "trusted-member",
			Body:          "/retest \r\n/other-command",
			State:         "open",
			IsPR:          true,
			ShouldBuild:   true,
			StartsExactly: "pull-jib",
			PruneHelp:     true,
		},
		{
			name:         "/test with unknown target results in a help message",
			Author:       "trusted-member",
			Body:         "/test FOO",
			State:        "open",
			IsPR:         true,
			AddedComment: pjutil.TargetNotFoundNote + helpComment + helpTestAllWithJobsComment,
		},
		{
			name:         "/test with unknown target in code block. Should be ignored",
			Author:       "trusted-member",
			Body:         produceCodeBlock("/test FOO", false),
			State:        "open",
			IsPR:         true,
			AddedComment: shouldNotAddComment,
		},
		{
			name:         "two `/test` with unknown target. One in a code block, and one is not.",
			Author:       "trusted-member",
			Body:         produceCodeBlock("/test FOO", true) + "/test BAR",
			State:        "open",
			IsPR:         true,
			AddedComment: pjutil.TargetNotFoundNote + helpComment + helpTestAllWithJobsComment,
		},
		{
			name:         "two `/test` with unknown target. One out of a code block, and one is inside.",
			Author:       "trusted-member",
			Body:         "/test FOO\n" + produceCodeBlock("/test BAR", false),
			State:        "open",
			IsPR:         true,
			AddedComment: pjutil.TargetNotFoundNote + helpComment + helpTestAllWithJobsComment,
		},
		{
			name:   "multiple code blocks; should be ignored.",
			Author: "trusted-member",
			Body: produceCodeBlock("/test FOO", true) +
				produceCodeBlock("/test BAR", true) +
				produceCodeBlock("/test BAZ", false),
			State:        "open",
			IsPR:         true,
			AddedComment: shouldNotAddComment,
		},
		{
			name:   "/test mixed with multiple code blocks; should be ignored.",
			Author: "trusted-member",
			Body: produceCodeBlock("/test FOO", true) + // code block
				produceCodeBlock("/test BAR", true) + // code block
				"/test BAZ\n" + // /test as a regular text
				"Will trigger the help message.\n" + // some regular text
				produceCodeBlock("/test QUX", false), // code block
			State:        "open",
			IsPR:         true,
			AddedComment: pjutil.TargetNotFoundNote + helpComment + helpTestAllWithJobsComment,
		},
		{
			name:   "help comment should list only eligible jobs under '/test all'",
			Author: "trusted-member",
			Body:   "/test ?",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test job$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			AddedComment: helpComment + helpTestAllWithJobsComment,
		},
		{
			name:   "when no jobs can be run with /test all, respond accordingly",
			Author: "trusted-member",
			Body:   "/test all",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: false,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test job$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						AlwaysRun: false,
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test jib$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			AddedComment: pjutil.ThereAreNoTestAllJobsNote + helpComment,
		},
		{
			name:   "when no jobs can be run with /test all, ignore if inside a code block",
			Author: "trusted-member",
			Body:   produceCodeBlock("/test all", false),
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: false,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test job$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						AlwaysRun: false,
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test jib$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			AddedComment: shouldNotAddComment,
		},
		{
			name:   "available presubmits should not list those excluded by branch",
			Author: "trusted-member",
			Body:   "/test ?",
			State:  "open",
			IsPR:   true,

			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job-excluded-by-brancher",
						},
						Brancher: config.Brancher{
							SkipBranches: []string{"master"},
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-job-excluded-by-brancher",
						},
						Trigger:      `(?m)^/test job-excluded$`,
						RerunCommand: `/test job-excluded`,
					},
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test job$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
				},
			},
			AddedComment: helpComment + helpTestAllWithJobsComment,
		},
		{
			name:   `help command "/test ?" differs between optional and required jobs`,
			Author: "trusted-member",
			Body:   "/test ?",
			State:  "open",
			IsPR:   true,
			Presubmits: map[string][]config.Presubmit{
				"org/repo": {
					{
						JobBase: config.JobBase{
							Name: "job",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-job",
						},
						Trigger:      `(?m)^/test (?:.*? )?job(?: .*?)?$`,
						RerunCommand: `/test job`,
					},
					{
						JobBase: config.JobBase{
							Name: "jib",
						},
						AlwaysRun: true,
						Reporter: config.Reporter{
							Context: "pull-jib",
						},
						Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
						RerunCommand: `/test jib`,
					},
					{
						JobBase: config.JobBase{
							Name: "jub",
						},
						AlwaysRun: true,
						Optional:  true,
						Reporter: config.Reporter{
							Context: "pull-jub",
						},
						Trigger:      `(?m)^/test (?:.*? )?jub(?: .*?)?$`,
						RerunCommand: `/test jub`,
					},
				},
			},
			AddedComment: helpComment +
				"The following commands are available to trigger optional jobs:\n```\n/test jub\n```\n\n" +
				"Use `/test all` to run all jobs.",
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.Branch == "" {
				tc.Branch = "master"
			}
			g := fakegithub.NewFakeClient()
			g.IssueComments = map[int][]github.IssueComment{}
			g.OrgMembers = map[string][]string{"org": {"trusted-member"}}
			g.PullRequests = map[int]*github.PullRequest{
				0: {
					User:   github.User{Login: tc.PRAuthor},
					Number: 0,
					Head: github.PullRequestBranch{
						SHA: "cafe",
					},
					Base: github.PullRequestBranch{
						Ref: tc.Branch,
						Repo: github.Repo{
							Owner: github.User{Login: "org"},
							Name:  "repo",
						},
					},
				},
			}
			g.IssueLabelsExisting = tc.IssueLabels
			g.PullRequestChanges = map[int][]github.PullRequestChange{0: {{Filename: "CHANGED"}}}
			g.CombinedStatuses = map[string]*github.CombinedStatus{
				"cafe": {
					Statuses: []github.Status{
						{State: github.StatusPending, Context: "pull-job"},
						{State: github.StatusFailure, Context: "pull-jib"},
						{State: github.StatusSuccess, Context: "pull-jub"},
					},
				},
			}
			g.Collaborators = []string{"k8s-ci-robot"}
			fakeConfig := &config.Config{ProwConfig: config.ProwConfig{ProwJobNamespace: "prowjobs"}}
			fakeProwJobClient := fake.NewSimpleClientset()
			c := Client{
				GitHubClient:  g,
				ProwJobClient: fakeProwJobClient.ProwV1().ProwJobs(fakeConfig.ProwJobNamespace),
				Config:        fakeConfig,
				Logger:        logrus.WithField("plugin", PluginName),
				GitClient:     nil,
			}
			presubmits := tc.Presubmits
			if presubmits == nil {
				presubmits = map[string][]config.Presubmit{
					"org/repo": {
						{
							JobBase: config.JobBase{
								Name: "job",
							},
							AlwaysRun: true,
							Reporter: config.Reporter{
								Context: "pull-job",
							},
							Trigger:      `(?m)^/test (?:.*? )?job(?: .*?)?$`,
							RerunCommand: `/test job`,
							Brancher:     config.Brancher{Branches: []string{"master"}},
						},
						{
							JobBase: config.JobBase{
								Name: "jib",
							},
							AlwaysRun: false,
							Reporter: config.Reporter{
								Context: "pull-jib",
							},
							Trigger:      `(?m)^/test (?:.*? )?jib(?: .*?)?$`,
							RerunCommand: `/test jib`,
						},
					},
				}
			}
			if err := c.Config.SetPresubmits(presubmits); err != nil {
				t.Fatalf("%s: failed to set presubmits: %v", tc.name, err)
			}

			event := github.GenericCommentEvent{
				Action: github.GenericCommentActionCreated,
				Repo: github.Repo{
					Owner:    github.User{Login: "org"},
					Name:     "repo",
					FullName: "org/repo",
				},
				Body:        tc.Body,
				User:        github.User{Login: tc.Author},
				IssueAuthor: github.User{Login: tc.PRAuthor},
				IssueState:  tc.State,
				IsPR:        tc.IsPR,
			}

			trigger := plugins.Trigger{
				IgnoreOkToTest: tc.IgnoreOkToTest,
			}
			trigger.SetDefaults()

			cp := &fakeCommentPruner{}

			log.Printf("running case %s", tc.name)
			// In some cases handleGenericComment can be called twice for the same event.
			// For instance on Issue/PR creation and modification.
			// Let's call it twice to ensure idempotency.
			if err := handleGenericComment(c, cp, trigger, event); err != nil {
				t.Fatalf("%s: didn't expect error: %s", tc.name, err)
			}
			validate(t, fakeProwJobClient.Fake.Actions(), g, cp, tc)
			if err := handleGenericComment(c, cp, trigger, event); err != nil {
				t.Fatalf("%s: didn't expect error: %s", tc.name, err)
			}
			validate(t, fakeProwJobClient.Fake.Actions(), g, cp, tc)
		})
	}
}

func validate(t *testing.T, actions []clienttesting.Action, g *fakegithub.FakeClient, cp *fakeCommentPruner, tc testcase) {
	startedContexts := sets.New[string]()
	for _, action := range actions {
		switch action := action.(type) {
		case clienttesting.CreateActionImpl:
			if prowJob, ok := action.Object.(*prowapi.ProwJob); ok {
				startedContexts.Insert(prowJob.Spec.Context)
			}
		}
	}
	if len(startedContexts) > 0 && !tc.ShouldBuild {
		t.Errorf("Built but should not have: %+v", tc)
	} else if len(startedContexts) == 0 && tc.ShouldBuild {
		t.Errorf("Not built but should have: %+v", tc)
	}
	if tc.StartsExactly != "" && (startedContexts.Len() != 1 || !startedContexts.Has(tc.StartsExactly)) {
		t.Errorf("didn't build expected context %v, instead built %v", tc.StartsExactly, startedContexts)
	}
	if !reflect.DeepEqual(g.IssueLabelsAdded, tc.AddedLabels) {
		t.Errorf("expected %q to be added, got %q", tc.AddedLabels, g.IssueLabelsAdded)
	}
	if !reflect.DeepEqual(g.IssueLabelsRemoved, tc.RemovedLabels) {
		t.Errorf("expected %q to be removed, got %q", tc.RemovedLabels, g.IssueLabelsRemoved)
	}
	if tc.AddedComment != "" {
		if tc.AddedComment == shouldNotAddComment {
			if len(g.IssueComments[0]) != 0 {
				t.Errorf("expected no comments, got %v", g.IssueComments[0])
			}
		} else {
			if len(g.IssueComments[0]) == 0 {
				t.Errorf("expected the comments to contain %s, got no comments", tc.AddedComment)
			}
			for _, c := range g.IssueComments[0] {
				if !strings.Contains(c.Body, tc.AddedComment) {
					t.Errorf("expected the comment to contain %s, got %s", tc.AddedComment, c.Body)
				}
			}
		}
	}
	if tc.PruneHelp && !cp.called {
		t.Errorf("expected to prune old help comment on successful trigger, but did not")
	} else if !tc.PruneHelp && cp.called {
		t.Errorf("expected to not prune old help comment, but did")
	}
}

func TestRetestFilter(t *testing.T) {
	var testCases = []struct {
		name           string
		failedContexts sets.Set[string]
		allContexts    sets.Set[string]
		presubmits     []config.Presubmit
		expected       [][]bool
	}{
		{
			name:           "retest filter matches jobs that produce contexts which have failed",
			failedContexts: sets.New[string]("failed"),
			allContexts:    sets.New[string]("failed", "succeeded"),
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "failed",
					},
					Reporter: config.Reporter{
						Context: "failed",
					},
				},
				{
					JobBase: config.JobBase{
						Name: "succeeded",
					},
					Reporter: config.Reporter{
						Context: "succeeded",
					},
				},
			},
			expected: [][]bool{{true, false, true}, {false, false, false}},
		},
		{
			name:           "retest filter matches jobs that would run automatically and haven't yet ",
			failedContexts: sets.New[string](),
			allContexts:    sets.New[string]("finished"),
			presubmits: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "finished",
					},
					Reporter: config.Reporter{
						Context: "finished",
					},
				},
				{
					JobBase: config.JobBase{
						Name: "not-yet-run",
					},
					AlwaysRun: true,
					Reporter: config.Reporter{
						Context: "not-yet-run",
					},
				},
			},
			expected: [][]bool{{false, false, false}, {true, false, false}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if len(testCase.presubmits) != len(testCase.expected) {
				t.Fatalf("%s: have %d presubmits but only %d expected filter outputs", testCase.name, len(testCase.presubmits), len(testCase.expected))
			}
			if err := config.SetPresubmitRegexes(testCase.presubmits); err != nil {
				t.Fatalf("%s: could not set presubmit regexes: %v", testCase.name, err)
			}
			filter := pjutil.NewRetestFilter(testCase.failedContexts, testCase.allContexts)
			for i, presubmit := range testCase.presubmits {
				actualFiltered, actualForced, actualDefault := filter.ShouldRun(presubmit)
				expectedFiltered, expectedForced, expectedDefault := testCase.expected[i][0], testCase.expected[i][1], testCase.expected[i][2]
				if actualFiltered != expectedFiltered {
					t.Errorf("%s: filter did not evaluate correctly, expected %v but got %v for %v", testCase.name, expectedFiltered, actualFiltered, presubmit.Name)
				}
				if actualForced != expectedForced {
					t.Errorf("%s: filter did not determine forced correctly, expected %v but got %v for %v", testCase.name, expectedForced, actualForced, presubmit.Name)
				}
				if actualDefault != expectedDefault {
					t.Errorf("%s: filter did not determine default correctly, expected %v but got %v for %v", testCase.name, expectedDefault, actualDefault, presubmit.Name)
				}
			}
		})
	}
}

func produceCodeBlock(text string, withNewLine bool) string {
	newline := ""
	if withNewLine {
		newline = "\n"
	}

	return fmt.Sprintf("```\n%s\n```%s", text, newline)
}

const (
	actionsOrg     = "org"
	actionsRepo    = "repo"
	actionsBranch  = "pr-branch"
	actionsHeadSHA = "abc123"
	actionsRunsKey = actionsOrg + "/" + actionsRepo + "/" + actionsBranch + "/" + actionsHeadSHA
)

// newActionsFakeClient returns a fake with PR 0 of org/repo. The user
// "author" opened the PR from the branch pr-branch of a fork at the SHA abc123.
func newActionsFakeClient() *fakegithub.FakeClient {
	return &fakegithub.FakeClient{
		IssueComments: map[int][]github.IssueComment{},
		OrgMembers:    map[string][]string{actionsOrg: {"trusted-member"}},
		PullRequests: map[int]*github.PullRequest{
			0: {
				Base: github.PullRequestBranch{
					Ref:  "master",
					Repo: github.Repo{FullName: actionsOrg + "/" + actionsRepo},
				},
				Head: github.PullRequestBranch{
					Ref:  actionsBranch,
					SHA:  actionsHeadSHA,
					Repo: github.Repo{FullName: "author/" + actionsRepo},
				},
				User: github.User{Login: "author"},
			},
		},
		IssueLabelsAdded:     []string{},
		IssueLabelsRemoved:   []string{},
		PendingApprovalRuns:  map[string][]github.WorkflowRun{},
		ApprovedWorkflowRuns: []string{},
	}
}

func handleActionsComment(g githubClient, logger *logrus.Entry, trigger plugins.Trigger, commenter, body string) (*fake.Clientset, error) {
	fakeConfig := &config.Config{ProwConfig: config.ProwConfig{ProwJobNamespace: "prowjobs"}}
	presubmits := map[string][]config.Presubmit{
		actionsOrg + "/" + actionsRepo: {
			{
				JobBase: config.JobBase{
					Name: "test-job",
				},
				Brancher:     config.Brancher{Branches: []string{"master"}},
				Reporter:     config.Reporter{Context: "pull-test-job"},
				Trigger:      `(?m)^/test (?:.*? )?test-job(?: .*?)?$`,
				RerunCommand: "/test test-job",
				AlwaysRun:    true,
			},
		},
	}
	if err := fakeConfig.SetPresubmits(presubmits); err != nil {
		return nil, fmt.Errorf("failed to set presubmits: %w", err)
	}

	fakeProwJobClient := fake.NewSimpleClientset()
	c := Client{
		GitHubClient:  g,
		ProwJobClient: fakeProwJobClient.ProwV1().ProwJobs(fakeConfig.ProwJobNamespace),
		Config:        fakeConfig,
		Logger:        logger,
	}

	event := github.GenericCommentEvent{
		Action: github.GenericCommentActionCreated,
		Repo: github.Repo{
			Owner:    github.User{Login: actionsOrg},
			Name:     actionsRepo,
			FullName: actionsOrg + "/" + actionsRepo,
		},
		Body:        body,
		User:        github.User{Login: commenter},
		IssueAuthor: github.User{Login: "author"},
		IssueState:  "open",
		IsPR:        true,
	}

	trigger.SetDefaults()
	return fakeProwJobClient, handleGenericComment(c, &fakeCommentPruner{}, trigger, event)
}

func countErrorEntries(hook *logrustest.Hook) int {
	var count int
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.ErrorLevel {
			count++
		}
	}
	return count
}

func TestApproveGitHubActionsWorkflowRuns(t *testing.T) {
	testCases := []struct {
		name                   string
		body                   string
		triggerGitHubWorkflows bool
		ignoreOkToTest         bool
		pendingRuns            []github.WorkflowRun
		commenter              string
		existingLabels         []string
		expectApproved         []string
		expectProwJob          bool
	}{
		{
			name:                   "/ok-to-test with TriggerGitHubWorkflows enabled - should approve",
			body:                   "/ok-to-test",
			triggerGitHubWorkflows: true,
			ignoreOkToTest:         false,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			expectApproved: []string{"org/repo/1"},
		},
		{
			name:                   "/ok-to-test with TriggerGitHubWorkflows disabled - should not approve",
			body:                   "/ok-to-test",
			triggerGitHubWorkflows: false,
			ignoreOkToTest:         false,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
		},
		{
			name:                   "/test all should not approve workflows",
			body:                   "/test all",
			triggerGitHubWorkflows: true,
			ignoreOkToTest:         false,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
		},
		{
			name:                   "/retest should not approve workflows",
			body:                   "/retest",
			triggerGitHubWorkflows: true,
			ignoreOkToTest:         false,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
		},
		{
			name:                   "IgnoreOkToTest=true with TriggerGitHubWorkflows=true - should not approve",
			body:                   "/ok-to-test",
			triggerGitHubWorkflows: true,
			ignoreOkToTest:         true,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
		},
		{
			name:                   "/ok-to-test with multiple pending runs",
			body:                   "/ok-to-test",
			triggerGitHubWorkflows: true,
			ignoreOkToTest:         false,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow-1", Status: "completed", Conclusion: "action_required"},
				{ID: 2, Name: "test-workflow-2", Status: "completed", Conclusion: "action_required"},
			},
			expectApproved: []string{"org/repo/1", "org/repo/2"},
		},
		{
			name:                   "/ok-to-test with no pending runs",
			body:                   "/ok-to-test",
			triggerGitHubWorkflows: true,
			ignoreOkToTest:         false,
			pendingRuns:            []github.WorkflowRun{},
		},
		{
			// The ok-to-test label makes the PR trusted, so the ProwJobs
			// start. The label does not make the commenter trusted.
			name:                   "/ok-to-test from an untrusted PR author on a PR with ok-to-test - should not approve",
			body:                   "/ok-to-test",
			triggerGitHubWorkflows: true,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			commenter:      "author",
			existingLabels: []string{"org/repo#0:" + labels.OkToTest},
			expectProwJob:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := newActionsFakeClient()
			g.PendingApprovalRuns[actionsRunsKey] = tc.pendingRuns
			g.IssueLabelsExisting = tc.existingLabels
			commenter := tc.commenter
			if commenter == "" {
				commenter = "trusted-member"
			}

			trigger := plugins.Trigger{
				TriggerGitHubWorkflows: tc.triggerGitHubWorkflows,
				IgnoreOkToTest:         tc.ignoreOkToTest,
			}
			prowJobClient, err := handleActionsComment(g, logrus.WithField("plugin", PluginName), trigger, commenter, tc.body)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// The handler waits for the approvals before it returns.
			if got, want := slices.Sorted(slices.Values(g.ApprovedWorkflowRuns)), tc.expectApproved; !slices.Equal(got, want) {
				t.Errorf("Expected approved runs %v, got %v", want, got)
			}
			if len(g.ReranWorkflowRuns) > 0 {
				t.Errorf("Expected no re-run runs, got %v", g.ReranWorkflowRuns)
			}
			if tc.expectProwJob {
				prowJobs, err := prowJobClient.ProwV1().ProwJobs("prowjobs").List(context.Background(), metav1.ListOptions{})
				if err != nil {
					t.Fatalf("failed to list ProwJobs: %v", err)
				}
				if len(prowJobs.Items) == 0 {
					t.Error("Expected a ProwJob, got none")
				}
			}
		})
	}
}

// blockingActionsClient blocks the approve call and the re-run call of the
// failed jobs until the test closes release.
type blockingActionsClient struct {
	*fakegithub.FakeClient
	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func (b *blockingActionsClient) block() {
	b.enteredOnce.Do(func() { close(b.entered) })
	<-b.release
}

func (b *blockingActionsClient) ApproveGitHubWorkflowRun(org, repo string, id int) error {
	b.block()
	return nil
}

func (b *blockingActionsClient) TriggerFailedGitHubWorkflow(org, repo string, id int) error {
	b.block()
	return nil
}

// Hook waits only for the handler on shutdown. An Actions call that is still
// in flight when the handler returns can stop in the middle.
func TestHandleGenericCommentWaitsForActionsCalls(t *testing.T) {
	testCases := []struct {
		name        string
		body        string
		pendingRuns []github.WorkflowRun
		failedRuns  []github.WorkflowRun
	}{
		{
			name:        "approval on /ok-to-test",
			body:        "/ok-to-test",
			pendingRuns: []github.WorkflowRun{{ID: 1, Status: "completed", Conclusion: "action_required"}},
		},
		{
			name:       "re-run of the failed jobs on /retest",
			body:       "/retest",
			failedRuns: []github.WorkflowRun{{ID: 1, Status: "completed", Conclusion: "failure"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := &blockingActionsClient{
				FakeClient: newActionsFakeClient(),
				entered:    make(chan struct{}),
				release:    make(chan struct{}),
			}
			g.PendingApprovalRuns[actionsRunsKey] = tc.pendingRuns
			g.FailedActionRuns = map[string][]github.WorkflowRun{actionsRunsKey: tc.failedRuns}
			// A t.Fatal before the release must not leave the handler blocked.
			release := sync.OnceFunc(func() { close(g.release) })
			t.Cleanup(release)

			done := make(chan error, 1)
			go func() {
				_, err := handleActionsComment(g, logrus.WithField("plugin", PluginName), plugins.Trigger{TriggerGitHubWorkflows: true}, "trusted-member", tc.body)
				done <- err
			}()

			select {
			case <-g.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the handler did not start the Actions call")
			}
			select {
			case <-done:
				t.Fatal("the handler returned while an Actions call was in flight")
			case <-time.After(100 * time.Millisecond):
			}
			release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the handler did not return after the Actions call ended")
			}
		})
	}
}

func TestIsSameRepoPullRequest(t *testing.T) {
	testCases := []struct {
		name     string
		headRepo string
		baseRepo string
		expected bool
	}{
		{name: "same repository", headRepo: "org/repo", baseRepo: "org/repo", expected: true},
		{name: "fork", headRepo: "author/repo", baseRepo: "org/repo", expected: false},
		{name: "same repository with a case difference", headRepo: "Org/Repo", baseRepo: "org/repo", expected: true},
		{name: "deleted fork", headRepo: "", baseRepo: "org/repo", expected: false},
		{name: "no repository names", headRepo: "", baseRepo: "", expected: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pr := github.PullRequest{
				Head: github.PullRequestBranch{Repo: github.Repo{FullName: tc.headRepo}},
				Base: github.PullRequestBranch{Repo: github.Repo{FullName: tc.baseRepo}},
			}
			if got := isSameRepoPullRequest(pr); got != tc.expected {
				t.Errorf("Expected %t, got %t", tc.expected, got)
			}
		})
	}
}

func TestApproveWorkflowRunsByRepository(t *testing.T) {
	const (
		forkRepo = "author/repo"
		baseRepo = "org/repo"
	)
	testCases := []struct {
		name           string
		headRepo       string
		baseRepo       string
		pendingRuns    []github.WorkflowRun
		approveErrors  map[string]error
		rerunErrors    map[string]error
		expectApproved []string
		expectReran    []string
		expectErrors   int
	}{
		{
			name:     "fork: successful approval, no re-run",
			headRepo: forkRepo,
			baseRepo: baseRepo,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			expectApproved: []string{"org/repo/1"},
		},
		{
			name:     "fork: 404 from approve, already approved, no re-run",
			headRepo: forkRepo,
			baseRepo: baseRepo,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			approveErrors: map[string]error{
				"org/repo/1": github.NewNotFound(),
			},
		},
		{
			name:     "fork: 403 from approve logs an error, no re-run",
			headRepo: forkRepo,
			baseRepo: baseRepo,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			approveErrors: map[string]error{
				"org/repo/1": github.NewForbidden(),
			},
			expectErrors: 1,
		},
		{
			name:     "fork: generic error from approve logs an error, no re-run",
			headRepo: forkRepo,
			baseRepo: baseRepo,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			approveErrors: map[string]error{
				"org/repo/1": fmt.Errorf("server error"),
			},
			expectErrors: 1,
		},
		{
			name:     "fork: one approval succeeds, one gets 403",
			headRepo: forkRepo,
			baseRepo: baseRepo,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "workflow-1", Status: "completed", Conclusion: "action_required"},
				{ID: 2, Name: "workflow-2", Status: "completed", Conclusion: "action_required"},
			},
			approveErrors: map[string]error{
				"org/repo/2": github.NewForbidden(),
			},
			expectApproved: []string{"org/repo/1"},
			expectErrors:   1,
		},
		{
			name:     "same repository: re-run without an approve call",
			headRepo: baseRepo,
			baseRepo: baseRepo,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "workflow-1", Status: "completed", Conclusion: "action_required"},
				{ID: 2, Name: "workflow-2", Status: "completed", Conclusion: "action_required"},
			},
			expectReran: []string{"org/repo/1", "org/repo/2"},
		},
		{
			name:     "same repository: re-run error logs an error",
			headRepo: baseRepo,
			baseRepo: baseRepo,
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			rerunErrors: map[string]error{
				"org/repo/1": fmt.Errorf("rerun failed"),
			},
			expectErrors: 1,
		},
		{
			name: "no repository names: approve path",
			pendingRuns: []github.WorkflowRun{
				{ID: 1, Name: "test-workflow", Status: "completed", Conclusion: "action_required"},
			},
			expectApproved: []string{"org/repo/1"},
		},
		{
			name:        "no pending runs: no-op",
			headRepo:    forkRepo,
			baseRepo:    baseRepo,
			pendingRuns: []github.WorkflowRun{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := &fakegithub.FakeClient{
				PendingApprovalRuns:      map[string][]github.WorkflowRun{actionsRunsKey: tc.pendingRuns},
				ApproveWorkflowRunErrors: tc.approveErrors,
				ReranWorkflowRunErrors:   tc.rerunErrors,
			}
			logger, hook := logrustest.NewNullLogger()
			c := Client{
				GitHubClient: g,
				Logger:       logrus.NewEntry(logger),
			}
			pr := &github.PullRequest{
				Base: github.PullRequestBranch{Repo: github.Repo{FullName: tc.baseRepo}},
				Head: github.PullRequestBranch{
					Ref:  actionsBranch,
					SHA:  actionsHeadSHA,
					Repo: github.Repo{FullName: tc.headRepo},
				},
			}

			approveGitHubActionsWorkflowRuns(c, actionsOrg, actionsRepo, pr).Wait()

			if got, want := slices.Sorted(slices.Values(g.ApprovedWorkflowRuns)), tc.expectApproved; !slices.Equal(got, want) {
				t.Errorf("Expected approved runs %v, got %v", want, got)
			}
			if got, want := slices.Sorted(slices.Values(g.ReranWorkflowRuns)), tc.expectReran; !slices.Equal(got, want) {
				t.Errorf("Expected re-run runs %v, got %v", want, got)
			}
			if errorEntries := countErrorEntries(hook); errorEntries != tc.expectErrors {
				t.Errorf("Expected %d error log entries, got %d", tc.expectErrors, errorEntries)
			}
		})
	}
}

func TestTriggerFailedGitHubWorkflows(t *testing.T) {
	failedRuns := []github.WorkflowRun{
		{ID: 1, Name: "workflow-1", Status: "completed", Conclusion: "failure"},
		{ID: 2, Name: "workflow-2", Status: "completed", Conclusion: "cancelled"},
	}
	testCases := []struct {
		name                   string
		body                   string
		triggerGitHubWorkflows bool
		lookupError            error
		rerunErrors            map[string]error
		commenter              string
		existingLabels         []string
		expectTriggered        []string
		expectErrors           int
	}{
		{
			name:                   "/retest re-runs the failed runs",
			body:                   "/retest",
			triggerGitHubWorkflows: true,
			expectTriggered:        []string{"org/repo/1", "org/repo/2"},
		},
		{
			name:                   "/test all re-runs the failed runs",
			body:                   "/test all",
			triggerGitHubWorkflows: true,
			expectTriggered:        []string{"org/repo/1", "org/repo/2"},
		},
		{
			name:                   "TriggerGitHubWorkflows disabled re-runs nothing",
			body:                   "/retest",
			triggerGitHubWorkflows: false,
		},
		{
			name:                   "/test of one job re-runs nothing",
			body:                   "/test test-job",
			triggerGitHubWorkflows: true,
		},
		{
			name:                   "lookup error re-runs nothing",
			body:                   "/retest",
			triggerGitHubWorkflows: true,
			lookupError:            fmt.Errorf("server error"),
			expectErrors:           1,
		},
		{
			name:                   "re-run error does not stop the other re-runs",
			body:                   "/retest",
			triggerGitHubWorkflows: true,
			rerunErrors: map[string]error{
				"org/repo/1": fmt.Errorf("rerun failed"),
			},
			expectTriggered: []string{"org/repo/2"},
			expectErrors:    1,
		},
		{
			// The ok-to-test label makes the PR trusted, but not the commenter.
			name:                   "/retest from an untrusted PR author on a PR with ok-to-test re-runs nothing",
			body:                   "/retest",
			triggerGitHubWorkflows: true,
			commenter:              "author",
			existingLabels:         []string{"org/repo#0:" + labels.OkToTest},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := newActionsFakeClient()
			g.FailedActionRuns = map[string][]github.WorkflowRun{actionsRunsKey: failedRuns}
			g.FailedActionRunsError = tc.lookupError
			g.TriggerFailedWorkflowRunErrors = tc.rerunErrors
			g.IssueLabelsExisting = tc.existingLabels
			commenter := tc.commenter
			if commenter == "" {
				commenter = "trusted-member"
			}
			logger, hook := logrustest.NewNullLogger()

			trigger := plugins.Trigger{TriggerGitHubWorkflows: tc.triggerGitHubWorkflows}
			if _, err := handleActionsComment(g, logrus.NewEntry(logger), trigger, commenter, tc.body); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got, want := slices.Sorted(slices.Values(g.TriggeredFailedWorkflowRuns)), tc.expectTriggered; !slices.Equal(got, want) {
				t.Errorf("Expected re-run runs %v, got %v", want, got)
			}
			if errorEntries := countErrorEntries(hook); errorEntries != tc.expectErrors {
				t.Errorf("Expected %d error log entries, got %d", tc.expectErrors, errorEntries)
			}
		})
	}
}
