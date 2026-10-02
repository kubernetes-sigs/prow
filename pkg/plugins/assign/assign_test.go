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

package assign

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

type fakeClient struct {
	assigned   map[string]int
	unassigned map[string]int

	requested    map[string]int
	unrequested  map[string]int
	contributors map[string]bool
	members      map[string]bool
	labels       []github.Label

	commented      bool
	comment        string
	memberChecks   []string
	memberCheckErr error
	labelCheckErr  error
}

func (c *fakeClient) UnassignIssue(owner, repo string, number int, assignees []string) error {
	for _, who := range assignees {
		c.unassigned[who]++
	}

	return nil
}

func (c *fakeClient) AssignIssue(owner, repo string, number int, assignees []string) error {
	var missing github.MissingUsers
	sort.Strings(assignees)
	if len(assignees) > 10 {
		missing.Users = append(missing.Users, assignees[10:]...)
		for _, who := range assignees[:10] {
			c.assigned[who]++
		}
	} else {
		for _, who := range assignees {
			if who != "evil" {
				c.assigned[who]++
			} else {
				missing.Users = append(missing.Users, who)
			}
		}
	}

	if len(missing.Users) == 0 {
		return nil
	}
	return missing
}

func (c *fakeClient) RequestReview(org, repo string, number int, logins []string) error {
	var missing github.MissingUsers
	for _, user := range logins {
		if c.contributors[user] {
			c.requested[user]++
		} else {
			missing.Users = append(missing.Users, user)
		}
	}
	if len(missing.Users) > 0 {
		return missing
	}
	return nil
}

func (c *fakeClient) UnrequestReview(org, repo string, number int, logins []string) error {
	for _, user := range logins {
		c.unrequested[user]++
	}
	return nil
}

func (c *fakeClient) CreateComment(owner, repo string, number int, comment string) error {
	c.commented = comment != ""
	c.comment = comment
	return nil
}

func (c *fakeClient) GetIssueLabels(org, repo string, number int) ([]github.Label, error) {
	if c.labelCheckErr != nil {
		return nil, c.labelCheckErr
	}
	return c.labels, nil
}

func (c *fakeClient) IsMember(org, user string) (bool, error) {
	c.memberChecks = append(c.memberChecks, user)
	if c.memberCheckErr != nil {
		return false, c.memberCheckErr
	}
	return c.members[user], nil
}

func newFakeClient(contribs []string) *fakeClient {
	c := &fakeClient{
		contributors: make(map[string]bool),
		members:      make(map[string]bool),
		requested:    make(map[string]int),
		unrequested:  make(map[string]int),
		assigned:     make(map[string]int),
		unassigned:   make(map[string]int),
	}
	for _, user := range contribs {
		c.contributors[user] = true
	}
	return c
}

func TestParseLogins(t *testing.T) {
	var testcases = []struct {
		name   string
		text   string
		logins []string
	}{
		{
			name: "empty",
			text: "",
		},
		{
			name:   "one",
			text:   " @jungle",
			logins: []string{"jungle"},
		},
		{
			name:   "two",
			text:   " @erick @fejta",
			logins: []string{"erick", "fejta"},
		},
		{
			name:   "one team",
			text:   " @kubernetes/sig-testing-misc",
			logins: []string{"kubernetes/sig-testing-misc"},
		},
		{
			name:   "two teams",
			text:   " @kubernetes/sig-testing-misc @kubernetes/sig-testing-bugs",
			logins: []string{"kubernetes/sig-testing-misc", "kubernetes/sig-testing-bugs"},
		},
	}
	for _, tc := range testcases {
		l := parseLogins(tc.text)
		if len(l) != len(tc.logins) {
			t.Errorf("For case %s, expected %s and got %s", tc.name, tc.logins, l)
		}
		for n, who := range l {
			if tc.logins[n] != who {
				t.Errorf("For case %s, expected %s and got %s", tc.name, tc.logins, l)
			}
		}
	}
}

