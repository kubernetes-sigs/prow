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
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/prow/pkg/config/org"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/logrusutil"
)

const (
	defaultMinAdmins = 5
	defaultDelta     = 0.25
	defaultTokens    = 300
	defaultBurst     = 100
)

type options struct {
	config            string
	confirm           bool
	dump              string
	dumpFull          bool
	maximumDelta      float64
	minAdmins         int
	requireSelf       bool
	requiredAdmins    flagutil.Strings
	fixOrg            bool
	fixOrgMembers     bool
	fixTeamMembers    bool
	fixTeams          bool
	fixTeamRepos      bool
	fixRepos          bool
	fixForks          bool
	fixCollaborators  bool
	ignoreInvitees    bool
	ignoreSecretTeams bool
	allowRepoArchival bool
	allowRepoPublish  bool
	github            flagutil.GitHubOptions

	logLevel string
}

func parseOptions() options {
	var o options
	if err := o.parseArgs(flag.CommandLine, os.Args[1:]); err != nil {
		logrus.Fatalf("Invalid flags: %v", err)
	}
	return o
}

func (o *options) parseArgs(flags *flag.FlagSet, args []string) error {
	o.requiredAdmins = flagutil.NewStrings()
	flags.Var(&o.requiredAdmins, "required-admins", "Ensure config specifies these users as admins")
	flags.IntVar(&o.minAdmins, "min-admins", defaultMinAdmins, "Ensure config specifies at least this many admins")
	flags.BoolVar(&o.requireSelf, "require-self", true, "Ensure --github-token-path user is an admin")
	flags.Float64Var(&o.maximumDelta, "maximum-removal-delta", defaultDelta, "Fail if config removes more than this fraction of current members")
	flags.StringVar(&o.config, "config-path", "", "Path to org config.yaml")
	flags.BoolVar(&o.confirm, "confirm", false, "Mutate github if set")
	flags.StringVar(&o.dump, "dump", "", "Output current config of this org if set")
	flags.BoolVar(&o.dumpFull, "dump-full", false, "Output current config of the org as a valid input config file instead of a snippet")
	flags.BoolVar(&o.ignoreInvitees, "ignore-invitees", false, "Do not compare missing members with active invitations (compatibility for GitHub Enterprise)")
	flags.BoolVar(&o.ignoreSecretTeams, "ignore-secret-teams", false, "Do not dump or update secret teams if set")
	flags.BoolVar(&o.fixOrg, "fix-org", false, "Change org metadata if set")
	flags.BoolVar(&o.fixOrgMembers, "fix-org-members", false, "Add/remove org members if set")
	flags.BoolVar(&o.fixTeams, "fix-teams", false, "Create/delete/update teams if set")
	flags.BoolVar(&o.fixTeamMembers, "fix-team-members", false, "Add/remove team members if set")
	flags.BoolVar(&o.fixTeamRepos, "fix-team-repos", false, "Add/remove team permissions on repos if set")
	flags.BoolVar(&o.fixRepos, "fix-repos", false, "Create/update repositories if set")
	flags.BoolVar(&o.fixForks, "fix-forks", false, "Create repository forks from upstream. Inherits from --fix-repos if not explicitly set")
	flags.BoolVar(&o.fixCollaborators, "fix-collaborators", false, "Add/remove/update repository collaborators if set")
	flags.BoolVar(&o.allowRepoArchival, "allow-repo-archival", false, "If set, archiving repos is allowed while updating repos")
	flags.BoolVar(&o.allowRepoPublish, "allow-repo-publish", false, "If set, making private repos public is allowed while updating repos")
	flags.StringVar(&o.logLevel, "log-level", logrus.InfoLevel.String(), fmt.Sprintf("Logging level, one of %v", logrus.AllLevels))
	o.github.AddCustomizedFlags(flags, flagutil.ThrottlerDefaults(defaultTokens, defaultBurst))
	if err := flags.Parse(args); err != nil {
		return err
	}

	// If --fix-forks was not explicitly set, inherit from --fix-repos
	fixForksExplicitlySet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "fix-forks" {
			fixForksExplicitlySet = true
		}
	})
	if !fixForksExplicitlySet {
		o.fixForks = o.fixRepos
	}

	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("--log-level invalid: %w", err)
	}
	logrus.SetLevel(level)
	logrus.SetReportCaller(level >= logrus.DebugLevel)

	if err := o.github.Validate(!o.confirm); err != nil {
		return err
	}

	if o.minAdmins < 2 {
		return fmt.Errorf("--min-admins=%d must be at least 2", o.minAdmins)
	}
	if o.maximumDelta > 1 || o.maximumDelta < 0 {
		return fmt.Errorf("--maximum-removal-delta=%f must be a non-negative number less than 1.0", o.maximumDelta)
	}

	if o.confirm && o.dump != "" && o.github.AppID == "" {
		return fmt.Errorf("--confirm cannot be used with --dump=%s", o.dump)
	}

	if o.dump != "" && !o.confirm && o.github.AppID != "" {
		return fmt.Errorf("--confirm has to be used with --dump=%s and --github-app-id", o.dump)
	}

	if o.config == "" && o.dump == "" {
		return errors.New("--config-path or --dump required")
	}
	if o.config != "" && o.dump != "" {
		return fmt.Errorf("--config-path=%s and --dump=%s cannot both be set", o.config, o.dump)
	}

	if o.dumpFull && o.dump == "" {
		return errors.New("--dump-full can't be used without --dump")
	}

	if o.fixTeamMembers && !o.fixTeams {
		return fmt.Errorf("--fix-team-members requires --fix-teams")
	}

	if o.fixTeamRepos && !o.fixTeams {
		return fmt.Errorf("--fix-team-repos requires --fix-teams")
	}

	return nil
}

func main() {
	logrusutil.ComponentInit()

	o := parseOptions()

	githubClient, err := o.github.GitHubClient(!o.confirm)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	if o.dump != "" {
		ret, err := dumpOrgConfig(githubClient, o.dump, o.ignoreSecretTeams, o.github.AppID)
		if err != nil {
			logrus.WithError(err).Fatalf("Dump %s failed to collect current data.", o.dump)
		}
		var output interface{}
		if o.dumpFull {
			output = org.FullConfig{
				Orgs: map[string]org.Config{o.dump: *ret},
			}
		} else {
			output = ret
		}
		out, err := yaml.Marshal(output)
		if err != nil {
			logrus.WithError(err).Fatalf("Dump %s failed to marshal output.", o.dump)
		}
		logrus.Infof("Dumping orgs[\"%s\"]:", o.dump)
		fmt.Println(string(out))
		return
	}

	raw, err := os.ReadFile(o.config)
	if err != nil {
		logrus.WithError(err).Fatal("Could not read --config-path file")
	}

	var cfg org.FullConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		logrus.WithError(err).Fatal("Failed to load configuration")
	}

	for name, orgcfg := range cfg.Orgs {
		if err := configureOrg(o, githubClient, name, orgcfg); err != nil {
			logrus.Fatalf("Configuration failed: %v", err)
		}
	}
	logrus.Info("Finished syncing configuration.")
}

type dumpClient interface {
	GetOrg(name string) (*github.Organization, error)
	ListOrgMembers(org, role string) ([]github.TeamMember, error)
	ListTeams(org string) ([]github.Team, error)
	ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error)
	ListTeamReposBySlug(org, teamSlug string) ([]github.Repo, error)
	GetRepo(owner, name string) (github.FullRepo, error)
	GetRepos(org string, isUser bool) ([]github.Repo, error)
	ListDirectCollaboratorsWithPermissions(org, repo string) (map[string]github.RepoPermissionLevel, error)
	BotUser() (*github.UserData, error)
}

