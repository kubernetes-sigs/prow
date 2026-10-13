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

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/prow/pkg/config/org"
	"sigs.k8s.io/prow/pkg/github"
)

// TestChangeReportNilSafe verifies that a nil *changeReport is a usable no-op
// recorder, so callers that do not care about reporting can pass nil.
func TestChangeReportNilSafe(t *testing.T) {
	var r *changeReport
	// None of these must panic.
	r.record(change{Kind: kindOrgMember, Org: "o", Target: "u", Action: actionAdd})
	r.recordMemberAdd(kindOrgMember, "o", "", "u", memberships{}, true)
	r.recordMemberRemove(kindOrgMember, "o", "", "u", memberships{})
	called := false
	if err := r.recordOnSuccess(change{Kind: kindTeam, Org: "o", Target: "t", Action: actionRemove}, func() error {
		called = true
		return nil
	}); err != nil {
		t.Errorf("recordOnSuccess on nil receiver returned %v, want nil", err)
	}
	if !called {
		t.Error("recordOnSuccess must still run the call on a nil receiver")
	}
}

// TestRecordOnSuccess pins the recording rule: the change is recorded iff the
// wrapped call returns nil, and the call's error is passed through.
func TestRecordOnSuccess(t *testing.T) {
	t.Run("records on success", func(t *testing.T) {
		r := newChangeReport(false)
		c := change{Kind: kindTeam, Org: "o", Target: "t", Action: actionRemove}
		if err := r.recordOnSuccess(c, func() error { return nil }); err != nil {
			t.Fatalf("recordOnSuccess returned %v, want nil", err)
		}
		if diff := cmp.Diff([]change{c}, r.output().Changes); diff != "" {
			t.Errorf("recorded changes mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("does not record on error", func(t *testing.T) {
		r := newChangeReport(false)
		wantErr := errors.New("boom")
		c := change{Kind: kindTeam, Org: "o", Target: "t", Action: actionRemove}
		if err := r.recordOnSuccess(c, func() error { return wantErr }); err != wantErr {
			t.Fatalf("recordOnSuccess returned %v, want %v", err, wantErr)
		}
		if got := r.output().Changes; len(got) != 0 {
			t.Errorf("recorded %v on a failed call, want nothing", got)
		}
	})
}

func TestChangeReportOutput(t *testing.T) {
	r := newChangeReport(true)
	// Record out of order and across kinds; output must be deterministically sorted.
	r.record(change{Kind: kindTeam, Org: "o", Target: "zebra", Action: actionRemove})
	r.record(change{Kind: kindOrgMember, Org: "o", Target: "bob", Action: actionRemove, Before: "admin"})
	r.record(change{Kind: kindOrgMember, Org: "o", Target: "alice", Action: actionAdd, After: "member"})
	r.record(change{Kind: kindTeam, Org: "o", Target: "apple", Action: actionAdd})

	out := r.output()

	if !out.DryRun {
		t.Errorf("DryRun = false, want true")
	}
	wantSummary := map[changeAction]int{actionAdd: 2, actionUpdate: 0, actionRemove: 2}
	if diff := cmp.Diff(wantSummary, out.Summary); diff != "" {
		t.Errorf("summary mismatch (-want +got):\n%s", diff)
	}
	want := []change{
		{Kind: kindOrgMember, Org: "o", Target: "alice", Action: actionAdd, After: "member"},
		{Kind: kindOrgMember, Org: "o", Target: "bob", Action: actionRemove, Before: "admin"},
		{Kind: kindTeam, Org: "o", Target: "apple", Action: actionAdd},
		{Kind: kindTeam, Org: "o", Target: "zebra", Action: actionRemove},
	}
	if diff := cmp.Diff(want, out.Changes); diff != "" {
		t.Errorf("changes not sorted as expected (-want +got):\n%s", diff)
	}
}

func TestRecordMemberAdd(t *testing.T) {
	// have: carol is a plain member, dave is a super (admin/maintainer).
	have := memberships{members: sets.New("carol"), super: sets.New("dave")}

	cases := []struct {
		name     string
		kind     string
		user     string
		super    bool
		want     change
		wantNone bool // expect nothing recorded (a no-op)
	}{
		{
			name: "new org member",
			kind: kindOrgMember, user: "erin", super: false,
			want: change{Kind: kindOrgMember, Org: "o", Target: "erin", Action: actionAdd, After: "member"},
		},
		{
			name: "member already member is a no-op",
			kind: kindOrgMember, user: "carol", super: false,
			wantNone: true,
		},
		{
			name: "admin already admin is a no-op",
			kind: kindOrgMember, user: "dave", super: true,
			wantNone: true,
		},
		{
			name: "new org admin",
			kind: kindOrgMember, user: "erin", super: true,
			want: change{Kind: kindOrgMember, Org: "o", Target: "erin", Action: actionAdd, After: "admin"},
		},
		{
			name: "promote member to admin",
			kind: kindOrgMember, user: "carol", super: true,
			want: change{Kind: kindOrgMember, Org: "o", Target: "carol", Action: actionUpdate, Before: "member", After: "admin"},
		},
		{
			name: "demote admin to member",
			kind: kindOrgMember, user: "dave", super: false,
			want: change{Kind: kindOrgMember, Org: "o", Target: "dave", Action: actionUpdate, Before: "admin", After: "member"},
		},
		{
			name: "new team maintainer uses team role labels",
			kind: kindTeamMember, user: "erin", super: true,
			want: change{Kind: kindTeamMember, Org: "o", Scope: "t", Target: "erin", Action: actionAdd, After: "maintainer"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newChangeReport(true)
			scope := ""
			if tc.kind == kindTeamMember {
				scope = "t"
			}
			r.recordMemberAdd(tc.kind, "o", scope, tc.user, have, tc.super)
			want := []change{}
			if !tc.wantNone {
				want = []change{tc.want}
			}
			if diff := cmp.Diff(want, r.output().Changes); diff != "" {
				t.Errorf("recordMemberAdd mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRecordMemberRemove(t *testing.T) {
	have := memberships{members: sets.New("carol"), super: sets.New("dave")}
	cases := []struct {
		name string
		user string
		want change
	}{
		{name: "remove admin captures prior role", user: "dave", want: change{Kind: kindOrgMember, Org: "o", Target: "dave", Action: actionRemove, Before: "admin"}},
		{name: "remove member captures prior role", user: "carol", want: change{Kind: kindOrgMember, Org: "o", Target: "carol", Action: actionRemove, Before: "member"}},
		{name: "remove unknown has no prior role", user: "ghost", want: change{Kind: kindOrgMember, Org: "o", Target: "ghost", Action: actionRemove}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newChangeReport(true)
			r.recordMemberRemove(kindOrgMember, "o", "", tc.user, have)
			if diff := cmp.Diff([]change{tc.want}, r.output().Changes); diff != "" {
				t.Errorf("recordMemberRemove mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	r := newChangeReport(false)
	r.record(change{Kind: kindRepo, Org: "o", Target: "repo1", Action: actionUpdate, ChangedFields: []string{"has_issues"}})

	var buf bytes.Buffer
	if err := writeJSONReport(&buf, r.output()); err != nil {
		t.Fatalf("writeJSONReport: %v", err)
	}

	var got struct {
		SchemaVersion string         `json:"schema_version"`
		DryRun        bool           `json:"dry_run"`
		Summary       map[string]int `json:"summary"`
		Changes       []struct {
			Kind          string   `json:"kind"`
			Org           string   `json:"org"`
			Target        string   `json:"target"`
			Action        string   `json:"action"`
			ChangedFields []string `json:"changed_fields"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal report JSON: %v\n%s", err, buf.String())
	}
	if got.SchemaVersion != reportSchemaVersion {
		t.Errorf("schema_version = %q, want %q", got.SchemaVersion, reportSchemaVersion)
	}
	if got.DryRun {
		t.Errorf("dry_run = true, want false")
	}
	if got.Summary["update"] != 1 || got.Summary["add"] != 0 || got.Summary["remove"] != 0 {
		t.Errorf("unexpected summary: %v", got.Summary)
	}
	if len(got.Changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(got.Changes))
	}
	c := got.Changes[0]
	if c.Kind != kindRepo || c.Target != "repo1" || c.Action != string(actionUpdate) {
		t.Errorf("unexpected change: %+v", c)
	}
	if diff := cmp.Diff([]string{"has_issues"}, c.ChangedFields); diff != "" {
		t.Errorf("changed_fields mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigureTeamRecordsChangedFields covers that a team update records the
// changed field names, and that clearing a description to empty records nothing
// (github.Team.Description is omitempty, so EditTeam cannot apply it).
func TestConfigureTeamRecordsChangedFields(t *testing.T) {
	desc := "new description"
	empty := ""
	cases := []struct {
		name   string
		config org.Team
		github github.Team
		want   []change
	}{
		{
			name:   "description change recorded as changed_fields",
			config: org.Team{TeamMetadata: org.TeamMetadata{Description: &desc}},
			github: github.Team{ID: 1, Name: "t", Slug: "t", Description: "old"},
			want:   []change{{Kind: kindTeam, Org: fakeOrg, Target: "t", Action: actionUpdate, ChangedFields: []string{"description"}}},
		},
		{
			name:   "clearing description to empty records nothing",
			config: org.Team{TeamMetadata: org.TeamMetadata{Description: &empty}},
			github: github.Team{ID: 2, Name: "t", Slug: "t", Description: "old"},
			want:   []change{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := makeFakeTeamClient(tc.github)
			report := newChangeReport(true)
			if _, err := configureTeam(fc, fakeOrg, tc.github.Name, tc.config, tc.github, nil, report); err != nil {
				t.Fatalf("configureTeam: %v", err)
			}
			if diff := cmp.Diff(tc.want, report.output().Changes); diff != "" {
				t.Errorf("recorded changes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestConfigureTeamReposRecordsChanges is an end-to-end check that configureTeamRepos
// records add/update/remove changes with the correct before/after permissions.
func TestConfigureTeamReposRecordsChanges(t *testing.T) {
	client := &fakeTeamRepoClient{repos: map[string][]github.Repo{"team": {
		{Name: "keep", Permissions: github.RepoPermissions{Pull: true}},                              // read, unchanged
		{Name: "promote", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}}, // write -> admin
		{Name: "obsolete", Permissions: github.RepoPermissions{Pull: true}},                          // read, removed
	}}}
	team := org.Team{Repos: map[string]github.RepoPermissionLevel{
		"keep":    github.Read,
		"promote": github.Admin,
		"new":     github.Write,
	}}

	report := newChangeReport(true)
	if err := configureTeamRepos(client, map[string]github.Team{"team": {ID: 1, Slug: "team"}}, "team", "org", team, report); err != nil {
		t.Fatalf("configureTeamRepos: %v", err)
	}

	want := []change{
		{Kind: kindTeamRepo, Org: "org", Scope: "team", Target: "new", Action: actionAdd, After: string(github.Write)},
		{Kind: kindTeamRepo, Org: "org", Scope: "team", Target: "obsolete", Action: actionRemove, Before: string(github.Read)},
		{Kind: kindTeamRepo, Org: "org", Scope: "team", Target: "promote", Action: actionUpdate, Before: string(github.Write), After: string(github.Admin)},
	}
	if diff := cmp.Diff(want, report.output().Changes); diff != "" {
		t.Errorf("recorded team_repo changes mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigureCollaboratorsRecordsChanges is an end-to-end check that
// configureCollaborators records add/update/remove changes for direct collaborators.
func TestConfigureCollaboratorsRecordsChanges(t *testing.T) {
	client := &fakeCollaboratorClient{
		collaborators: map[string]github.RepoPermissionLevel{
			"keep":     github.Read,  // unchanged
			"promote":  github.Read,  // read -> write
			"obsolete": github.Write, // removed
		},
		members: sets.New[string](),
	}
	repo := org.Repo{Collaborators: map[string]github.RepoPermissionLevel{
		"keep":    github.Read,
		"promote": github.Write,
		// Mixed case in config; the report Target must be the normalized login.
		"New-Collab": github.Admin,
	}}

	report := newChangeReport(true)
	if err := configureCollaborators(client, "test-org", "test-repo", repo, report); err != nil {
		t.Fatalf("configureCollaborators: %v", err)
	}

	want := []change{
		{Kind: kindCollaborator, Org: "test-org", Scope: "test-repo", Target: "new-collab", Action: actionAdd, After: string(github.Admin)},
		{Kind: kindCollaborator, Org: "test-org", Scope: "test-repo", Target: "obsolete", Action: actionRemove, Before: string(github.Write)},
		{Kind: kindCollaborator, Org: "test-org", Scope: "test-repo", Target: "promote", Action: actionUpdate, Before: string(github.Read), After: string(github.Write)},
	}
	if diff := cmp.Diff(want, report.output().Changes); diff != "" {
		t.Errorf("recorded collaborator changes mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigureOrgMembersRecordsChanges is an end-to-end check that
// configureOrgMembers records add/update/remove through the real reconciliation
// path, exercising the add-vs-update classification (promotion/demotion) that
// recordMemberAdd derives from the current membership. Recording follows a
// successful mutation (see the change type's rule); the fake client's calls
// succeed, so every decided change is recorded. In dry-run the real client is a
// no-op that returns nil, so the dry-run report is the same.
func TestConfigureOrgMembersRecordsChanges(t *testing.T) {
	client := &fakeClient{
		admins:     sets.New("stay-admin", "demote-me"),
		members:    sets.New("stay-member", "promote-me", "remove-me"),
		removed:    sets.Set[string]{},
		newAdmins:  sets.Set[string]{},
		newMembers: sets.Set[string]{},
	}
	cfg := org.Config{
		Admins:  []string{"stay-admin", "promote-me"},
		Members: []string{"stay-member", "demote-me"},
	}

	report := newChangeReport(true)
	opt := options{maximumDelta: 1}
	if err := configureOrgMembers(opt, client, "org", cfg, sets.New[string](), nil, report); err != nil {
		t.Fatalf("configureOrgMembers: %v", err)
	}

	want := []change{
		{Kind: kindOrgMember, Org: "org", Target: "demote-me", Action: actionUpdate, Before: "admin", After: "member"},
		{Kind: kindOrgMember, Org: "org", Target: "promote-me", Action: actionUpdate, Before: "member", After: "admin"},
		{Kind: kindOrgMember, Org: "org", Target: "remove-me", Action: actionRemove, Before: "member"},
	}
	if diff := cmp.Diff(want, report.output().Changes); diff != "" {
		t.Errorf("recorded org member changes mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigureOrgMembersDoesNotRecordSwallowed404 verifies that an add whose
// UpdateOrgMembership PUT 404s (a deleted account or a typo'd login, which
// configureOrgMembers swallows without failing the run) is NOT recorded, so a
// confirm-mode report does not claim an applied change that never happened. A
// real add in the same run is still recorded.
func TestConfigureOrgMembersDoesNotRecordSwallowed404(t *testing.T) {
	client := &fakeClient{
		admins:     sets.New("stay-admin"),
		members:    sets.New("stay-member"),
		removed:    sets.Set[string]{},
		newAdmins:  sets.Set[string]{},
		newMembers: sets.Set[string]{},
	}
	cfg := org.Config{
		Admins:  []string{"stay-admin"},
		Members: []string{"stay-member", "realadd", "ghost"},
	}

	report := newChangeReport(false) // confirm mode
	opt := options{maximumDelta: 1}
	if err := configureOrgMembers(opt, client, "org", cfg, sets.New[string](), nil, report); err != nil {
		t.Fatalf("configureOrgMembers: %v", err)
	}

	// Only the real add is recorded; "ghost" (swallowed 404) is not.
	want := []change{
		{Kind: kindOrgMember, Org: "org", Target: "realadd", Action: actionAdd, After: "member"},
	}
	if diff := cmp.Diff(want, report.output().Changes); diff != "" {
		t.Errorf("recorded org member changes mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigureTeamMembersRecordsChanges is an end-to-end check that
// configureTeamMembers records add/update/remove through the real reconciliation
// path. It also verifies that the current membership is matched after login
// normalization (GitHub returns mixed-case logins) so a role change is reported
// as an update, not an add. Like the org-member path, recording follows a
// successful mutation; the fake client succeeds, so in dry-run (a no-op client
// returning nil) the same changes are recorded.
func TestConfigureTeamMembersRecordsChanges(t *testing.T) {
	client := &fakeClient{
		// Current state (GitHub casing): maintainers come from admins, members from
		// members. ListTeamMembersBySlug returns admins for RoleMaintainer.
		admins:     sets.New("Stay-Maintainer", "Demote-Me"),
		members:    sets.New("stay-member", "Promote-Me", "remove-me"),
		removed:    sets.Set[string]{},
		newAdmins:  sets.Set[string]{},
		newMembers: sets.Set[string]{},
	}
	gt := github.Team{Slug: configuredTeamSlug, Name: "t"}
	team := org.Team{
		Maintainers: []string{"stay-maintainer", "promote-me"},
		Members:     []string{"stay-member", "demote-me"},
	}

	// The config team name differs from the GitHub slug so the test pins that
	// team_member scope is the team name (matching team_repo and the change.Scope
	// contract), not the slug.
	const teamName = "Team Name"
	report := newChangeReport(true)
	if err := configureTeamMembers(client, "org", teamName, gt, team, true /*ignoreInvitees*/, report); err != nil {
		t.Fatalf("configureTeamMembers: %v", err)
	}

	want := []change{
		{Kind: kindTeamMember, Org: "org", Scope: teamName, Target: "demote-me", Action: actionUpdate, Before: "maintainer", After: "member"},
		{Kind: kindTeamMember, Org: "org", Scope: teamName, Target: "promote-me", Action: actionUpdate, Before: "member", After: "maintainer"},
		{Kind: kindTeamMember, Org: "org", Scope: teamName, Target: "remove-me", Action: actionRemove, Before: "member"},
	}
	if diff := cmp.Diff(want, report.output().Changes); diff != "" {
		t.Errorf("recorded team member changes mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigureOrgRolesRecordsChanges is an end-to-end check that configureOrgRoles
// records add/remove for both team and user role assignments (scope is the role
// name), and that a user with a pending org invitation is skipped rather than
// recorded, mirroring the assignment logic. Recording follows a successful
// mutation; the fake client succeeds, so in dry-run (a no-op client returning
// nil) the same changes are recorded.
func TestConfigureOrgRolesRecordsChanges(t *testing.T) {
	client := &fakeOrgRolesClient{
		roles: []github.OrganizationRole{{ID: 1, Name: "security-manager"}},
		teamsWithRole: map[int][]github.OrganizationRoleAssignment{
			1: {
				{Slug: "keep-team", Assignment: "direct"},     // wanted, unchanged
				{Slug: "obsolete-team", Assignment: "direct"}, // not wanted, removed
			},
		},
		usersWithRole: map[int][]github.OrganizationRoleAssignment{
			1: {
				{Login: "keepuser", Assignment: "direct"},     // wanted, unchanged
				{Login: "obsoleteuser", Assignment: "direct"}, // not wanted, removed
			},
		},
	}
	orgConfig := org.Config{
		Roles: map[string]org.Role{
			"security-manager": {
				Teams: []string{"KeepTeam", "NewTeam"},
				Users: []string{"keepuser", "newuser", "pendinguser"},
			},
		},
	}
	githubTeams := map[string]github.Team{
		"KeepTeam": {Slug: "keep-team"},
		"NewTeam":  {Slug: "new-team"},
	}
	// pendinguser has an unaccepted org invitation, so the assignment is deferred and
	// must NOT be recorded.
	invitees := sets.New("pendinguser")

	report := newChangeReport(true)
	if err := configureOrgRoles(client, "test-org", orgConfig, githubTeams, nil, nil, invitees, report); err != nil {
		t.Fatalf("configureOrgRoles: %v", err)
	}

	want := []change{
		{Kind: kindOrgRoleTeam, Org: "test-org", Scope: "security-manager", Target: "new-team", Action: actionAdd},
		{Kind: kindOrgRoleTeam, Org: "test-org", Scope: "security-manager", Target: "obsolete-team", Action: actionRemove},
		{Kind: kindOrgRoleUser, Org: "test-org", Scope: "security-manager", Target: "newuser", Action: actionAdd},
		{Kind: kindOrgRoleUser, Org: "test-org", Scope: "security-manager", Target: "obsoleteuser", Action: actionRemove},
	}
	if diff := cmp.Diff(want, report.output().Changes); diff != "" {
		t.Errorf("recorded org role changes mismatch (-want +got):\n%s", diff)
	}
}

// TestChangedRepoFields verifies that changedRepoFields derives json field names
// from the RepoUpdateRequest struct by reflection, including fields on the
// embedded RepoRequest, so the report cannot drift from the struct definition.
func TestChangedRepoFields(t *testing.T) {
	name, desc := "n", "d"
	private, archived := true, false
	delta := github.RepoUpdateRequest{
		RepoRequest: github.RepoRequest{Name: &name, Description: &desc, Private: &private},
		Archived:    &archived,
	}
	// Order follows struct definition: embedded RepoRequest fields first, then
	// RepoUpdateRequest's own fields.
	want := []string{"name", "description", "private", "archived"}
	if diff := cmp.Diff(want, changedRepoFields(delta)); diff != "" {
		t.Errorf("changedRepoFields mismatch (-want +got):\n%s", diff)
	}
	if got := changedRepoFields(github.RepoUpdateRequest{}); len(got) != 0 {
		t.Errorf("empty delta should report no fields, got %v", got)
	}
}
