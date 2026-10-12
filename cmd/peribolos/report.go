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
	"encoding/json"
	"io"
	"sort"
	"sync"

	"sigs.k8s.io/prow/pkg/config/secret"
)

// changeAction enumerates the mutations peribolos performs during a run.
type changeAction string

const (
	actionAdd    changeAction = "add"
	actionUpdate changeAction = "update"
	actionRemove changeAction = "remove"
)

// Change kinds. These mirror the objects peribolos reconciles.
const (
	kindOrgMeta      = "org_meta"
	kindOrgMember    = "org_member"
	kindTeam         = "team"
	kindTeamMember   = "team_member"
	kindTeamRepo     = "team_repo"
	kindRepo         = "repo"
	kindCollaborator = "collaborator"
	kindOrgRoleTeam  = "org_role_team"
	kindOrgRoleUser  = "org_role_user"
)

// change is a single planned or applied mutation. The same value is recorded in
// dry-run and confirm mode: --confirm only gates the mutating API call, not the
// decision to make it, so what the report describes equals what peribolos does.
type change struct {
	// Kind is the type of object changed (see the kind* constants).
	Kind string `json:"kind"`
	// Org is the organization the change belongs to.
	Org string `json:"org"`
	// Scope is the containing object for nested changes: the team name for
	// team_member and team_repo changes, the repo name for collaborator changes,
	// and the role name for org_role_team and org_role_user changes. It is empty
	// for org-level changes.
	Scope string `json:"scope,omitempty"`
	// Target is the object acted on: a login, team name or repo name.
	Target string `json:"target"`
	// Action is add, update or remove.
	Action changeAction `json:"action"`
	// Before and After describe the change. They hold non-sensitive descriptors
	// (roles, permission levels, or the names of changed fields), never
	// credential values. Report output is additionally run through
	// secret.Censor before it is written.
	Before any `json:"before,omitempty"`
	After  any `json:"after,omitempty"`
}

// changeReport accumulates the changes decided during a run. It is safe for
// concurrent use. A nil *changeReport is a valid no-op recorder, so callers (and
// tests) that do not care about reporting may pass nil.
type changeReport struct {
	mu      sync.Mutex
	dryRun  bool
	changes []change
}

func newChangeReport(dryRun bool) *changeReport {
	return &changeReport{dryRun: dryRun}
}

// record appends c. Callers only record real mutations, so no-ops never reach
// here. A nil receiver is a no-op.
func (r *changeReport) record(c change) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}

// membershipRoles returns the (super, member) role labels for a membership kind.
func membershipRoles(kind string) (super, member string) {
	if kind == kindTeamMember {
		return "maintainer", "member"
	}
	return "admin", "member"
}

// recordMemberAdd records the effect of an adder call: an add for a new member,
// or an update when an existing member changes role (e.g. member -> admin). have
// must use normalized logins, matching user.
func (r *changeReport) recordMemberAdd(kind, org, scope, user string, have memberships, super bool) {
	superLabel, memberLabel := membershipRoles(kind)
	after := memberLabel
	if super {
		after = superLabel
	}
	switch {
	case have.super.Has(user):
		r.record(change{Kind: kind, Org: org, Scope: scope, Target: user, Action: actionUpdate, Before: superLabel, After: after})
	case have.members.Has(user):
		r.record(change{Kind: kind, Org: org, Scope: scope, Target: user, Action: actionUpdate, Before: memberLabel, After: after})
	default:
		r.record(change{Kind: kind, Org: org, Scope: scope, Target: user, Action: actionAdd, After: after})
	}
}

// recordMemberRemove records a membership removal, capturing the prior role when
// known. have must use normalized logins, matching user.
func (r *changeReport) recordMemberRemove(kind, org, scope, user string, have memberships) {
	superLabel, memberLabel := membershipRoles(kind)
	var before any
	switch {
	case have.super.Has(user):
		before = superLabel
	case have.members.Has(user):
		before = memberLabel
	}
	r.record(change{Kind: kind, Org: org, Scope: scope, Target: user, Action: actionRemove, Before: before})
}

// reportSchemaVersion identifies the structure of the --report-path JSON.
//
// It is a single major version ("v1", "v2", ...). The forward-compatibility
// contract for consumers follows Terraform's json-format policy:
//
//   - Backward-compatible changes (adding a new field to reportOutput or change,
//     or a new kind/action value) do NOT bump the version. Consumers MUST ignore
//     object properties with unrecognized names so they keep working against
//     newer peribolos versions.
//   - Breaking changes (renaming/removing a field, or changing the meaning of an
//     existing one) bump the major version. Consumers MUST reject a
//     schema_version whose major they do not recognize.
const reportSchemaVersion = "v1"

// reportOutput is the machine-readable form written by --report-path. See
// reportSchemaVersion for the consumer compatibility contract.
type reportOutput struct {
	SchemaVersion string               `json:"schema_version"`
	DryRun        bool                 `json:"dry_run"`
	Summary       map[changeAction]int `json:"summary"`
	Changes       []change             `json:"changes"`
}

// output returns a deterministic snapshot: changes sorted by (kind, org, scope,
// target, action), plus a per-action tally. Determinism comes from this sort,
// never from the order changes were recorded in.
func (r *changeReport) output() reportOutput {
	r.mu.Lock()
	defer r.mu.Unlock()

	sorted := make([]change, len(r.changes))
	copy(sorted, r.changes)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		switch {
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		case a.Org != b.Org:
			return a.Org < b.Org
		case a.Scope != b.Scope:
			return a.Scope < b.Scope
		case a.Target != b.Target:
			return a.Target < b.Target
		default:
			return a.Action < b.Action
		}
	})

	summary := map[changeAction]int{
		actionAdd:    0,
		actionUpdate: 0,
		actionRemove: 0,
	}
	for _, c := range sorted {
		summary[c.Action]++
	}
	return reportOutput{SchemaVersion: reportSchemaVersion, DryRun: r.dryRun, Summary: summary, Changes: sorted}
}

// writeJSONReport writes the machine-readable report. Registered secrets are censored.
func writeJSONReport(w io.Writer, out reportOutput) error {
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	raw = append(secret.Censor(raw), '\n')
	_, err = w.Write(raw)
	return err
}
