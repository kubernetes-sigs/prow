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

package main

import (
	"errors"
	"flag"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/prow/pkg/config/org"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
)

func TestOptions(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected *options
	}{
		{
			name: "missing --config",
			args: []string{},
		},
		{
			name: "bad --github-endpoint",
			args: []string{"--config-path=foo", "--github-endpoint=ht!tp://:dumb"},
		},
		{
			name: "--minAdmins too low",
			args: []string{"--config-path=foo", "--min-admins=1"},
		},
		{
			name: "--maximum-removal-delta too high",
			args: []string{"--config-path=foo", "--maximum-removal-delta=1.1"},
		},
		{
			name: "--maximum-removal-delta too low",
			args: []string{"--config-path=foo", "--maximum-removal-delta=-0.1"},
		},
		{
			name: "reject --dump-full-config without --dump",
			args: []string{"--config-path=foo", "--dump-full-config"},
		},
		{
			name: "maximal delta",
			args: []string{"--config-path=foo", "--maximum-removal-delta=1"},
			expected: &options{
				config:       "foo",
				minAdmins:    defaultMinAdmins,
				requireSelf:  true,
				maximumDelta: 1,
				logLevel:     "info",
			},
		},
		{
			name: "minimal delta",
			args: []string{"--config-path=foo", "--maximum-removal-delta=0"},
			expected: &options{
				config:       "foo",
				minAdmins:    defaultMinAdmins,
				requireSelf:  true,
				maximumDelta: 0,
				logLevel:     "info",
			},
		},
		{
			name: "minimal admins",
			args: []string{"--config-path=foo", "--min-admins=2"},
			expected: &options{
				config:       "foo",
				minAdmins:    2,
				requireSelf:  true,
				maximumDelta: defaultDelta,
				logLevel:     "info",
			},
		},
		{
			name: "reject dump and confirm",
			args: []string{"--confirm", "--dump=frogger"},
		},
		{
			name: "reject dump and config-path",
			args: []string{"--config-path=foo", "--dump=frogger"},
		},
		{
			name: "reject --fix-team-members without --fix-teams",
			args: []string{"--config-path=foo", "--fix-team-members"},
		},
		{
			name: "allow dump without config",
			args: []string{"--dump=frogger"},
			expected: &options{
				minAdmins:    defaultMinAdmins,
				requireSelf:  true,
				maximumDelta: defaultDelta,
				dump:         "frogger",
				logLevel:     "info",
			},
		},
		{
			name: "minimal",
			args: []string{"--config-path=foo"},
			expected: &options{
				config:       "foo",
				minAdmins:    defaultMinAdmins,
				requireSelf:  true,
				maximumDelta: defaultDelta,
				logLevel:     "info",
			},
		},
		{
			name: "full",
			args: []string{"--config-path=foo", "--github-token-path=bar", "--github-endpoint=weird://url", "--confirm=true", "--require-self=false", "--dump=", "--fix-org", "--fix-org-members", "--fix-teams", "--fix-team-members", "--log-level=debug"},
			expected: &options{
				config:         "foo",
				confirm:        true,
				requireSelf:    false,
				minAdmins:      defaultMinAdmins,
				maximumDelta:   defaultDelta,
				fixOrg:         true,
				fixOrgMembers:  true,
				fixTeams:       true,
				fixTeamMembers: true,
				logLevel:       "debug",
			},
		},
		{
			name: "--fix-forks inherits from --fix-repos when not set",
			args: []string{"--config-path=foo", "--fix-repos"},
			expected: &options{
				config:       "foo",
				minAdmins:    defaultMinAdmins,
				requireSelf:  true,
				maximumDelta: defaultDelta,
				fixRepos:     true,
				fixForks:     true, // Inherited from --fix-repos
				logLevel:     "info",
			},
		},
		{
			name: "--fix-forks=false overrides --fix-repos inheritance",
			args: []string{"--config-path=foo", "--fix-repos", "--fix-forks=false"},
			expected: &options{
				config:       "foo",
				minAdmins:    defaultMinAdmins,
				requireSelf:  true,
				maximumDelta: defaultDelta,
				fixRepos:     true,
				fixForks:     false, // Explicitly set to false
				logLevel:     "info",
			},
		},
		{
			name: "--fix-forks=true without --fix-repos",
			args: []string{"--config-path=foo", "--fix-forks=true"},
			expected: &options{
				config:       "foo",
				minAdmins:    defaultMinAdmins,
				requireSelf:  true,
				maximumDelta: defaultDelta,
				fixRepos:     false,
				fixForks:     true, // Explicitly set to true
				logLevel:     "info",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			var actual options
			err := actual.parseArgs(flags, tc.args)
			actual.github = flagutil.GitHubOptions{}
			switch {
			case err == nil && tc.expected == nil:
				t.Errorf("%s: failed to return an error", tc.name)
			case err != nil && tc.expected != nil:
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			case tc.expected != nil && !reflect.DeepEqual(*tc.expected, actual):
				t.Errorf("%s: got incorrect options: %v", tc.name, cmp.Diff(actual, *tc.expected, cmp.AllowUnexported(options{}, flagutil.Strings{}, flagutil.GitHubOptions{})))
			}
		})
	}
}

type fakeClient struct {
	orgMembers sets.Set[string]
	admins     sets.Set[string]
	invitees   sets.Set[string]
	members    sets.Set[string]
	removed    sets.Set[string]
	newAdmins  sets.Set[string]
	newMembers sets.Set[string]
}

func (c *fakeClient) BotUser() (*github.UserData, error) {
	return &github.UserData{Login: "me"}, nil
}

func (c fakeClient) makeMembers(people sets.Set[string]) []github.TeamMember {
	var ret []github.TeamMember
	for p := range people {
		ret = append(ret, github.TeamMember{Login: p})
	}
	return ret
}

func (c *fakeClient) ListOrgMembers(org, role string) ([]github.TeamMember, error) {
	switch role {
	case github.RoleMember:
		return c.makeMembers(c.members), nil
	case github.RoleAdmin:
		return c.makeMembers(c.admins), nil
	default:
		// RoleAll: implement when/if necessary
		return nil, fmt.Errorf("bad role: %s", role)
	}
}

func (c *fakeClient) ListOrgInvitations(org string) ([]github.OrgInvitation, error) {
	var ret []github.OrgInvitation
	for p := range c.invitees {
		if p == "fail" {
			return nil, errors.New("injected list org invitations failure")
		}
		ret = append(ret, github.OrgInvitation{
			TeamMember: github.TeamMember{
				Login: p,
			},
		})
	}
	return ret, nil
}

func (c *fakeClient) RemoveOrgMembership(org, user string) error {
	if user == "fail" {
		return errors.New("injected remove org membership failure")
	}
	c.removed.Insert(user)
	c.admins.Delete(user)
	c.members.Delete(user)
	return nil
}

func (c *fakeClient) UpdateOrgMembership(org, user string, admin bool) (*github.OrgMembership, error) {
	if user == "fail" {
		return nil, errors.New("injected update org failure")
	}
	var state string
	if c.members.Has(user) || c.admins.Has(user) {
		state = github.StateActive
	} else {
		state = github.StatePending
	}
	var role string
	if admin {
		c.newAdmins.Insert(user)
		c.admins.Insert(user)
		role = github.RoleAdmin
	} else {
		c.newMembers.Insert(user)
		c.members.Insert(user)
		role = github.RoleMember
	}
	return &github.OrgMembership{
		Membership: github.Membership{
			Role:  role,
			State: state,
		},
	}, nil
}

func (c *fakeClient) ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error) {
	if teamSlug != configuredTeamSlug {
		return nil, fmt.Errorf("only team: %s supported, not %s", configuredTeamSlug, teamSlug)
	}
	switch role {
	case github.RoleMember:
		return c.makeMembers(c.members), nil
	case github.RoleMaintainer:
		return c.makeMembers(c.admins), nil
	default:
		return nil, fmt.Errorf("fake does not support: %s", role)
	}
}

func (c *fakeClient) ListTeamInvitationsBySlug(org, teamSlug string) ([]github.OrgInvitation, error) {
	if teamSlug != configuredTeamSlug {
		return nil, fmt.Errorf("only team: %s supported, not %s", configuredTeamSlug, teamSlug)
	}
	var ret []github.OrgInvitation
	for p := range c.invitees {
		if p == "fail" {
			return nil, errors.New("injected list org invitations failure")
		}
		ret = append(ret, github.OrgInvitation{
			TeamMember: github.TeamMember{
				Login: p,
			},
		})
	}
	return ret, nil
}

const configuredTeamSlug = "team-slug"

func (c *fakeClient) UpdateTeamMembershipBySlug(org, teamSlug, user string, maintainer bool) (*github.TeamMembership, error) {
	if teamSlug != configuredTeamSlug {
		return nil, fmt.Errorf("only team: %s supported, not %s", configuredTeamSlug, teamSlug)
	}
	if user == "fail" {
		return nil, fmt.Errorf("injected failure for %s", user)
	}
	var state string
	if c.orgMembers.Has(user) || len(c.orgMembers) == 0 {
		state = github.StateActive
	} else {
		state = github.StatePending
	}
	var role string
	if maintainer {
		c.newAdmins.Insert(user)
		c.admins.Insert(user)
		role = github.RoleMaintainer
	} else {
		c.newMembers.Insert(user)
		c.members.Insert(user)
		role = github.RoleMember
	}
	return &github.TeamMembership{
		Membership: github.Membership{
			Role:  role,
			State: state,
		},
	}, nil
}

func (c *fakeClient) RemoveTeamMembershipBySlug(org, teamSlug, user string) error {
	if teamSlug != configuredTeamSlug {
		return fmt.Errorf("only team: %s supported, not %s", configuredTeamSlug, teamSlug)
	}
	if user == "fail" {
		return fmt.Errorf("injected failure for %s", user)
	}
	c.removed.Insert(user)
	c.admins.Delete(user)
	c.members.Delete(user)
	return nil
}

func TestConfigureMembers(t *testing.T) {
	cases := []struct {
		name     string
		want     memberships
		have     memberships
		remove   sets.Set[string]
		members  sets.Set[string]
		supers   sets.Set[string]
		invitees sets.Set[string]
		err      bool
	}{
		{
			name: "forgot to remove duplicate entry",
			want: memberships{
				members: sets.New[string]("me"),
				super:   sets.New[string]("me"),
			},
			err: true,
		},
		{
			name: "removal fails",
			have: memberships{
				members: sets.New[string]("fail"),
			},
			err: true,
		},
		{
			name: "adding admin fails",
			want: memberships{
				super: sets.New[string]("fail"),
			},
			err: true,
		},
		{
			name: "adding member fails",
			want: memberships{
				members: sets.New[string]("fail"),
			},
			err: true,
		},
		{
			name: "promote to admin",
			have: memberships{
				members: sets.New[string]("promote"),
			},
			want: memberships{
				super: sets.New[string]("promote"),
			},
			supers: sets.New[string]("promote"),
		},
		{
			name: "downgrade to member",
			have: memberships{
				super: sets.New[string]("downgrade"),
			},
			want: memberships{
				members: sets.New[string]("downgrade"),
			},
			members: sets.New[string]("downgrade"),
		},
		{
			name: "some of everything",
			have: memberships{
				super:   sets.New[string]("keep-admin", "drop-admin"),
				members: sets.New[string]("keep-member", "drop-member"),
			},
			want: memberships{
				members: sets.New[string]("keep-member", "new-member"),
				super:   sets.New[string]("keep-admin", "new-admin"),
			},
			remove:  sets.New[string]("drop-admin", "drop-member"),
			members: sets.New[string]("new-member"),
			supers:  sets.New[string]("new-admin"),
		},
		{
			name: "ensure case insensitivity",
			have: memberships{
				super:   sets.New[string]("lower"),
				members: sets.New[string]("UPPER"),
			},
			want: memberships{
				super:   sets.New[string]("Lower"),
				members: sets.New[string]("UpPeR"),
			},
		},
		{
			name: "remove invites for those not in org config",
			have: memberships{
				members: sets.New[string]("member-one", "member-two"),
			},
			want: memberships{
				members: sets.New[string]("member-one", "member-two"),
			},
			remove:   sets.New[string]("member-three"),
			invitees: sets.New[string]("member-three"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			removed := sets.Set[string]{}
			members := sets.Set[string]{}
			supers := sets.Set[string]{}
			adder := func(user string, super bool) error {
				if user == "fail" {
					return fmt.Errorf("injected adder failure for %s", user)
				}
				if super {
					supers.Insert(user)
				} else {
					members.Insert(user)
				}
				return nil
			}

			remover := func(user string) error {
				if user == "fail" {
					return fmt.Errorf("injected remover failure for %s", user)
				}
				removed.Insert(user)
				return nil
			}

			err := configureMembers(tc.have, tc.want, tc.invitees, adder, remover)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("Unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("Failed to receive error")
			default:
				if err := cmpLists(sets.List(tc.remove), sets.List(removed)); err != nil {
					t.Errorf("Wrong users removed: %v", err)
				} else if err := cmpLists(sets.List(tc.members), sets.List(members)); err != nil {
					t.Errorf("Wrong members added: %v", err)
				} else if err := cmpLists(sets.List(tc.supers), sets.List(supers)); err != nil {
					t.Errorf("Wrong supers added: %v", err)
				}
			}
		})
	}
}