func dumpOrgConfig(client dumpClient, orgName string, ignoreSecretTeams bool, appID string) (*org.Config, error) {
	out := org.Config{}
	meta, err := client.GetOrg(orgName)
	if err != nil {
		return nil, fmt.Errorf("failed to get org: %w", err)
	}
	out.Metadata.BillingEmail = &meta.BillingEmail
	out.Metadata.Company = &meta.Company
	out.Metadata.Email = &meta.Email
	out.Metadata.Name = &meta.Name
	out.Metadata.Description = &meta.Description
	out.Metadata.Location = &meta.Location
	out.Metadata.HasOrganizationProjects = &meta.HasOrganizationProjects
	out.Metadata.HasRepositoryProjects = &meta.HasRepositoryProjects
	drp := github.RepoPermissionLevel(meta.DefaultRepositoryPermission)
	out.Metadata.DefaultRepositoryPermission = &drp
	out.Metadata.MembersCanCreateRepositories = &meta.MembersCanCreateRepositories

	var runningAsAdmin bool
	runningAs, err := client.BotUser()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain username for this token")
	}
	admins, err := client.ListOrgMembers(orgName, github.RoleAdmin)
	if err != nil {
		return nil, fmt.Errorf("failed to list org admins: %w", err)
	}
	logrus.Debugf("Found %d admins", len(admins))
	for _, m := range admins {
		logrus.WithField("login", m.Login).Debug("Recording admin.")
		out.Admins = append(out.Admins, m.Login)
		if runningAs.Login == m.Login || appID != "" {
			runningAsAdmin = true
		}
	}

	if !runningAsAdmin {
		return nil, fmt.Errorf("--dump must be run with admin:org scope token")
	}

	orgMembers, err := client.ListOrgMembers(orgName, github.RoleMember)
	if err != nil {
		return nil, fmt.Errorf("failed to list org members: %w", err)
	}
	logrus.Debugf("Found %d members", len(orgMembers))
	for _, m := range orgMembers {
		logrus.WithField("login", m.Login).Debug("Recording member.")
		out.Members = append(out.Members, m.Login)
	}

	teams, err := client.ListTeams(orgName)
	if err != nil {
		return nil, fmt.Errorf("failed to list teams: %w", err)
	}
	logrus.Debugf("Found %d teams", len(teams))

	names := map[int]string{}   // what's the name of a team?
	idMap := map[int]org.Team{} // metadata for a team
	children := map[int][]int{} // what children does it have
	var tops []int              // what are the top-level teams

	for _, t := range teams {
		logger := logrus.WithFields(logrus.Fields{"id": t.ID, "name": t.Name})
		p := org.Privacy(t.Privacy)
		if ignoreSecretTeams && p == org.Secret {
			logger.Debug("Ignoring secret team.")
			continue
		}
		d := t.Description
		nt := org.Team{
			TeamMetadata: org.TeamMetadata{
				Description: &d,
				Privacy:     &p,
			},
			Maintainers: []string{},
			Members:     []string{},
			Children:    map[string]org.Team{},
			Repos:       map[string]github.RepoPermissionLevel{},
		}
		maintainers, err := client.ListTeamMembersBySlug(orgName, t.Slug, github.RoleMaintainer)
		if err != nil {
			return nil, fmt.Errorf("failed to list team %d(%s) maintainers: %w", t.ID, t.Name, err)
		}
		logger.Debugf("Found %d maintainers.", len(maintainers))
		for _, m := range maintainers {
			logger.WithField("login", m.Login).Debug("Recording maintainer.")
			nt.Maintainers = append(nt.Maintainers, m.Login)
		}
		teamMembers, err := client.ListTeamMembersBySlug(orgName, t.Slug, github.RoleMember)
		if err != nil {
			return nil, fmt.Errorf("failed to list team %d(%s) members: %w", t.ID, t.Name, err)
		}
		logger.Debugf("Found %d members.", len(teamMembers))
		for _, m := range teamMembers {
			logger.WithField("login", m.Login).Debug("Recording member.")
			nt.Members = append(nt.Members, m.Login)
		}

		names[t.ID] = t.Name
		idMap[t.ID] = nt

		if t.Parent == nil { // top level team
			logger.Debug("Marking as top-level team.")
			tops = append(tops, t.ID)
		} else { // add this id to the list of the parent's children
			logger.Debugf("Marking as child team of %d.", t.Parent.ID)
			children[t.Parent.ID] = append(children[t.Parent.ID], t.ID)
		}

		repos, err := client.ListTeamReposBySlug(orgName, t.Slug)
		if err != nil {
			return nil, fmt.Errorf("failed to list team %d(%s) repos: %w", t.ID, t.Name, err)
		}
		logger.Debugf("Found %d repo permissions.", len(repos))
		for _, repo := range repos {
			level := github.LevelFromPermissions(repo.Permissions)
			logger.WithFields(logrus.Fields{"repo": repo, "permission": level}).Debug("Recording repo permission.")
			nt.Repos[repo.Name] = level
		}
	}

	var makeChild func(id int) org.Team
	makeChild = func(id int) org.Team {
		t := idMap[id]
		for _, cid := range children[id] {
			child := makeChild(cid)
			t.Children[names[cid]] = child
		}
		return t
	}

	out.Teams = make(map[string]org.Team, len(tops))
	for _, id := range tops {
		out.Teams[names[id]] = makeChild(id)
	}

	repos, err := client.GetRepos(orgName, false)
	if err != nil {
		return nil, fmt.Errorf("failed to list org repos: %w", err)
	}
	logrus.Debugf("Found %d repos", len(repos))
	out.Repos = make(map[string]org.Repo, len(repos))
	for _, repo := range repos {
		full, err := client.GetRepo(orgName, repo.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get repo: %w", err)
		}
		logrus.WithField("repo", full.FullName).Debug("Recording repo.")

		repoConfig := org.PruneRepoDefaults(org.Repo{
			RepoMetadata: org.RepoMetadata{
				Description:      &full.Description,
				HomePage:         &full.Homepage,
				Private:          &full.Private,
				HasIssues:        &full.HasIssues,
				HasProjects:      &full.HasProjects,
				HasWiki:          &full.HasWiki,
				AllowMergeCommit: &full.AllowMergeCommit,
				AllowSquashMerge: &full.AllowSquashMerge,
				AllowRebaseMerge: &full.AllowRebaseMerge,
				Archived:         &full.Archived,
				DefaultBranch:    &full.DefaultBranch,
			},
			// Collaborators will be set conditionally below
		})

		// If repo is a fork, record the upstream
		if full.Fork && full.Parent.FullName != "" {
			forkFrom := full.Parent.FullName
			repoConfig.ForkFrom = &forkFrom
			logrus.WithFields(logrus.Fields{"repo": full.FullName, "upstream": forkFrom}).Debug("Recording fork upstream.")
		}

		// Get direct collaborators (explicitly added) via GraphQL
		if directCollabs, err := client.ListDirectCollaboratorsWithPermissions(orgName, repo.Name); err != nil {
			logrus.WithError(err).Warnf("Failed to list direct collaborators for %s/%s", orgName, repo.Name)
		} else if len(directCollabs) > 0 {
			repoConfig.Collaborators = directCollabs
		}
		out.Repos[full.Name] = repoConfig
	}

	return &out, nil
}

type orgClient interface {
	BotUser() (*github.UserData, error)
	ListOrgMembers(org, role string) ([]github.TeamMember, error)
	RemoveOrgMembership(org, user string) error
	UpdateOrgMembership(org, user string, admin bool) (*github.OrgMembership, error)
}

