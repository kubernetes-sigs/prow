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

package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

const (
	// EventGUID is sent by GitHub in a header of every webhook request.
	// Used as a log field across prow.
	EventGUID = "event-GUID"
	// PrLogField is the number of a PR.
	// Used as a log field across prow.
	PrLogField = "pr"
	// OrgLogField is the organization of a PR.
	// Used as a log field across prow.
	OrgLogField = "org"
	// RepoLogField is the repository of a PR.
	// Used as a log field across prow.
	RepoLogField = "repo"

	// SearchTimeFormat is a time.Time format string for ISO8601 which is the
	// format that GitHub requires for times specified as part of a search query.
	SearchTimeFormat = "2006-01-02T15:04:05Z"

	// DefaultAPIEndpoint is the default GitHub API endpoint.
	DefaultAPIEndpoint = "https://api.github.com"

	// DefaultHost is the default GitHub base endpoint.
	DefaultHost = "github.com"

	// DefaultGraphQLEndpoint is the default GitHub GraphQL API endpoint.
	DefaultGraphQLEndpoint = "https://api.github.com/graphql"
)

var (
	// FoundingYear is the year GitHub was founded. This is just used so that
	// we can lower bound dates related to PRs and issues.
	FoundingYear, _ = time.Parse(SearchTimeFormat, "2007-01-01T00:00:00Z")
)

// These are possible State entries for a Status.
const (
	StatusPending = "pending"
	StatusSuccess = "success"
	StatusError   = "error"
	StatusFailure = "failure"
)

// Possible contents for reactions.
const (
	ReactionThumbsUp                  = "+1"
	ReactionThumbsDown                = "-1"
	ReactionLaugh                     = "laugh"
	ReactionConfused                  = "confused"
	ReactionHeart                     = "heart"
	ReactionHooray                    = "hooray"
	stateCannotBeChangedMessagePrefix = "state cannot be changed."
)

func unmarshalClientError(b []byte) error {
	var errors []error
	clientError := ClientError{}
	err := json.Unmarshal(b, &clientError)
	if err == nil {
		return clientError
	}
	errors = append(errors, err)
	alternativeClientError := AlternativeClientError{}
	err = json.Unmarshal(b, &alternativeClientError)
	if err == nil {
		return alternativeClientError
	}
	errors = append(errors, err)
	return utilerrors.NewAggregate(errors)
}

// ClientError represents https://developer.github.com/v3/#client-errors
type ClientError struct {
	Message string                `json:"message"`
	Errors  []clientErrorSubError `json:"errors,omitempty"`
}

type clientErrorSubError struct {
	Resource string `json:"resource"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message,omitempty"`
}

func (r ClientError) Error() string {
	return r.Message
}

// AlternativeClientError represents an alternative format for https://developer.github.com/v3/#client-errors
// This is probably a GitHub bug, as documentation_url should appear only in custom errors
type AlternativeClientError struct {
	Message          string   `json:"message"`
	Errors           []string `json:"errors,omitempty"`
	DocumentationURL string   `json:"documentation_url,omitempty"`
}

func (r AlternativeClientError) Error() string {
	return r.Message
}

// Reaction holds the type of emotional reaction.
type Reaction struct {
	Content string `json:"content"`
}

// Status is used to set a commit status line.
type Status struct {
	State       string `json:"state"`
	TargetURL   string `json:"target_url,omitempty"`
	Description string `json:"description,omitempty"`
	Context     string `json:"context,omitempty"`
}

// CombinedStatus is the latest statuses for a ref.
type CombinedStatus struct {
	SHA      string   `json:"sha"`
	Statuses []Status `json:"statuses"`
	State    string   `json:"state"`
}

// User is a GitHub user account.
type User struct {
	Login       string          `json:"login"`
	Name        string          `json:"name"`
	Email       string          `json:"email"`
	ID          int             `json:"id"`
	HTMLURL     string          `json:"html_url"`
	Permissions RepoPermissions `json:"permissions"`
	Type        string          `json:"type"`
}

const (
	// UserTypeUser identifies an actual user account in the User.Type field
	UserTypeUser = "User"
	// UserTypeBot identifies a github app bot user in the User.Type field
	UserTypeBot = "Bot"
)

// NormLogin normalizes GitHub login strings
func NormLogin(login string) string {
	return strings.TrimPrefix(strings.ToLower(login), "@")
}

// PullRequestEventAction enumerates the triggers for this
// webhook payload type. See also:
// https://developer.github.com/v3/activity/events/types/#pullrequestevent
type PullRequestEventAction string

const (
	// PullRequestActionAssigned means assignees were added.
	PullRequestActionAssigned PullRequestEventAction = "assigned"
	// PullRequestActionUnassigned means assignees were removed.
	PullRequestActionUnassigned PullRequestEventAction = "unassigned"
	// PullRequestActionReviewRequested means review requests were added.
	PullRequestActionReviewRequested PullRequestEventAction = "review_requested"
	// PullRequestActionReviewRequestRemoved means review requests were removed.
	PullRequestActionReviewRequestRemoved PullRequestEventAction = "review_request_removed"
	// PullRequestActionLabeled means labels were added.
	PullRequestActionLabeled PullRequestEventAction = "labeled"
	// PullRequestActionUnlabeled means labels were removed
	PullRequestActionUnlabeled PullRequestEventAction = "unlabeled"
	// PullRequestActionOpened means the PR was created
	PullRequestActionOpened PullRequestEventAction = "opened"
	// PullRequestActionEdited means the PR body changed.
	PullRequestActionEdited PullRequestEventAction = "edited"
	// PullRequestActionClosed means the PR was closed (or was merged).
	PullRequestActionClosed PullRequestEventAction = "closed"
	// PullRequestActionReopened means the PR was reopened.
	PullRequestActionReopened PullRequestEventAction = "reopened"
	// PullRequestActionSynchronize means the git state changed.
	PullRequestActionSynchronize PullRequestEventAction = "synchronize"
	// PullRequestActionReadyForReview means the PR is no longer a draft PR.
	PullRequestActionReadyForReview PullRequestEventAction = "ready_for_review"
	// PullRequestActionConvertedToDraft means the PR is now a draft PR.
	PullRequestActionConvertedToDraft PullRequestEventAction = "converted_to_draft"
	// PullRequestActionLocked means labels were added.
	PullRequestActionLocked PullRequestEventAction = "locked"
	// PullRequestActionUnlocked means labels were removed
	PullRequestActionUnlocked PullRequestEventAction = "unlocked"
	// PullRequestActionAutoMergeEnabled means auto merge was enabled
	PullRequestActionAutoMergeEnabled PullRequestEventAction = "auto_merge_enabled"
	// PullRequestActionAutoMergeDisabled means auto merge was disabled
	PullRequestActionAutoMergeDisabled PullRequestEventAction = "auto_merge_disabled"
)

// GenericEvent is a lightweight struct containing just Sender, Organization and Repo as
// they are allWebhook payload object common properties:
// https://developer.github.com/webhooks/event-payloads/#webhook-payload-object-common-properties
type GenericEvent struct {
	Sender User         `json:"sender"`
	Org    Organization `json:"organization"`
	Repo   Repo         `json:"repository"`
}