func TestConfigureOrgMembers(t *testing.T) {
	cases := []struct {
		name        string
		opt         options
		config      org.Config
		admins      []string
		members     []string
		invitations []string
		err         bool
		remove      []string
		addAdmins   []string
		addMembers  []string
	}{
		{
			name: "too few admins",
			opt: options{
				minAdmins: 5,
			},
			config: org.Config{
				Admins: []string{"joe"},
			},
			err: true,
		},
		{
			name: "remove too many admins",
			opt: options{
				maximumDelta: 0.3,
			},
			config: org.Config{
				Admins: []string{"keep", "me"},
			},
			admins: []string{"a", "b", "c", "keep"},
			err:    true,
		},
		{
			name: "forgot to add self",
			opt: options{
				requireSelf: true,
			},
			config: org.Config{
				Admins: []string{"other"},
			},
			err: true,
		},
		{
			name: "forgot to add required admins",
			opt: options{
				requiredAdmins: flagutil.NewStrings("francis"),
			},
			err: true,
		},
		{
			name:   "can remove self with flag",
			config: org.Config{},
			opt: options{
				maximumDelta: 1,
				requireSelf:  false,
			},
			admins: []string{"me"},
			remove: []string{"me"},
		},
		{
			name: "reject same person with both roles",
			config: org.Config{
				Admins:  []string{"me"},
				Members: []string{"me"},
			},
			err: true,
		},
		{
			name:   "github remove rpc fails",
			admins: []string{"fail"},
			err:    true,
		},
		{
			name: "github add rpc fails",
			config: org.Config{
				Admins: []string{"fail"},
			},
			err: true,
		},
		{
			name: "require team member to be org member",
			config: org.Config{
				Teams: map[string]org.Team{
					"group": {
						Members: []string{"non-member"},
					},
				},
			},
			err: true,
		},
		{
			name: "require team maintainer to be org member",
			config: org.Config{
				Teams: map[string]org.Team{
					"group": {
						Maintainers: []string{"non-member"},
					},
				},
			},
			err: true,
		},
		{
			name: "require team members with upper name to be org member",
			config: org.Config{
				Teams: map[string]org.Team{
					"foo": {
						Members: []string{"Me"},
					},
				},
				Members: []string{"Me"},
			},
			members: []string{"Me"},
		},
		{
			name: "require team maintainer with upper name to be org member",
			config: org.Config{
				Teams: map[string]org.Team{
					"foo": {
						Maintainers: []string{"Me"},
					},
				},
				Admins: []string{"Me"},
			},
			admins: []string{"Me"},
		},
		{
			name: "disallow duplicate names",
			config: org.Config{
				Teams: map[string]org.Team{
					"duplicate": {},
					"other": {
						Previously: []string{"duplicate"},
					},
				},
			},
			err: true,
		},
		{
			name: "disallow duplicate names (single team)",
			config: org.Config{
				Teams: map[string]org.Team{
					"foo": {
						Previously: []string{"foo"},
					},
				},
			},
			err: true,
		},
		{
			name: "trivial case works",
		},
		{
			name: "some of everything",
			config: org.Config{
				Admins:  []string{"keep-admin", "new-admin"},
				Members: []string{"keep-member", "new-member"},
			},
			opt: options{
				maximumDelta: 0.5,
			},
			admins:     []string{"keep-admin", "drop-admin"},
			members:    []string{"keep-member", "drop-member"},
			remove:     []string{"drop-admin", "drop-member"},
			addMembers: []string{"new-member"},
			addAdmins:  []string{"new-admin"},
		},
		{
			name: "do not reinvite",
			config: org.Config{
				Admins:  []string{"invited-admin"},
				Members: []string{"invited-member"},
			},
			invitations: []string{"invited-admin", "invited-member"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{
				admins:     sets.New[string](tc.admins...),
				members:    sets.New[string](tc.members...),
				removed:    sets.Set[string]{},
				newAdmins:  sets.Set[string]{},
				newMembers: sets.Set[string]{},
			}

			err := configureOrgMembers(tc.opt, fc, fakeOrg, tc.config, sets.New[string](tc.invitations...))
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("Unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("Failed to receive error")
			default:
				if err := cmpLists(tc.remove, sets.List(fc.removed)); err != nil {
					t.Errorf("Wrong users removed: %v", err)
				} else if err := cmpLists(tc.addMembers, sets.List(fc.newMembers)); err != nil {
					t.Errorf("Wrong members added: %v", err)
				} else if err := cmpLists(tc.addAdmins, sets.List(fc.newAdmins)); err != nil {
					t.Errorf("Wrong admins added: %v", err)
				}
			}
		})
	}
}

type fakeTeamClient struct {
	teams map[string]github.Team
	max   int
}

func makeFakeTeamClient(teams ...github.Team) *fakeTeamClient {
	fc := fakeTeamClient{
		teams: map[string]github.Team{},
	}
	for _, t := range teams {
		fc.teams[t.Slug] = t
		if t.ID >= fc.max {
			fc.max = t.ID + 1
		}
	}
	return &fc
}

const fakeOrg = "random-org"

func (c *fakeTeamClient) CreateTeam(org string, team github.Team) (*github.Team, error) {
	if org != fakeOrg {
		return nil, fmt.Errorf("org must be %s, not %s", fakeOrg, org)
	}
	if team.Name == "fail" {
		return nil, errors.New("injected CreateTeam error")
	}
	c.max++
	team.ID = c.max
	c.teams[team.Slug] = team
	return &team, nil

}

func (c *fakeTeamClient) ListTeams(name string) ([]github.Team, error) {
	if name == "fail" {
		return nil, errors.New("injected ListTeams error")
	}
	var teams []github.Team
	for _, t := range c.teams {
		teams = append(teams, t)
	}
	return teams, nil
}

func (c *fakeTeamClient) DeleteTeamBySlug(org, teamSlug string) error {
	switch _, ok := c.teams[teamSlug]; {
	case !ok:
		return fmt.Errorf("not found %s", teamSlug)
	case teamSlug == "":
		return errors.New("injected DeleteTeam error")
	}
	delete(c.teams, teamSlug)
	return nil
}

func (c *fakeTeamClient) EditTeam(org string, team github.Team) (*github.Team, error) {
	slug := team.Slug
	t, ok := c.teams[slug]
	if !ok {
		return nil, fmt.Errorf("team %s does not exist", slug)
	}
	switch {
	case team.Description == "fail":
		return nil, errors.New("injected description failure")
	case team.Name == "fail":
		return nil, errors.New("injected name failure")
	case team.Privacy == "fail":
		return nil, errors.New("injected privacy failure")
	}
	if team.Description != "" {
		t.Description = team.Description
	}
	if team.Name != "" {
		t.Name = team.Name
	}
	if team.Privacy != "" {
		t.Privacy = team.Privacy
	}
	if team.ParentTeamID != nil {
		t.Parent = &github.Team{
			ID: *team.ParentTeamID,
		}
	} else {
		t.Parent = nil
	}
	c.teams[slug] = t
	return &t, nil
}

func TestFindTeam(t *testing.T) {
	cases := []struct {
		name     string
		teams    map[string]github.Team
		current  string
		previous []string
		expected int
	}{
		{
			name: "will find current team",
			teams: map[string]github.Team{
				"hello": {ID: 17},
			},
			current:  "hello",
			expected: 17,
		},
		{
			name: "team does not exist returns nil",
			teams: map[string]github.Team{
				"unrelated": {ID: 5},
			},
			current: "hypothetical",
		},
		{
			name: "will find previous name",
			teams: map[string]github.Team{
				"deprecated name": {ID: 1},
			},
			current:  "current name",
			previous: []string{"archaic name", "deprecated name"},
			expected: 1,
		},
		{
			name: "prioritize current when previous also exists",
			teams: map[string]github.Team{
				"deprecated": {ID: 1},
				"current":    {ID: 2},
			},
			current:  "current",
			previous: []string{"deprecated"},
			expected: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := findTeam(tc.teams, tc.current, tc.previous...)
			switch {
			case actual == nil:
				if tc.expected != 0 {
					t.Errorf("failed to find team %d", tc.expected)
				}
			case tc.expected == 0:
				t.Errorf("unexpected team returned: %v", *actual)
			case actual.ID != tc.expected:
				t.Errorf("team %v != expected ID %d", actual, tc.expected)
			}
		})
	}
}

func TestConfigureTeams(t *testing.T) {
	desc := "so interesting"
	priv := org.Secret
	cases := []struct {
		name              string
		err               bool
		orgNameOverride   string
		ignoreSecretTeams bool
		config            org.Config
		teams             []github.Team
		expected          map[string]github.Team
		deleted           []string
		delta             float64
	}{
		{
			name: "do nothing without error",
		},
		{
			name: "reject duplicated team names (different teams)",
			err:  true,
			config: org.Config{
				Teams: map[string]org.Team{
					"hello": {},
					"there": {Previously: []string{"hello"}},
				},
			},
		},
		{
			name: "reject duplicated team names (single team)",
			err:  true,
			config: org.Config{
				Teams: map[string]org.Team{
					"hello": {Previously: []string{"hello"}},
				},
			},
		},
		{
			name:            "fail to list teams",
			orgNameOverride: "fail",
			err:             true,
		},
		{
			name: "fail to create team",
			config: org.Config{
				Teams: map[string]org.Team{
					"fail": {},
				},
			},
			err: true,
		},
		{
			name: "fail to delete team",
			teams: []github.Team{
				{Name: "fail", ID: -55},
			},
			err: true,
		},
		{
			name: "create missing team",
			teams: []github.Team{
				{Name: "old", ID: 1},
			},
			config: org.Config{
				Teams: map[string]org.Team{
					"new": {},
					"old": {},
				},
			},
			expected: map[string]github.Team{
				"old": {Name: "old", ID: 1},
				"new": {Name: "new", ID: 3},
			},
		},
		{
			name: "reuse existing teams",
			teams: []github.Team{
				{Name: "current", Slug: "current", ID: 1},
				{Name: "deprecated", Slug: "deprecated", ID: 5},
			},
			config: org.Config{
				Teams: map[string]org.Team{
					"current": {},
					"updated": {Previously: []string{"deprecated"}},
				},
			},
			expected: map[string]github.Team{
				"current": {Name: "current", Slug: "current", ID: 1},
				"updated": {Name: "deprecated", Slug: "deprecated", ID: 5},
			},
		},
		{
			name: "delete unused teams",
			teams: []github.Team{
				{
					Name: "unused",
					Slug: "unused",
					ID:   1,
				},
				{
					Name: "used",
					Slug: "used",
					ID:   2,
				},
			},
			config: org.Config{
				Teams: map[string]org.Team{
					"used": {},
				},
			},
			expected: map[string]github.Team{
				"used": {ID: 2, Name: "used", Slug: "used"},
			},
			deleted: []string{"unused"},
		},
		{
			name: "create team with metadata",
			config: org.Config{
				Teams: map[string]org.Team{
					"new": {
						TeamMetadata: org.TeamMetadata{
							Description: &desc,
							Privacy:     &priv,
						},
					},
				},
			},
			expected: map[string]github.Team{
				"new": {ID: 1, Name: "new", Description: desc, Privacy: string(priv)},
			},
		},
		{
			name: "allow deleting many teams",
			teams: []github.Team{
				{
					Name: "unused",
					Slug: "unused",
					ID:   1,
				},
				{
					Name: "used",
					Slug: "used",
					ID:   2,
				},
			},
			config: org.Config{
				Teams: map[string]org.Team{
					"used": {},
				},
			},
			expected: map[string]github.Team{
				"used": {ID: 2, Name: "used", Slug: "used"},
			},
			deleted: []string{"unused"},
			delta:   0.6,
		},
		{
			name: "refuse to delete too many teams",
			teams: []github.Team{
				{
					Name: "unused",
					Slug: "unused",
					ID:   1,
				},
				{
					Name: "used",
					Slug: "used",
					ID:   2,
				},
			},
			config: org.Config{
				Teams: map[string]org.Team{
					"used": {},
				},
			},
			err:   true,
			delta: 0.1,
		},
		{
			name:              "refuse to delete private teams if ignoring them",
			ignoreSecretTeams: true,
			teams: []github.Team{
				{
					Name:    "secret",
					Slug:    "secret",
					ID:      1,
					Privacy: string(org.Secret),
				},
				{
					Name:    "closed",
					Slug:    "closed",
					ID:      2,
					Privacy: string(org.Closed),
				},
			},
			config:   org.Config{Teams: map[string]org.Team{}},
			err:      false,
			expected: map[string]github.Team{},
			deleted:  []string{"closed"},
			delta:    1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := makeFakeTeamClient(tc.teams...)
			orgName := tc.orgNameOverride
			if orgName == "" {
				orgName = fakeOrg
			}
			if tc.expected == nil {
				tc.expected = map[string]github.Team{}
			}
			if tc.delta == 0 {
				tc.delta = 1
			}
			actual, err := configureTeams(fc, orgName, tc.config, tc.delta, tc.ignoreSecretTeams)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive error")
			case !reflect.DeepEqual(actual, tc.expected):
				t.Errorf("%#v != actual %#v", tc.expected, actual)
			}
			for _, slug := range tc.deleted {
				if team, ok := fc.teams[slug]; ok {
					t.Errorf("%s still present: %#v", slug, team)
				}
			}
			original, current, deleted := sets.New[string](), sets.New[string](), sets.New[string](tc.deleted...)
			for _, team := range tc.teams {
				original.Insert(team.Slug)
			}
			for slug := range fc.teams {
				current.Insert(slug)
			}
			if unexpected := original.Difference(current).Difference(deleted); unexpected.Len() > 0 {
				t.Errorf("the following teams were unexpectedly deleted: %v", sets.List(unexpected))
			}
		})
	}
}

func TestConfigureTeam(t *testing.T) {
	old := "old value"
	cur := "current value"
	fail := "fail"
	pfail := org.Privacy(fail)
	whatev := "whatever"
	secret := org.Secret
	parent := 2
	cases := []struct {
		name     string
		err      bool
		teamName string
		parent   *int
		config   org.Team
		github   github.Team
		expected github.Team
	}{
		{
			name:     "patch team when name changes",
			teamName: cur,
			config: org.Team{
				Previously: []string{old},
			},
			github: github.Team{
				ID:   1,
				Name: old,
			},
			expected: github.Team{
				ID:   1,
				Name: cur,
			},
		},
		{
			name:     "patch team when description changes",
			teamName: whatev,
			parent:   nil,
			config: org.Team{
				TeamMetadata: org.TeamMetadata{
					Description: &cur,
				},
			},
			github: github.Team{
				ID:          2,
				Name:        whatev,
				Description: old,
			},
			expected: github.Team{
				ID:          2,
				Name:        whatev,
				Description: cur,
			},
		},
		{
			name:     "patch team when privacy changes",
			teamName: whatev,
			parent:   nil,
			config: org.Team{
				TeamMetadata: org.TeamMetadata{
					Privacy: &secret,
				},
			},
			github: github.Team{
				ID:      3,
				Name:    whatev,
				Privacy: string(org.Closed),
			},
			expected: github.Team{
				ID:      3,
				Name:    whatev,
				Privacy: string(secret),
			},
		},
		{
			name:     "patch team when parent changes",
			teamName: whatev,
			parent:   &parent,
			config:   org.Team{},
			github: github.Team{
				ID:   3,
				Name: whatev,
				Parent: &github.Team{
					ID: 4,
				},
			},
			expected: github.Team{
				ID:   3,
				Name: whatev,
				Parent: &github.Team{
					ID: 2,
				},
				Privacy: string(org.Closed),
			},
		},
		{
			name:     "patch team when parent removed",
			teamName: whatev,
			parent:   nil,
			config:   org.Team{},
			github: github.Team{
				ID:   3,
				Name: whatev,
				Parent: &github.Team{
					ID: 2,
				},
			},
			expected: github.Team{
				ID:     3,
				Name:   whatev,
				Parent: nil,
			},
		},
		{
			name:     "do not patch team when values are the same",
			teamName: fail,
			parent:   &parent,
			config: org.Team{
				TeamMetadata: org.TeamMetadata{
					Description: &fail,
					Privacy:     &pfail,
				},
			},
			github: github.Team{
				ID:          4,
				Name:        fail,
				Description: fail,
				Privacy:     fail,
				Parent: &github.Team{
					ID: 2,
				},
			},
			expected: github.Team{
				ID:          4,
				Name:        fail,
				Description: fail,
				Privacy:     fail,
				Parent: &github.Team{
					ID: 2,
				},
			},
		},
		{
			name:     "fail to patch team",
			teamName: "team",
			parent:   nil,
			config: org.Team{
				TeamMetadata: org.TeamMetadata{
					Description: &fail,
				},
			},
			github: github.Team{
				ID:          1,
				Name:        "team",
				Description: whatev,
			},
			err: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := makeFakeTeamClient(tc.github)
			err := configureTeam(fc, fakeOrg, tc.teamName, tc.config, tc.github, tc.parent)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive expected error")
			case !reflect.DeepEqual(fc.teams[tc.expected.Slug], tc.expected):
				t.Errorf("actual %+v != expected %+v", fc.teams[tc.expected.Slug], tc.expected)
			}
		})
	}
}