// TestAssignAndReview tests that the handle function uses the github client
// to correctly create and/or delete assignments and PR review requests.
func TestAssignAndReview(t *testing.T) {
	var testcases = []struct {
		name        string
		body        string
		commenter   string
		assigned    []string
		unassigned  []string
		requested   []string
		unrequested []string
		commented   bool
	}{
		{
			name:      "unrelated comment",
			body:      "uh oh",
			commenter: "o",
		},
		{
			name:      "assign on open",
			body:      "/assign",
			commenter: "rando",
			assigned:  []string{"rando"},
		},
		{
			name:      "assign me",
			body:      "/assign",
			commenter: "rando",
			assigned:  []string{"rando"},
		},
		{
			name:       "unassign myself",
			body:       "/unassign",
			commenter:  "rando",
			unassigned: []string{"rando"},
		},
		{
			name:      "tab completion",
			body:      "/assign @fejta ",
			commenter: "rando",
			assigned:  []string{"fejta"},
		},
		{
			name:      "no @ works too",
			body:      "/assign fejta",
			commenter: "rando",
			assigned:  []string{"fejta"},
		},
		{
			name:       "multi commands",
			body:       "/assign @fejta\n/unassign @spxtr",
			commenter:  "rando",
			assigned:   []string{"fejta"},
			unassigned: []string{"spxtr"},
		},
		{
			name:      "interesting names",
			body:      "/assign @hello-world @allow_underscore",
			commenter: "rando",
			assigned:  []string{"hello-world", "allow_underscore"},
		},
		{
			name:      "bad login",
			commenter: "rando",
			body:      "/assign @Invalid$User",
		},
		{
			name:      "bad login, no @",
			commenter: "rando",
			body:      "/assign Invalid$User",
		},
		{
			name:      "assign friends",
			body:      "/assign @bert @ernie",
			commenter: "rando",
			assigned:  []string{"bert", "ernie"},
		},
		{
			name:      "assign greater than 10 users",
			body:      "/assign @user1 @user2 @user3 @user4 @user5 @user6 @user7 @user8 @user9 @user10 @user11 @user12 @user13",
			commenter: "rando",
			commented: true,
			assigned:  []string{"user12", "user13", "user6", "user1", "user11", "user2", "user3", "user4", "user5", "user10"},
		},
		{
			name:       "unassign buddies",
			body:       "/unassign @ashitaka @eboshi",
			commenter:  "san",
			unassigned: []string{"ashitaka", "eboshi"},
		},
		{
			name:       "unassign buddies, trailing space.",
			body:       "/unassign @ashitaka @eboshi \r",
			commenter:  "san",
			unassigned: []string{"ashitaka", "eboshi"},
		},
		{
			name:      "evil commenter",
			body:      "/assign @merlin",
			commenter: "evil",
			assigned:  []string{"merlin"},
		},
		{
			name:      "evil commenter self assign",
			body:      "/assign",
			commenter: "evil",
			commented: true,
		},
		{
			name:      "evil assignee",
			body:      "/assign @evil @evil @evil @evil @merlin",
			commenter: "innocent",
			assigned:  []string{"merlin"},
			commented: true,
		},
		{
			name:       "evil unassignee",
			body:       "/unassign @evil @merlin",
			commenter:  "innocent",
			unassigned: []string{"evil", "merlin"},
		},
		{
			name:      "review on open",
			body:      "/cc @merlin",
			commenter: "rando",
			requested: []string{"merlin"},
		},
		{
			name:      "tab completion",
			body:      "/cc @cjwagner ",
			commenter: "rando",
			requested: []string{"cjwagner"},
		},
		{
			name:      "no @ works too",
			body:      "/cc cjwagner ",
			commenter: "rando",
			requested: []string{"cjwagner"},
		},
		{
			name:        "multi commands",
			body:        "/cc @cjwagner\n/uncc @spxtr",
			commenter:   "rando",
			requested:   []string{"cjwagner"},
			unrequested: []string{"spxtr"},
		},
		{
			name:      "interesting names",
			body:      "/cc @hello-world @allow_underscore",
			commenter: "rando",
			requested: []string{"hello-world", "allow_underscore"},
		},
		{
			name:      "bad login",
			commenter: "rando",
			body:      "/cc @Invalid$User",
		},
		{
			name:      "bad login",
			commenter: "rando",
			body:      "/cc Invalid$User",
		},
		{
			name:      "request multiple",
			body:      "/cc @cjwagner @merlin",
			commenter: "rando",
			requested: []string{"cjwagner", "merlin"},
		},
		{
			name:        "unrequest buddies",
			body:        "/uncc @ashitaka @eboshi",
			commenter:   "san",
			unrequested: []string{"ashitaka", "eboshi"},
		},
		{
			name:      "evil commenter",
			body:      "/cc @merlin",
			commenter: "evil",
			requested: []string{"merlin"},
		},
		{
			name:      "evil reviewer requested",
			body:      "/cc @evil @merlin",
			commenter: "innocent",
			requested: []string{"merlin"},
			commented: true,
		},
		{
			name:        "evil reviewer unrequested",
			body:        "/uncc @evil @merlin",
			commenter:   "innocent",
			unrequested: []string{"evil", "merlin"},
		},
		{
			name:        "multi command types",
			body:        "/assign @fejta\n/unassign @spxtr @cjwagner\n/uncc @merlin \n/cc @cjwagner",
			commenter:   "rando",
			assigned:    []string{"fejta"},
			unassigned:  []string{"spxtr", "cjwagner"},
			requested:   []string{"cjwagner"},
			unrequested: []string{"merlin"},
		},
		{
			name:      "request review self",
			body:      "/cc",
			commenter: "cjwagner",
			requested: []string{"cjwagner"},
		},
		{
			name:        "unrequest review self",
			body:        "/uncc",
			commenter:   "cjwagner",
			unrequested: []string{"cjwagner"},
		},
		{
			name:        "request review self, with unrequest friend, with trailing space.",
			body:        "/cc \n/uncc @spxtr ",
			commenter:   "cjwagner",
			requested:   []string{"cjwagner"},
			unrequested: []string{"spxtr"},
		},
		{
			name:      "request team review",
			body:      "/cc @kubernetes/sig-testing-misc",
			commenter: "rando",
			requested: []string{"kubernetes/sig-testing-misc"},
		},
		{
			name:        "unrequest team review",
			body:        "/uncc @kubernetes/sig-testing-misc",
			commenter:   "rando",
			unrequested: []string{"kubernetes/sig-testing-misc"},
		},
	}
	for _, tc := range testcases {
		fc := newFakeClient([]string{"hello-world", "allow_underscore", "cjwagner", "merlin", "kubernetes/sig-testing-misc"})
		e := github.GenericCommentEvent{
			Body:   tc.body,
			User:   github.User{Login: tc.commenter},
			Repo:   github.Repo{Name: "repo", Owner: github.User{Login: "org"}},
			Number: 5,
		}
		if err := handle(newAssignHandler(e, fc, logrus.WithField("plugin", pluginName), &plugins.Assign{})); err != nil {
			t.Errorf("For case %s, didn't expect error from handle: %v", tc.name, err)
			continue
		}
		if err := handle(newReviewHandler(e, fc, logrus.WithField("plugin", pluginName))); err != nil {
			t.Errorf("For case %s, didn't expect error from handle: %v", tc.name, err)
			continue
		}

		if tc.commented != fc.commented {
			t.Errorf("For case %s, expect commented: %v, got commented %v", tc.name, tc.commented, fc.commented)
		}

		if len(fc.assigned) != len(tc.assigned) {
			t.Errorf("For case %s, assigned actual %v != expected %s", tc.name, fc.assigned, tc.assigned)
		} else {
			for _, who := range tc.assigned {
				if n, ok := fc.assigned[who]; !ok || n < 1 {
					t.Errorf("For case %s, assigned actual %v != expected %s", tc.name, fc.assigned, tc.assigned)
					break
				}
			}
		}
		if len(fc.unassigned) != len(tc.unassigned) {
			t.Errorf("For case %s, unassigned %v != %s", tc.name, fc.unassigned, tc.unassigned)
		} else {
			for _, who := range tc.unassigned {
				if n, ok := fc.unassigned[who]; !ok || n < 1 {
					t.Errorf("For case %s, unassigned %v != %s", tc.name, fc.unassigned, tc.unassigned)
					break
				}
			}
		}

		if len(fc.requested) != len(tc.requested) {
			t.Errorf("For case %s, requested actual %v != expected %s", tc.name, fc.requested, tc.requested)
		} else {
			for _, who := range tc.requested {
				if n, ok := fc.requested[who]; !ok || n < 1 {
					t.Errorf("For case %s, requested actual %v != expected %s", tc.name, fc.requested, tc.requested)
					break
				}
			}
		}
		if len(fc.unrequested) != len(tc.unrequested) {
			t.Errorf("For case %s, unrequested %v != %s", tc.name, fc.unrequested, tc.unrequested)
		} else {
			for _, who := range tc.unrequested {
				if n, ok := fc.unrequested[who]; !ok || n < 1 {
					t.Errorf("For case %s, unrequested %v != %s", tc.name, fc.unrequested, tc.unrequested)
					break
				}
			}
		}
	}
}