// PullRequestEvent is what GitHub sends us when a PR is changed.
type PullRequestEvent struct {
	Action      PullRequestEventAction `json:"action"`
	Number      int                    `json:"number"`
	PullRequest PullRequest            `json:"pull_request"`
	Repo        Repo                   `json:"repository"`
	Label       Label                  `json:"label"`
	Sender      User                   `json:"sender"`

	// Changes holds raw change data, which we must inspect
	// and deserialize later as this is a polymorphic field
	Changes json.RawMessage `json:"changes"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

const (
	PullRequestStateOpen   = "open"
	PullRequestStateClosed = "closed"
)

// PullRequest contains information about a PullRequest.
type PullRequest struct {
	ID                 int               `json:"id"`
	NodeID             string            `json:"node_id"`
	Number             int               `json:"number"`
	HTMLURL            string            `json:"html_url"`
	User               User              `json:"user"`
	Labels             []Label           `json:"labels"`
	Base               PullRequestBranch `json:"base"`
	Head               PullRequestBranch `json:"head"`
	Title              string            `json:"title"`
	Body               string            `json:"body"`
	RequestedReviewers []User            `json:"requested_reviewers"`
	RequestedTeams     []Team            `json:"requested_teams"`
	Assignees          []User            `json:"assignees"`
	State              string            `json:"state"`
	Draft              bool              `json:"draft"`
	Merged             bool              `json:"merged"`
	CreatedAt          time.Time         `json:"created_at,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at,omitempty"`
	// ref https://developer.github.com/v3/pulls/#get-a-single-pull-request
	// If Merged is true, MergeSHA is the SHA of the merge commit, or squashed commit
	// If Merged is false, MergeSHA is a commit SHA that github created to test if
	// the PR can be merged automatically.
	MergeSHA *string `json:"merge_commit_sha"`
	// ref https://developer.github.com/v3/pulls/#response-1
	// The value of the mergeable attribute can be true, false, or null. If the value
	// is null, this means that the mergeability hasn't been computed yet, and a
	// background job was started to compute it. When the job is complete, the response
	// will include a non-null value for the mergeable attribute.
	Mergable *bool `json:"mergeable,omitempty"`
	// If the PR doesn't have any milestone, `milestone` is null and is unmarshaled to nil.
	Milestone         *Milestone `json:"milestone,omitempty"`
	Commits           int        `json:"commits"`
	AuthorAssociation string     `json:"author_association,omitempty"`
}

// PullRequestBranch contains information about a particular branch in a PR.
type PullRequestBranch struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo Repo   `json:"repo"`
}

// Label describes a GitHub label.
type Label struct {
	URL         string `json:"url"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

// PullRequestFileStatus enumerates the statuses for this webhook payload type.
type PullRequestFileStatus string

const (
	// PullRequestFileModified means a file changed.
	PullRequestFileModified PullRequestFileStatus = "modified"
	// PullRequestFileAdded means a file was added.
	PullRequestFileAdded = "added"
	// PullRequestFileRemoved means a file was deleted.
	PullRequestFileRemoved = "removed"
	// PullRequestFileRenamed means a file moved.
	PullRequestFileRenamed = "renamed"
)

// PullRequestChange contains information about what a PR changed.
type PullRequestChange struct {
	SHA              string `json:"sha"`
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	Patch            string `json:"patch"`
	BlobURL          string `json:"blob_url"`
	PreviousFilename string `json:"previous_filename"`
}

// Repo contains general repository information: it includes fields available
// in repo records returned by GH "List" methods but not those returned by GH
// "Get" method. Use FullRepo struct for "Get" method.
// See also https://developer.github.com/v3/repos/#list-organization-repositories
type Repo struct {
	Owner         User   `json:"owner"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	Fork          bool   `json:"fork"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Private       bool   `json:"private"`
	Description   string `json:"description"`
	Homepage      string `json:"homepage"`
	HasIssues     bool   `json:"has_issues"`
	HasProjects   bool   `json:"has_projects"`
	HasWiki       bool   `json:"has_wiki"`
	NodeID        string `json:"node_id"`
	// Permissions reflect the permission level for the requester, so
	// on a repository GET call this will be for the user whose token
	// is being used, if listing a team's repos this will be for the
	// team's privilege level in the repo
	Permissions RepoPermissions `json:"permissions"`
	Parent      ParentRepo      `json:"parent"`
}

// ParentRepo contains a small subsection of general repository information: it
// just includes the information needed to confirm that a parent repo exists
// and what the name of that repo is.
type ParentRepo struct {
	Owner    User   `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

// Repo contains detailed repository information, including items
// that are not available in repo records returned by GH "List" methods
// but are in those returned by GH "Get" method.
// See https://developer.github.com/v3/repos/#list-organization-repositories
// See https://developer.github.com/v3/repos/#get
type FullRepo struct {
	Repo

	AllowSquashMerge         bool   `json:"allow_squash_merge,omitempty"`
	AllowMergeCommit         bool   `json:"allow_merge_commit,omitempty"`
	AllowRebaseMerge         bool   `json:"allow_rebase_merge,omitempty"`
	SquashMergeCommitTitle   string `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage string `json:"squash_merge_commit_message,omitempty"`
}

// RepoRequest contains metadata used in requests to create or update a Repo.
// Compared to `Repo`, its members are pointers to allow the "not set/use default
// semantics.
// See also:
// - https://developer.github.com/v3/repos/#create
// - https://developer.github.com/v3/repos/#edit
type RepoRequest struct {
	Name                     *string `json:"name,omitempty"`
	Description              *string `json:"description,omitempty"`
	Homepage                 *string `json:"homepage,omitempty"`
	Private                  *bool   `json:"private,omitempty"`
	HasIssues                *bool   `json:"has_issues,omitempty"`
	HasProjects              *bool   `json:"has_projects,omitempty"`
	HasWiki                  *bool   `json:"has_wiki,omitempty"`
	AllowSquashMerge         *bool   `json:"allow_squash_merge,omitempty"`
	AllowMergeCommit         *bool   `json:"allow_merge_commit,omitempty"`
	AllowRebaseMerge         *bool   `json:"allow_rebase_merge,omitempty"`
	SquashMergeCommitTitle   *string `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage *string `json:"squash_merge_commit_message,omitempty"`
}

type WorkflowRuns struct {
	Count       int           `json:"total_count,omitempty"`
	WorflowRuns []WorkflowRun `json:"workflow_runs"`
}

// RepoCreateRequest contains metadata used in requests to create a repo.
// See also: https://developer.github.com/v3/repos/#create
type RepoCreateRequest struct {
	RepoRequest `json:",omitempty"`

	AutoInit          *bool   `json:"auto_init,omitempty"`
	GitignoreTemplate *string `json:"gitignore_template,omitempty"`
	LicenseTemplate   *string `json:"license_template,omitempty"`
}

func (r RepoRequest) ToRepo() *FullRepo {
	setString := func(dest, src *string) {
		if src != nil {
			*dest = *src
		}
	}
	setBool := func(dest, src *bool) {
		if src != nil {
			*dest = *src
		}
	}

	var repo FullRepo
	setString(&repo.Name, r.Name)
	setString(&repo.Description, r.Description)
	setString(&repo.Homepage, r.Homepage)
	setBool(&repo.Private, r.Private)
	setBool(&repo.HasIssues, r.HasIssues)
	setBool(&repo.HasProjects, r.HasProjects)
	setBool(&repo.HasWiki, r.HasWiki)
	setBool(&repo.AllowSquashMerge, r.AllowSquashMerge)
	setBool(&repo.AllowMergeCommit, r.AllowMergeCommit)
	setBool(&repo.AllowRebaseMerge, r.AllowRebaseMerge)
	setString(&repo.SquashMergeCommitTitle, r.SquashMergeCommitTitle)
	setString(&repo.SquashMergeCommitMessage, r.SquashMergeCommitMessage)

	return &repo
}

// Defined returns true if at least one of the pointer fields are not nil
func (r RepoRequest) Defined() bool {
	return r.Name != nil || r.Description != nil || r.Homepage != nil || r.Private != nil ||
		r.HasIssues != nil || r.HasProjects != nil || r.HasWiki != nil || r.AllowSquashMerge != nil ||
		r.AllowMergeCommit != nil || r.AllowRebaseMerge != nil
}

// RepoUpdateRequest contains metadata used for updating a repository
// See also: https://developer.github.com/v3/repos/#edit
type RepoUpdateRequest struct {
	RepoRequest `json:",omitempty"`

	DefaultBranch *string `json:"default_branch,omitempty"`
	Archived      *bool   `json:"archived,omitempty"`
}

func (r RepoUpdateRequest) ToRepo() *FullRepo {
	repo := r.RepoRequest.ToRepo()
	if r.DefaultBranch != nil {
		repo.DefaultBranch = *r.DefaultBranch
	}
	if r.Archived != nil {
		repo.Archived = *r.Archived
	}

	return repo
}

func (r RepoUpdateRequest) Defined() bool {
	return r.RepoRequest.Defined() || r.DefaultBranch != nil || r.Archived != nil
}

// RepoPermissions describes which permission level an entity has in a
// repo. At most one of the booleans here should be true.
type RepoPermissions struct {
	// Pull is equivalent to "Read" permissions in the web UI
	Pull   bool `json:"pull"`
	Triage bool `json:"triage"`
	// Push is equivalent to "Edit" permissions in the web UI
	Push     bool `json:"push"`
	Maintain bool `json:"maintain"`
	Admin    bool `json:"admin"`
}

// RepoPermissionLevel is admin, write, read or none.
//
// See https://developer.github.com/v3/repos/collaborators/#review-a-users-permission-level
type RepoPermissionLevel string

// For more information on access levels, see:
// https://docs.github.com/en/github/setting-up-and-managing-organizations-and-teams/repository-permission-levels-for-an-organization
const (
	// Read allows pull but not push
	Read RepoPermissionLevel = "read"
	// Triage allows Read and managing issues
	// pull requests but not push
	Triage RepoPermissionLevel = "triage"
	// Write allows Read plus push
	Write RepoPermissionLevel = "write"
	// Maintain allows Write along with managing
	// repository without access to sensitive or
	// destructive instructions.
	Maintain RepoPermissionLevel = "maintain"
	// Admin allows Write plus change others' rights.
	Admin RepoPermissionLevel = "admin"
	// None disallows everything
	None RepoPermissionLevel = "none"
)

var repoPermissionLevels = map[RepoPermissionLevel]bool{
	Read:     true,
	Triage:   true,
	Write:    true,
	Maintain: true,
	Admin:    true,
	None:     true,
}

// MarshalText returns the byte representation of the permission
func (l RepoPermissionLevel) MarshalText() ([]byte, error) {
	return []byte(l), nil
}

// UnmarshalText validates the text is a valid string
func (l *RepoPermissionLevel) UnmarshalText(text []byte) error {
	v := RepoPermissionLevel(text)
	if _, ok := repoPermissionLevels[v]; !ok {
		return fmt.Errorf("bad repo permission: %s not in %v", v, repoPermissionLevels)
	}
	*l = v
	return nil
}

type TeamPermission string

const (
	RepoPull     TeamPermission = "pull"
	RepoTriage   TeamPermission = "triage"
	RepoMaintain TeamPermission = "maintain"
	RepoPush     TeamPermission = "push"
	RepoAdmin    TeamPermission = "admin"
)

// Branch contains general branch information.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"` // only included for ?protection=true requests
	// TODO(fejta): consider including undocumented protection key
}

// BranchProtection represents protections
// currently in place for a branch
// See also: https://developer.github.com/v3/repos/branches/#get-branch-protection
type BranchProtection struct {
	RequiredStatusChecks       *RequiredStatusChecks       `json:"required_status_checks"`
	EnforceAdmins              EnforceAdmins               `json:"enforce_admins"`
	RequiredPullRequestReviews *RequiredPullRequestReviews `json:"required_pull_request_reviews"`
	Restrictions               *Restrictions               `json:"restrictions"`
	AllowForcePushes           AllowForcePushes            `json:"allow_force_pushes"`
	RequiredLinearHistory      RequiredLinearHistory       `json:"required_linear_history"`
	AllowDeletions             AllowDeletions              `json:"allow_deletions"`
}

// AllowDeletions specifies whether to permit users with push access to delete matching branches.
type AllowDeletions struct {
	Enabled bool `json:"enabled"`
}

// RequiredLinearHistory specifies whether to prevent merge commits from being pushed to matching branches.
type RequiredLinearHistory struct {
	Enabled bool `json:"enabled"`
}

// AllowForcePushes specifies whether to permit force pushes for all users with push access.
type AllowForcePushes struct {
	Enabled bool `json:"enabled"`
}

// EnforceAdmins specifies whether to enforce the
// configured branch restrictions for administrators.
type EnforceAdmins struct {
	Enabled bool `json:"enabled"`
}

// RequiredPullRequestReviews exposes the state of review rights.
type RequiredPullRequestReviews struct {
	DismissalRestrictions        *DismissalRestrictions `json:"dismissal_restrictions"`
	DismissStaleReviews          bool                   `json:"dismiss_stale_reviews"`
	RequireCodeOwnerReviews      bool                   `json:"require_code_owner_reviews"`
	RequiredApprovingReviewCount int                    `json:"required_approving_review_count"`
	BypassRestrictions           *BypassRestrictions    `json:"bypass_pull_request_allowances"`
}

// DismissalRestrictions exposes restrictions in github for an activity to people/teams.
type DismissalRestrictions struct {
	Users []User `json:"users,omitempty"`
	Teams []Team `json:"teams,omitempty"`
}

// BypassRestrictions exposes bypass option in github for a pull request to people/teams.
type BypassRestrictions struct {
	Users []User `json:"users,omitempty"`
	Teams []Team `json:"teams,omitempty"`
}

// Restrictions exposes restrictions in github for an activity to apps/people/teams.
type Restrictions struct {
	Apps  []App  `json:"apps,omitempty"`
	Users []User `json:"users,omitempty"`
	Teams []Team `json:"teams,omitempty"`
}

// BranchProtectionRequest represents
// protections to put in place for a branch.
// See also: https://developer.github.com/v3/repos/branches/#update-branch-protection
type BranchProtectionRequest struct {
	RequiredStatusChecks       *RequiredStatusChecks              `json:"required_status_checks"`
	EnforceAdmins              *bool                              `json:"enforce_admins"`
	RequiredPullRequestReviews *RequiredPullRequestReviewsRequest `json:"required_pull_request_reviews"`
	Restrictions               *RestrictionsRequest               `json:"restrictions"`
	RequiredLinearHistory      bool                               `json:"required_linear_history"`
	AllowForcePushes           bool                               `json:"allow_force_pushes"`
	AllowDeletions             bool                               `json:"allow_deletions"`
}

func (r BranchProtectionRequest) String() string {
	bytes, err := json.Marshal(&r)
	if err != nil {
		return fmt.Sprintf("%#v", r)
	}
	return string(bytes)
}

// RequiredStatusChecks specifies which contexts must pass to merge.
type RequiredStatusChecks struct {
	Strict   bool     `json:"strict"` // PR must be up to date (include latest base branch commit).
	Contexts []string `json:"contexts"`
}

// RequiredPullRequestReviewsRequest controls a request for review rights.
type RequiredPullRequestReviewsRequest struct {
	DismissalRestrictions        DismissalRestrictionsRequest `json:"dismissal_restrictions"`
	DismissStaleReviews          bool                         `json:"dismiss_stale_reviews"`
	RequireCodeOwnerReviews      bool                         `json:"require_code_owner_reviews"`
	RequiredApprovingReviewCount int                          `json:"required_approving_review_count"`
	BypassRestrictions           BypassRestrictionsRequest    `json:"bypass_pull_request_allowances"`
}

// DismissalRestrictionsRequest tells github to restrict an activity to people/teams.
//
// Use *[]string in order to distinguish unset and empty list.
// This is needed by dismissal_restrictions to distinguish
// do not restrict (empty object) and restrict everyone (nil user/teams list)
type DismissalRestrictionsRequest struct {
	// Users is a list of user logins
	Users *[]string `json:"users,omitempty"`
	// Teams is a list of team slugs
	Teams *[]string `json:"teams,omitempty"`
}

// BypassRestrictionsRequest tells github to restrict PR bypass activity to people/teams.
//
// Use *[]string in order to distinguish unset and empty list.
// This is needed by bypass_pull_request_allowances to distinguish
// do not restrict (empty object) and restrict everyone (nil user/teams list)
type BypassRestrictionsRequest struct {
	// Users is a list of user logins
	Users *[]string `json:"users,omitempty"`
	// Teams is a list of team slugs
	Teams *[]string `json:"teams,omitempty"`
}

// RestrictionsRequest tells github to restrict an activity to apps/people/teams.
//
// Use *[]string in order to distinguish unset and empty list.
// do not restrict (empty object) and restrict everyone (nil apps/user/teams list)
type RestrictionsRequest struct {
	// Apps is a list of app names
	Apps *[]string `json:"apps,omitempty"`
	// Users is a list of user logins
	Users *[]string `json:"users,omitempty"`
	// Teams is a list of team slugs
	Teams *[]string `json:"teams,omitempty"`
}

// HookConfig holds the endpoint and its secret.
type HookConfig struct {
	URL         string  `json:"url"`
	ContentType *string `json:"content_type,omitempty"`
	Secret      *string `json:"secret,omitempty"`
}

// Hook holds info about the webhook configuration.
type Hook struct {
	ID     int        `json:"id"`
	Name   string     `json:"name"`
	Events []string   `json:"events"`
	Active bool       `json:"active"`
	Config HookConfig `json:"config"`
}

// HookRequest can create and/or edit a webhook.
//
// AddEvents and RemoveEvents are only valid during an edit, and only for a repo
type HookRequest struct {
	Name         string      `json:"name,omitempty"` // must be web or "", only create
	Active       *bool       `json:"active,omitempty"`
	AddEvents    []string    `json:"add_events,omitempty"` // only repo edit
	Config       *HookConfig `json:"config,omitempty"`
	Events       []string    `json:"events,omitempty"`
	RemoveEvents []string    `json:"remove_events,omitempty"` // only repo edit
}

// AllHookEvents causes github to send all events.
// https://developer.github.com/v3/activity/events/types/
var AllHookEvents = []string{"*"}

// IssueEventAction enumerates the triggers for this
// webhook payload type. See also:
// https://developer.github.com/v3/activity/events/types/#issuesevent
type IssueEventAction string

const (
	// IssueActionAssigned means assignees were added.
	IssueActionAssigned IssueEventAction = "assigned"
	// IssueActionUnassigned means assignees were added.
	IssueActionUnassigned IssueEventAction = "unassigned"
	// IssueActionLabeled means labels were added.
	IssueActionLabeled IssueEventAction = "labeled"
	// IssueActionUnlabeled means labels were removed.
	IssueActionUnlabeled IssueEventAction = "unlabeled"
	// IssueActionOpened means issue was opened/created.
	IssueActionOpened IssueEventAction = "opened"
	// IssueActionEdited means issue body was edited.
	IssueActionEdited IssueEventAction = "edited"
	// IssueActionDeleted means the issue was deleted.
	IssueActionDeleted IssueEventAction = "deleted"
	// IssueActionMilestoned means the milestone was added/changed.
	IssueActionMilestoned IssueEventAction = "milestoned"
	// IssueActionDemilestoned means a milestone was removed.
	IssueActionDemilestoned IssueEventAction = "demilestoned"
	// IssueActionClosed means issue was closed.
	IssueActionClosed IssueEventAction = "closed"
	// IssueActionReopened means issue was reopened.
	IssueActionReopened IssueEventAction = "reopened"
	// IssueActionPinned means the issue was pinned.
	IssueActionPinned IssueEventAction = "pinned"
	// IssueActionUnpinned means the issue was unpinned.
	IssueActionUnpinned IssueEventAction = "unpinned"
	// IssueActionTransferred means the issue was transferred to another repo.
	IssueActionTransferred IssueEventAction = "transferred"
	// IssueActionLocked means the issue was locked.
	IssueActionLocked IssueEventAction = "locked"
	// IssueActionUnlocked means the issue was unlocked.
	IssueActionUnlocked IssueEventAction = "unlocked"
)

// IssueEvent represents an issue event from a webhook payload (not from the events API).
type IssueEvent struct {
	Action IssueEventAction `json:"action"`
	Issue  Issue            `json:"issue"`
	Repo   Repo             `json:"repository"`
	// Label is specified for IssueActionLabeled and IssueActionUnlabeled events.
	Label  Label `json:"label"`
	Sender User  `json:"sender"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

// ListedIssueEvent represents an issue event from the events API (not from a webhook payload).
// https://developer.github.com/v3/issues/events/
type ListedIssueEvent struct {
	ID  int64  `json:"id,omitempty"`
	URL string `json:"url,omitempty"`

	// The User that generated this event.
	Actor User `json:"actor"`

	// This is the same as IssueEvent.Action
	Event IssueEventAction `json:"event"`

	CreatedAt time.Time `json:"created_at"`
	Issue     Issue     `json:"issue,omitempty"`

	// Only present on certain events.
	Assignee          User            `json:"assignee,omitempty"`
	Assigner          User            `json:"assigner,omitempty"`
	CommitID          string          `json:"commit_id,omitempty"`
	Milestone         Milestone       `json:"milestone,omitempty"`
	Label             Label           `json:"label"`
	Rename            Rename          `json:"rename,omitempty"`
	LockReason        string          `json:"lock_reason,omitempty"`
	ProjectCard       ProjectCard     `json:"project_card,omitempty"`
	DismissedReview   DismissedReview `json:"dismissed_review,omitempty"`
	RequestedReviewer User            `json:"requested_reviewer,omitempty"`
	ReviewRequester   User            `json:"review_requester,omitempty"`
}

// Rename contains details for 'renamed' events.
type Rename struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// DismissedReview represents details for 'dismissed_review' events.
type DismissedReview struct {
	// State represents the state of the dismissed review.DismissedReview
	// Possible values are: "commented", "approved", and "changes_requested".
	State             string `json:"state,omitempty"`
	ReviewID          int64  `json:"review_id,omitempty"`
	DismissalMessage  string `json:"dismissal_message,omitempty"`
	DismissalCommitID string `json:"dismissal_commit_id,omitempty"`
}

// IssueCommentEventAction enumerates the triggers for this
// webhook payload type. See also:
// https://developer.github.com/v3/activity/events/types/#issuecommentevent
type IssueCommentEventAction string

const (
	// IssueCommentActionCreated means the comment was created.
	IssueCommentActionCreated IssueCommentEventAction = "created"
	// IssueCommentActionEdited means the comment was edited.
	IssueCommentActionEdited IssueCommentEventAction = "edited"
	// IssueCommentActionDeleted means the comment was deleted.
	IssueCommentActionDeleted IssueCommentEventAction = "deleted"
)

// IssueCommentEvent is what GitHub sends us when an issue comment is changed.
type IssueCommentEvent struct {
	Action  IssueCommentEventAction `json:"action"`
	Issue   Issue                   `json:"issue"`
	Comment IssueComment            `json:"comment"`
	Repo    Repo                    `json:"repository"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

// Issue represents general info about an issue.
type Issue struct {
	ID          int       `json:"id"`
	NodeID      string    `json:"node_id"`
	User        User      `json:"user"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	HTMLURL     string    `json:"html_url"`
	Labels      []Label   `json:"labels"`
	Assignees   []User    `json:"assignees"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Milestone   Milestone `json:"milestone"`
	StateReason string    `json:"state_reason"`

	// This will be non-nil if it is a pull request.
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// IsAssignee checks if a user is assigned to the issue.
func (i Issue) IsAssignee(login string) bool {
	for _, assignee := range i.Assignees {
		if NormLogin(login) == NormLogin(assignee.Login) {
			return true
		}
	}
	return false
}

// IsAuthor checks if a user is the author of the issue.
func (i Issue) IsAuthor(login string) bool {
	return NormLogin(i.User.Login) == NormLogin(login)
}

// IsPullRequest checks if an issue is a pull request.
func (i Issue) IsPullRequest() bool {
	return i.PullRequest != nil
}

// HasLabel checks if an issue has a given label.
func (i Issue) HasLabel(labelToFind string) bool {
	for _, label := range i.Labels {
		if strings.EqualFold(label.Name, labelToFind) {
			return true
		}
	}
	return false
}

// IssueComment represents general info about an issue comment.
type IssueComment struct {
	ID        int       `json:"id,omitempty"`
	Body      string    `json:"body"`
	User      User      `json:"user,omitempty"`
	HTMLURL   string    `json:"html_url,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// StatusEvent fires whenever a git commit changes.
//
// See https://developer.github.com/v3/activity/events/types/#statusevent
type StatusEvent struct {
	SHA         string `json:"sha,omitempty"`
	State       string `json:"state,omitempty"`
	Description string `json:"description,omitempty"`
	TargetURL   string `json:"target_url,omitempty"`
	ID          int    `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Context     string `json:"context,omitempty"`
	Sender      User   `json:"sender,omitempty"`
	Repo        Repo   `json:"repository,omitempty"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

// IssuesSearchResult represents the result of an issues search.
type IssuesSearchResult struct {
	Total  int     `json:"total_count,omitempty"`
	Issues []Issue `json:"items,omitempty"`
}

// PushEvent is what GitHub sends us when a user pushes to a repo.
type PushEvent struct {
	Ref     string   `json:"ref"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Created bool     `json:"created"`
	Deleted bool     `json:"deleted"`
	Forced  bool     `json:"forced"`
	Compare string   `json:"compare"`
	Commits []Commit `json:"commits"`
	// Pusher is the user that pushed the commit, valid in a webhook event.
	Pusher User `json:"pusher"`
	// Sender contains more information that Pusher about the user.
	Sender User `json:"sender"`
	Repo   Repo `json:"repository"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

// Branch returns the name of the branch to which the user pushed.
func (pe PushEvent) Branch() string {
	ref := strings.TrimPrefix(pe.Ref, "refs/heads/") // if Ref is a branch
	ref = strings.TrimPrefix(ref, "refs/tags/")      // if Ref is a tag
	return ref
}

// Commit represents general info about a commit.
type Commit struct {
	ID       string   `json:"id"`
	Message  string   `json:"message"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

// Tree represents a GitHub tree.
type Tree struct {
	SHA string `json:"sha,omitempty"`
}

// ReviewEventAction enumerates the triggers for this
// webhook payload type. See also:
// https://developer.github.com/v3/activity/events/types/#pullrequestreviewevent
type ReviewEventAction string

const (
	// ReviewActionSubmitted means the review was submitted.
	ReviewActionSubmitted ReviewEventAction = "submitted"
	// ReviewActionEdited means the review was edited.
	ReviewActionEdited ReviewEventAction = "edited"
	// ReviewActionDismissed means the review was dismissed.
	ReviewActionDismissed ReviewEventAction = "dismissed"
)

// ReviewEvent is what GitHub sends us when a PR review is changed.
type ReviewEvent struct {
	Action      ReviewEventAction `json:"action"`
	PullRequest PullRequest       `json:"pull_request"`
	Repo        Repo              `json:"repository"`
	Review      Review            `json:"review"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

// ReviewState is the state a review can be in.
type ReviewState string

// Possible review states.
const (
	ReviewStateApproved         ReviewState = "APPROVED"
	ReviewStateChangesRequested             = "CHANGES_REQUESTED"
	ReviewStateCommented                    = "COMMENTED"
	ReviewStateDismissed                    = "DISMISSED"
	ReviewStatePending                      = "PENDING"
)

// Review describes a Pull Request review.
type Review struct {
	ID          int         `json:"id"`
	NodeID      string      `json:"node_id"`
	User        User        `json:"user"`
	Body        string      `json:"body"`
	State       ReviewState `json:"state"`
	HTMLURL     string      `json:"html_url"`
	SubmittedAt time.Time   `json:"submitted_at"`
}

// ReviewCommentEventAction enumerates the triggers for this
// webhook payload type. See also:
// https://developer.github.com/v3/activity/events/types/#pullrequestreviewcommentevent
type ReviewCommentEventAction string

const (
	// ReviewCommentActionCreated means the comment was created.
	ReviewCommentActionCreated ReviewCommentEventAction = "created"
	// ReviewCommentActionEdited means the comment was edited.
	ReviewCommentActionEdited ReviewCommentEventAction = "edited"
	// ReviewCommentActionDeleted means the comment was deleted.
	ReviewCommentActionDeleted ReviewCommentEventAction = "deleted"
)

// ReviewCommentEvent is what GitHub sends us when a PR review comment is changed.
type ReviewCommentEvent struct {
	Action      ReviewCommentEventAction `json:"action"`
	PullRequest PullRequest              `json:"pull_request"`
	Repo        Repo                     `json:"repository"`
	Comment     ReviewComment            `json:"comment"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

// DiffSide enumerates the sides of the diff that the PR's changes appear on.
// See also: https://docs.github.com/en/rest/reference/pulls#create-a-review-comment-for-a-pull-request
type DiffSide string

const (
	// DiffSideLeft means left side of the diff.
	DiffSideLeft = "LEFT"
	// DiffSideRight means right side of the diff.
	DiffSideRight = "RIGHT"
)

// ReviewComment describes a Pull Request review.
type ReviewComment struct {
	ID        int       `json:"id"`
	NodeID    string    `json:"node_id"`
	ReviewID  int       `json:"pull_request_review_id"`
	User      User      `json:"user"`
	Body      string    `json:"body"`
	Path      string    `json:"path"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Position will be nil if the code has changed such that the comment is no
	// longer relevant.
	Position  *int     `json:"position,omitempty"`
	Side      DiffSide `json:"side,omitempty"`
	StartSide DiffSide `json:"start_side,omitempty"`
	Line      int      `json:"line,omitempty"`
	StartLine int      `json:"start_line,omitempty"`
}

// ReviewAction is the action that a review can be made with.
type ReviewAction string

// Possible review actions. Leave Action blank for a pending review.
const (
	Approve        ReviewAction = "APPROVE"
	RequestChanges              = "REQUEST_CHANGES"
	Comment                     = "COMMENT"
)

// DraftReview is what we give GitHub when we want to make a PR Review. This is
// different than what we receive when we ask for a Review.
type DraftReview struct {
	// If unspecified, defaults to the most recent commit in the PR.
	CommitSHA string `json:"commit_id,omitempty"`
	Body      string `json:"body"`
	// If unspecified, defaults to PENDING.
	Action   ReviewAction         `json:"event,omitempty"`
	Comments []DraftReviewComment `json:"comments,omitempty"`
}

// DraftReviewComment is a comment in a draft review.
type DraftReviewComment struct {
	Path string `json:"path"`
	// Position in the patch, not the line number in the file.
	Position int    `json:"position"`
	Body     string `json:"body"`
}

// Content is some base64 encoded github file content
// It include selected fields available in content record returned by
// GH "GET" method. See also:
// https://docs.github.com/en/free-pro-team@latest/rest/reference/repos#get-repository-content
type Content struct {
	Content string `json:"content"`
	SHA     string `json:"sha"`
}

const (
	// PrivacySecret memberships are only visible to other team members.
	PrivacySecret = "secret"
	// PrivacyClosed memberships are visible to org members.
	PrivacyClosed = "closed"
)

// Team is a github organizational team
type Team struct {
	ID           int            `json:"id,omitempty"`
	Name         string         `json:"name"`
	Slug         string         `json:"slug"`
	Description  string         `json:"description,omitempty"`
	Privacy      string         `json:"privacy,omitempty"`
	Parent       *Team          `json:"parent,omitempty"`         // Only present in responses
	ParentTeamID *int           `json:"parent_team_id,omitempty"` // Only valid in creates/edits
	Permission   TeamPermission `json:"permission,omitempty"`
}

// TeamMember is a member of an organizational team
type TeamMember struct {
	Login string `json:"login"`
}

const (
	// RoleAll lists both members and admins
	RoleAll = "all"
	// RoleAdmin specifies the user is an org admin, or lists only admins
	RoleAdmin = "admin"
	// RoleMaintainer specifies the user is a team maintainer, or lists only maintainers
	RoleMaintainer = "maintainer"
	// RoleMember specifies the user is a regular user, or only lists regular users
	RoleMember = "member"
	// StatePending specifies the user has an invitation to the org/team.
	StatePending = "pending"
	// StateActive specifies the user's membership is active.
	StateActive = "active"
)

// Membership specifies the role and state details for an org and/or team.
type Membership struct {
	// admin or member
	Role string `json:"role"`
	// pending or active
	State string `json:"state,omitempty"`
}

// Organization stores metadata information about an organization
type Organization struct {
	// Login has the same meaning as Name, but it's more reliable to use as Name can sometimes be empty,
	// see https://developer.github.com/v3/orgs/#list-organizations
	Login  string `json:"login"`
	Id     int    `json:"id"`
	NodeId string `json:"node_id"`
	// BillingEmail holds private billing address
	BillingEmail string `json:"billing_email"`
	Company      string `json:"company"`
	// Email is publicly visible
	Email                        string `json:"email"`
	Location                     string `json:"location"`
	Name                         string `json:"name"`
	Description                  string `json:"description"`
	HasOrganizationProjects      bool   `json:"has_organization_projects"`
	HasRepositoryProjects        bool   `json:"has_repository_projects"`
	DefaultRepositoryPermission  string `json:"default_repository_permission"`
	MembersCanCreateRepositories bool   `json:"members_can_create_repositories"`
}

// OrgMembership contains Membership fields for user membership in an org.
type OrgMembership struct {
	Membership
}

// TeamMembership contains Membership fields for user membership on a team.
type TeamMembership struct {
	Membership
}

// OrgInvitation contains Login and other details about the invitation.
type OrgInvitation struct {
	TeamMember
	Email   string     `json:"email"`
	Inviter TeamMember `json:"inviter"`
}

// UserRepoInvitation is returned by repo invitation obtained by user.
type UserRepoInvitation struct {
	InvitationID int                 `json:"id"`
	Repository   *Repo               `json:"repository,omitempty"`
	Permission   RepoPermissionLevel `json:"permissions"`
}

// OrgPermissionLevel is admin, and member
//
// See https://docs.github.com/en/rest/reference/orgs#set-organization-membership-for-a-user
type OrgPermissionLevel string

const (
	// OrgMember is the member
	OrgMember OrgPermissionLevel = "member"
	// OrgAdmin manages the org
	OrgAdmin OrgPermissionLevel = "admin"
	// OrgUnaffiliated probably means user not a member yet, this was returned
	// from an org invitation, had to add it so unmarshal doesn't crash
	OrgUnaffiliated OrgPermissionLevel = "unaffiliated"
	// OrgReinstate means the user was removed and the invited again before n months have passed.
	// More info here: https://docs.github.com/en/github-ae@latest/organizations/managing-membership-in-your-organization/reinstating-a-former-member-of-your-organization
	OrgReinstate OrgPermissionLevel = "reinstate"
)

var orgPermissionLevels = map[OrgPermissionLevel]bool{
	OrgMember:       true,
	OrgAdmin:        true,
	OrgUnaffiliated: true,
	OrgReinstate:    true,
}

// MarshalText returns the byte representation of the permission
func (l OrgPermissionLevel) MarshalText() ([]byte, error) {
	return []byte(l), nil
}

// UnmarshalText validates the text is a valid string
func (l *OrgPermissionLevel) UnmarshalText(text []byte) error {
	v := OrgPermissionLevel(text)
	if _, ok := orgPermissionLevels[v]; !ok {
		return fmt.Errorf("bad org permission: %s not in %v", v, orgPermissionLevels)
	}
	*l = v
	return nil
}

// UserOrganization contains info consumed by UserOrgInvitation.
type UserOrganization struct {
	// Login is the name of org
	Login string `json:"login"`
}

// UserOrgInvitation is returned by org invitation obtained by user.
type UserOrgInvitation struct {
	State string             `json:"state"`
	Role  OrgPermissionLevel `json:"role"`
	Org   UserOrganization   `json:"organization"`
}

// GenericCommentEventAction coerces multiple actions into its generic equivalent.
type GenericCommentEventAction string

// Comments indicate values that are coerced to the specified value.
const (
	// GenericCommentActionCreated means something was created/opened/submitted
	GenericCommentActionCreated GenericCommentEventAction = "created" // "opened", "submitted"
	// GenericCommentActionEdited means something was edited.
	GenericCommentActionEdited GenericCommentEventAction = "edited"
	// GenericCommentActionDeleted means something was deleted/dismissed.
	GenericCommentActionDeleted GenericCommentEventAction = "deleted" // "dismissed"
)

// GeneralizeCommentAction normalizes the action string to a GenericCommentEventAction or returns ""
// if the action is unrelated to the comment text. (For example a PR 'label' action.)
func GeneralizeCommentAction(action string) GenericCommentEventAction {
	switch action {
	case "created", "opened", "submitted":
		return GenericCommentActionCreated
	case "edited":
		return GenericCommentActionEdited
	case "deleted", "dismissed":
		return GenericCommentActionDeleted
	}
	// The action is not related to the text body.
	return ""
}

// GenericCommentEvent is a fake event type that is instantiated for any github event that contains
// comment like content.
// The specific events that are also handled as GenericCommentEvents are:
// - issue_comment events
// - pull_request_review events
// - pull_request_review_comment events
// - pull_request events with action in ["opened", "edited"]
// - issue events with action in ["opened", "edited"]
//
// Issue and PR "closed" events are not coerced to the "deleted" Action and do not trigger
// a GenericCommentEvent because these events don't actually remove the comment content from GH.
type GenericCommentEvent struct {
	ID           int    `json:"id"`
	NodeID       string `json:"node_id"`
	CommentID    *int
	IsPR         bool
	Action       GenericCommentEventAction
	Body         string
	HTMLURL      string
	Number       int
	Repo         Repo
	User         User
	IssueAuthor  User
	Assignees    []User
	IssueState   string
	IssueTitle   string
	IssueBody    string
	IssueHTMLURL string
	GUID         string
}

// Milestone is a milestone defined on a github repository
type Milestone struct {
	Title  string `json:"title"`
	Number int    `json:"number"`
	State  string `json:"state"`
}

// RepositoryCommit represents a commit in a repo.
// Note that it's wrapping a GitCommit, so author/committer information is in two places,
// but contain different details about them: in RepositoryCommit "github details", in GitCommit - "git details".
// Get single commit also use it, see: https://developer.github.com/v3/repos/commits/#get-a-single-commit.
type RepositoryCommit struct {
	NodeID      string      `json:"node_id,omitempty"`
	SHA         string      `json:"sha,omitempty"`
	Commit      GitCommit   `json:"commit,omitempty"`
	Author      User        `json:"author,omitempty"`
	Committer   User        `json:"committer,omitempty"`
	Parents     []GitCommit `json:"parents,omitempty"`
	HTMLURL     string      `json:"html_url,omitempty"`
	URL         string      `json:"url,omitempty"`
	CommentsURL string      `json:"comments_url,omitempty"`

	// Details about how many changes were made in this commit. Only filled in during GetCommit!
	Stats *CommitStats `json:"stats,omitempty"`
	// Details about which files, and how this commit touched. Only filled in during GetCommit!
	Files []CommitFile `json:"files,omitempty"`
}

// CommitStats represents the number of additions / deletions from a file in a given RepositoryCommit or GistCommit.
type CommitStats struct {
	Additions int `json:"additions,omitempty"`
	Deletions int `json:"deletions,omitempty"`
	Total     int `json:"total,omitempty"`
}

// CommitFile represents a file modified in a commit.
type CommitFile struct {
	SHA              string `json:"sha,omitempty"`
	Filename         string `json:"filename,omitempty"`
	Additions        int    `json:"additions,omitempty"`
	Deletions        int    `json:"deletions,omitempty"`
	Changes          int    `json:"changes,omitempty"`
	Status           string `json:"status,omitempty"`
	Patch            string `json:"patch,omitempty"`
	BlobURL          string `json:"blob_url,omitempty"`
	RawURL           string `json:"raw_url,omitempty"`
	ContentsURL      string `json:"contents_url,omitempty"`
	PreviousFilename string `json:"previous_filename,omitempty"`
}

// GitCommit represents a GitHub commit.
type GitCommit struct {
	SHA          string                 `json:"sha,omitempty"`
	Author       CommitAuthor           `json:"author,omitempty"`
	Committer    CommitAuthor           `json:"committer,omitempty"`
	Message      string                 `json:"message,omitempty"`
	Tree         Tree                   `json:"tree,omitempty"`
	Parents      []GitCommit            `json:"parents,omitempty"`
	Stats        *CommitStats           `json:"stats,omitempty"`
	HTMLURL      string                 `json:"html_url,omitempty"`
	URL          string                 `json:"url,omitempty"`
	Verification *SignatureVerification `json:"verification,omitempty"`
	NodeID       string                 `json:"node_id,omitempty"`

	// CommentCount is the number of GitHub comments on the commit. This
	// is only populated for requests that fetch GitHub data like
	// Pulls.ListCommits, Repositories.ListCommits, etc.
	CommentCount *int `json:"comment_count,omitempty"`
}

// CommitAuthor represents the author or committer of a commit. The commit
// author may not correspond to a GitHub User.
type CommitAuthor struct {
	Date  time.Time `json:"date,omitempty"`
	Name  string    `json:"name,omitempty"`
	Email string    `json:"email,omitempty"`

	// The following fields are only populated by Webhook events.
	Login *string `json:"username,omitempty"`
}

// SignatureVerification represents GPG signature verification.
type SignatureVerification struct {
	Verified  bool   `json:"verified,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Signature string `json:"signature,omitempty"`
	Payload   string `json:"payload,omitempty"`
}

// Project is a github project
type Project struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// ProjectColumn is a colunm in a github project
type ProjectColumn struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// ProjectCard is a github project card
type ProjectCard struct {
	ID          int    `json:"id"`
	ContentID   int    `json:"content_id"`
	ContentType string `json:"content_type"`
	ContentURL  string `json:"content_url"`
}

type CheckRunList struct {
	Total     int        `json:"total_count,omitempty"`
	CheckRuns []CheckRun `json:"check_runs,omitempty"`
}

type CheckRun struct {
	ID           int64          `json:"id,omitempty"`
	NodeID       string         `json:"node_id,omitempty"`
	HeadSHA      string         `json:"head_sha,omitempty"`
	ExternalID   string         `json:"external_id,omitempty"`
	URL          string         `json:"url,omitempty"`
	HTMLURL      string         `json:"html_url,omitempty"`
	DetailsURL   string         `json:"details_url,omitempty"`
	Status       string         `json:"status,omitempty"`
	Conclusion   string         `json:"conclusion,omitempty"`
	StartedAt    string         `json:"started_at,omitempty"`
	CompletedAt  string         `json:"completed_at,omitempty"`
	Output       CheckRunOutput `json:"output,omitempty"`
	Name         string         `json:"name,omitempty"`
	CheckSuite   CheckSuite     `json:"check_suite,omitempty"`
	App          App            `json:"app,omitempty"`
	PullRequests []PullRequest  `json:"pull_requests,omitempty"`
}

type CheckRunOutput struct {
	Title            string               `json:"title,omitempty"`
	Summary          string               `json:"summary,omitempty"`
	Text             string               `json:"text,omitempty"`
	AnnotationsCount int                  `json:"annotations_count,omitempty"`
	AnnotationsURL   string               `json:"annotations_url,omitempty"`
	Annotations      []CheckRunAnnotation `json:"annotations,omitempty"`
	Images           []CheckRunImage      `json:"images,omitempty"`
}

type CheckRunAnnotation struct {
	Path            string `json:"path,omitempty"`
	StartLine       int    `json:"start_line,omitempty"`
	EndLine         int    `json:"end_line,omitempty"`
	StartColumn     int    `json:"start_column,omitempty"`
	EndColumn       int    `json:"end_column,omitempty"`
	AnnotationLevel string `json:"annotation_level,omitempty"`
	Message         string `json:"message,omitempty"`
	Title           string `json:"title,omitempty"`
	RawDetails      string `json:"raw_details,omitempty"`
}

type CheckRunImage struct {
	Alt      string `json:"alt,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Caption  string `json:"caption,omitempty"`
}

type CheckSuite struct {
	ID           int64         `json:"id,omitempty"`
	NodeID       string        `json:"node_id,omitempty"`
	HeadBranch   string        `json:"head_branch,omitempty"`
	HeadSHA      string        `json:"head_sha,omitempty"`
	URL          string        `json:"url,omitempty"`
	BeforeSHA    string        `json:"before,omitempty"`
	AfterSHA     string        `json:"after,omitempty"`
	Status       string        `json:"status,omitempty"`
	Conclusion   string        `json:"conclusion,omitempty"`
	App          *App          `json:"app,omitempty"`
	Repository   *Repo         `json:"repository,omitempty"`
	PullRequests []PullRequest `json:"pull_requests,omitempty"`

	// The following fields are only populated by Webhook events.
	HeadCommit *Commit `json:"head_commit,omitempty"`
}

type App struct {
	ID          int64                    `json:"id,omitempty"`
	Slug        string                   `json:"slug,omitempty"`
	NodeID      string                   `json:"node_id,omitempty"`
	Owner       User                     `json:"owner,omitempty"`
	Name        string                   `json:"name,omitempty"`
	Description string                   `json:"description,omitempty"`
	ExternalURL string                   `json:"external_url,omitempty"`
	HTMLURL     string                   `json:"html_url,omitempty"`
	CreatedAt   string                   `json:"created_at,omitempty"`
	UpdatedAt   string                   `json:"updated_at,omitempty"`
	Permissions *InstallationPermissions `json:"permissions,omitempty"`
	Events      []string                 `json:"events,omitempty"`
}

type InstallationPermissions struct {
	Administration              string `json:"administration,omitempty"`
	Blocking                    string `json:"blocking,omitempty"`
	Checks                      string `json:"checks,omitempty"`
	Contents                    string `json:"contents,omitempty"`
	ContentReferences           string `json:"content_references,omitempty"`
	Deployments                 string `json:"deployments,omitempty"`
	Emails                      string `json:"emails,omitempty"`
	Followers                   string `json:"followers,omitempty"`
	Issues                      string `json:"issues,omitempty"`
	Metadata                    string `json:"metadata,omitempty"`
	Members                     string `json:"members,omitempty"`
	OrganizationAdministration  string `json:"organization_administration,omitempty"`
	OrganizationHooks           string `json:"organization_hooks,omitempty"`
	OrganizationPlan            string `json:"organization_plan,omitempty"`
	OrganizationPreReceiveHooks string `json:"organization_pre_receive_hooks,omitempty"`
	OrganizationProjects        string `json:"organization_projects,omitempty"`
	OrganizationUserBlocking    string `json:"organization_user_blocking,omitempty"`
	Packages                    string `json:"packages,omitempty"`
	Pages                       string `json:"pages,omitempty"`
	PullRequests                string `json:"pull_requests,omitempty"`
	RepositoryHooks             string `json:"repository_hooks,omitempty"`
	RepositoryProjects          string `json:"repository_projects,omitempty"`
	RepositoryPreReceiveHooks   string `json:"repository_pre_receive_hooks,omitempty"`
	SingleFile                  string `json:"single_file,omitempty"`
	Statuses                    string `json:"statuses,omitempty"`
	TeamDiscussions             string `json:"team_discussions,omitempty"`
	VulnerabilityAlerts         string `json:"vulnerability_alerts,omitempty"`
}

// AppInstallation represents a GitHub Apps installation.
type AppInstallation struct {
	ID                  int64                   `json:"id,omitempty"`
	AppSlug             string                  `json:"app_slug,omitempty"`
	NodeID              string                  `json:"node_id,omitempty"`
	AppID               int64                   `json:"app_id,omitempty"`
	TargetID            int64                   `json:"target_id,omitempty"`
	Account             User                    `json:"account,omitempty"`
	AccessTokensURL     string                  `json:"access_tokens_url,omitempty"`
	RepositoriesURL     string                  `json:"repositories_url,omitempty"`
	HTMLURL             string                  `json:"html_url,omitempty"`
	TargetType          string                  `json:"target_type,omitempty"`
	SingleFileName      string                  `json:"single_file_name,omitempty"`
	RepositorySelection string                  `json:"repository_selection,omitempty"`
	Events              []string                `json:"events,omitempty"`
	Permissions         InstallationPermissions `json:"permissions,omitempty"`
	CreatedAt           string                  `json:"created_at,omitempty"`
	UpdatedAt           string                  `json:"updated_at,omitempty"`
}

// AppInstallationList represents the result of an AppInstallationList search.
type AppInstallationList struct {
	Total         int               `json:"total_count,omitempty"`
	Installations []AppInstallation `json:"installations,omitempty"`
}

// AppInstallationToken is the response when retrieving an app installation
// token.
type AppInstallationToken struct {
	Token        string                  `json:"token,omitempty"`
	ExpiresAt    time.Time               `json:"expires_at,omitempty"`
	Permissions  InstallationPermissions `json:"permissions,omitempty"`
	Repositories []Repo                  `json:"repositories,omitempty"`
}

// DirectoryContent contains information about a github directory.
// It include selected fields available in content records returned by
// GH "GET" method. See also:
// https://docs.github.com/en/free-pro-team@latest/rest/reference/repos#get-repository-content
type DirectoryContent struct {
	SHA  string `json:"sha"`
	Type string `json:"type"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// WorkflowRunEvent holds information about an `workflow_run` GitHub webhook event.
// see // https://docs.github.com/en/developers/webhooks-and-events/webhooks/webhook-events-and-payloads#workflow_run
type WorkflowRunEvent struct {
	Action       string       `json:"action"`
	WorkflowRun  WorkflowRun  `json:"workflow_run"`
	Workflow     Workflow     `json:"workflow"`
	Repo         *Repo        `json:"repository"`
	Organization Organization `json:"organization"`
	Sender       User         `json:"sender"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

type WorkflowRun struct {
	ID               int           `json:"id"`
	Name             string        `json:"name"`
	NodeID           string        `json:"node_id"`
	HeadBranch       string        `json:"head_branch"`
	HeadSha          string        `json:"head_sha"`
	RunNumber        int           `json:"run_number"`
	Event            string        `json:"event"`
	Status           string        `json:"status"`
	Conclusion       string        `json:"conclusion"`
	WorkflowID       int           `json:"workflow_id"`
	CheckSuiteID     int64         `json:"check_suite_id"`
	CheckSuiteNodeID string        `json:"check_suite_node_id"`
	URL              string        `json:"url"`
	PullRequests     []PullRequest `json:"pull_requests"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	RunAttempt       int           `json:"run_attempt"`
	RunStartedAt     time.Time     `json:"run_started_at"`
	HeadCommit       *Commit       `json:"head_commit"`
	Repository       *Repo         `json:"repository"`
}

type Workflow struct {
	ID        int       `json:"id"`
	NodeID    string    `json:"node_id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RegistryPackageEvent holds information about an `registry_package` GitHub webhook event.
// see https://docs.github.com/en/webhooks/webhook-events-and-payloads#registry_package
type RegistryPackageEvent struct {
	Action          string          `json:"action"`
	RegistryPackage RegistryPackage `json:"registry_package"`
	Repo            *Repo           `json:"repository"`
	Organization    Organization    `json:"organization"`
	Sender          User            `json:"sender"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

type RegistryPackage struct {
	ID             int            `json:"id"`
	Name           string         `json:"name"`
	Namespace      string         `json:"namespace"`
	Description    string         `json:"description"`
	Ecosystem      string         `json:"ecosystem"`
	PackageType    string         `json:"package_type"`
	HTMLURL        string         `json:"html_url"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Owner          User           `json:"owner"`
	Registry       Registry       `json:"registry"`
	PackageVersion PackageVersion `json:"package_version"`
}

type Registry struct {
	AboutURL string `json:"about_url"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Vendor   string `json:"vendor"`
}

type PackageVersion struct {
	ID                  int               `json:"id"`
	Version             string            `json:"version"`
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	Summary             string            `json:"summary"`
	Manifest            string            `json:"manifest"`
	HTMLURL             string            `json:"html_url"`
	TargetCommitish     string            `json:"target_commitish"`
	TargetOid           string            `json:"target_oid"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	Metadata            []interface{}     `json:"metadata"`
	ContainerMetadata   ContainerMetadata `json:"container_metadata"`
	PackageFiles        []interface{}     `json:"package_files"`
	Author              User              `json:"author"`
	InstallationCommand string            `json:"installation_command"`
	PackageURL          string            `json:"package_url"`
}

type ContainerMetadata struct {
	Tag      Tag      `json:"tag"`
	Labels   Labels   `json:"labels"`
	Manifest Manifest `json:"manifest"`
}
type Tag struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type Labels struct {
	Description string `json:"description"`
	Source      string `json:"source"`
	Revision    string `json:"revision"`
	ImageURL    string `json:"image_url"`
	Licenses    string `json:"licenses"`
}

type Manifest struct {
	Digest    string   `json:"digest"`
	MediaType string   `json:"media_type"`
	URI       string   `json:"uri"`
	Size      int      `json:"size"`
	Config    Config   `json:"config"`
	Layers    []Layers `json:"layers"`
}

type Config struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
	Size      int    `json:"size"`
}

type Layers struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
	Size      int    `json:"size"`
}