func TestConfigureTeamMembers(t *testing.T) {
	cases := []struct {
		name           string
		err            bool
		members        sets.Set[string]
		maintainers    sets.Set[string]
		remove         sets.Set[string]
		addMembers     sets.Set[string]
		addMaintainers sets.Set[string]
		ignoreInvitees bool
		invitees       sets.Set[string]
		team           org.Team
		slug           string
	}{
		{
			name: "fail when listing fails",
			slug: "some-slug",
			err:  true,
		},
		{
			name:    "fail when removal fails",
			members: sets.New[string]("fail"),
			err:     true,
		},
		{
			name: "fail when add fails",
			team: org.Team{
				Maintainers: []string{"fail"},
			},
			err: true,
		},
		{
			name: "some of everything",
			team: org.Team{
				Maintainers: []string{"keep-maintainer", "new-maintainer"},
				Members:     []string{"keep-member", "new-member"},
			},
			maintainers:    sets.New[string]("keep-maintainer", "drop-maintainer"),
			members:        sets.New[string]("keep-member", "drop-member"),
			remove:         sets.New[string]("drop-maintainer", "drop-member"),
			addMembers:     sets.New[string]("new-member"),
			addMaintainers: sets.New[string]("new-maintainer"),
		},
		{
			name: "do not reinvitee invitees",
			team: org.Team{
				Maintainers: []string{"invited-maintainer", "newbie"},
				Members:     []string{"invited-member"},
			},
			invitees:       sets.New[string]("invited-maintainer", "invited-member"),
			addMaintainers: sets.New[string]("newbie"),
		},
		{
			name: "do not remove pending invitees",
			team: org.Team{
				Maintainers: []string{"keep-maintainer"},
				Members:     []string{"invited-member"},
			},
			maintainers: sets.New[string]("keep-maintainer"),
			invitees:    sets.New[string]("invited-member"),
			remove:      sets.Set[string]{},
		},
		{
			name: "ignore invitees",
			team: org.Team{
				Maintainers: []string{"keep-maintainer"},
				Members:     []string{"keep-member", "new-member"},
			},
			maintainers:    sets.New[string]("keep-maintainer"),
			members:        sets.New[string]("keep-member"),
			invitees:       sets.Set[string]{},
			remove:         sets.Set[string]{},
			addMembers:     sets.New[string]("new-member"),
			ignoreInvitees: true,
		},
	}

	for _, tc := range cases {
		gt := github.Team{
			Slug: configuredTeamSlug,
			Name: "whatev",
		}
		if tc.slug != "" {
			gt.Slug = tc.slug
		}
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{
				admins:     sets.KeySet[string](tc.maintainers),
				members:    sets.KeySet[string](tc.members),
				invitees:   sets.KeySet[string](tc.invitees),
				removed:    sets.Set[string]{},
				newAdmins:  sets.Set[string]{},
				newMembers: sets.Set[string]{},
			}
			err := configureTeamMembers(fc, "", gt, tc.team, tc.ignoreInvitees)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("Unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("Failed to receive error")
			default:
				if err := cmpLists(sets.List(tc.remove), sets.List(fc.removed)); err != nil {
					t.Errorf("Wrong users removed: %v", err)
				} else if err := cmpLists(sets.List(tc.addMembers), sets.List(fc.newMembers)); err != nil {
					t.Errorf("Wrong members added: %v", err)
				} else if err := cmpLists(sets.List(tc.addMaintainers), sets.List(fc.newAdmins)); err != nil {
					t.Errorf("Wrong admins added: %v", err)
				}
			}

		})
	}
}

func cmpLists(a, b []string) error {
	if a == nil {
		a = []string{}
	}
	if b == nil {
		b = []string{}
	}
	sort.Strings(a)
	sort.Strings(b)
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("%v != %v", a, b)
	}
	return nil
}

type fakeOrgClient struct {
	current github.Organization
	changed bool
}

func (o *fakeOrgClient) GetOrg(name string) (*github.Organization, error) {
	if name == "fail" {
		return nil, errors.New("injected GetOrg error")
	}
	return &o.current, nil
}

func (o *fakeOrgClient) EditOrg(name string, org github.Organization) (*github.Organization, error) {
	if org.Description == "fail" {
		return nil, errors.New("injected EditOrg error")
	}
	o.current = org
	o.changed = true
	return &o.current, nil
}

func TestUpdateBool(t *testing.T) {
	yes := true
	no := false
	cases := []struct {
		name string
		have *bool
		want *bool
		end  bool
		ret  *bool
	}{
		{
			name: "panic on nil have",
			want: &no,
		},
		{
			name: "never change on nil want",
			want: nil,
			have: &yes,
			end:  yes,
			ret:  &no,
		},
		{
			name: "do not change if same",
			want: &yes,
			have: &yes,
			end:  yes,
			ret:  &no,
		},
		{
			name: "change if different",
			want: &no,
			have: &yes,
			end:  no,
			ret:  &yes,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				wantPanic := tc.ret == nil
				r := recover()
				gotPanic := r != nil
				switch {
				case gotPanic && !wantPanic:
					t.Errorf("unexpected panic: %v", r)
				case wantPanic && !gotPanic:
					t.Errorf("failed to receive panic")
				}
			}()
			if tc.have != nil { // prevent overwriting what tc.have points to for next test case
				have := *tc.have
				tc.have = &have
			}
			ret := updateBool(tc.have, tc.want)
			switch {
			case ret != *tc.ret:
				t.Errorf("return value %t != expected %t", ret, *tc.ret)
			case *tc.have != tc.end:
				t.Errorf("end value %t != expected %t", *tc.have, tc.end)
			}
		})
	}
}

func TestUpdateString(t *testing.T) {
	no := false
	yes := true
	hello := "hello"
	world := "world"
	empty := ""
	cases := []struct {
		name     string
		have     *string
		want     *string
		expected string
		ret      *bool
	}{
		{
			name: "panic on nil have",
			want: &hello,
		},
		{
			name:     "never change on nil want",
			want:     nil,
			have:     &hello,
			expected: hello,
			ret:      &no,
		},
		{
			name:     "do not change if same",
			want:     &world,
			have:     &world,
			expected: world,
			ret:      &no,
		},
		{
			name:     "change if different",
			want:     &empty,
			have:     &hello,
			expected: empty,
			ret:      &yes,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				wantPanic := tc.ret == nil
				r := recover()
				gotPanic := r != nil
				switch {
				case gotPanic && !wantPanic:
					t.Errorf("unexpected panic: %v", r)
				case wantPanic && !gotPanic:
					t.Errorf("failed to receive panic")
				}
			}()
			if tc.have != nil { // prevent overwriting what tc.have points to for next test case
				have := *tc.have
				tc.have = &have
			}
			ret := updateString(tc.have, tc.want)
			switch {
			case ret != *tc.ret:
				t.Errorf("return value %t != expected %t", ret, *tc.ret)
			case *tc.have != tc.expected:
				t.Errorf("end value %s != expected %s", *tc.have, tc.expected)
			}
		})
	}
}

func TestConfigureOrgMeta(t *testing.T) {
	filled := github.Organization{
		BillingEmail:                 "be",
		Company:                      "co",
		Email:                        "em",
		Location:                     "lo",
		Name:                         "na",
		Description:                  "de",
		HasOrganizationProjects:      true,
		HasRepositoryProjects:        true,
		DefaultRepositoryPermission:  "not-a-real-value",
		MembersCanCreateRepositories: true,
	}
	yes := true
	no := false
	str := "random-letters"
	fail := "fail"
	read := github.Read

	cases := []struct {
		name     string
		orgName  string
		want     org.Metadata
		have     github.Organization
		expected github.Organization
		err      bool
		change   bool
	}{
		{
			name:     "no want means no change",
			have:     filled,
			expected: filled,
			change:   false,
		},
		{
			name:    "fail if GetOrg fails",
			orgName: fail,
			err:     true,
		},
		{
			name: "fail if EditOrg fails",
			want: org.Metadata{Description: &fail},
			err:  true,
		},
		{
			name: "billing diff causes change",
			want: org.Metadata{BillingEmail: &str},
			expected: github.Organization{
				BillingEmail: str,
			},
			change: true,
		},
		{
			name: "company diff causes change",
			want: org.Metadata{Company: &str},
			expected: github.Organization{
				Company: str,
			},
			change: true,
		},
		{
			name: "email diff causes change",
			want: org.Metadata{Email: &str},
			expected: github.Organization{
				Email: str,
			},
			change: true,
		},
		{
			name: "location diff causes change",
			want: org.Metadata{Location: &str},
			expected: github.Organization{
				Location: str,
			},
			change: true,
		},
		{
			name: "name diff causes change",
			want: org.Metadata{Name: &str},
			expected: github.Organization{
				Name: str,
			},
			change: true,
		},
		{
			name: "org projects diff causes change",
			want: org.Metadata{HasOrganizationProjects: &yes},
			expected: github.Organization{
				HasOrganizationProjects: yes,
			},
			change: true,
		},
		{
			name: "repo projects diff causes change",
			want: org.Metadata{HasRepositoryProjects: &yes},
			expected: github.Organization{
				HasRepositoryProjects: yes,
			},
			change: true,
		},
		{
			name: "default permission diff causes change",
			want: org.Metadata{DefaultRepositoryPermission: &read},
			expected: github.Organization{
				DefaultRepositoryPermission: string(read),
			},
			change: true,
		},
		{
			name: "members can create diff causes change",
			want: org.Metadata{MembersCanCreateRepositories: &yes},
			expected: github.Organization{
				MembersCanCreateRepositories: yes,
			},
			change: true,
		},
		{
			name: "change all values at once",
			have: filled,
			want: org.Metadata{
				BillingEmail:                 &str,
				Company:                      &str,
				Email:                        &str,
				Location:                     &str,
				Name:                         &str,
				Description:                  &str,
				HasOrganizationProjects:      &no,
				HasRepositoryProjects:        &no,
				MembersCanCreateRepositories: &no,
				DefaultRepositoryPermission:  &read,
			},
			expected: github.Organization{
				BillingEmail:                 str,
				Company:                      str,
				Email:                        str,
				Location:                     str,
				Name:                         str,
				Description:                  str,
				HasOrganizationProjects:      no,
				HasRepositoryProjects:        no,
				MembersCanCreateRepositories: no,
				DefaultRepositoryPermission:  string(read),
			},
			change: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.orgName == "" {
				tc.orgName = "whatever"
			}
			fc := fakeOrgClient{
				current: tc.have,
			}
			err := configureOrgMeta(&fc, tc.orgName, tc.want)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive error")
			case tc.change != fc.changed:
				t.Errorf("changed %t != expected %t", fc.changed, tc.change)
			case !reflect.DeepEqual(fc.current, tc.expected):
				t.Errorf("current %#v != expected %#v", fc.current, tc.expected)
			}
		})
	}
}