func TestAssignRestrict(t *testing.T) {
	block := &plugins.Assign{Restrict: &plugins.AssignRestrict{Action: plugins.AssignActionBlock}}
	warn := &plugins.Assign{Restrict: &plugins.AssignRestrict{Action: plugins.AssignActionWarn}}
	blockExempt := &plugins.Assign{Restrict: &plugins.AssignRestrict{Action: plugins.AssignActionBlock, ExemptLabels: []string{"good first issue"}}}
	warnExempt := &plugins.Assign{Restrict: &plugins.AssignRestrict{Action: plugins.AssignActionWarn, ExemptLabels: []string{"good first issue"}}}

	const (
		blockComment = "Only [org members](https://github.com/orgs/org/people) can use `/assign` on this issue."
		warnComment  = "Note that you are not a member of the **org** organization."
	)

	testcases := []struct {
		name           string
		body           string
		commenter      string
		isPR           bool
		cfg            *plugins.Assign
		labels         []github.Label
		memberCheckErr error
		labelCheckErr  error

		assigned       []string
		unassigned     []string
		commentContain string
		// memberChecks lists the users whose org membership is looked up.
		memberChecks []string
		expectErr    bool
	}{
		// No restriction configured: behavior is unchanged.
		{
			name:      "no restriction: non-member self-assigns",
			body:      "/assign",
			commenter: "outsider",
			cfg:       &plugins.Assign{},
			assigned:  []string{"outsider"},
		},
		// Pull requests are never restricted.
		{
			name:      "block, PR: non-member assigns an approver",
			body:      "/assign @member1",
			commenter: "outsider",
			isPR:      true,
			cfg:       block,
			assigned:  []string{"member1"},
		},
		{
			name:      "block, PR: non-member self-assigns",
			body:      "/assign",
			commenter: "outsider",
			isPR:      true,
			cfg:       block,
			assigned:  []string{"outsider"},
		},
		{
			name:      "warn, PR: non-member self-assigns without a warning",
			body:      "/assign",
			commenter: "outsider",
			isPR:      true,
			cfg:       warn,
			assigned:  []string{"outsider"},
		},
		// Org members are not restricted.
		{
			name:         "block: member self-assigns",
			body:         "/assign",
			commenter:    "member1",
			cfg:          block,
			assigned:     []string{"member1"},
			memberChecks: []string{"member1"},
		},
		{
			name:         "warn: member self-assigns without a warning",
			body:         "/assign",
			commenter:    "member1",
			cfg:          warn,
			assigned:     []string{"member1"},
			memberChecks: []string{"member1"},
		},
		{
			name:         "block: member assigns a non-member",
			body:         "/assign @outsider",
			commenter:    "member1",
			cfg:          block,
			assigned:     []string{"outsider"},
			memberChecks: []string{"member1"},
		},
		{
			name:         "warn: member assigns a non-member without a warning",
			body:         "/assign @outsider",
			commenter:    "member1",
			cfg:          warn,
			assigned:     []string{"outsider"},
			memberChecks: []string{"member1"},
		},
		{
			name:         "block: member assigns several users with one membership check",
			body:         "/assign @member2 @outsider @outsider2",
			commenter:    "member1",
			cfg:          block,
			assigned:     []string{"member2", "outsider", "outsider2"},
			memberChecks: []string{"member1"},
		},
		{
			name:           "block: users GitHub rejects are still reported for a member",
			body:           "/assign @evil",
			commenter:      "member1",
			cfg:            block,
			commentContain: "GitHub didn't allow me to assign the following users: evil.",
			memberChecks:   []string{"member1"},
		},
		{
			name:         "block: member on an issue without an exempt label",
			body:         "/assign",
			commenter:    "member1",
			cfg:          blockExempt,
			labels:       []github.Label{{Name: "kind/bug"}},
			assigned:     []string{"member1"},
			memberChecks: []string{"member1"},
		},
		// Non-members are restricted on issues.
		{
			name:           "block: non-member self-assign is rejected",
			body:           "/assign",
			commenter:      "outsider",
			cfg:            block,
			commentContain: blockComment,
			memberChecks:   []string{"outsider"},
		},
		{
			name:           "warn: non-member self-assign is assigned with a nudge",
			body:           "/assign",
			commenter:      "outsider",
			cfg:            warn,
			assigned:       []string{"outsider"},
			commentContain: warnComment,
			memberChecks:   []string{"outsider"},
		},
		{
			name:           "block: non-member assigns a member is rejected",
			body:           "/assign @member1",
			commenter:      "outsider",
			cfg:            block,
			commentContain: blockComment,
			memberChecks:   []string{"outsider"},
		},
		{
			name:           "warn: non-member assigns a member with a nudge",
			body:           "/assign @member1",
			commenter:      "outsider",
			cfg:            warn,
			assigned:       []string{"member1"},
			commentContain: warnComment,
			memberChecks:   []string{"outsider"},
		},
		{
			name:           "block: non-member assigns several users, nobody is assigned",
			body:           "/assign @member1 @outsider",
			commenter:      "outsider",
			cfg:            block,
			commentContain: blockComment,
			memberChecks:   []string{"outsider"},
		},
		{
			name:       "block: unassign is not restricted",
			body:       "/unassign",
			commenter:  "outsider",
			cfg:        block,
			unassigned: []string{"outsider"},
		},
		{
			name:       "warn: unassign is not restricted",
			body:       "/unassign @member1",
			commenter:  "outsider",
			cfg:        warn,
			unassigned: []string{"member1"},
		},
		// Exempt labels.
		{
			name:      "block: exempt label lets a non-member self-assign",
			body:      "/assign",
			commenter: "outsider",
			cfg:       blockExempt,
			labels:    []github.Label{{Name: "good first issue"}},
			assigned:  []string{"outsider"},
		},
		{
			name:      "block: exempt label matches case-insensitively",
			body:      "/assign",
			commenter: "outsider",
			cfg:       blockExempt,
			labels:    []github.Label{{Name: "Good First Issue"}},
			assigned:  []string{"outsider"},
		},
		{
			name:      "warn: exempt label has no nudge",
			body:      "/assign",
			commenter: "outsider",
			cfg:       warnExempt,
			labels:    []github.Label{{Name: "good first issue"}},
			assigned:  []string{"outsider"},
		},
		{
			name:           "block: other labels do not exempt the issue",
			body:           "/assign",
			commenter:      "outsider",
			cfg:            blockExempt,
			labels:         []github.Label{{Name: "help wanted"}},
			commentContain: "can use `/assign` on this issue, unless it has one of the following labels: `good first issue`.",
			memberChecks:   []string{"outsider"},
		},
		{
			name:           "warn: exempt labels are suggested in the nudge",
			body:           "/assign",
			commenter:      "outsider",
			cfg:            warnExempt,
			labels:         []github.Label{{Name: "kind/bug"}},
			assigned:       []string{"outsider"},
			commentContain: "this issue has not been marked as available for new contributors. If you are new to this project, issues labeled `good first issue` are a good place to start.",
			memberChecks:   []string{"outsider"},
		},
		// Errors: nobody is assigned and the error is returned.
		{
			name:           "warn: IsMember error propagates and nobody is assigned",
			body:           "/assign",
			commenter:      "someone",
			cfg:            warn,
			memberCheckErr: errors.New("api rate limit"),
			memberChecks:   []string{"someone"},
			expectErr:      true,
		},
		{
			name:           "block: IsMember error propagates and nobody is assigned",
			body:           "/assign @member1",
			commenter:      "someone",
			cfg:            block,
			memberCheckErr: errors.New("api rate limit"),
			memberChecks:   []string{"someone"},
			expectErr:      true,
		},
		{
			name:          "GetIssueLabels error propagates and nobody is assigned",
			body:          "/assign",
			commenter:     "someone",
			cfg:           blockExempt,
			labelCheckErr: errors.New("api error"),
			expectErr:     true,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeClient(nil)
			for _, m := range []string{"member1", "member2", "evil"} {
				fc.members[m] = true
			}
			fc.labels = tc.labels
			fc.memberCheckErr = tc.memberCheckErr
			fc.labelCheckErr = tc.labelCheckErr
			e := github.GenericCommentEvent{
				Body:   tc.body,
				User:   github.User{Login: tc.commenter},
				Repo:   github.Repo{Name: "repo", Owner: github.User{Login: "org"}},
				Number: 5,
				IsPR:   tc.isPR,
			}
			err := handle(newAssignHandler(e, fc, logrus.WithField("plugin", pluginName), tc.cfg))
			if tc.expectErr && err == nil {
				t.Error("expected error but got none")
			}
			if !tc.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.memberChecks, fc.memberChecks); diff != "" {
				t.Errorf("unexpected org membership checks (-want +got):\n%s", diff)
			}
			if tc.commentContain == "" && fc.commented {
				t.Errorf("expected no comment, got %q", fc.comment)
			}
			if tc.commentContain != "" && !strings.Contains(fc.comment, tc.commentContain) {
				t.Errorf("expected comment to contain %q, got %q", tc.commentContain, fc.comment)
			}
			if diff := cmp.Diff(sets.New(tc.assigned...), sets.KeySet(fc.assigned)); diff != "" {
				t.Errorf("unexpected assignees (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(sets.New(tc.unassigned...), sets.KeySet(fc.unassigned)); diff != "" {
				t.Errorf("unexpected unassigned users (-want +got):\n%s", diff)
			}
		})
	}
}