func configureOrgMembers(opt options, client orgClient, orgName string, orgConfig org.Config, invitees sets.Set[string]) error {
	// Get desired state
	wantAdmins := sets.New[string](orgConfig.Admins...)
	wantMembers := sets.New[string](orgConfig.Members...)

	// Sanity desired state
	if n := len(wantAdmins); n < opt.minAdmins {
		return fmt.Errorf("%s must specify at least %d admins, only found %d", orgName, opt.minAdmins, n)
	}
	var missing []string
	for _, r := range opt.requiredAdmins.Strings() {
		if !wantAdmins.Has(r) {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s must specify %v as admins, missing %v", orgName, opt.requiredAdmins, missing)
	}
	if opt.requireSelf {
		if me, err := client.BotUser(); err != nil {
			return fmt.Errorf("cannot determine user making requests for %s: %v", opt.github.TokenPath, err)
		} else if !wantAdmins.Has(me.Login) {
			return fmt.Errorf("authenticated user %s is not an admin of %s", me.Login, orgName)
		}
	}

	// Get current state
	haveAdmins := sets.Set[string]{}
	haveMembers := sets.Set[string]{}
	ms, err := client.ListOrgMembers(orgName, github.RoleAdmin)
	if err != nil {
		return fmt.Errorf("failed to list %s admins: %w", orgName, err)
	}
	for _, m := range ms {
		haveAdmins.Insert(m.Login)
	}
	if ms, err = client.ListOrgMembers(orgName, github.RoleMember); err != nil {
		return fmt.Errorf("failed to list %s members: %w", orgName, err)
	}
	for _, m := range ms {
		haveMembers.Insert(m.Login)
	}

	have := memberships{members: haveMembers, super: haveAdmins}
	want := memberships{members: wantMembers, super: wantAdmins}
	have.normalize()
	want.normalize()
	// Figure out who to remove
	remove := have.all().Difference(want.all())

	// Sanity check changes
	if d := float64(len(remove)) / float64(len(have.all())); d > opt.maximumDelta {
		return fmt.Errorf("cannot delete %d memberships or %.3f of %s (exceeds limit of %.3f)", len(remove), d, orgName, opt.maximumDelta)
	}

	teamMembers := sets.Set[string]{}
	teamNames := sets.Set[string]{}
	duplicateTeamNames := sets.Set[string]{}
	for name, team := range orgConfig.Teams {
		teamMembers.Insert(team.Members...)
		teamMembers.Insert(team.Maintainers...)
		if teamNames.Has(name) {
			duplicateTeamNames.Insert(name)
		}
		teamNames.Insert(name)
		for _, n := range team.Previously {
			if teamNames.Has(n) {
				duplicateTeamNames.Insert(n)
			}
			teamNames.Insert(n)
		}
	}

	teamMembers = normalize(teamMembers)
	if outside := teamMembers.Difference(want.all()); len(outside) > 0 {
		return fmt.Errorf("all team members/maintainers must also be org members: %s", strings.Join(sets.List(outside), ", "))
	}

	if n := len(duplicateTeamNames); n > 0 {
		return fmt.Errorf("team names must be unique (including previous names), %d duplicated names: %s", n, strings.Join(sets.List(duplicateTeamNames), ", "))
	}

	adder := func(user string, super bool) error {
		if invitees.Has(user) { // Do not add them, as this causes another invite.
			logrus.Infof("Waiting for %s to accept invitation to %s", user, orgName)
			return nil
		}
		role := github.RoleMember
		if super {
			role = github.RoleAdmin
		}
		om, err := client.UpdateOrgMembership(orgName, user, super)
		if err != nil {
			logrus.WithError(err).Warnf("UpdateOrgMembership(%s, %s, %t) failed", orgName, user, super)
			if github.IsNotFound(err) {
				// this could be caused by someone removing their account
				// or a typo in the configuration but should not crash the sync
				err = nil
			}
		} else if om.State == github.StatePending {
			logrus.Infof("Invited %s to %s as a %s", user, orgName, role)
		} else {
			logrus.Infof("Set %s as a %s of %s", user, role, orgName)
		}
		return err
	}

	remover := func(user string) error {
		err := client.RemoveOrgMembership(orgName, user)
		if err != nil {
			logrus.WithError(err).Warnf("RemoveOrgMembership(%s, %s) failed", orgName, user)
		}
		return err
	}

	return configureMembers(have, want, invitees, adder, remover)
}

type memberships struct {
	members sets.Set[string]
	super   sets.Set[string]
}

func (m memberships) all() sets.Set[string] {
	return m.members.Union(m.super)
}

func normalize(s sets.Set[string]) sets.Set[string] {
	out := sets.Set[string]{}
	for i := range s {
		out.Insert(github.NormLogin(i))
	}
	return out
}

// collaboratorInfo holds permission and original username for a normalized user
type collaboratorInfo struct {
	permission   github.RepoPermissionLevel
	originalName string
}

// collaboratorMap manages collaborator usernames to permissions with normalization support
type collaboratorMap struct {
	collaborators map[string]collaboratorInfo // normalized_username -> collaborator info
}

// newCollaboratorMap creates a collaborator map from a raw username->permission map
func newCollaboratorMap(raw map[string]github.RepoPermissionLevel) collaboratorMap {
	cm := collaboratorMap{
		collaborators: make(map[string]collaboratorInfo, len(raw)),
	}
	for username, permission := range raw {
		normalized := github.NormLogin(username)
		cm.collaborators[normalized] = collaboratorInfo{
			permission:   permission,
			originalName: username,
		}
	}
	return cm
}

// originalName returns the original casing for a normalized username
func (cm collaboratorMap) originalName(normalizedUser string) string {
	return cm.collaborators[normalizedUser].originalName
}

func (m *memberships) normalize() {
	m.members = normalize(m.members)
	m.super = normalize(m.super)
}

// repoInvitationsData returns pending repository invitations with both permissions and IDs
func repoInvitationsData(client collaboratorClient, orgName, repoName string) (map[string]github.RepoPermissionLevel, map[string]int, error) {
	permissions := map[string]github.RepoPermissionLevel{}
	invitationIDs := map[string]int{}

	is, err := client.ListRepoInvitations(orgName, repoName)
	if err != nil {
		return nil, nil, err
	}

	for _, i := range is {
		if i.Invitee == nil || i.Invitee.Login == "" {
			continue
		}
		normalizedLogin := github.NormLogin(i.Invitee.Login)
		permissions[normalizedLogin] = i.Permission
		invitationIDs[normalizedLogin] = i.InvitationID
	}

	return permissions, invitationIDs, nil
}

func configureMembers(have, want memberships, invitees sets.Set[string], adder func(user string, super bool) error, remover func(user string) error) error {
	have.normalize()
	want.normalize()
	if both := want.super.Intersection(want.members); len(both) > 0 {
		return fmt.Errorf("users in both roles: %s", strings.Join(sets.List(both), ", "))
	}
	havePlusInvites := have.all().Union(invitees)
	remove := havePlusInvites.Difference(want.all())
	members := want.members.Difference(have.members)
	supers := want.super.Difference(have.super)

	var errs []error
	for u := range members {
		if err := adder(u, false); err != nil {
			errs = append(errs, err)
		}
	}
	for u := range supers {
		if err := adder(u, true); err != nil {
			errs = append(errs, err)
		}
	}

	for u := range remove {
		if err := remover(u); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

// findTeam returns teams[n] for the first n in [name, previousNames, ...] that is in teams.
func findTeam(teams map[string]github.Team, name string, previousNames ...string) *github.Team {
	if t, ok := teams[name]; ok {
		return &t
	}
	for _, p := range previousNames {
		if t, ok := teams[p]; ok {
			return &t
		}
	}
	return nil
}

// validateTeamNames returns an error if any current/previous names are used multiple times in the config.
func validateTeamNames(orgConfig org.Config) error {
	// Does the config duplicate any team names?
	used := sets.Set[string]{}
	dups := sets.Set[string]{}
	for name, orgTeam := range orgConfig.Teams {
		if used.Has(name) {
			dups.Insert(name)
		} else {
			used.Insert(name)
		}
		for _, n := range orgTeam.Previously {
			if used.Has(n) {
				dups.Insert(n)
			} else {
				used.Insert(n)
			}
		}
	}
	if n := len(dups); n > 0 {
		return fmt.Errorf("%d duplicated names: %s", n, strings.Join(sets.List(dups), ", "))
	}
	return nil
}

type teamClient interface {
	ListTeams(org string) ([]github.Team, error)
	CreateTeam(org string, team github.Team) (*github.Team, error)
	DeleteTeamBySlug(org, teamSlug string) error
}

// configureTeams returns the ids for all expected team names, creating/deleting teams as necessary.
func configureTeams(client teamClient, orgName string, orgConfig org.Config, maxDelta float64, ignoreSecretTeams bool) (map[string]github.Team, error) {
	if err := validateTeamNames(orgConfig); err != nil {
		return nil, err
	}

	// What teams exist?
	teams := map[string]github.Team{}
	slugs := sets.Set[string]{}
	teamList, err := client.ListTeams(orgName)
	if err != nil {
		return nil, fmt.Errorf("failed to list teams: %w", err)
	}
	logrus.Debugf("Found %d teams", len(teamList))
	for _, t := range teamList {
		if ignoreSecretTeams && org.Privacy(t.Privacy) == org.Secret {
			continue
		}
		teams[t.Slug] = t
		slugs.Insert(t.Slug)
	}
	if ignoreSecretTeams {
		logrus.Debugf("Found %d non-secret teams", len(teamList))
	}

	// What is the lowest ID for each team?
	older := map[string][]github.Team{}
	names := map[string]github.Team{}
	for _, t := range teams {
		logger := logrus.WithFields(logrus.Fields{"id": t.ID, "name": t.Name})
		n := t.Name
		switch val, ok := names[n]; {
		case !ok: // first occurrence of the name
			logger.Debug("First occurrence of this team name.")
			names[n] = t
		case ok && t.ID < val.ID: // t has the lower ID, replace and send current to older set
			logger.Debugf("Replacing previous recorded team (%d) with this one due to smaller ID.", val.ID)
			names[n] = t
			older[n] = append(older[n], val)
		default: // t does not have smallest id, add it to older set
			logger.Debugf("Adding team (%d) to older set as a smaller ID is already recoded for it.", val.ID)
			older[n] = append(older[n], val)
		}
	}

	// What team are we using for each configured name, and which names are missing?
	matches := map[string]github.Team{}
	missing := map[string]org.Team{}
	used := sets.Set[string]{}
	var match func(teams map[string]org.Team)
	match = func(teams map[string]org.Team) {
		for name, orgTeam := range teams {
			logger := logrus.WithField("name", name)
			match(orgTeam.Children)
			t := findTeam(names, name, orgTeam.Previously...)
			if t == nil {
				missing[name] = orgTeam
				logger.Debug("Could not find team in GitHub for this configuration.")
				continue
			}
			matches[name] = *t // t.Name != name if we matched on orgTeam.Previously
			logger.WithField("id", t.ID).Debug("Found a team in GitHub for this configuration.")
			used.Insert(t.Slug)
		}
	}
	match(orgConfig.Teams)

	// First compute teams we will delete, ensure we are not deleting too many
	unused := slugs.Difference(used)
	if delta := float64(len(unused)) / float64(len(slugs)); delta > maxDelta {
		return nil, fmt.Errorf("cannot delete %d teams or %.3f of %s teams (exceeds limit of %.3f)", len(unused), delta, orgName, maxDelta)
	}

	// Create any missing team names
	var failures []string
	for name, orgTeam := range missing {
		t := &github.Team{Name: name}
		if orgTeam.Description != nil {
			t.Description = *orgTeam.Description
		}
		if orgTeam.Privacy != nil {
			t.Privacy = string(*orgTeam.Privacy)
		}
		t, err := client.CreateTeam(orgName, *t)
		if err != nil {
			logrus.WithError(err).Warnf("Failed to create %s in %s", name, orgName)
			failures = append(failures, name)
			continue
		}
		matches[name] = *t
		// t.Slug may include a slug already present in slugs if other actors are deleting teams.
		used.Insert(t.Slug)
	}
	if n := len(failures); n > 0 {
		return nil, fmt.Errorf("failed to create %d teams: %s", n, strings.Join(failures, ", "))
	}

	// Remove any IDs returned by CreateTeam() that are in the unused set.
	if reused := unused.Intersection(used); len(reused) > 0 {
		// Logically possible for:
		// * another actor to delete team N after the ListTeams() call
		// * github to reuse team N after someone deleted it
		// Therefore used may now include IDs in unused, handle this situation.
		logrus.Warnf("Will not delete %d team IDs reused by github: %v", len(reused), sets.List(reused))
		unused = unused.Difference(reused)
	}
	// Delete undeclared teams.
	for slug := range unused {
		if err := client.DeleteTeamBySlug(orgName, slug); err != nil {
			str := fmt.Sprintf("%s(%s)", slug, teams[slug].Name)
			logrus.WithError(err).Warnf("Failed to delete team %s from %s", str, orgName)
			failures = append(failures, str)
		}
	}
	if n := len(failures); n > 0 {
		return nil, fmt.Errorf("failed to delete %d teams: %s", n, strings.Join(failures, ", "))
	}

	// Return matches
	return matches, nil
}

// updateString will return true and set have to want iff they are set and different.
func updateString(have, want *string) bool {
	switch {
	case have == nil:
		panic("have must be non-nil")
	case want == nil:
		return false // do not care what we have
	case *have == *want:
		return false // already have it
	}
	*have = *want // update value
	return true
}

// updateBool will return true and set have to want iff they are set and different.
func updateBool(have, want *bool) bool {
	switch {
	case have == nil:
		panic("have must not be nil")
	case want == nil:
		return false // do not care what we have
	case *have == *want:
		return false // already have it
	}
	*have = *want // update value
	return true
}

type orgMetadataClient interface {
	GetOrg(name string) (*github.Organization, error)
	EditOrg(name string, org github.Organization) (*github.Organization, error)
}

// configureOrgMeta will update github to have the non-nil wanted metadata values.
func configureOrgMeta(client orgMetadataClient, orgName string, want org.Metadata) error {
	cur, err := client.GetOrg(orgName)
	if err != nil {
		return fmt.Errorf("failed to get %s metadata: %w", orgName, err)
	}
	change := false
	change = updateString(&cur.BillingEmail, want.BillingEmail) || change
	change = updateString(&cur.Company, want.Company) || change
	change = updateString(&cur.Email, want.Email) || change
	change = updateString(&cur.Name, want.Name) || change
	change = updateString(&cur.Description, want.Description) || change
	change = updateString(&cur.Location, want.Location) || change
	if want.DefaultRepositoryPermission != nil {
		w := string(*want.DefaultRepositoryPermission)
		change = updateString(&cur.DefaultRepositoryPermission, &w) || change
	}
	change = updateBool(&cur.HasOrganizationProjects, want.HasOrganizationProjects) || change
	change = updateBool(&cur.HasRepositoryProjects, want.HasRepositoryProjects) || change
	change = updateBool(&cur.MembersCanCreateRepositories, want.MembersCanCreateRepositories) || change
	if change {
		if _, err := client.EditOrg(orgName, *cur); err != nil {
			return fmt.Errorf("failed to edit %s metadata: %w", orgName, err)
		}
	}
	return nil
}

type inviteClient interface {
	ListOrgInvitations(org string) ([]github.OrgInvitation, error)
}

func orgInvitations(opt options, client inviteClient, orgName string) (sets.Set[string], error) {
	invitees := sets.Set[string]{}
	if (!opt.fixOrgMembers && !opt.fixTeamMembers) || opt.ignoreInvitees {
		return invitees, nil
	}
	is, err := client.ListOrgInvitations(orgName)
	if err != nil {
		return nil, err
	}
	for _, i := range is {
		if i.Login == "" {
			continue
		}
		invitees.Insert(github.NormLogin(i.Login))
	}
	return invitees, nil
}

func configureOrg(opt options, client github.Client, orgName string, orgConfig org.Config) error {
	// Ensure that metadata is configured correctly.
	if !opt.fixOrg {
		logrus.Infof("Skipping org metadata configuration")
	} else if err := configureOrgMeta(client, orgName, orgConfig.Metadata); err != nil {
		return err
	}

	invitees, err := orgInvitations(opt, client, orgName)
	if err != nil {
		return fmt.Errorf("failed to list %s invitations: %w", orgName, err)
	}

	// Invite/remove/update members to the org.
	if !opt.fixOrgMembers {
		logrus.Infof("Skipping org member configuration")
	} else if err := configureOrgMembers(opt, client, orgName, orgConfig, invitees); err != nil {
		return fmt.Errorf("failed to configure %s members: %w", orgName, err)
	}

	// Create repository forks from upstream (must run before configureRepos so forkNames is available)
	// forkNames maps config repo name -> actual GitHub repo name (for renamed forks)
	var forkNames map[string]string
	if !opt.fixForks {
		logrus.Info("Skipping repository forks configuration")
		forkNames = make(map[string]string)
	} else {
		var err error
		forkNames, err = configureForks(client, orgName, orgConfig)
		if err != nil {
			return fmt.Errorf("failed to configure %s forks: %w", orgName, err)
		}
	}

	// Create repositories in the org
	if !opt.fixRepos {
		logrus.Info("Skipping org repositories configuration")
	} else if err := configureRepos(opt, client, orgName, orgConfig, forkNames); err != nil {
		return fmt.Errorf("failed to configure %s repos: %w", orgName, err)
	}

	// Configure repository collaborators
	if !opt.fixCollaborators {
		logrus.Info("Skipping repository collaborators configuration")
	} else {
		for repoName, repo := range orgConfig.Repos {
			if err := configureCollaborators(client, orgName, repoName, repo, forkNames); err != nil {
				return fmt.Errorf("failed to configure %s/%s collaborators: %w", orgName, repoName, err)
			}
		}
	}

	if !opt.fixTeams {
		logrus.Infof("Skipping team and team member configuration")
		return nil
	}

	// Find the id and current state of each declared team (create/delete as necessary)
	githubTeams, err := configureTeams(client, orgName, orgConfig, opt.maximumDelta, opt.ignoreSecretTeams)
	if err != nil {
		return fmt.Errorf("failed to configure %s teams: %w", orgName, err)
	}

	for name, team := range orgConfig.Teams {
		err := configureTeamAndMembers(opt, client, githubTeams, name, orgName, team, nil)
		if err != nil {
			return fmt.Errorf("failed to configure %s teams: %w", orgName, err)
		}

		if !opt.fixTeamRepos {
			logrus.Infof("Skipping team repo permissions configuration")
			continue
		}
		if err := configureTeamRepos(client, githubTeams, name, orgName, team); err != nil {
			return fmt.Errorf("failed to configure %s team %s repos: %w", orgName, name, err)
		}
	}
	return nil
}

type repoClient interface {
	GetRepo(orgName, repo string) (github.FullRepo, error)
	GetRepos(orgName string, isUser bool) ([]github.Repo, error)
	CreateRepo(owner string, isUser bool, repo github.RepoCreateRequest) (*github.FullRepo, error)
	UpdateRepo(owner, name string, repo github.RepoUpdateRequest) (*github.FullRepo, error)
}

func newRepoCreateRequest(name string, definition org.Repo) github.RepoCreateRequest {
	repoCreate := github.RepoCreateRequest{
		RepoRequest: github.RepoRequest{
			Name:                     &name,
			Description:              definition.Description,
			Homepage:                 definition.HomePage,
			Private:                  definition.Private,
			HasIssues:                definition.HasIssues,
			HasProjects:              definition.HasProjects,
			HasWiki:                  definition.HasWiki,
			AllowSquashMerge:         definition.AllowSquashMerge,
			AllowMergeCommit:         definition.AllowMergeCommit,
			AllowRebaseMerge:         definition.AllowRebaseMerge,
			SquashMergeCommitTitle:   definition.SquashMergeCommitTitle,
			SquashMergeCommitMessage: definition.SquashMergeCommitMessage,
		},
	}

	if definition.OnCreate != nil {
		repoCreate.AutoInit = definition.OnCreate.AutoInit
		repoCreate.GitignoreTemplate = definition.OnCreate.GitignoreTemplate
		repoCreate.LicenseTemplate = definition.OnCreate.LicenseTemplate
	}

	return repoCreate
}

func validateRepos(repos map[string]org.Repo) error {
	seen := map[string]string{}
	var dups []string

	for wantName, repo := range repos {
		toCheck := append([]string{wantName}, repo.Previously...)
		for _, name := range toCheck {
			normName := strings.ToLower(name)
			if seenName, have := seen[normName]; have {
				dups = append(dups, fmt.Sprintf("%s/%s", seenName, name))
			}
		}
		for _, name := range toCheck {
			normName := strings.ToLower(name)
			seen[normName] = name
		}

	}

	if len(dups) > 0 {
		return fmt.Errorf("found duplicate repo names (GitHub repo names are case-insensitive): %s", strings.Join(dups, ", "))
	}

	return nil
}

// newRepoUpdateRequest creates a minimal github.RepoUpdateRequest instance
// needed to update the current repo into the target state.
func newRepoUpdateRequest(current github.FullRepo, name string, repo org.Repo) github.RepoUpdateRequest {
	setString := func(current string, want *string) *string {
		if want != nil && *want != current {
			return want
		}
		return nil
	}
	setBool := func(current bool, want *bool) *bool {
		if want != nil && *want != current {
			return want
		}
		return nil
	}
	repoUpdate := github.RepoUpdateRequest{
		RepoRequest: github.RepoRequest{
			Name:                     setString(current.Name, &name),
			Description:              setString(current.Description, repo.Description),
			Homepage:                 setString(current.Homepage, repo.HomePage),
			Private:                  setBool(current.Private, repo.Private),
			HasIssues:                setBool(current.HasIssues, repo.HasIssues),
			HasProjects:              setBool(current.HasProjects, repo.HasProjects),
			HasWiki:                  setBool(current.HasWiki, repo.HasWiki),
			AllowSquashMerge:         setBool(current.AllowSquashMerge, repo.AllowSquashMerge),
			AllowMergeCommit:         setBool(current.AllowMergeCommit, repo.AllowMergeCommit),
			AllowRebaseMerge:         setBool(current.AllowRebaseMerge, repo.AllowRebaseMerge),
			SquashMergeCommitTitle:   setString(current.SquashMergeCommitTitle, repo.SquashMergeCommitTitle),
			SquashMergeCommitMessage: setString(current.SquashMergeCommitMessage, repo.SquashMergeCommitMessage),
		},
		DefaultBranch: setString(current.DefaultBranch, repo.DefaultBranch),
		Archived:      setBool(current.Archived, repo.Archived),
	}

	return repoUpdate

}

func sanitizeRepoDelta(opt options, delta *github.RepoUpdateRequest) []error {
	var errs []error
	if delta.Archived != nil && !*delta.Archived {
		delta.Archived = nil
		errs = append(errs, fmt.Errorf("asked to unarchive an archived repo, unsupported by GH API"))
	}
	if delta.Archived != nil && *delta.Archived && !opt.allowRepoArchival {
		delta.Archived = nil
		errs = append(errs, fmt.Errorf("asked to archive a repo but this is not allowed by default (see --allow-repo-archival)"))
	}
	if delta.Private != nil && !(*delta.Private || opt.allowRepoPublish) {
		delta.Private = nil
		errs = append(errs, fmt.Errorf("asked to publish a private repo but this is not allowed by default (see --allow-repo-publish)"))
	}

	return errs
}

func configureRepos(opt options, client repoClient, orgName string, orgConfig org.Config, forkNames map[string]string) error {
	if err := validateRepos(orgConfig.Repos); err != nil {
		return err
	}

	repoList, err := client.GetRepos(orgName, false)
	if err != nil {
		return fmt.Errorf("failed to get repos: %w", err)
	}
	logrus.Debugf("Found %d repositories", len(repoList))
	byName := make(map[string]github.Repo, len(repoList))
	for _, repo := range repoList {
		byName[strings.ToLower(repo.Name)] = repo
	}

	var allErrors []error

	for wantName, wantRepo := range orgConfig.Repos {
		// Determine the actual GitHub repo name (may differ for forks)
		actualName := wantName
		if mappedName, ok := forkNames[wantName]; ok {
			actualName = mappedName
		}
		repoLogger := logrus.WithField("repo", wantName)
		if actualName != wantName {
			repoLogger = repoLogger.WithField("actual_name", actualName)
		}
		pastErrors := len(allErrors)
		var existing *github.FullRepo = nil

		// For forks, also check if the repo exists with the actual name (which may differ from config key)
		namesToCheck := append([]string{wantName}, wantRepo.Previously...)
		if actualName != wantName {
			namesToCheck = append([]string{actualName}, namesToCheck...)
		}

		for _, possibleName := range namesToCheck {
			if repo, exists := byName[strings.ToLower(possibleName)]; exists {
				switch {
				case existing == nil:
					if full, err := client.GetRepo(orgName, repo.Name); err != nil {
						repoLogger.WithError(err).Error("failed to get repository data")
						allErrors = append(allErrors, err)
					} else {
						existing = &full
					}
				case existing.Name != repo.Name:
					err := fmt.Errorf("different repos already exist for current and previous names: %s and %s", existing.Name, repo.Name)
					allErrors = append(allErrors, err)
				}
			}
		}

		if len(allErrors) > pastErrors {
			continue
		}

		// Check if this is a fork repo
		isFork := wantRepo.ForkFrom != nil && *wantRepo.ForkFrom != ""

		if existing == nil {
			// Skip repos that should be created as forks - they're handled by configureForks
			if isFork {
				repoLogger.Debug("repo has fork_from set, skipping creation (will be handled by --fix-forks)")
				continue
			}
			if wantRepo.Archived != nil && *wantRepo.Archived {
				repoLogger.Error("repo does not exist but is configured as archived: not creating")
				allErrors = append(allErrors, fmt.Errorf("nonexistent repo configured as archived: %s", wantName))
				continue
			}
			repoLogger.Info("repo does not exist, creating")
			created, err := client.CreateRepo(orgName, false, newRepoCreateRequest(wantName, wantRepo))
			if err != nil {
				repoLogger.WithError(err).Error("failed to create repository")
				allErrors = append(allErrors, err)
			} else {
				existing = created
			}
		}

		if existing != nil {
			if existing.Archived {
				if wantRepo.Archived != nil && *wantRepo.Archived {
					repoLogger.Infof("repo %q is archived, skipping changes", wantName)
					continue
				}
			}
			repoLogger.Info("repo exists, considering an update")
			// For forks, use the actual name to avoid trying to rename
			updateName := wantName
			if isFork && actualName != wantName {
				updateName = actualName
			}
			// Note on fork metadata: Forks inherit metadata from their upstream repository.
			// If a metadata field is set in the config, it will override the inherited value.
			// If a metadata field is not set (nil), the fork keeps its current value (which
			// may be inherited from upstream or previously modified).
			delta := newRepoUpdateRequest(*existing, updateName, wantRepo)
			if deltaErrors := sanitizeRepoDelta(opt, &delta); len(deltaErrors) > 0 {
				for _, err := range deltaErrors {
					repoLogger.WithError(err).Error("requested repo change is not allowed, removing from delta")
				}
				allErrors = append(allErrors, deltaErrors...)
			}
			if delta.Defined() {
				repoLogger.Info("repo exists and differs from desired state, updating")
				if _, err := client.UpdateRepo(orgName, existing.Name, delta); err != nil {
					repoLogger.WithError(err).Error("failed to update repository")
					allErrors = append(allErrors, err)
				}
			}
		}
	}

	return utilerrors.NewAggregate(allErrors)
}

type forkClient interface {
	GetRepo(owner, name string) (github.FullRepo, error)
	GetRepos(org string, isUser bool) ([]github.Repo, error)
	CreateForkInOrg(owner, repo, targetOrg string, defaultBranchOnly bool, name string) (string, error)
}

// waitForFork polls until the fork repository is available.
// GitHub's fork API returns HTTP 202 (accepted) and creates the fork asynchronously.
// This function polls GetRepo until the fork exists or the timeout is reached.
func waitForFork(client forkClient, org, repo string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	logger := logrus.WithFields(logrus.Fields{"org": org, "repo": repo})

	for time.Now().Before(deadline) {
		_, err := client.GetRepo(org, repo)
		if err == nil {
			logger.Debug("fork is now available")
			return nil
		}

		logger.WithError(err).Debug("fork not yet available, waiting...")
		time.Sleep(interval)
	}

	return fmt.Errorf("timeout waiting for fork %s/%s to become available after %v", org, repo, timeout)
}

// configureForks creates repository forks from upstream repositories as specified in the config.
// This function only creates forks - it does not delete existing forks that are not in the config.
// Returns a mapping of config repo names to actual GitHub repo names (for forks that were renamed).
func configureForks(client forkClient, orgName string, orgConfig org.Config) (map[string]string, error) {
	// forkNames maps config repo name -> actual GitHub repo name
	// This is needed because GitHub may rename forks to avoid conflicts
	forkNames := make(map[string]string)

	// Get existing repos in the org
	repoList, err := client.GetRepos(orgName, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get repos: %w", err)
	}
	logrus.Debugf("Found %d repositories", len(repoList))

	// Build maps for lookups
	byName := make(map[string]github.Repo, len(repoList))
	for _, repo := range repoList {
		byName[strings.ToLower(repo.Name)] = repo
	}

	// Build a map of upstream -> existing fork repo name
	// This ensures idempotency even if GitHub renamed the fork
	forksByUpstream := make(map[string]string) // upstream full name -> fork repo name
	for _, repo := range repoList {
		if repo.Fork {
			fullRepo, err := client.GetRepo(orgName, repo.Name)
			if err != nil {
				logrus.WithError(err).WithField("repo", repo.Name).Debug("failed to get fork parent info")
				continue
			}
			if fullRepo.Parent.FullName != "" {
				forksByUpstream[strings.ToLower(fullRepo.Parent.FullName)] = repo.Name
			}
		}
	}

	var allErrors []error

	for repoName, repoCfg := range orgConfig.Repos {
		// Skip repos that don't have ForkFrom configured
		if repoCfg.ForkFrom == nil || *repoCfg.ForkFrom == "" {
			continue
		}

		repoLogger := logrus.WithFields(logrus.Fields{
			"repo":     repoName,
			"upstream": *repoCfg.ForkFrom,
		})

		// Parse upstream owner/repo
		parts := strings.SplitN(*repoCfg.ForkFrom, "/", 2)
		if len(parts) != 2 {
			err := fmt.Errorf("invalid fork_from format %q, expected 'owner/repo'", *repoCfg.ForkFrom)
			repoLogger.WithError(err).Error("invalid fork configuration")
			allErrors = append(allErrors, err)
			continue
		}
		expectedUpstream := fmt.Sprintf("%s/%s", parts[0], parts[1])

		// First: check if ANY repo in the org is already a fork of this upstream
		// This handles the case where GitHub renamed the fork
		if existingForkName, found := forksByUpstream[strings.ToLower(expectedUpstream)]; found {
			// Record the mapping for configureCollaborators
			forkNames[repoName] = existingForkName
			if strings.EqualFold(existingForkName, repoName) {
				repoLogger.Debug("fork already exists with correct upstream")
			} else {
				repoLogger.WithField("actual_name", existingForkName).Info("fork of upstream already exists with different name")
			}
			continue
		}

		// Check if a repo with the config name already exists
		existingRepo, exists := byName[strings.ToLower(repoName)]
		if exists {
			// Repo with this name exists but is not a fork of our upstream
			// (if it were, we would have found it in forksByUpstream above)
			if existingRepo.Fork {
				// It's a fork, but of a different upstream
				fullRepo, err := client.GetRepo(orgName, existingRepo.Name)
				if err != nil {
					repoLogger.WithError(err).Error("failed to get full repo info")
					allErrors = append(allErrors, err)
					continue
				}
				err = fmt.Errorf("repo %s exists as fork of %s, but config specifies %s", repoName, fullRepo.Parent.FullName, expectedUpstream)
				repoLogger.WithError(err).Error("fork upstream mismatch")
				allErrors = append(allErrors, err)
			} else {
				// It's not a fork at all
				err := fmt.Errorf("repo %s already exists but is not a fork", repoName)
				repoLogger.WithError(err).Error("cannot create fork - repo exists")
				allErrors = append(allErrors, err)
			}
			continue
		}

		// No fork of this upstream exists - create it
		defaultBranchOnly := false
		if repoCfg.DefaultBranchOnly != nil {
			defaultBranchOnly = *repoCfg.DefaultBranchOnly
		}

		repoLogger.Info("creating fork from upstream")
		// Pass the config key as the desired fork name - GitHub will use this name for the fork
		createdName, err := client.CreateForkInOrg(parts[0], parts[1], orgName, defaultBranchOnly, repoName)
		if err != nil {
			repoLogger.WithError(err).Error("failed to create fork")
			allErrors = append(allErrors, err)
			continue
		}

		// Note: GitHub may name the fork differently if there's a naming conflict
		if createdName != repoName {
			repoLogger.WithField("created_name", createdName).Warn("fork was created with a different name than expected")
		}

		// Wait for the fork to become available (GitHub creates forks asynchronously)
		repoLogger.Info("waiting for fork to become available")
		if err := waitForFork(client, orgName, createdName, 5*time.Minute, 10*time.Second); err != nil {
			repoLogger.WithError(err).Error("fork creation timed out")
			allErrors = append(allErrors, err)
			continue
		}

		// Record the mapping for configureCollaborators
		forkNames[repoName] = createdName
		repoLogger.Info("fork created successfully")
	}

	return forkNames, utilerrors.NewAggregate(allErrors)
}

type collaboratorClient interface {
	ListCollaborators(org, repo string) ([]github.User, error)
	ListDirectCollaboratorsWithPermissions(org, repo string) (map[string]github.RepoPermissionLevel, error)
	AddCollaborator(org, repo, user string, permission github.RepoPermissionLevel) error
	UpdateCollaborator(org, repo, user string, permission github.RepoPermissionLevel) error
	UpdateCollaboratorRepoInvitation(org, repo string, invitationID int, permission github.RepoPermissionLevel) error
	DeleteCollaboratorRepoInvitation(org, repo string, invitationID int) error
	RemoveCollaborator(org, repo, user string) error
	UpdateCollaboratorPermission(org, repo, user string, permission github.RepoPermissionLevel) error
	ListRepoInvitations(org, repo string) ([]github.CollaboratorRepoInvitation, error)
}

// configureCollaborators updates the list of repository collaborators when necessary
// This function uses GraphQL to get only direct collaborators (explicitly added) and manages them
// according to the configuration. Org members with inherited access are not affected.
func configureCollaborators(client collaboratorClient, orgName, repoName string, repo org.Repo, forkNames map[string]string) error {
	// Use the actual GitHub repo name if this fork was renamed
	actualRepoName := repoName
	if mappedName, ok := forkNames[repoName]; ok {
		actualRepoName = mappedName
		if actualRepoName != repoName {
			logrus.WithFields(logrus.Fields{
				"config_name": repoName,
				"actual_name": actualRepoName,
			}).Debug("using actual fork name for collaborators")
		}
	}

	want := repo.Collaborators
	if want == nil {
		want = map[string]github.RepoPermissionLevel{}
	}

	// Get current direct collaborators (only explicitly added ones) with their permissions via GraphQL
	currentCollaboratorsRaw, err := client.ListDirectCollaboratorsWithPermissions(orgName, actualRepoName)
	if err != nil {
		return fmt.Errorf("failed to list direct collaborators for %s/%s: %w", orgName, repoName, err)
	}
	logrus.Debugf("Found %d direct collaborators", len(currentCollaboratorsRaw))

	// Get pending repository invitations with their permission levels and IDs
	pendingInvitations, pendingInvitationIDs, err := repoInvitationsData(client, orgName, actualRepoName)
	if err != nil {
		logrus.WithError(err).Warnf("Failed to list repository invitations for %s/%s, may send duplicate invitations", orgName, repoName)
		pendingInvitations = map[string]github.RepoPermissionLevel{} // Continue with empty map
		pendingInvitationIDs = map[string]int{}                      // Continue with empty map
	}

	// Create combined map of current direct collaborators + pending invitations
	// This treats pending invitations as current collaborators for removal purposes
	combinedCollaboratorsRaw := make(map[string]github.RepoPermissionLevel)
	for user, permission := range currentCollaboratorsRaw {
		combinedCollaboratorsRaw[user] = permission
	}
	for user, permission := range pendingInvitations {
		// Add pending invitations to our combined view (normalized usernames)
		if _, exists := combinedCollaboratorsRaw[user]; !exists {
			combinedCollaboratorsRaw[user] = permission
		}
	}

	currentCollaborators := newCollaboratorMap(currentCollaboratorsRaw)
	combinedCollaborators := newCollaboratorMap(combinedCollaboratorsRaw)

	// Determine what actions to take
	actions := map[string]github.RepoPermissionLevel{}

	// Process wanted collaborators using normalized approach
	wantedCollaborators := newCollaboratorMap(want)

	// Add or update permissions for users in our config
	for normalizedUser, collaboratorInfo := range wantedCollaborators.collaborators {
		wantPermission := collaboratorInfo.permission
		wantUser := wantedCollaborators.originalName(normalizedUser)

		if currentInfo, exists := currentCollaborators.collaborators[normalizedUser]; exists && currentInfo.permission == wantPermission {
			// Permission is already correct
			continue
		}

		// Check if this user already has a pending invitation with the same permission
		if pendingPermission, hasPendingInvitation := pendingInvitations[normalizedUser]; hasPendingInvitation && pendingPermission == wantPermission {
			logrus.Infof("Waiting for %s to accept invitation to %s/%s with %s permission", wantUser, orgName, repoName, wantPermission)
			continue
		}

		// Need to create or update this permission
		actions[wantUser] = wantPermission

		// Determine the appropriate action and log message
		if _, exists := currentCollaborators.collaborators[normalizedUser]; exists {
			logrus.Infof("Will update collaborator %s with %s permission", wantUser, wantPermission)
		} else if pendingPermission, hasPendingInvitation := pendingInvitations[normalizedUser]; hasPendingInvitation {
			logrus.Infof("Will update pending invitation for %s from %s to %s permission", wantUser, pendingPermission, wantPermission)
		} else {
			logrus.Infof("Will add collaborator %s with %s permission", wantUser, wantPermission)
		}
	}

	// Remove direct collaborators not in our config (including those with pending invitations)
	// Since we only get direct collaborators via GraphQL, we can safely remove anyone not in config
	for normalizedCurrentUser := range combinedCollaborators.collaborators {
		// Check if this user (normalized) is in our wanted config
		if _, exists := wantedCollaborators.collaborators[normalizedCurrentUser]; !exists {
			originalName := combinedCollaborators.originalName(normalizedCurrentUser)
			actions[originalName] = github.None

			// Check if this is a pending invitation
			if _, isPending := pendingInvitations[normalizedCurrentUser]; isPending {
				logrus.Infof("Will remove pending collaborator invitation for %s (not in config)", originalName)
			} else {
				logrus.Infof("Will remove direct collaborator %s (not in config)", originalName)
			}
		}
	}

	// Execute the actions
	var updateErrors []error
	for user, permission := range actions {
		var err error
		switch permission {
		case github.None:
			// Determine the appropriate removal method based on whether this is a pending invitation
			normalizedUser := github.NormLogin(user)
			if invitationID, hasPendingInvitation := pendingInvitationIDs[normalizedUser]; hasPendingInvitation {
				// Use DeleteRepoInvitation (DELETE) for pending invitations with invitation ID
				err = client.DeleteCollaboratorRepoInvitation(orgName, actualRepoName, invitationID)
				if err != nil {
					logrus.WithError(err).Warnf("Failed to delete pending invitation for %s", user)
				} else {
					logrus.Infof("Deleted pending invitation for %s from %s/%s", user, orgName, repoName)
				}
			} else {
				// Use RemoveCollaborator (DELETE) for actual collaborators
				err = client.RemoveCollaborator(orgName, actualRepoName, user)
				if err != nil {
					logrus.WithError(err).Warnf("Failed to remove collaborator %s", user)
				} else {
					logrus.Infof("Removed collaborator %s from %s/%s", user, orgName, repoName)
				}
			}
		case github.Admin, github.Maintain, github.Triage, github.Write, github.Read:
			// Determine the appropriate API call based on whether this is updating a pending invitation
			normalizedUser := github.NormLogin(user)
			if invitationID, hasPendingInvitation := pendingInvitationIDs[normalizedUser]; hasPendingInvitation {
				// Use UpdateRepoInvitation (PATCH) for pending invitations with invitation ID
				err = client.UpdateCollaboratorRepoInvitation(orgName, actualRepoName, invitationID, permission)
				if err != nil {
					logrus.WithError(err).Warnf("Failed to update pending invitation for %s to %s permission", user, permission)
				} else {
					logrus.Infof("Updated pending invitation for %s to %s permission on %s/%s", user, permission, orgName, repoName)
				}
			} else {
				// Use AddCollaborator (PUT) for new invitations or existing collaborators
				err = client.AddCollaborator(orgName, actualRepoName, user, permission)
				if err != nil {
					logrus.WithError(err).Warnf("Failed to set %s permission for collaborator %s", permission, user)
				} else {
					logrus.Infof("Set %s as %s collaborator on %s/%s", user, permission, orgName, repoName)
				}
			}
		}

		if err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("failed to update %s/%s collaborator %s to %s: %w", orgName, repoName, user, permission, err))
		}
	}

	return utilerrors.NewAggregate(updateErrors)
}

func configureTeamAndMembers(opt options, client github.Client, githubTeams map[string]github.Team, name, orgName string, team org.Team, parent *int) error {
	gt, ok := githubTeams[name]
	if !ok { // configureTeams is buggy if this is the case
		return fmt.Errorf("%s not found in id list", name)
	}

	// Configure team metadata
	err := configureTeam(client, orgName, name, team, gt, parent)
	if err != nil {
		return fmt.Errorf("failed to update %s metadata: %w", name, err)
	}

	// Configure team members
	if !opt.fixTeamMembers {
		logrus.Infof("Skipping %s member configuration", name)
	} else if err = configureTeamMembers(client, orgName, gt, team, opt.ignoreInvitees); err != nil {
		if opt.confirm {
			return fmt.Errorf("failed to update %s members: %w", name, err)
		}
		logrus.WithError(err).Warnf("failed to update %s members: %s", name, err)
		return nil
	}

	for childName, childTeam := range team.Children {
		err = configureTeamAndMembers(opt, client, githubTeams, childName, orgName, childTeam, &gt.ID)
		if err != nil {
			return fmt.Errorf("failed to update %s child teams: %w", name, err)
		}
	}

	return nil
}

type editTeamClient interface {
	EditTeam(org string, team github.Team) (*github.Team, error)
}

// configureTeam patches the team name/description/privacy when values differ
func configureTeam(client editTeamClient, orgName, teamName string, team org.Team, gt github.Team, parent *int) error {
	// Do we need to reconfigure any team settings?
	patch := false
	if gt.Name != teamName {
		patch = true
	}
	gt.Name = teamName
	if team.Description != nil && gt.Description != *team.Description {
		patch = true
		gt.Description = *team.Description
	} else {
		gt.Description = ""
	}
	// doesn't have parent in github, but has parent in config
	if gt.Parent == nil && parent != nil {
		patch = true
		gt.ParentTeamID = parent
	}
	if gt.Parent != nil { // has parent in github ...
		if parent == nil { // ... but doesn't need one
			patch = true
			gt.Parent = nil
			gt.ParentTeamID = parent
		} else if gt.Parent.ID != *parent { // but it's different than the config
			patch = true
			gt.Parent = nil
			gt.ParentTeamID = parent
		}
	}

	if team.Privacy != nil && gt.Privacy != string(*team.Privacy) {
		patch = true
		gt.Privacy = string(*team.Privacy)

	} else if team.Privacy == nil && (parent != nil || len(team.Children) > 0) && gt.Privacy != "closed" {
		patch = true
		gt.Privacy = github.PrivacyClosed // nested teams must be closed
	}

	if patch { // yes we need to patch
		if _, err := client.EditTeam(orgName, gt); err != nil {
			return fmt.Errorf("failed to edit %s team %s(%s): %w", orgName, gt.Slug, gt.Name, err)
		}
	}
	return nil
}

type teamRepoClient interface {
	ListTeamReposBySlug(org, teamSlug string) ([]github.Repo, error)
	UpdateTeamRepoBySlug(org, teamSlug, repo string, permission github.TeamPermission) error
	RemoveTeamRepoBySlug(org, teamSlug, repo string) error
}

// configureTeamRepos updates the list of repos that the team has permissions for when necessary
func configureTeamRepos(client teamRepoClient, githubTeams map[string]github.Team, name, orgName string, team org.Team) error {
	gt, ok := githubTeams[name]
	if !ok { // configureTeams is buggy if this is the case
		return fmt.Errorf("%s not found in id list", name)
	}

	want := team.Repos
	have := map[string]github.RepoPermissionLevel{}
	repos, err := client.ListTeamReposBySlug(orgName, gt.Slug)
	if err != nil {
		return fmt.Errorf("failed to list team %d(%s) repos: %w", gt.ID, name, err)
	}
	for _, repo := range repos {
		have[repo.Name] = github.LevelFromPermissions(repo.Permissions)
	}

	actions := map[string]github.RepoPermissionLevel{}
	for wantRepo, wantPermission := range want {
		if havePermission, haveRepo := have[wantRepo]; haveRepo && havePermission == wantPermission {
			// nothing to do
			continue
		}
		// create or update this permission
		actions[wantRepo] = wantPermission
	}

	for haveRepo := range have {
		if _, wantRepo := want[haveRepo]; !wantRepo {
			// should remove these permissions
			actions[haveRepo] = github.None
		}
	}

	var updateErrors []error
	for repo, permission := range actions {
		var err error
		switch permission {
		case github.None:
			err = client.RemoveTeamRepoBySlug(orgName, gt.Slug, repo)
		case github.Admin:
			err = client.UpdateTeamRepoBySlug(orgName, gt.Slug, repo, github.RepoAdmin)
		case github.Write:
			err = client.UpdateTeamRepoBySlug(orgName, gt.Slug, repo, github.RepoPush)
		case github.Read:
			err = client.UpdateTeamRepoBySlug(orgName, gt.Slug, repo, github.RepoPull)
		case github.Triage:
			err = client.UpdateTeamRepoBySlug(orgName, gt.Slug, repo, github.RepoTriage)
		case github.Maintain:
			err = client.UpdateTeamRepoBySlug(orgName, gt.Slug, repo, github.RepoMaintain)
		}

		if err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("failed to update team %d(%s) permissions on repo %s to %s: %w", gt.ID, name, repo, permission, err))
		}
	}

	for childName, childTeam := range team.Children {
		if err := configureTeamRepos(client, githubTeams, childName, orgName, childTeam); err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("failed to configure %s child team %s repos: %w", orgName, childName, err))
		}
	}

	return utilerrors.NewAggregate(updateErrors)
}