func TestDumpOrgConfig(t *testing.T) {
	empty := ""
	hello := "Hello"
	details := "wise and brilliant exemplary human specimens"
	yes := true
	no := false
	perm := github.Write
	pub := org.Privacy("")
	secret := org.Secret
	closed := org.Closed
	repoName := "project"
	repoDescription := "awesome testing project"
	repoHomepage := "https://www.somewhe.re/something/"
	master := "master-branch"
	cases := []struct {
		name              string
		orgOverride       string
		ignoreSecretTeams bool
		meta              github.Organization
		members           []string
		admins            []string
		teams             []github.Team
		teamMembers       map[string][]string
		maintainers       map[string][]string
		repoPermissions   map[string][]github.Repo
		repos             []github.FullRepo
		expected          org.Config
		err               bool
	}{
		{
			name:        "fails if GetOrg fails",
			orgOverride: "fail",
			err:         true,
		},
		{
			name:    "fails if ListOrgMembers fails",
			err:     true,
			members: []string{"hello", "fail"},
		},
		{
			name: "fails if ListTeams fails",
			err:  true,
			teams: []github.Team{
				{
					Name: "fail",
					ID:   3,
				},
			},
		},
		{
			name: "fails if ListTeamMembersFails",
			err:  true,
			teams: []github.Team{
				{
					Name: "fred",
					ID:   -1,
				},
			},
		},
		{
			name: "fails if GetTeams fails",
			err:  true,
			repos: []github.FullRepo{
				{
					Repo: github.Repo{
						Name: "fail",
					},
				},
			},
		},
		{
			name:   "fails if not as an admin of the org",
			err:    true,
			admins: []string{"not-admin"},
		},
		{
			name: "basically works",
			meta: github.Organization{
				Name:                         hello,
				MembersCanCreateRepositories: yes,
				DefaultRepositoryPermission:  string(perm),
			},
			members: []string{"george", "jungle", "banana"},
			admins:  []string{"admin", "james", "giant", "peach"},
			teams: []github.Team{
				{
					ID:          5,
					Slug:        "team-5",
					Name:        "friends",
					Description: details,
				},
				{
					ID:   6,
					Slug: "team-6",
					Name: "enemies",
				},
				{
					ID:   7,
					Slug: "team-7",
					Name: "archenemies",
					Parent: &github.Team{
						ID:   6,
						Slug: "team-6",
						Name: "enemies",
					},
					Privacy: string(org.Secret),
				},
			},
			teamMembers: map[string][]string{
				"team-5": {"george", "james"},
				"team-6": {"george"},
				"team-7": {},
			},
			maintainers: map[string][]string{
				"team-5": {},
				"team-6": {"giant", "jungle"},
				"team-7": {"banana"},
			},
			repoPermissions: map[string][]github.Repo{
				"team-5": {},
				"team-6": {{Name: "pull-repo", Permissions: github.RepoPermissions{Pull: true}}},
				"team-7": {{Name: "pull-repo", Permissions: github.RepoPermissions{Pull: true}}, {Name: "admin-repo", Permissions: github.RepoPermissions{Admin: true}}},
			},
			repos: []github.FullRepo{
				{
					Repo: github.Repo{
						Name:          repoName,
						Description:   repoDescription,
						Homepage:      repoHomepage,
						Private:       false,
						HasIssues:     true,
						HasProjects:   true,
						HasWiki:       true,
						Archived:      true,
						DefaultBranch: master,
					},
				},
			},
			expected: org.Config{
				Metadata: org.Metadata{
					Name:                         &hello,
					BillingEmail:                 &empty,
					Company:                      &empty,
					Email:                        &empty,
					Description:                  &empty,
					Location:                     &empty,
					HasOrganizationProjects:      &no,
					HasRepositoryProjects:        &no,
					DefaultRepositoryPermission:  &perm,
					MembersCanCreateRepositories: &yes,
				},
				Teams: map[string]org.Team{
					"friends": {
						TeamMetadata: org.TeamMetadata{
							Description: &details,
							Privacy:     &pub,
						},
						Members:     []string{"george", "james"},
						Maintainers: []string{},
						Children:    map[string]org.Team{},
						Repos:       map[string]github.RepoPermissionLevel{},
					},
					"enemies": {
						TeamMetadata: org.TeamMetadata{
							Description: &empty,
							Privacy:     &pub,
						},
						Members:     []string{"george"},
						Maintainers: []string{"giant", "jungle"},
						Repos: map[string]github.RepoPermissionLevel{
							"pull-repo": github.Read,
						},
						Children: map[string]org.Team{
							"archenemies": {
								TeamMetadata: org.TeamMetadata{
									Description: &empty,
									Privacy:     &secret,
								},
								Members:     []string{},
								Maintainers: []string{"banana"},
								Repos: map[string]github.RepoPermissionLevel{
									"pull-repo":  github.Read,
									"admin-repo": github.Admin,
								},
								Children: map[string]org.Team{},
							},
						},
					},
				},
				Members: []string{"george", "jungle", "banana"},
				Admins:  []string{"admin", "james", "giant", "peach"},
				Repos: map[string]org.Repo{
					"project": {
						RepoMetadata: org.RepoMetadata{
							Description:      &repoDescription,
							HomePage:         &repoHomepage,
							HasProjects:      &yes,
							AllowMergeCommit: &no,
							AllowRebaseMerge: &no,
							AllowSquashMerge: &no,
							Archived:         &yes,
							DefaultBranch:    &master,
						},
					},
				},
			},
		},
		{
			name:              "ignores private teams when expected to",
			ignoreSecretTeams: true,
			meta: github.Organization{
				Name:                         hello,
				MembersCanCreateRepositories: yes,
				DefaultRepositoryPermission:  string(perm),
			},
			members: []string{"george", "jungle", "banana"},
			admins:  []string{"admin", "james", "giant", "peach"},
			teams: []github.Team{
				{
					ID:          5,
					Slug:        "team-5",
					Name:        "friends",
					Description: details,
				},
				{
					ID:   6,
					Slug: "team-6",
					Name: "enemies",
				},
				{
					ID:   7,
					Slug: "team-7",
					Name: "archenemies",
					Parent: &github.Team{
						Slug: "team-6",
						Name: "enemies",
					},
					Privacy: string(org.Secret),
				},
				{
					ID:   8,
					Slug: "team-8",
					Name: "frenemies",
					Parent: &github.Team{
						ID:   6,
						Slug: "team-6",
						Name: "enemies",
					},
					Privacy: string(org.Closed),
				},
			},
			teamMembers: map[string][]string{
				"team-5": {"george", "james"},
				"team-6": {"george"},
				"team-7": {},
				"team-8": {"patrick"},
			},
			maintainers: map[string][]string{
				"team-5": {},
				"team-6": {"giant", "jungle"},
				"team-7": {"banana"},
				"team-8": {"starfish"},
			},
			expected: org.Config{
				Metadata: org.Metadata{
					Name:                         &hello,
					BillingEmail:                 &empty,
					Company:                      &empty,
					Email:                        &empty,
					Description:                  &empty,
					Location:                     &empty,
					HasOrganizationProjects:      &no,
					HasRepositoryProjects:        &no,
					DefaultRepositoryPermission:  &perm,
					MembersCanCreateRepositories: &yes,
				},
				Teams: map[string]org.Team{
					"friends": {
						TeamMetadata: org.TeamMetadata{
							Description: &details,
							Privacy:     &pub,
						},
						Members:     []string{"george", "james"},
						Maintainers: []string{},
						Children:    map[string]org.Team{},
						Repos:       map[string]github.RepoPermissionLevel{},
					},
					"enemies": {
						TeamMetadata: org.TeamMetadata{
							Description: &empty,
							Privacy:     &pub,
						},
						Members:     []string{"george"},
						Maintainers: []string{"giant", "jungle"},
						Children: map[string]org.Team{
							"frenemies": {
								TeamMetadata: org.TeamMetadata{
									Description: &empty,
									Privacy:     &closed,
								},
								Members:     []string{"patrick"},
								Maintainers: []string{"starfish"},
								Children:    map[string]org.Team{},
								Repos:       map[string]github.RepoPermissionLevel{},
							},
						},
						Repos: map[string]github.RepoPermissionLevel{},
					},
				},
				Members: []string{"george", "jungle", "banana"},
				Admins:  []string{"admin", "james", "giant", "peach"},
				Repos:   map[string]org.Repo{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orgName := "random-org"
			if tc.orgOverride != "" {
				orgName = tc.orgOverride
			}
			fc := fakeDumpClient{
				name:            orgName,
				members:         tc.members,
				admins:          tc.admins,
				meta:            tc.meta,
				teams:           tc.teams,
				teamMembers:     tc.teamMembers,
				maintainers:     tc.maintainers,
				repoPermissions: tc.repoPermissions,
				repos:           tc.repos,
			}
			actual, err := dumpOrgConfig(fc, orgName, tc.ignoreSecretTeams, "")
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive error")
			default:
				fixup(actual)
				fixup(&tc.expected)
				if diff := cmp.Diff(actual, &tc.expected); diff != "" {
					t.Errorf("did not get correct config, diff: %s", diff)
				}

			}
		})
	}
}

type fakeDumpClient struct {
	name            string
	members         []string
	admins          []string
	meta            github.Organization
	teams           []github.Team
	teamMembers     map[string][]string
	maintainers     map[string][]string
	repoPermissions map[string][]github.Repo
	repos           []github.FullRepo
}

func (c fakeDumpClient) GetOrg(name string) (*github.Organization, error) {
	if name != c.name {
		return nil, errors.New("bad name")
	}
	if name == "fail" {
		return nil, errors.New("injected GetOrg error")
	}
	return &c.meta, nil
}

func (c fakeDumpClient) makeMembers(people []string) ([]github.TeamMember, error) {
	var ret []github.TeamMember
	for _, p := range people {
		if p == "fail" {
			return nil, errors.New("injected makeMembers error")
		}
		ret = append(ret, github.TeamMember{Login: p})
	}
	return ret, nil
}

func (c fakeDumpClient) ListOrgMembers(name, role string) ([]github.TeamMember, error) {
	switch {
	case name != c.name:
		return nil, fmt.Errorf("bad org: %s", name)
	case role == github.RoleAdmin:
		return c.makeMembers(c.admins)
	case role == github.RoleMember:
		return c.makeMembers(c.members)
	}
	return nil, fmt.Errorf("bad role: %s", role)
}

func (c fakeDumpClient) ListTeams(name string) ([]github.Team, error) {
	if name != c.name {
		return nil, fmt.Errorf("bad org: %s", name)
	}

	for _, t := range c.teams {
		if t.Name == "fail" {
			return nil, errors.New("injected ListTeams error")
		}
	}
	return c.teams, nil
}

func (c fakeDumpClient) ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error) {
	var mapping map[string][]string
	switch {
	case teamSlug == "":
		return nil, errors.New("injected ListTeamMembers error")
	case role == github.RoleMaintainer:
		mapping = c.maintainers
	case role == github.RoleMember:
		mapping = c.teamMembers
	default:
		return nil, fmt.Errorf("bad role: %s", role)
	}
	people, ok := mapping[teamSlug]
	if !ok {
		return nil, fmt.Errorf("team does not exist: %s", teamSlug)
	}
	return c.makeMembers(people)
}

func (c fakeDumpClient) ListTeamReposBySlug(org, teamSlug string) ([]github.Repo, error) {
	if teamSlug == "" {
		return nil, errors.New("injected ListTeamRepos error")
	}

	return c.repoPermissions[teamSlug], nil
}

func (c fakeDumpClient) GetRepos(org string, isUser bool) ([]github.Repo, error) {
	var repos []github.Repo
	for _, repo := range c.repos {
		if repo.Name == "fail" {
			return nil, fmt.Errorf("injected GetRepos error")
		}
		repos = append(repos, repo.Repo)
	}

	return repos, nil
}

func (c fakeDumpClient) GetRepo(owner, repo string) (github.FullRepo, error) {
	for _, r := range c.repos {
		switch {
		case r.Name == "fail":
			return r, fmt.Errorf("injected GetRepo error")
		case r.Name == repo:
			return r, nil
		}
	}

	return github.FullRepo{}, fmt.Errorf("not found")
}

func (c fakeDumpClient) BotUser() (*github.UserData, error) {
	return &github.UserData{Login: "admin"}, nil
}

func (c fakeDumpClient) ListCollaborators(org, repo string) ([]github.User, error) {
	return []github.User{}, nil
}

func (c fakeDumpClient) ListDirectCollaboratorsWithPermissions(org, repo string) (map[string]github.RepoPermissionLevel, error) {
	// For dump tests, return empty by default
	return map[string]github.RepoPermissionLevel{}, nil
}

func (c fakeDumpClient) GetUserPermission(org, repo, user string) (string, error) {
	return "read", nil
}

func (c fakeDumpClient) ListRepoInvitations(org, repo string) ([]github.CollaboratorRepoInvitation, error) {
	return []github.CollaboratorRepoInvitation{}, nil
}

func fixup(ret *org.Config) {
	if ret == nil {
		return
	}
	sort.Strings(ret.Members)
	sort.Strings(ret.Admins)
	for name, team := range ret.Teams {
		sort.Strings(team.Members)
		sort.Strings(team.Maintainers)
		sort.Strings(team.Previously)
		ret.Teams[name] = team
	}
}

func TestOrgInvitations(t *testing.T) {
	cases := []struct {
		name     string
		opt      options
		invitees sets.Set[string] // overrides
		expected sets.Set[string]
		err      bool
	}{
		{
			name:     "do not call on empty options",
			invitees: sets.New[string]("him", "her", "them"),
			expected: sets.Set[string]{},
		},
		{
			name: "call if fixOrgMembers",
			opt: options{
				fixOrgMembers: true,
			},
			invitees: sets.New[string]("him", "her", "them"),
			expected: sets.New[string]("him", "her", "them"),
		},
		{
			name: "call if fixTeamMembers",
			opt: options{
				fixTeamMembers: true,
			},
			invitees: sets.New[string]("him", "her", "them"),
			expected: sets.New[string]("him", "her", "them"),
		},
		{
			name: "ensure case normalization",
			opt: options{
				fixOrgMembers:  true,
				fixTeamMembers: true,
			},
			invitees: sets.New[string]("MiXeD", "lower", "UPPER"),
			expected: sets.New[string]("mixed", "lower", "upper"),
		},
		{
			name: "error if list fails",
			opt: options{
				fixTeamMembers: true,
				fixOrgMembers:  true,
			},
			invitees: sets.New[string]("erick", "fail"),
			err:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{
				invitees: tc.invitees,
			}
			actual, err := orgInvitations(tc.opt, fc, "random-org")
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Errorf("failed to receive an error")
			case !reflect.DeepEqual(actual, tc.expected):
				t.Errorf("%#v != expected %#v", actual, tc.expected)
			}
		})
	}
}

type fakeTeamRepoClient struct {
	repos                            map[string][]github.Repo
	failList, failUpdate, failRemove bool
}

func (c *fakeTeamRepoClient) ListTeamReposBySlug(org, teamSlug string) ([]github.Repo, error) {
	if c.failList {
		return nil, errors.New("injected failure to ListTeamRepos")
	}
	return c.repos[teamSlug], nil
}

func (c *fakeTeamRepoClient) UpdateTeamRepoBySlug(org, teamSlug, repo string, permission github.TeamPermission) error {
	if c.failUpdate {
		return errors.New("injected failure to UpdateTeamRepos")
	}

	permissions := github.PermissionsFromTeamPermission(permission)
	updated := false
	for i, repository := range c.repos[teamSlug] {
		if repository.Name == repo {
			c.repos[teamSlug][i].Permissions = permissions
			updated = true
			break
		}
	}

	if !updated {
		c.repos[teamSlug] = append(c.repos[teamSlug], github.Repo{Name: repo, Permissions: permissions})
	}

	return nil
}

func (c *fakeTeamRepoClient) RemoveTeamRepoBySlug(org, teamSlug, repo string) error {
	if c.failRemove {
		return errors.New("injected failure to RemoveTeamRepos")
	}

	for i, repository := range c.repos[teamSlug] {
		if repository.Name == repo {
			c.repos[teamSlug] = append(c.repos[teamSlug][:i], c.repos[teamSlug][i+1:]...)
			break
		}
	}

	return nil
}

func TestConfigureTeamRepos(t *testing.T) {
	var testCases = []struct {
		name          string
		githubTeams   map[string]github.Team
		teamName      string
		team          org.Team
		existingRepos map[string][]github.Repo
		failList      bool
		failUpdate    bool
		failRemove    bool
		expected      map[string][]github.Repo
		expectedErr   bool
	}{
		{
			name:        "githubTeams cache not containing team errors",
			githubTeams: map[string]github.Team{},
			teamName:    "team",
			expectedErr: true,
		},
		{
			name:        "listing repos failing errors",
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}},
			teamName:    "team",
			failList:    true,
			expectedErr: true,
		},
		{
			name:        "nothing to do",
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}},
			teamName:    "team",
			team: org.Team{
				Repos: map[string]github.RepoPermissionLevel{
					"read":     github.Read,
					"triage":   github.Triage,
					"write":    github.Write,
					"maintain": github.Maintain,
					"admin":    github.Admin,
				},
			},
			existingRepos: map[string][]github.Repo{"team": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "triage", Permissions: github.RepoPermissions{Pull: true, Triage: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "maintain", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
			expected: map[string][]github.Repo{"team": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "triage", Permissions: github.RepoPermissions{Pull: true, Triage: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "maintain", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
		},
		{
			name:        "new requirement in org config gets added",
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}},
			teamName:    "team",
			team: org.Team{
				Repos: map[string]github.RepoPermissionLevel{
					"read":        github.Read,
					"write":       github.Write,
					"admin":       github.Admin,
					"other-admin": github.Admin,
				},
			},
			existingRepos: map[string][]github.Repo{"team": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
			expected: map[string][]github.Repo{"team": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
				{Name: "other-admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
		},
		{
			name:        "change in permission on existing gets updated",
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}},
			teamName:    "team",
			team: org.Team{
				Repos: map[string]github.RepoPermissionLevel{
					"read":  github.Read,
					"write": github.Write,
					"admin": github.Read,
				},
			},
			existingRepos: map[string][]github.Repo{"team": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
			expected: map[string][]github.Repo{"team": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true}},
			}},
		},
		{
			name:        "omitted requirement gets removed",
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}},
			teamName:    "team",
			team: org.Team{
				Repos: map[string]github.RepoPermissionLevel{
					"write": github.Write,
					"admin": github.Read,
				},
			},
			existingRepos: map[string][]github.Repo{"team": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
			expected: map[string][]github.Repo{"team": {
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true}},
			}},
		},
		{
			name:        "failed update errors",
			failUpdate:  true,
			githubTeams: map[string]github.Team{"team": {ID: 1}},
			teamName:    "team",
			team: org.Team{
				Repos: map[string]github.RepoPermissionLevel{
					"will-fail": github.Write,
				},
			},
			existingRepos: map[string][]github.Repo{"some-team": {}},
			expected:      map[string][]github.Repo{"some-team": {}},
			expectedErr:   true,
		},
		{
			name:        "failed delete errors",
			failRemove:  true,
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}},
			teamName:    "team",
			team: org.Team{
				Repos: map[string]github.RepoPermissionLevel{},
			},
			existingRepos: map[string][]github.Repo{"team": {
				{Name: "needs-deletion", Permissions: github.RepoPermissions{Pull: true}},
			}},
			expected: map[string][]github.Repo{"team": {
				{Name: "needs-deletion", Permissions: github.RepoPermissions{Pull: true}},
			}},
			expectedErr: true,
		},
		{
			name:        "new requirement in child team config gets added",
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}, "child": {ID: 2, Slug: "child"}},
			teamName:    "team",
			team: org.Team{
				Children: map[string]org.Team{
					"child": {
						Repos: map[string]github.RepoPermissionLevel{
							"read":        github.Read,
							"write":       github.Write,
							"admin":       github.Admin,
							"other-admin": github.Admin,
						},
					},
				},
			},
			existingRepos: map[string][]github.Repo{"child": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
			expected: map[string][]github.Repo{"child": {
				{Name: "read", Permissions: github.RepoPermissions{Pull: true}},
				{Name: "write", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true}},
				{Name: "admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
				{Name: "other-admin", Permissions: github.RepoPermissions{Pull: true, Triage: true, Push: true, Maintain: true, Admin: true}},
			}},
		},
		{
			name:        "failure in a child errors",
			failRemove:  true,
			githubTeams: map[string]github.Team{"team": {ID: 1, Slug: "team"}, "child": {ID: 2, Slug: "child"}},
			teamName:    "team",
			team: org.Team{
				Repos: map[string]github.RepoPermissionLevel{},
				Children: map[string]org.Team{
					"child": {
						Repos: map[string]github.RepoPermissionLevel{},
					},
				},
			},
			existingRepos: map[string][]github.Repo{"child": {
				{Name: "needs-deletion", Permissions: github.RepoPermissions{Pull: true}},
			}},
			expected: map[string][]github.Repo{"child": {
				{Name: "needs-deletion", Permissions: github.RepoPermissions{Pull: true}},
			}},
			expectedErr: true,
		},
	}

	for _, testCase := range testCases {
		client := fakeTeamRepoClient{
			repos:      testCase.existingRepos,
			failList:   testCase.failList,
			failUpdate: testCase.failUpdate,
			failRemove: testCase.failRemove,
		}
		err := configureTeamRepos(&client, testCase.githubTeams, testCase.teamName, "org", testCase.team)
		if err == nil && testCase.expectedErr {
			t.Errorf("%s: expected an error but got none", testCase.name)
		}
		if err != nil && !testCase.expectedErr {
			t.Errorf("%s: expected no error but got one: %v", testCase.name, err)
		}
		if diff := cmp.Diff(client.repos, testCase.expected); diff != "" {
			t.Errorf("%s: got incorrect team repos: %s", testCase.name, diff)
		}
	}
}