// teamMembersClient can list/remove/update people to a team.
type teamMembersClient interface {
	ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error)
	ListTeamInvitationsBySlug(org, teamSlug string) ([]github.OrgInvitation, error)
	RemoveTeamMembershipBySlug(org, teamSlug, user string) error
	UpdateTeamMembershipBySlug(org, teamSlug, user string, maintainer bool) (*github.TeamMembership, error)
}

func teamInvitations(client teamMembersClient, orgName, teamSlug string) (sets.Set[string], error) {
	invitees := sets.Set[string]{}
	is, err := client.ListTeamInvitationsBySlug(orgName, teamSlug)
	if err != nil {
		return nil, err
	}
	for _, i := range is {
		if i.Login == "" {
			continue
		}
		invitees.Insert(github.NormLogin(i.Login))
	}
	return invitees, nil
}

// configureTeamMembers will add/update people to the appropriate role on the team, and remove anyone else.
func configureTeamMembers(client teamMembersClient, orgName string, gt github.Team, team org.Team, ignoreInvitees bool) error {
	// Get desired state
	wantMaintainers := sets.New[string](team.Maintainers...)
	wantMembers := sets.New[string](team.Members...)

	// Get current state
	haveMaintainers := sets.Set[string]{}
	haveMembers := sets.Set[string]{}

	members, err := client.ListTeamMembersBySlug(orgName, gt.Slug, github.RoleMember)
	if err != nil {
		return fmt.Errorf("failed to list %s(%s) members: %w", gt.Slug, gt.Name, err)
	}
	for _, m := range members {
		haveMembers.Insert(m.Login)
	}

	maintainers, err := client.ListTeamMembersBySlug(orgName, gt.Slug, github.RoleMaintainer)
	if err != nil {
		return fmt.Errorf("failed to list %s(%s) maintainers: %w", gt.Slug, gt.Name, err)
	}
	for _, m := range maintainers {
		haveMaintainers.Insert(m.Login)
	}

	invitees := sets.Set[string]{}
	if !ignoreInvitees {
		invitees, err = teamInvitations(client, orgName, gt.Slug)
		if err != nil {
			return fmt.Errorf("failed to list %s(%s) invitees: %w", gt.Slug, gt.Name, err)
		}
	}

	adder := func(user string, super bool) error {
		if invitees.Has(user) {
			logrus.Infof("Waiting for %s to accept invitation to %s(%s)", user, gt.Slug, gt.Name)
			return nil
		}
		role := github.RoleMember
		if super {
			role = github.RoleMaintainer
		}
		tm, err := client.UpdateTeamMembershipBySlug(orgName, gt.Slug, user, super)
		if err != nil {
			// Augment the error with the operation we attempted so that the error makes sense after return
			err = fmt.Errorf("UpdateTeamMembership(%s(%s), %s, %t) failed: %w", gt.Slug, gt.Name, user, super, err)
			logrus.Warnf("%s", err.Error())
		} else if tm.State == github.StatePending {
			logrus.Infof("Invited %s to %s(%s) as a %s", user, gt.Slug, gt.Name, role)
		} else {
			logrus.Infof("Set %s as a %s of %s(%s)", user, role, gt.Slug, gt.Name)
		}
		return err
	}

	remover := func(user string) error {
		err := client.RemoveTeamMembershipBySlug(orgName, gt.Slug, user)
		if err != nil {
			// Augment the error with the operation we attempted so that the error makes sense after return
			err = fmt.Errorf("RemoveTeamMembership(%s(%s), %s) failed: %w", gt.Slug, gt.Name, user, err)
			logrus.Warnf("%s", err.Error())
		} else {
			logrus.Infof("Removed %s from team %s(%s)", user, gt.Slug, gt.Name)
		}
		return err
	}

	want := memberships{members: wantMembers, super: wantMaintainers}
	have := memberships{members: haveMembers, super: haveMaintainers}
	return configureMembers(have, want, invitees, adder, remover)
}