type fakeRepoClient struct {
	t     *testing.T
	repos map[string]github.FullRepo
}

func (f fakeRepoClient) GetRepo(owner, name string) (github.FullRepo, error) {
	repo, ok := f.repos[name]
	if !ok {
		return repo, fmt.Errorf("repo not found")
	}
	return repo, nil
}

func (f fakeRepoClient) GetRepos(orgName string, isUser bool) ([]github.Repo, error) {
	if orgName == "fail" {
		return nil, fmt.Errorf("injected GetRepos failure")
	}

	repos := make([]github.Repo, 0, len(f.repos))
	for _, repo := range f.repos {
		repos = append(repos, repo.Repo)
	}

	// sort for deterministic output
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Name < repos[j].Name
	})

	return repos, nil
}

func (f fakeRepoClient) CreateRepo(owner string, isUser bool, repoReq github.RepoCreateRequest) (*github.FullRepo, error) {
	if *repoReq.Name == "fail" {
		return nil, fmt.Errorf("injected CreateRepo failure")
	}

	if _, hasRepo := f.repos[*repoReq.Name]; hasRepo {
		f.t.Errorf("CreateRepo() called on repo that already exists")
		return nil, fmt.Errorf("CreateRepo() called on repo that already exists")
	}

	repo := repoReq.ToRepo()
	f.repos[*repoReq.Name] = *repo

	return repo, nil
}

func (f fakeRepoClient) UpdateRepo(owner, name string, want github.RepoUpdateRequest) (*github.FullRepo, error) {
	if name == "fail" {
		return nil, fmt.Errorf("injected UpdateRepo failure")
	}
	if want.Archived != nil && !*want.Archived {
		f.t.Errorf("UpdateRepo() called to unarchive a repo (not supported by API)")
		return nil, fmt.Errorf("UpdateRepo() called to unarchive a repo (not supported by API)")
	}

	have, exists := f.repos[name]
	if !exists {
		f.t.Errorf("UpdateRepo() called on repo that does not exists")
		return nil, fmt.Errorf("UpdateRepo() called on repo that does not exist")
	}

	if have.Archived {
		return nil, fmt.Errorf("Repository was archived so is read-only.")
	}

	updateString := func(have, want *string) {
		if want != nil {
			*have = *want
		}
	}

	updateBool := func(have, want *bool) {
		if want != nil {
			*have = *want
		}
	}

	updateString(&have.Name, want.Name)
	updateString(&have.DefaultBranch, want.DefaultBranch)
	updateString(&have.Homepage, want.Homepage)
	updateString(&have.Description, want.Description)
	updateBool(&have.Archived, want.Archived)
	updateBool(&have.Private, want.Private)
	updateBool(&have.HasIssues, want.HasIssues)
	updateBool(&have.HasProjects, want.HasProjects)
	updateBool(&have.HasWiki, want.HasWiki)
	updateBool(&have.AllowSquashMerge, want.AllowSquashMerge)
	updateBool(&have.AllowMergeCommit, want.AllowMergeCommit)
	updateBool(&have.AllowRebaseMerge, want.AllowRebaseMerge)
	updateString(&have.SquashMergeCommitTitle, want.SquashMergeCommitTitle)
	updateString(&have.SquashMergeCommitMessage, want.SquashMergeCommitMessage)

	f.repos[name] = have
	return &have, nil
}

func makeFakeRepoClient(t *testing.T, repos ...github.FullRepo) fakeRepoClient {
	fc := fakeRepoClient{
		repos: make(map[string]github.FullRepo, len(repos)),
		t:     t,
	}
	for _, repo := range repos {
		fc.repos[repo.Name] = repo
	}

	return fc
}

func TestConfigureRepos(t *testing.T) {
	orgName := "test-org"
	isOrg := false
	no := false
	yes := true
	updated := "UPDATED"

	oldName := "old"
	oldRepo := github.Repo{
		Name:        oldName,
		FullName:    fmt.Sprintf("%s/%s", orgName, oldName),
		Description: "An old existing repository",
	}

	newName := "new"
	newDescription := "A new repository."
	newConfigRepo := org.Repo{
		RepoMetadata: org.RepoMetadata{
			Description: &newDescription,
		},
	}
	newRepo := github.Repo{
		Name:        newName,
		Description: newDescription,
	}

	fail := "fail"
	failRepo := github.Repo{
		Name: fail,
	}

	testCases := []struct {
		description     string
		opts            options
		orgConfig       org.Config
		orgNameOverride string
		repos           []github.FullRepo

		expectError   bool
		expectedRepos []github.Repo
	}{
		{
			description:   "survives empty config",
			expectedRepos: []github.Repo{},
		},
		{
			description: "survives nil repos config",
			orgConfig: org.Config{
				Repos: nil,
			},
			expectedRepos: []github.Repo{},
		},
		{
			description: "survives empty repos config",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{},
			},
			expectedRepos: []github.Repo{},
		},
		{
			description: "nonexistent repo is created",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectedRepos: []github.Repo{newRepo, oldRepo},
		},
		{
			description: "repo with fork_from is skipped (handled by configureForks)",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"forked-repo": {ForkFrom: ptr.To("upstream/repo")},
				},
			},
			repos:         []github.FullRepo{},
			expectedRepos: []github.Repo{}, // Should NOT create the repo
		},
		{
			description:     "GetRepos failure is propagated",
			orgNameOverride: "fail",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectError:   true,
			expectedRepos: []github.Repo{oldRepo},
		},
		{
			description: "CreateRepo failure is propagated",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					fail: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectError:   true,
			expectedRepos: []github.Repo{oldRepo},
		},
		{
			description: "duplicate repo names different only by case are detected",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"repo": newConfigRepo,
					"REPO": newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},

			expectError:   true,
			expectedRepos: []github.Repo{oldRepo},
		},
		{
			description: "existing repo is updated",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: newConfigRepo,
				},
			},
			repos: []github.FullRepo{{Repo: oldRepo}},
			expectedRepos: []github.Repo{
				{
					Name:        oldName,
					Description: newDescription,
					FullName:    fmt.Sprintf("%s/%s", orgName, oldName),
				},
			},
		},
		{
			description: "UpdateRepo failure is propagated",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"fail": newConfigRepo,
				},
			},
			repos:         []github.FullRepo{{Repo: failRepo}},
			expectError:   true,
			expectedRepos: []github.Repo{failRepo},
		},
		{
			// https://developer.github.com/v3/repos/#edit
			// "Note: You cannot unarchive repositories through the API."
			// Archived repositories are read-only, and updates fail with 403:
			// "Repository was archived so is read-only."
			description: "request to unarchive a repo fails, repo is read-only",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {RepoMetadata: org.RepoMetadata{Archived: &no, Description: &updated}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: true, Description: "OLD"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Archived: true, Description: "OLD"}},
		},
		{
			// https://developer.github.com/v3/repos/#edit
			// "Note: You cannot unarchive repositories through the API."
			// Archived repositories are read-only, and updates fail with 403:
			// "Repository was archived so is read-only."
			description: "no field changes on archived repo",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {RepoMetadata: org.RepoMetadata{Archived: &yes, Description: &updated}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: true, Description: "OLD"}}},
			expectError:   false,
			expectedRepos: []github.Repo{{Name: oldName, Archived: true, Description: "OLD"}},
		},
		{
			description: "request to archive repo fails when not allowed, but updates other fields",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {RepoMetadata: org.RepoMetadata{Archived: &yes, Description: &updated}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: false, Description: "OLD"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Archived: false, Description: updated}},
		},
		{
			description: "request to archive repo succeeds when allowed",
			opts: options{
				allowRepoArchival: true,
			},
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {RepoMetadata: org.RepoMetadata{Archived: &yes}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Archived: false}}},
			expectedRepos: []github.Repo{{Name: oldName, Archived: true}},
		},
		{
			description: "request to publish a private repo fails when not allowed, but updates other fields",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {RepoMetadata: org.RepoMetadata{Private: &no, Description: &updated}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Private: true, Description: "OLD"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Private: true, Description: updated}},
		},
		{
			description: "request to publish a private repo succeeds when allowed",
			opts: options{
				allowRepoPublish: true,
			},
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {RepoMetadata: org.RepoMetadata{Private: &no}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Private: true}}},
			expectedRepos: []github.Repo{{Name: oldName, Private: false}},
		},
		{
			description: "renaming a repo is successful",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Description: "renamed repo"}}},
			expectedRepos: []github.Repo{{Name: newName, Description: "renamed repo"}},
		},
		{
			description: "renaming a repo by just changing case is successful",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"repo": {Previously: []string{"REPO"}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: "REPO", Description: "renamed repo"}}},
			expectedRepos: []github.Repo{{Name: "repo", Description: "renamed repo"}},
		},
		{
			description: "dup between a repo name and a previous name is detected",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName}},
					oldName: {RepoMetadata: org.RepoMetadata{Description: &newDescription}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Description: "this repo shall not be touched"}},
		},
		{
			description: "dup between two previous names is detected",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"wants-projects": {Previously: []string{oldName}, RepoMetadata: org.RepoMetadata{HasProjects: &yes, HasWiki: &no}},
					"wants-wiki":     {Previously: []string{oldName}, RepoMetadata: org.RepoMetadata{HasProjects: &no, HasWiki: &yes}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}}},
			expectError:   true,
			expectedRepos: []github.Repo{{Name: oldName, Description: "this repo shall not be touched"}},
		},
		{
			description: "error detected when both a repo and a repo of its previous name exist",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName}, RepoMetadata: org.RepoMetadata{Description: &newDescription}},
				},
			},
			repos: []github.FullRepo{
				{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}},
				{Repo: github.Repo{Name: newName, Description: "this repo shall not be touched too"}},
			},
			expectError: true,
			expectedRepos: []github.Repo{
				{Name: newName, Description: "this repo shall not be touched too"},
				{Name: oldName, Description: "this repo shall not be touched"},
			},
		},
		{
			description: "error detected when multiple previous repos exist",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					newName: {Previously: []string{oldName, "even-older"}, RepoMetadata: org.RepoMetadata{Description: &newDescription}},
				},
			},
			repos: []github.FullRepo{
				{Repo: github.Repo{Name: oldName, Description: "this repo shall not be touched"}},
				{Repo: github.Repo{Name: "even-older", Description: "this repo shall not be touched too"}},
			},
			expectError: true,
			expectedRepos: []github.Repo{
				{Name: "even-older", Description: "this repo shall not be touched too"},
				{Name: oldName, Description: "this repo shall not be touched"},
			},
		},
		{
			description: "repos are renamed to defined case even without explicit `previously` field",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"CamelCase": {RepoMetadata: org.RepoMetadata{Description: &newDescription}},
				},
			},
			repos:         []github.FullRepo{{Repo: github.Repo{Name: "CAMELCASE", Description: newDescription}}},
			expectedRepos: []github.Repo{{Name: "CamelCase", Description: newDescription}},
		},
		{
			description: "avoid creating archived repo",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					oldName: {RepoMetadata: org.RepoMetadata{Archived: &yes}},
				},
			},
			repos:         []github.FullRepo{},
			expectError:   true,
			expectedRepos: []github.Repo{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fc := makeFakeRepoClient(t, tc.repos...)
			var err error
			if len(tc.orgNameOverride) > 0 {
				err = configureRepos(tc.opts, fc, tc.orgNameOverride, tc.orgConfig)
			} else {
				err = configureRepos(tc.opts, fc, orgName, tc.orgConfig)
			}
			if err != nil && !tc.expectError {
				t.Errorf("%s: unexpected error: %v", tc.description, err)
			}
			if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got none", tc.description)
			}

			reposAfter, err := fc.GetRepos(orgName, isOrg)
			if err != nil {
				t.Fatalf("%s: unexpected GetRepos error: %v", tc.description, err)
			}
			if !reflect.DeepEqual(reposAfter, tc.expectedRepos) {
				t.Errorf("%s: unexpected repos after configureRepos():\n%s", tc.description, cmp.Diff(reposAfter, tc.expectedRepos))
			}
		})
	}
}

func TestValidateRepos(t *testing.T) {
	description := "cool repo"
	testCases := []struct {
		description string
		config      map[string]org.Repo
		expectError bool
	}{
		{
			description: "handles nil map",
		},
		{
			description: "handles empty map",
			config:      map[string]org.Repo{},
		},
		{
			description: "handles valid config",
			config: map[string]org.Repo{
				"repo": {RepoMetadata: org.RepoMetadata{Description: &description}},
			},
		},
		{
			description: "finds repo names duplicate when normalized",
			config: map[string]org.Repo{
				"repo": {RepoMetadata: org.RepoMetadata{Description: &description}},
				"Repo": {RepoMetadata: org.RepoMetadata{Description: &description}},
			},
			expectError: true,
		},
		{
			description: "finds name conflict between previous and current names",
			config: map[string]org.Repo{
				"repo":     {Previously: []string{"conflict"}},
				"conflict": {RepoMetadata: org.RepoMetadata{Description: &description}},
			},
			expectError: true,
		},
		{
			description: "finds name conflict between two previous names",
			config: map[string]org.Repo{
				"repo":         {Previously: []string{"conflict"}},
				"another-repo": {Previously: []string{"conflict"}},
			},
			expectError: true,
		},
		{
			description: "allows case-duplicate name between former and current name",
			config: map[string]org.Repo{
				"repo": {Previously: []string{"REPO"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			err := validateRepos(tc.config)
			if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got none", tc.description)
			} else if err != nil && !tc.expectError {
				t.Errorf("%s: unexpected error: %v", tc.description, err)
			}
		})
	}
}

func TestNewRepoUpdateRequest(t *testing.T) {
	repoName := "repo-name"
	newRepoName := "renamed-repo"
	description := "description of repo-name"
	homepage := "https://somewhe.re"
	master := "master"
	branch := "branch"
	squashMergeCommitTitle := "PR_TITLE"
	squashMergeCommitMessage := "COMMIT_MESSAGES"

	testCases := []struct {
		description string
		current     github.FullRepo
		name        string
		newState    org.Repo

		expected github.RepoUpdateRequest
	}{
		{
			description: "update is just a delta from current state",
			current: github.FullRepo{
				Repo: github.Repo{
					Name:          repoName,
					Description:   description,
					Homepage:      homepage,
					DefaultBranch: master,
				},
			},
			name: repoName,
			newState: org.Repo{
				RepoMetadata: org.RepoMetadata{
					Description:   &description,
					DefaultBranch: &branch,
				},
			},
			expected: github.RepoUpdateRequest{
				DefaultBranch: &branch,
			},
		},
		{
			description: "empty delta is returned when no update is needed",
			current: github.FullRepo{Repo: github.Repo{
				Name:        repoName,
				Description: description,
			}},
			name: repoName,
			newState: org.Repo{
				RepoMetadata: org.RepoMetadata{
					Description: &description,
				},
			},
		},
		{
			description: "request to rename a repo works",
			current: github.FullRepo{Repo: github.Repo{
				Name: repoName,
			}},
			name: newRepoName,
			newState: org.Repo{
				RepoMetadata: org.RepoMetadata{
					Description: &description,
				},
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					Name:        &newRepoName,
					Description: &description,
				},
			},
		},
		{
			description: "request to update commit messages works",
			current: github.FullRepo{
				Repo: github.Repo{
					Name: repoName,
				},
				SquashMergeCommitTitle:   "COMMIT_MESSAGES",
				SquashMergeCommitMessage: "COMMIT_OR_PR_TITLE",
			},
			name: newRepoName,
			newState: org.Repo{
				RepoMetadata: org.RepoMetadata{
					Description:              &description,
					SquashMergeCommitTitle:   &squashMergeCommitTitle,
					SquashMergeCommitMessage: &squashMergeCommitMessage,
				},
			},
			expected: github.RepoUpdateRequest{
				RepoRequest: github.RepoRequest{
					Name:                     &newRepoName,
					Description:              &description,
					SquashMergeCommitTitle:   &squashMergeCommitTitle,
					SquashMergeCommitMessage: &squashMergeCommitMessage,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			update := newRepoUpdateRequest(tc.current, tc.name, tc.newState)
			if !reflect.DeepEqual(tc.expected, update) {
				t.Errorf("%s: update request differs from expected:%s", tc.description, cmp.Diff(tc.expected, update))
			}
		})
	}
}

func TestConfigureCollaborators(t *testing.T) {
	testCases := []struct {
		name                   string
		repo                   org.Repo
		existingCollaborators  map[string]github.RepoPermissionLevel
		existingMembers        []string
		failListCollaborators  bool
		failGetUserPermission  bool
		failAddCollaborator    bool
		failRemoveCollaborator bool
		expectedCollaborators  map[string]github.RepoPermissionLevel
		expectedErr            bool
	}{
		{
			name: "no collaborators configured",
			repo: org.Repo{},
			existingCollaborators: map[string]github.RepoPermissionLevel{
				"external-user": github.Read,
			},
			existingMembers:       []string{},
			expectedCollaborators: map[string]github.RepoPermissionLevel{},
		},
		{
			name: "add new external collaborator",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"new-user": github.Write,
				},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{},
			existingMembers:       []string{},
			expectedCollaborators: map[string]github.RepoPermissionLevel{
				"new-user": github.Write,
			},
		},
		{
			name: "update existing collaborator permission",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"existing-user": github.Admin,
				},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{
				"existing-user": github.Read,
			},
			existingMembers: []string{},
			expectedCollaborators: map[string]github.RepoPermissionLevel{
				"existing-user": github.Admin,
			},
		},
		{
			name: "remove external collaborator not in config",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"keep-user": github.Write,
				},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{
				"keep-user":   github.Write,
				"remove-user": github.Read,
			},
			existingMembers: []string{},
			expectedCollaborators: map[string]github.RepoPermissionLevel{
				"keep-user": github.Write,
			},
		},
		{
			name: "remove direct collaborator not in config (including org members)",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"external-user": github.Write,
				},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{
				"external-user": github.Write,
				"org-member":    github.Read,
			},
			existingMembers: []string{"org-member"},
			expectedCollaborators: map[string]github.RepoPermissionLevel{
				"external-user": github.Write,
				// org-member should be removed since it's a direct collaborator not in config
			},
		},
		{
			name: "org member in collaborators config gets updated",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"org-member": github.Admin,
				},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{
				"org-member": github.Read,
			},
			existingMembers: []string{"org-member"},
			expectedCollaborators: map[string]github.RepoPermissionLevel{
				"org-member": github.Admin,
			},
		},
		{
			name: "permission already correct - no change",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"user1": github.Write,
					"user2": github.Read,
				},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{
				"user1": github.Write,
				"user2": github.Read,
			},
			existingMembers: []string{},
			expectedCollaborators: map[string]github.RepoPermissionLevel{
				"user1": github.Write,
				"user2": github.Read,
			},
		},
		{
			name: "ListCollaborators failure propagates",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"user": github.Write,
				},
			},
			failListCollaborators: true,
			expectedErr:           true,
		},
		{
			name: "AddCollaborator failure propagates",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{
					"user": github.Write,
				},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{},
			existingMembers:       []string{},
			failAddCollaborator:   true,
			expectedErr:           true,
		},
		{
			name: "RemoveCollaborator failure propagates",
			repo: org.Repo{
				Collaborators: map[string]github.RepoPermissionLevel{},
			},
			existingCollaborators: map[string]github.RepoPermissionLevel{
				"external-user": github.Read,
			},
			existingMembers:        []string{},
			failRemoveCollaborator: true,
			expectedErr:            true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeCollaboratorClient{
				collaborators:          make(map[string]github.RepoPermissionLevel),
				members:                sets.New(tc.existingMembers...),
				failListCollaborators:  tc.failListCollaborators,
				failGetUserPermission:  tc.failGetUserPermission,
				failAddCollaborator:    tc.failAddCollaborator,
				failRemoveCollaborator: tc.failRemoveCollaborator,
			}

			// Set up existing collaborators
			for user, permission := range tc.existingCollaborators {
				client.collaborators[user] = permission
			}

			err := configureCollaborators(client, "test-org", "test-repo", tc.repo, map[string]string{})

			if tc.expectedErr && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tc.expectedErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !tc.expectedErr {
				if diff := cmp.Diff(client.collaborators, tc.expectedCollaborators); diff != "" {
					t.Errorf("Collaborators mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

type fakeCollaboratorClient struct {
	collaborators          map[string]github.RepoPermissionLevel
	members                sets.Set[string]
	failListCollaborators  bool
	failGetUserPermission  bool
	failAddCollaborator    bool
	failRemoveCollaborator bool
}

func (f *fakeCollaboratorClient) ListCollaborators(org, repo string) ([]github.User, error) {
	if f.failListCollaborators {
		return nil, fmt.Errorf("ListCollaborators failed")
	}

	var users []github.User
	for username := range f.collaborators {
		users = append(users, github.User{Login: username})
	}
	return users, nil
}

func (f *fakeCollaboratorClient) GetUserPermission(org, repo, user string) (string, error) {
	if f.failGetUserPermission {
		return "", fmt.Errorf("GetUserPermission failed")
	}

	if permission, exists := f.collaborators[user]; exists {
		return string(permission), nil
	}
	return "", fmt.Errorf("user not found")
}

func (f *fakeCollaboratorClient) ListDirectCollaboratorsWithPermissions(org, repo string) (map[string]github.RepoPermissionLevel, error) {
	if f.failListCollaborators {
		return nil, fmt.Errorf("ListDirectCollaboratorsWithPermissions failed")
	}

	// For testing, return the same as the regular collaborators
	// In real usage, this would only return direct collaborators via GraphQL
	return f.collaborators, nil
}

func (f *fakeCollaboratorClient) AddCollaborator(org, repo, user string, permission github.RepoPermissionLevel) error {
	if f.failAddCollaborator {
		return fmt.Errorf("AddCollaborator failed")
	}

	f.collaborators[user] = permission
	return nil
}

func (f *fakeCollaboratorClient) UpdateCollaborator(org, repo, user string, permission github.RepoPermissionLevel) error {
	if f.failAddCollaborator { // Reuse the same failure flag for simplicity
		return fmt.Errorf("UpdateCollaborator failed")
	}

	f.collaborators[user] = permission
	return nil
}

func (f *fakeCollaboratorClient) UpdateCollaboratorRepoInvitation(org, repo string, invitationID int, permission github.RepoPermissionLevel) error {
	if f.failAddCollaborator { // Reuse the same failure flag for simplicity
		return fmt.Errorf("UpdateCollaboratorRepoInvitation failed")
	}

	// For testing, we need to find the user by invitation ID
	// This is a simplified implementation for testing
	f.collaborators[fmt.Sprintf("invitation-%d", invitationID)] = permission
	return nil
}

func (f *fakeCollaboratorClient) DeleteCollaboratorRepoInvitation(org, repo string, invitationID int) error {
	if f.failRemoveCollaborator { // Reuse the same failure flag for simplicity
		return fmt.Errorf("DeleteCollaboratorRepoInvitation failed")
	}

	// For testing, remove the invitation by ID
	delete(f.collaborators, fmt.Sprintf("invitation-%d", invitationID))
	return nil
}

func (f *fakeCollaboratorClient) RemoveCollaborator(org, repo, user string) error {
	if f.failRemoveCollaborator {
		return fmt.Errorf("RemoveCollaborator failed")
	}

	delete(f.collaborators, user)
	return nil
}

func (f *fakeCollaboratorClient) UpdateCollaboratorPermission(org, repo, user string, permission github.RepoPermissionLevel) error {
	// For testing, this is the same as AddCollaborator
	return f.AddCollaborator(org, repo, user, permission)
}

func (f *fakeCollaboratorClient) ListOrgMembers(org, role string) ([]github.TeamMember, error) {
	var members []github.TeamMember
	for member := range f.members {
		members = append(members, github.TeamMember{Login: member})
	}
	return members, nil
}

func (f *fakeCollaboratorClient) ListRepoInvitations(org, repo string) ([]github.CollaboratorRepoInvitation, error) {
	// For testing, return empty list
	return []github.CollaboratorRepoInvitation{}, nil
}

func TestConfigureCollaboratorsRemovePendingInvitations(t *testing.T) {
	// Test that pending invitations are removed when users are not in config
	// This matches the behavior of organization membership invitations
	client := &fakeCollaboratorClientWithInvitations{
		fakeCollaboratorClient: &fakeCollaboratorClient{
			collaborators: make(map[string]github.RepoPermissionLevel),
			members:       sets.Set[string]{},
		},
		pendingInvitations: []github.CollaboratorRepoInvitation{
			{
				InvitationID: 1001,
				Invitee:      &github.User{Login: "remove-pending"},
				Permission:   github.Read, // Has pending invitation but not in config - should be removed
			},
			{
				InvitationID: 1002,
				Invitee:      &github.User{Login: "keep-pending"},
				Permission:   github.Write, // Has pending invitation and is in config - should be kept
			},
		},
	}

	repo := org.Repo{
		Collaborators: map[string]github.RepoPermissionLevel{
			"keep-pending": github.Write, // This user has pending invitation and should be kept
			"new-user":     github.Admin, // This user has no invitation and should get one
		},
		// Note: "remove-pending" is NOT in the config, so their invitation should be removed
	}

	err := configureCollaborators(client, "test-org", "test-repo", repo, map[string]string{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify that remove-pending invitation was deleted (DeleteRepoInvitation called)
	// Since remove-pending is a pending invitation, it should use DeleteRepoInvitation, not RemoveCollaborator
	expectedDeletedInvitations := []int{1001} // remove-pending has invitation ID 1001
	actualDeletedInvitations := client.deleteInvitationCalls

	if len(actualDeletedInvitations) != len(expectedDeletedInvitations) {
		t.Errorf("Expected invitation deletion calls for %v, got deletion calls for %v", expectedDeletedInvitations, actualDeletedInvitations)
	}

	for _, expectedID := range expectedDeletedInvitations {
		found := false
		for _, deletedID := range actualDeletedInvitations {
			if deletedID == expectedID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected invitation ID %d to be deleted, but was not", expectedID)
		}
	}

	// Verify that no actual collaborators were removed (removedUsers should be empty since remove-pending is an invitation)
	if len(client.removedUsers) != 0 {
		t.Errorf("Expected no RemoveCollaborator calls, got %v", client.removedUsers)
	}

	// Verify keep-pending was NOT deleted (should not be in deleteInvitationCalls)
	for _, deletedID := range actualDeletedInvitations {
		if deletedID == 1002 { // keep-pending has invitation ID 1002
			t.Errorf("Invitation ID %d (keep-pending) should not have been deleted", deletedID)
		}
	}
}

func TestConfigureCollaboratorsInvitationManagement(t *testing.T) {
	// Comprehensive test for all invitation scenarios:
	// 1. Pending invitation with correct permission -> wait
	// 2. Pending invitation with wrong permission -> update
	// 3. Pending invitation not in config -> remove
	// 4. No invitation, user in config -> create
	// 5. Current collaborator with wrong permission -> update
	client := &fakeCollaboratorClientWithInvitations{
		fakeCollaboratorClient: &fakeCollaboratorClient{
			collaborators: map[string]github.RepoPermissionLevel{
				"current-user": github.Read, // Current collaborator with wrong permission
			},
			members: sets.Set[string]{},
		},
		pendingInvitations: []github.CollaboratorRepoInvitation{
			{
				InvitationID: 2001,
				Invitee:      &github.User{Login: "pending-correct"},
				Permission:   github.Write, // Correct permission - should wait
			},
			{
				InvitationID: 2002,
				Invitee:      &github.User{Login: "pending-wrong"},
				Permission:   github.Read, // Wrong permission - should update
			},
			{
				InvitationID: 2003,
				Invitee:      &github.User{Login: "pending-remove"},
				Permission:   github.Admin, // Not in config - should remove
			},
		},
	}

	repo := org.Repo{
		Collaborators: map[string]github.RepoPermissionLevel{
			"pending-correct": github.Write, // Matches pending - should wait
			"pending-wrong":   github.Admin, // Different from pending - should update invitation
			"new-user":        github.Read,  // No invitation - should create
			"current-user":    github.Admin, // Current user - should update
			// Note: "pending-remove" not in config - should remove invitation
		},
	}

	err := configureCollaborators(client, "test-org", "test-repo", repo, map[string]string{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify API calls were made for the right users
	expectedAPICallUsers := []string{"pending-wrong", "new-user", "current-user"}
	actualAPICallUsers := client.apiCallsUsers

	if len(actualAPICallUsers) != len(expectedAPICallUsers) {
		t.Errorf("Expected API calls for %v, got API calls for %v", expectedAPICallUsers, actualAPICallUsers)
	}

	// Verify removals - since pending-remove is a pending invitation, it should use DeleteRepoInvitation
	expectedDeletedInvitations := []int{2003} // pending-remove has invitation ID 2003
	actualDeletedInvitations := client.deleteInvitationCalls

	if len(actualDeletedInvitations) != len(expectedDeletedInvitations) {
		t.Errorf("Expected invitation deletions for %v, got deletions for %v", expectedDeletedInvitations, actualDeletedInvitations)
	}

	// Verify pending-correct was NOT touched (should not be in API calls or deletions)
	for _, user := range actualAPICallUsers {
		if user == "pending-correct" {
			t.Errorf("pending-correct should not have API call (has correct pending invitation)")
		}
	}
	for _, deletedID := range actualDeletedInvitations {
		if deletedID == 2001 { // pending-correct has invitation ID 2001
			t.Errorf("Invitation ID %d (pending-correct) should not have been deleted", deletedID)
		}
	}
}

func TestConfigureCollaboratorsInvitationPermissionChecking(t *testing.T) {
	// Test that invitation checking compares permissions, not just existence
	client := &fakeCollaboratorClientWithInvitations{
		fakeCollaboratorClient: &fakeCollaboratorClient{
			collaborators: make(map[string]github.RepoPermissionLevel),
			members:       sets.Set[string]{},
		},
		pendingInvitations: []github.CollaboratorRepoInvitation{
			{
				InvitationID: 3001,
				Invitee:      &github.User{Login: "pending-user"},
				Permission:   github.Read, // Has pending invitation with READ permission
			},
			{
				InvitationID: 3002,
				Invitee:      &github.User{Login: "Another-Pending"},
				Permission:   github.Write, // Has pending invitation with WRITE permission
			},
		},
	}

	repo := org.Repo{
		Collaborators: map[string]github.RepoPermissionLevel{
			"pending-user":    github.Read,  // Matches pending permission - should skip API call
			"another-pending": github.Admin, // Different from pending permission - should update invitation
			"new-user":        github.Write, // No pending invitation - should create invitation
		},
	}

	err := configureCollaborators(client, "test-org", "test-repo", repo, map[string]string{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify correct behavior:
	// - pending-user should be skipped (correct pending permission)
	// - another-pending should be updated (wrong pending permission)
	// - new-user should be added (no pending invitation)

	// Check which users had API calls made
	expectedAPICallUsers := []string{"another-pending", "new-user"}
	actualAPICallUsers := client.apiCallsUsers

	if len(actualAPICallUsers) != len(expectedAPICallUsers) {
		t.Errorf("Expected API calls for %v, got API calls for %v", expectedAPICallUsers, actualAPICallUsers)
	}

	// pending-user should NOT have had an API call
	for _, user := range actualAPICallUsers {
		if user == "pending-user" {
			t.Errorf("pending-user should not have API call (has correct pending invitation)")
		}
	}
}

// fakeCollaboratorClientWithInvitations extends the fake client to track API calls
type fakeCollaboratorClientWithInvitations struct {
	*fakeCollaboratorClient
	pendingInvitations      []github.CollaboratorRepoInvitation
	apiCallsUsers           []string // Track which users had API calls made
	removedUsers            []string // Track which users were removed
	updateInvitationCalls   []int    // Track invitation IDs that were updated
	deleteInvitationCalls   []int    // Track invitation IDs that were deleted
	addCollaboratorCalls    []string // Track users that were added via AddCollaborator
	updateCollaboratorCalls []string // Track users that were updated via UpdateCollaborator
}

func (c *fakeCollaboratorClientWithInvitations) ListRepoInvitations(org, repo string) ([]github.CollaboratorRepoInvitation, error) {
	return c.pendingInvitations, nil
}

func (c *fakeCollaboratorClientWithInvitations) ListDirectCollaboratorsWithPermissions(org, repo string) (map[string]github.RepoPermissionLevel, error) {
	return c.fakeCollaboratorClient.ListDirectCollaboratorsWithPermissions(org, repo)
}

func (c *fakeCollaboratorClientWithInvitations) AddCollaborator(org, repo, user string, permission github.RepoPermissionLevel) error {
	c.apiCallsUsers = append(c.apiCallsUsers, user)
	c.addCollaboratorCalls = append(c.addCollaboratorCalls, user)
	return c.fakeCollaboratorClient.AddCollaborator(org, repo, user, permission)
}

func (c *fakeCollaboratorClientWithInvitations) UpdateCollaborator(org, repo, user string, permission github.RepoPermissionLevel) error {
	c.apiCallsUsers = append(c.apiCallsUsers, user)
	c.updateCollaboratorCalls = append(c.updateCollaboratorCalls, user)
	return c.fakeCollaboratorClient.UpdateCollaborator(org, repo, user, permission)
}

func (c *fakeCollaboratorClientWithInvitations) UpdateCollaboratorRepoInvitation(org, repo string, invitationID int, permission github.RepoPermissionLevel) error {
	c.apiCallsUsers = append(c.apiCallsUsers, fmt.Sprintf("invitation-%d", invitationID))
	c.updateInvitationCalls = append(c.updateInvitationCalls, invitationID)
	return c.fakeCollaboratorClient.UpdateCollaboratorRepoInvitation(org, repo, invitationID, permission)
}

func (c *fakeCollaboratorClientWithInvitations) DeleteCollaboratorRepoInvitation(org, repo string, invitationID int) error {
	c.deleteInvitationCalls = append(c.deleteInvitationCalls, invitationID)
	return c.fakeCollaboratorClient.DeleteCollaboratorRepoInvitation(org, repo, invitationID)
}

func (c *fakeCollaboratorClientWithInvitations) UpdateCollaboratorPermission(org, repo, user string, permission github.RepoPermissionLevel) error {
	c.apiCallsUsers = append(c.apiCallsUsers, user)
	return c.fakeCollaboratorClient.UpdateCollaboratorPermission(org, repo, user, permission)
}

func (c *fakeCollaboratorClientWithInvitations) RemoveCollaborator(org, repo, user string) error {
	c.removedUsers = append(c.removedUsers, user)
	return c.fakeCollaboratorClient.RemoveCollaborator(org, repo, user)
}

func TestConfigureCollaboratorsLargeSet(t *testing.T) {
	// Generate a large set of collaborators and invitations to ensure we handle scale and don’t miss actions
	const numExisting = 500
	const numInvites = 300
	const numDesiredAdds = 400

	existing := make(map[string]github.RepoPermissionLevel, numExisting)
	for i := 0; i < numExisting; i++ {
		existing[fmt.Sprintf("existing-%04d", i)] = github.Read
	}

	var invites []github.CollaboratorRepoInvitation
	for i := 0; i < numInvites; i++ {
		invites = append(invites, github.CollaboratorRepoInvitation{
			InvitationID: 10000 + i,
			Invitee:      &github.User{Login: fmt.Sprintf("invite-%04d", i)},
			Permission:   github.Read,
		})
	}

	desired := make(map[string]github.RepoPermissionLevel, numDesiredAdds)
	// Keep half of existing (should update to write), drop the rest
	for i := 0; i < numExisting; i += 2 {
		desired[fmt.Sprintf("existing-%04d", i)] = github.Write
	}
	// Keep half of invites (should update to write), drop the rest
	for i := 0; i < numInvites; i += 2 {
		desired[fmt.Sprintf("invite-%04d", i)] = github.Write
	}
	// Add fresh desired users
	for i := 0; i < numDesiredAdds; i++ {
		desired[fmt.Sprintf("new-%04d", i)] = github.Admin
	}

	client := &fakeCollaboratorClientWithInvitations{
		fakeCollaboratorClient: &fakeCollaboratorClient{
			collaborators: existing,
			members:       sets.Set[string]{},
		},
		pendingInvitations: invites,
	}

	repo := org.Repo{Collaborators: desired}
	if err := configureCollaborators(client, "org", "repo", repo, map[string]string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Sanity: we should have removals for dropped existing and dropped invites
	expectedRemovals := (numExisting / 2) + (numInvites / 2)
	if len(client.removedUsers)+len(client.deleteInvitationCalls) < expectedRemovals {
		t.Errorf("expected at least %d removals/cancellations, got %d removals and %d cancellations", expectedRemovals, len(client.removedUsers), len(client.deleteInvitationCalls))
	}

	// Sanity: we should have adds for fresh desired users
	if len(client.addCollaboratorCalls) < numDesiredAdds {
		t.Errorf("expected at least %d add calls, got %d", numDesiredAdds, len(client.addCollaboratorCalls))
	}

	// No panics and deterministic behavior implied by sorted order; basic coverage for large sets
}

func TestConfigureCollaboratorsCorrectAPIEndpoints(t *testing.T) {
	// Test that the correct API endpoints are called with invitation IDs
	client := &fakeCollaboratorClientWithInvitations{
		fakeCollaboratorClient: &fakeCollaboratorClient{
			collaborators: map[string]github.RepoPermissionLevel{
				"existing-user": github.Read, // Current collaborator
			},
			members: sets.Set[string]{},
		},
		pendingInvitations: []github.CollaboratorRepoInvitation{
			{
				InvitationID: 4001,
				Invitee:      &github.User{Login: "pending-update"},
				Permission:   github.Read, // Will be updated to Write
			},
		},
	}

	repo := org.Repo{
		Collaborators: map[string]github.RepoPermissionLevel{
			"existing-user":  github.Write, // Update existing collaborator
			"pending-update": github.Write, // Update pending invitation
			"new-user":       github.Admin, // New invitation
		},
	}

	err := configureCollaborators(client, "test-org", "test-repo", repo, map[string]string{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify that UpdateRepoInvitation was called with the correct invitation ID
	expectedInvitationIDs := []int{4001}
	if len(client.updateInvitationCalls) != len(expectedInvitationIDs) {
		t.Errorf("Expected UpdateRepoInvitation calls for %v, got %v", expectedInvitationIDs, client.updateInvitationCalls)
	}
	for i, expectedID := range expectedInvitationIDs {
		if i < len(client.updateInvitationCalls) && client.updateInvitationCalls[i] != expectedID {
			t.Errorf("Expected invitation ID %d, got %d", expectedID, client.updateInvitationCalls[i])
		}
	}

	// Verify that AddCollaborator was called for existing user and new user
	expectedAddCalls := []string{"existing-user", "new-user"}
	if len(client.addCollaboratorCalls) != len(expectedAddCalls) {
		t.Errorf("Expected AddCollaborator calls for %v, got %v", expectedAddCalls, client.addCollaboratorCalls)
	}

	// Verify that UpdateCollaborator was NOT called (we should use UpdateRepoInvitation for pending invitations)
	if len(client.updateCollaboratorCalls) != 0 {
		t.Errorf("Expected no UpdateCollaborator calls, got %v", client.updateCollaboratorCalls)
	}
}

func TestConfigureCollaboratorsInvitationVsCollaboratorRemoval(t *testing.T) {
	// Test that the correct removal method is used: DeleteRepoInvitation for invitations, RemoveCollaborator for actual collaborators
	client := &fakeCollaboratorClientWithInvitations{
		fakeCollaboratorClient: &fakeCollaboratorClient{
			collaborators: map[string]github.RepoPermissionLevel{
				"actual-collaborator": github.Read, // This is an actual collaborator, should use RemoveCollaborator
			},
			members: sets.Set[string]{},
		},
		pendingInvitations: []github.CollaboratorRepoInvitation{
			{
				InvitationID: 5001,
				Invitee:      &github.User{Login: "pending-invitation"},
				Permission:   github.Write, // This is a pending invitation, should use DeleteRepoInvitation
			},
		},
	}

	repo := org.Repo{
		Collaborators: map[string]github.RepoPermissionLevel{
			// Both actual-collaborator and pending-invitation are NOT in config, so both should be removed
			// but using different methods
		},
	}

	err := configureCollaborators(client, "test-org", "test-repo", repo, map[string]string{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify that DeleteRepoInvitation was called for the pending invitation
	expectedDeletedInvitations := []int{5001}
	if len(client.deleteInvitationCalls) != len(expectedDeletedInvitations) {
		t.Errorf("Expected DeleteRepoInvitation calls for %v, got %v", expectedDeletedInvitations, client.deleteInvitationCalls)
	}

	// Verify that RemoveCollaborator was called for the actual collaborator
	expectedRemovedUsers := []string{"actual-collaborator"}
	if len(client.removedUsers) != len(expectedRemovedUsers) {
		t.Errorf("Expected RemoveCollaborator calls for %v, got %v", expectedRemovedUsers, client.removedUsers)
	}
}

func TestConfigureCollaborators_Idempotent_NoChangeForDirectCollaborator(t *testing.T) {
	client := &fakeCollaboratorClientWithInvitations{
		fakeCollaboratorClient: &fakeCollaboratorClient{
			collaborators: map[string]github.RepoPermissionLevel{
				"user": github.Write,
			},
			members: sets.Set[string]{},
		},
		pendingInvitations: []github.CollaboratorRepoInvitation{},
	}

	repo := org.Repo{
		Collaborators: map[string]github.RepoPermissionLevel{
			"user": github.Write,
		},
	}

	err := configureCollaborators(client, "test-org", "test-repo", repo, map[string]string{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(client.apiCallsUsers) != 0 || len(client.updateInvitationCalls) != 0 || len(client.deleteInvitationCalls) != 0 || len(client.removedUsers) != 0 {
		t.Errorf("expected no API calls for idempotent state; got apiCalls=%v updateInv=%v deleteInv=%v removed=%v", client.apiCallsUsers, client.updateInvitationCalls, client.deleteInvitationCalls, client.removedUsers)
	}
}

func TestConfigureCollaborators_PermissionMatrix_TransitionsExistingCollaborator(t *testing.T) {
	levels := []github.RepoPermissionLevel{github.Admin, github.Maintain, github.Write, github.Triage, github.Read}

	for _, from := range levels {
		for _, to := range levels {
			if from == to {
				continue
			}
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				client := &fakeCollaboratorClientWithInvitations{
					fakeCollaboratorClient: &fakeCollaboratorClient{
						collaborators: map[string]github.RepoPermissionLevel{
							"user": from,
						},
						members: sets.Set[string]{},
					},
					pendingInvitations: []github.CollaboratorRepoInvitation{},
				}

				repo := org.Repo{Collaborators: map[string]github.RepoPermissionLevel{"user": to}}

				err := configureCollaborators(client, "org", "repo", repo, map[string]string{})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if got := client.collaborators["user"]; got != to {
					t.Errorf("permission not updated: want %s, got %s", to, got)
				}

				if len(client.updateInvitationCalls) != 0 || len(client.deleteInvitationCalls) != 0 {
					t.Errorf("unexpected invitation operations: update=%v delete=%v", client.updateInvitationCalls, client.deleteInvitationCalls)
				}

				if len(client.addCollaboratorCalls) != 1 || client.addCollaboratorCalls[0] != "user" {
					t.Errorf("expected exactly one AddCollaborator call for 'user', got %v", client.addCollaboratorCalls)
				}
			})
		}
	}
}

func TestConfigureCollaborators_PermissionMatrix_PendingInvitationUpdates(t *testing.T) {
	levels := []github.RepoPermissionLevel{github.Admin, github.Maintain, github.Write, github.Triage, github.Read}

	for _, from := range levels {
		for _, to := range levels {
			if from == to {
				continue
			}
			t.Run(fmt.Sprintf("pending_%s_to_%s", from, to), func(t *testing.T) {
				client := &fakeCollaboratorClientWithInvitations{
					fakeCollaboratorClient: &fakeCollaboratorClient{
						collaborators: map[string]github.RepoPermissionLevel{},
						members:       sets.Set[string]{},
					},
					pendingInvitations: []github.CollaboratorRepoInvitation{
						{InvitationID: 9001, Invitee: &github.User{Login: "user"}, Permission: from},
					},
				}

				repo := org.Repo{Collaborators: map[string]github.RepoPermissionLevel{"user": to}}

				err := configureCollaborators(client, "org", "repo", repo, map[string]string{})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if len(client.updateInvitationCalls) != 1 || client.updateInvitationCalls[0] != 9001 {
					t.Errorf("expected one UpdateRepoInvitation call for invitation 9001, got %v", client.updateInvitationCalls)
				}

				if len(client.addCollaboratorCalls) != 0 {
					t.Errorf("expected no AddCollaborator calls when updating pending invitation, got %v", client.addCollaboratorCalls)
				}
			})
		}
	}
}

// forkCreation tracks details of a fork creation call
type forkCreation struct {
	upstream          string // "owner/repo"
	defaultBranchOnly bool
}

// fakeForkClient implements the forkClient interface for testing
type fakeForkClient struct {
	repos            map[string]github.Repo     // repo name -> repo
	fullRepos        map[string]github.FullRepo // repo name -> full repo
	createdForks     []forkCreation             // list of fork creation calls with details
	createForkErr    error
	getRepoErr       error
	getReposErr      error
	forkNameOverride string // if set, CreateForkInOrg returns this name instead
}

func (f *fakeForkClient) GetRepo(owner, name string) (github.FullRepo, error) {
	if f.getRepoErr != nil {
		return github.FullRepo{}, f.getRepoErr
	}
	if repo, ok := f.fullRepos[name]; ok {
		return repo, nil
	}
	return github.FullRepo{}, fmt.Errorf("repo not found: %s/%s", owner, name)
}

func (f *fakeForkClient) GetRepos(org string, isUser bool) ([]github.Repo, error) {
	if f.getReposErr != nil {
		return nil, f.getReposErr
	}
	var repos []github.Repo
	for _, r := range f.repos {
		repos = append(repos, r)
	}
	return repos, nil
}

func (f *fakeForkClient) CreateForkInOrg(owner, repo, targetOrg string, defaultBranchOnly bool) (string, error) {
	if f.createForkErr != nil {
		return "", f.createForkErr
	}
	f.createdForks = append(f.createdForks, forkCreation{
		upstream:          fmt.Sprintf("%s/%s", owner, repo),
		defaultBranchOnly: defaultBranchOnly,
	})
	createdName := repo
	if f.forkNameOverride != "" {
		createdName = f.forkNameOverride
	}
	// Simulate fork becoming available for waitForFork
	if f.fullRepos == nil {
		f.fullRepos = make(map[string]github.FullRepo)
	}
	f.fullRepos[createdName] = github.FullRepo{
		Repo: github.Repo{
			Name: createdName,
			Fork: true,
			Parent: github.ParentRepo{
				FullName: fmt.Sprintf("%s/%s", owner, repo),
			},
		},
	}
	return createdName, nil
}

func TestConfigureForks(t *testing.T) {
	upstream := "upstream-org/upstream-repo"
	forkName := "upstream-repo"

	testCases := []struct {
		description      string
		orgConfig        org.Config
		existingRepos    map[string]github.Repo
		fullRepos        map[string]github.FullRepo
		createForkErr    error
		getReposErr      error
		getRepoErr       error
		forkNameOverride string

		expectError   bool
		expectedForks []forkCreation
	}{
		{
			description: "no forks configured - does nothing",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"regular-repo": {RepoMetadata: org.RepoMetadata{Description: ptr.To("a regular repo")}},
				},
			},
			existingRepos: map[string]github.Repo{},
			expectedForks: nil,
		},
		{
			description: "creates fork when repo doesn't exist",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{},
			expectedForks: []forkCreation{{upstream: upstream, defaultBranchOnly: false}},
		},
		{
			description: "skips fork when repo already exists as correct fork",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{
				forkName: {Name: forkName, Fork: true},
			},
			fullRepos: map[string]github.FullRepo{
				forkName: {
					Repo: github.Repo{Name: forkName, Fork: true, Parent: github.ParentRepo{FullName: upstream}},
				},
			},
			expectedForks: nil,
		},
		{
			description: "errors when repo exists but is not a fork",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{
				forkName: {Name: forkName, Fork: false},
			},
			fullRepos: map[string]github.FullRepo{
				forkName: {Repo: github.Repo{Name: forkName, Fork: false}},
			},
			expectError: true,
		},
		{
			description: "errors when fork exists from different upstream",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{
				forkName: {Name: forkName, Fork: true},
			},
			fullRepos: map[string]github.FullRepo{
				forkName: {
					Repo: github.Repo{Name: forkName, Fork: true, Parent: github.ParentRepo{FullName: "other-org/other-repo"}},
				},
			},
			expectError: true,
		},
		{
			description: "errors on invalid fork_from format",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To("invalid-format")},
				},
			},
			existingRepos: map[string]github.Repo{},
			expectError:   true,
		},
		{
			description: "handles CreateForkInOrg error (e.g., generic failure)",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{},
			createForkErr: errors.New("failed to create fork"),
			expectError:   true,
		},
		{
			description: "errors when upstream repo does not exist (404)",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To("nonexistent-org/nonexistent-repo")},
				},
			},
			existingRepos: map[string]github.Repo{},
			createForkErr: errors.New("Not Found"),
			expectError:   true,
		},
		{
			description: "creates multiple forks",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"fork1": {ForkFrom: ptr.To("org1/repo1")},
					"fork2": {ForkFrom: ptr.To("org2/repo2")},
				},
			},
			existingRepos: map[string]github.Repo{},
			expectedForks: []forkCreation{
				{upstream: "org1/repo1", defaultBranchOnly: false},
				{upstream: "org2/repo2", defaultBranchOnly: false},
			},
		},
		// New test cases for full coverage
		{
			description:   "GetRepos failure is propagated",
			orgConfig:     org.Config{Repos: map[string]org.Repo{forkName: {ForkFrom: ptr.To(upstream)}}},
			existingRepos: map[string]github.Repo{},
			getReposErr:   errors.New("failed to get repos"),
			expectError:   true,
		},
		{
			description: "GetRepo failure when checking existing fork parent",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{
				forkName: {Name: forkName, Fork: true},
			},
			getRepoErr:  errors.New("failed to get repo details"),
			expectError: true,
		},
		{
			description: "empty fork_from string is skipped",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"repo-with-empty-fork": {ForkFrom: ptr.To("")},
					"regular-repo":         {RepoMetadata: org.RepoMetadata{Description: ptr.To("normal")}},
				},
			},
			existingRepos: map[string]github.Repo{},
			expectedForks: nil, // No forks should be created
		},
		{
			description: "case-insensitive repo name matching",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"My-Fork": {ForkFrom: ptr.To(upstream)}, // Config uses different case
				},
			},
			existingRepos: map[string]github.Repo{
				"my-fork": {Name: "my-fork", Fork: true}, // Org has lowercase
			},
			fullRepos: map[string]github.FullRepo{
				"my-fork": {
					Repo: github.Repo{Name: "my-fork", Fork: true, Parent: github.ParentRepo{FullName: upstream}},
				},
			},
			expectedForks: nil, // Should recognize existing fork despite case difference
		},
		{
			description: "DefaultBranchOnly parameter is passed correctly",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					forkName: {ForkFrom: ptr.To(upstream), DefaultBranchOnly: ptr.To(true)},
				},
			},
			existingRepos: map[string]github.Repo{},
			expectedForks: []forkCreation{{upstream: upstream, defaultBranchOnly: true}},
		},
		{
			description: "fork created with different name logs warning (no error)",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"my-custom-name": {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos:    map[string]github.Repo{},
			forkNameOverride: "upstream-repo", // GitHub returns different name
			expectedForks:    []forkCreation{{upstream: upstream, defaultBranchOnly: false}},
			expectError:      false, // Should succeed with warning, not error
		},
		{
			description: "mixed success and failure - one fork succeeds, one fails",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"good-fork":    {ForkFrom: ptr.To("good-org/good-repo")},
					"invalid-fork": {ForkFrom: ptr.To("no-slash")}, // Invalid format
				},
			},
			existingRepos: map[string]github.Repo{},
			expectError:   true,                                                                       // Should error due to invalid fork
			expectedForks: []forkCreation{{upstream: "good-org/good-repo", defaultBranchOnly: false}}, // But good fork should still be created
		},
		{
			description:   "nil Repos map does nothing",
			orgConfig:     org.Config{Repos: nil},
			existingRepos: map[string]github.Repo{},
			expectedForks: nil,
		},
		{
			description:   "empty Repos map does nothing",
			orgConfig:     org.Config{Repos: map[string]org.Repo{}},
			existingRepos: map[string]github.Repo{},
			expectedForks: nil,
		},
		// Idempotency tests: fork lookup by upstream parent
		{
			description: "idempotency: fork exists with different name than config (renamed by GitHub)",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"my-custom-name": {ForkFrom: ptr.To(upstream)}, // Config uses custom name
				},
			},
			existingRepos: map[string]github.Repo{
				// GitHub created it as "upstream-repo" (upstream's name), not "my-custom-name"
				"upstream-repo": {Name: "upstream-repo", Fork: true},
			},
			fullRepos: map[string]github.FullRepo{
				"upstream-repo": {
					Repo: github.Repo{Name: "upstream-repo", Fork: true, Parent: github.ParentRepo{FullName: upstream}},
				},
			},
			expectedForks: nil,   // Should NOT try to create - fork of upstream already exists
			expectError:   false, // No error - just logs that fork exists with different name
		},
		{
			description: "idempotency: fork exists with same name and correct upstream (standard case)",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"upstream-repo": {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{
				"upstream-repo": {Name: "upstream-repo", Fork: true},
			},
			fullRepos: map[string]github.FullRepo{
				"upstream-repo": {
					Repo: github.Repo{Name: "upstream-repo", Fork: true, Parent: github.ParentRepo{FullName: upstream}},
				},
			},
			expectedForks: nil, // Should NOT try to create - already exists correctly
			expectError:   false,
		},
		{
			description: "idempotency: case-insensitive upstream matching",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"my-fork": {ForkFrom: ptr.To("UPSTREAM-ORG/UPSTREAM-REPO")}, // Uppercase in config
				},
			},
			existingRepos: map[string]github.Repo{
				"existing-fork": {Name: "existing-fork", Fork: true},
			},
			fullRepos: map[string]github.FullRepo{
				"existing-fork": {
					Repo: github.Repo{Name: "existing-fork", Fork: true, Parent: github.ParentRepo{FullName: "upstream-org/upstream-repo"}}, // Lowercase from GitHub
				},
			},
			expectedForks: nil, // Should match despite case difference
			expectError:   false,
		},
		{
			description: "idempotency: no existing fork of upstream - creates new fork",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"my-fork": {ForkFrom: ptr.To(upstream)},
				},
			},
			existingRepos: map[string]github.Repo{
				// Org has other forks, but none from our target upstream
				"other-fork": {Name: "other-fork", Fork: true},
			},
			fullRepos: map[string]github.FullRepo{
				"other-fork": {
					Repo: github.Repo{Name: "other-fork", Fork: true, Parent: github.ParentRepo{FullName: "different-org/different-repo"}},
				},
			},
			expectedForks: []forkCreation{{upstream: upstream, defaultBranchOnly: false}}, // Should create since no fork of upstream exists
			expectError:   false,
		},
		{
			description: "idempotency: multiple configs, one upstream already forked",
			orgConfig: org.Config{
				Repos: map[string]org.Repo{
					"fork-a": {ForkFrom: ptr.To("org-a/repo-a")}, // Already forked (exists as "repo-a")
					"fork-b": {ForkFrom: ptr.To("org-b/repo-b")}, // Not yet forked
				},
			},
			existingRepos: map[string]github.Repo{
				"repo-a": {Name: "repo-a", Fork: true}, // Fork of org-a/repo-a exists with different name
			},
			fullRepos: map[string]github.FullRepo{
				"repo-a": {
					Repo: github.Repo{Name: "repo-a", Fork: true, Parent: github.ParentRepo{FullName: "org-a/repo-a"}},
				},
			},
			expectedForks: []forkCreation{{upstream: "org-b/repo-b", defaultBranchOnly: false}}, // Only fork-b should be created
			expectError:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			client := &fakeForkClient{
				repos:            tc.existingRepos,
				fullRepos:        tc.fullRepos,
				createForkErr:    tc.createForkErr,
				getReposErr:      tc.getReposErr,
				getRepoErr:       tc.getRepoErr,
				forkNameOverride: tc.forkNameOverride,
			}

			forkNames, err := configureForks(client, "test-org", tc.orgConfig)

			if tc.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				// Verify forkNames is not nil on success
				if forkNames == nil {
					t.Error("forkNames should not be nil on success")
				}
			}

			// Check created forks
			if tc.expectedForks == nil {
				if len(client.createdForks) != 0 {
					t.Errorf("expected no forks to be created, but got: %v", client.createdForks)
				}
			} else {
				// Sort both slices for comparison
				sort.Slice(client.createdForks, func(i, j int) bool {
					return client.createdForks[i].upstream < client.createdForks[j].upstream
				})
				sort.Slice(tc.expectedForks, func(i, j int) bool {
					return tc.expectedForks[i].upstream < tc.expectedForks[j].upstream
				})
				if !reflect.DeepEqual(client.createdForks, tc.expectedForks) {
					t.Errorf("created forks mismatch:\nexpected: %v\ngot: %v", tc.expectedForks, client.createdForks)
				}
			}
		})
	}
}
