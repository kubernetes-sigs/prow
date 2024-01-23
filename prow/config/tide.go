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

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/git/types"
	"k8s.io/test-infra/prow/git/v2"
)

// TideQueries is a TideQuery slice.
type TideQueries []TideQuery

type TideBranchMergeType struct {
	MergeType types.PullRequestMergeType
	Regexpr   *regexp.Regexp
}

func (tbmt TideBranchMergeType) Match(branch string) bool {
	return tbmt.Regexpr.MatchString(branch)
}

func (tbmt TideBranchMergeType) MarshalJSON() ([]byte, error) {
	return json.Marshal(tbmt.MergeType)
}

func (tbmt *TideBranchMergeType) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &tbmt.MergeType)
}

type TideRepoMergeType struct {
	Branches  map[string]TideBranchMergeType
	MergeType types.PullRequestMergeType
}

// When TideRepoMergeType.MergeType is present, unmarshal into:
//
//	kubernetes: squash
//
// when TideRepoMergeType.Branches is not empty, unmarshal into:
//
//	kubernetes:
//	  main: squash
func (trmt TideRepoMergeType) MarshalJSON() ([]byte, error) {
	if trmt.MergeType != "" {
		return json.Marshal(trmt.MergeType)
	}
	if trmt.Branches == nil || len(trmt.Branches) == 0 {
		return json.Marshal("")
	}
	return json.Marshal(trmt.Branches)
}

// Full configuration:
//
//	test-infra:
//	  main: merge
//
// unmarshal into map[string][TideBranchMergeType]
//
// Repo-wide configuration:
//
//	test-infra: merge
//
// unmarshal into types.PullRequestMergeType
func (trmt *TideRepoMergeType) UnmarshalJSON(b []byte) error {
	var mt types.PullRequestMergeType
	if err := json.Unmarshal(b, &mt); err == nil {
		trmt.MergeType = mt
		return nil
	}
	var branches map[string]TideBranchMergeType
	if err := json.Unmarshal(b, &branches); err != nil {
		return err
	}
	trmt.Branches = branches
	return nil
}

type TideOrgMergeType struct {
	Repos     map[string]TideRepoMergeType
	MergeType types.PullRequestMergeType
}

// When TideOrgMergeType.MergeType is present, unmarshal into:
//
//	kubernetes: squash
//
// when TideOrgMergeType.Repos is not empty, unmarshal into:
//
//	kubernetes:
//	  test-infra: squash
func (tomt TideOrgMergeType) MarshalJSON() ([]byte, error) {
	if tomt.MergeType != "" {
		return json.Marshal(tomt.MergeType)
	}
	if tomt.Repos == nil || len(tomt.Repos) == 0 {
		return json.Marshal("")
	}
	return json.Marshal(tomt.Repos)
}

// Org-wide configuration:
//
//	kubernetes: merge
//
// unmarshal into types.PullRequestMergeType.
//
// Full configuration:
//
//	kubernetes:
//	  test-infra:
//	    main: merge
//
// unmarshal into map[string][TideRepoMergeType]:
func (tomt *TideOrgMergeType) UnmarshalJSON(b []byte) error {
	var mt types.PullRequestMergeType
	if err := json.Unmarshal(b, &mt); err == nil {
		tomt.MergeType = mt
		return nil
	}
	var repos map[string]TideRepoMergeType
	if err := json.Unmarshal(b, &repos); err != nil {
		return err
	}
	tomt.Repos = repos
	return nil
}

// TideContextPolicy configures options about how to handle various contexts.
type TideContextPolicy struct {
	// whether to consider unknown contexts optional (skip) or required.
	SkipUnknownContexts       *bool    `json:"skip-unknown-contexts,omitempty"`
	RequiredContexts          []string `json:"required-contexts,omitempty"`
	RequiredIfPresentContexts []string `json:"required-if-present-contexts,omitempty"`
	OptionalContexts          []string `json:"optional-contexts,omitempty"`
	// Infer required and optional jobs from Branch Protection configuration
	FromBranchProtection *bool `json:"from-branch-protection,omitempty"`
}

// TideOrgContextPolicy overrides the policy for an org, and any repo overrides.
type TideOrgContextPolicy struct {
	TideContextPolicy `json:",inline"`
	Repos             map[string]TideRepoContextPolicy `json:"repos,omitempty"`
}

// TideRepoContextPolicy overrides the policy for repo, and any branch overrides.
type TideRepoContextPolicy struct {
	TideContextPolicy `json:",inline"`
	Branches          map[string]TideContextPolicy `json:"branches,omitempty"`
}

// TideContextPolicyOptions holds the default policy, and any org overrides.
type TideContextPolicyOptions struct {
	TideContextPolicy `json:",inline"`
	// GitHub Orgs
	Orgs map[string]TideOrgContextPolicy `json:"orgs,omitempty"`
}

// TideMergeCommitTemplate holds templates to use for merge commits.
type TideMergeCommitTemplate struct {
	TitleTemplate string `json:"title,omitempty"`
	BodyTemplate  string `json:"body,omitempty"`

	Title *template.Template `json:"-"`
	Body  *template.Template `json:"-"`
}

// TidePriority contains a list of labels used to prioritize PRs in the merge pool
type TidePriority struct {
	Labels []string `json:"labels,omitempty"`
}

// Tide is config for the tide pool.
type Tide struct {
	Gerrit *TideGerritConfig `json:"gerrit,omitempty"`
	// SyncPeriod specifies how often Tide will sync jobs with GitHub. Defaults to 1m.
	SyncPeriod *metav1.Duration `json:"sync_period,omitempty"`
	// MaxGoroutines is the maximum number of goroutines spawned inside the
	// controller to handle org/repo:branch pools. Defaults to 20. Needs to be a
	// positive number.
	MaxGoroutines int `json:"max_goroutines,omitempty"`
	// BatchSizeLimitMap is a key/value pair of an org or org/repo as the key and
	// integer batch size limit as the value. Use "*" as key to set a global default.
	// Special values:
	//  0 => unlimited batch size
	// -1 => batch merging disabled :(
	BatchSizeLimitMap map[string]int `json:"batch_size_limit,omitempty"`
	// PrioritizeExistingBatches configures on org or org/repo level if Tide should continue
	// testing pre-existing batches instead of immediately including new PRs as they become
	// eligible. Continuing on an old batch allows to re-use all existing test results whereas
	// starting a new one requires to start new instances of all tests.
	// Use '*' as key to set this globally. Defaults to true.
	PrioritizeExistingBatchesMap map[string]bool `json:"prioritize_existing_batches,omitempty"`

	TideGitHubConfig `json:",inline"`
}

// TideGitHubConfig is the tide config for GitHub.
type TideGitHubConfig struct {
	// StatusUpdatePeriod specifies how often Tide will update GitHub status contexts.
	// Defaults to the value of SyncPeriod.
	StatusUpdatePeriod *metav1.Duration `json:"status_update_period,omitempty"`
	// Queries represents a list of GitHub search queries that collectively
	// specify the set of PRs that meet merge requirements.
	Queries TideQueries `json:"queries,omitempty"`

	// A key/value pair of an org/repo as the key and merge method to override
	// the default method of merge. Valid options are squash, rebase, and merge.
	MergeType map[string]TideOrgMergeType `json:"merge_method,omitempty"`

	// A key/value pair of an org/repo as the key and Go template to override
	// the default merge commit title and/or message. Template is passed the
	// PullRequest struct (prow/github/types.go#PullRequest)
	MergeTemplate map[string]TideMergeCommitTemplate `json:"merge_commit_template,omitempty"`

	// URL for tide status contexts.
	// We can consider allowing this to be set separately for separate repos, or
	// allowing it to be a template.
	TargetURL string `json:"target_url,omitempty"`

	// TargetURLs is a map from "*", <org>, or <org/repo> to the URL for the tide status contexts.
	// The most specific key that matches will be used.
	// This field is mutually exclusive with TargetURL.
	TargetURLs map[string]string `json:"target_urls,omitempty"`

	// PRStatusBaseURL is the base URL for the PR status page.
	// This is used to link to a merge requirements overview
	// in the tide status context.
	// Will be deprecated on June 2020.
	PRStatusBaseURL string `json:"pr_status_base_url,omitempty"`

	// PRStatusBaseURLs is the base URL for the PR status page
	// mapped by org or org/repo level.
	PRStatusBaseURLs map[string]string `json:"pr_status_base_urls,omitempty"`

	// BlockerLabel is an optional label that is used to identify merge blocking
	// GitHub issues.
	// Leave this blank to disable this feature and save 1 API token per sync loop.
	BlockerLabel string `json:"blocker_label,omitempty"`

	// SquashLabel is an optional label that is used to identify PRs that should
	// always be squash merged.
	// Leave this blank to disable this feature.
	SquashLabel string `json:"squash_label,omitempty"`

	// RebaseLabel is an optional label that is used to identify PRs that should
	// always be rebased and merged.
	// Leave this blank to disable this feature.
	RebaseLabel string `json:"rebase_label,omitempty"`

	// MergeLabel is an optional label that is used to identify PRs that should
	// always be merged with all individual commits from the PR.
	// Leave this blank to disable this feature.
	MergeLabel string `json:"merge_label,omitempty"`

	// TideContextPolicyOptions defines merge options for context. If not set it will infer
	// the required and optional contexts from the prow jobs configured and use the github
	// combined status; otherwise it may apply the branch protection setting or let user
	// define their own options in case branch protection is not used.
	ContextOptions TideContextPolicyOptions `json:"context_options,omitempty"`

	// BatchSizeLimitMap is a key/value pair of an org or org/repo as the key and
	// integer batch size limit as the value. Use "*" as key to set a global default.
	// Special values:
	//  0 => unlimited batch size
	// -1 => batch merging disabled :(
	BatchSizeLimitMap map[string]int `json:"batch_size_limit,omitempty"`

	// Priority is an ordered list of sets of labels that would be prioritized before other PRs
	// PRs should match all labels contained in a set to be prioritized. The first entry has
	// the highest priority.
	Priority []TidePriority `json:"priority,omitempty"`

	// DisplayAllQueriesInStatus controls if Tide should mention all queries in the status it
	// creates. The default is to only mention the one to which we are closest (Calculated
	// by total number of requirements - fulfilled number of requirements).
	DisplayAllQueriesInStatus bool `json:"display_all_tide_queries_in_status,omitempty"`
}

// TideGerritConfig contains all Gerrit related configurations for tide.
type TideGerritConfig struct {
	Queries GerritOrgRepoConfigs `json:"queries"`
	// RateLimit defines how many changes to query per gerrit API call
	// default is 5.
	RateLimit int `json:"ratelimit,omitempty"`
}

func (t *Tide) mergeFrom(additional *Tide) error {

	// Duplicate queries are pointless but not harmful, we
	// have code to de-duplicate them down the line to not
	// increase token usage needlessly.
	t.Queries = append(t.Queries, additional.Queries...)

	if t.MergeType == nil {
		t.MergeType = additional.MergeType
		return nil
	}

	var errs []error
	for orgOrRepo, mergeMethod := range additional.MergeType {
		if _, alreadyConfigured := t.MergeType[orgOrRepo]; alreadyConfigured {
			errs = append(errs, fmt.Errorf("config for org or repo %s passed more than once", orgOrRepo))
			continue
		}
		t.MergeType[orgOrRepo] = mergeMethod
	}

	return utilerrors.NewAggregate(errs)
}

func (t *Tide) PrioritizeExistingBatches(repo OrgRepo) bool {
	if val, set := t.PrioritizeExistingBatchesMap[repo.String()]; set {
		return val
	}
	if val, set := t.PrioritizeExistingBatchesMap[repo.Org]; set {
		return val
	}

	if val, set := t.PrioritizeExistingBatchesMap["*"]; set {
		return val
	}

	return true
}

func (t *Tide) BatchSizeLimit(repo OrgRepo) int {
	if limit, ok := t.BatchSizeLimitMap[repo.String()]; ok {
		return limit
	}
	if limit, ok := t.BatchSizeLimitMap[repo.Org]; ok {
		return limit
	}
	return t.BatchSizeLimitMap["*"]
}

// MergeMethod returns the merge method to use for a repo. The default of merge is
// returned when not overridden.
func (t *Tide) MergeMethod(repo OrgRepo) types.PullRequestMergeType {
	return t.OrgRepoBranchMergeMethod(repo, "")
}

// OrgRepoBranchMergeMethod returns the merge method to use for a given triple: org, repo, branch.
// The following matching criteria apply, the priority goes from the highest to the lowest:
//
//  1. kubernetes/test-infra@main: rebase       org/repo@branch shorthand
//
//  2. kubernetes:
//     test-infra:
//     ma(ster|in): rebase                  branch level regex
//
//  3. kubernetes/test-infra: rebase            org/repo shorthand
//
//  4. kubernetes:
//     test-infra: rebase                     repo-wide config
//
//  5. kubernetes: rebase                       org shorthand
//
//  6. default to "merge"
func (t *Tide) OrgRepoBranchMergeMethod(orgRepo OrgRepo, branch string) types.PullRequestMergeType {
	isOrgSet, isRepoSet, isBranchSet := orgRepo.Org != "", orgRepo.Repo != "", branch != ""
	var orgFound, repoFound bool

	// The repository to look for can either be provided as an input or the "*" wildcard
	repo := orgRepo.Repo

	// Check if the org exists
	if isOrgSet {
		_, orgFound = t.MergeType[orgRepo.Org]
	}

	// Check if the repo exists
	if isOrgSet && isRepoSet && orgFound {
		_, repoFound = t.MergeType[orgRepo.Org].Repos[orgRepo.Repo]
		_, wildcardRepoFound := t.MergeType[orgRepo.Org].Repos["*"]
		if !repoFound && wildcardRepoFound {
			repoFound = true
			repo = "*"
		}
	}

	// 1. "$org/$repo@$branch" shorthand
	if isOrgSet && isRepoSet && isBranchSet {
		orgRepoBranchShorthand := fmt.Sprintf("%s/%s@%s", orgRepo.Org, orgRepo.Repo, branch)
		if orgRepoBranch, found := t.MergeType[orgRepoBranchShorthand]; found && orgRepoBranch.MergeType != "" {
			return orgRepoBranch.MergeType
		}
	}

	// 2. Branch level regex match
	if orgFound && repoFound {
		branches := t.MergeType[orgRepo.Org].Repos[repo].Branches
		keys := make([]string, 0, len(branches))

		for k := range branches {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			branchConfig := branches[key]
			if branchConfig.Regexpr.MatchString(branch) {
				return branchConfig.MergeType
			}
		}
	}

	// 3. "$org/$repo" shorthand
	if isOrgSet && isRepoSet {
		orgRepoShorthand := fmt.Sprintf("%s/%s", orgRepo.Org, orgRepo.Repo)
		if orgRepo, found := t.MergeType[orgRepoShorthand]; found && orgRepo.MergeType != "" {
			return orgRepo.MergeType
		}
	}

	// 4. Repo-wide match
	if orgFound && repoFound {
		if t.MergeType[orgRepo.Org].Repos[repo].MergeType != "" {
			return t.MergeType[orgRepo.Org].Repos[repo].MergeType
		}
	}

	// 5. "$org" shorthand
	if orgFound {
		if t.MergeType[orgRepo.Org].MergeType != "" {
			return t.MergeType[orgRepo.Org].MergeType
		}
	}

	// 6. Default
	return types.MergeMerge
}

// MergeCommitTemplate returns a struct with Go template string(s) or nil
func (t *Tide) MergeCommitTemplate(repo OrgRepo) TideMergeCommitTemplate {
	v, ok := t.MergeTemplate[repo.String()]
	if !ok {
		return t.MergeTemplate[repo.Org]
	}

	return v
}

func (t *Tide) GetPRStatusBaseURL(repo OrgRepo) string {
	if byOrgRepo, ok := t.PRStatusBaseURLs[repo.String()]; ok {
		return byOrgRepo
	}
	if byOrg, ok := t.PRStatusBaseURLs[repo.Org]; ok {
		return byOrg
	}

	return t.PRStatusBaseURLs["*"]
}

func (t *Tide) GetTargetURL(repo OrgRepo) string {
	if byOrgRepo, ok := t.TargetURLs[repo.String()]; ok {
		return byOrgRepo
	}
	if byOrg, ok := t.TargetURLs[repo.Org]; ok {
		return byOrg
	}

	return t.TargetURLs["*"]
}

// TideQuery is turned into a GitHub search query. See the docs for details:
// https://help.github.com/articles/searching-issues-and-pull-requests/
type TideQuery struct {
	Author string `json:"author,omitempty"`

	Labels        []string `json:"labels,omitempty"`
	MissingLabels []string `json:"missingLabels,omitempty"`

	ExcludedBranches []string `json:"excludedBranches,omitempty"`
	IncludedBranches []string `json:"includedBranches,omitempty"`

	Milestone string `json:"milestone,omitempty"`

	ReviewApprovedRequired bool `json:"reviewApprovedRequired,omitempty"`

	Orgs          []string `json:"orgs,omitempty"`
	Repos         []string `json:"repos,omitempty"`
	ExcludedRepos []string `json:"excludedRepos,omitempty"`
}

func (q TideQuery) TenantIDs(cfg Config) []string {
	res := sets.Set[string]{}
	for _, org := range q.Orgs {
		res.Insert(cfg.GetProwJobDefault(org, "*").TenantID)
	}
	for _, repo := range q.Repos {
		res.Insert(cfg.GetProwJobDefault(repo, "*").TenantID)
	}
	return sets.List(res)
}

// tideQueryConfig contains the subset of attributes by which we de-duplicate
// tide queries. Together with tideQueryTarget it must contain the full set
// of all TideQuery properties.
type tideQueryConfig struct {
	Author                 string
	ExcludedBranches       []string
	IncludedBranches       []string
	Labels                 []string
	MissingLabels          []string
	Milestone              string
	ReviewApprovedRequired bool
	TenantIDs              []string
}

type tideQueryTarget struct {
	Orgs          []string
	Repos         []string
	ExcludedRepos []string
}

// constructQuery returns a map[org][]orgSpecificQueryParts (org, repo, -repo), remainingQueryString
func (tq *TideQuery) constructQuery() (map[string][]string, string) {
	// map org->repo directives (if any)
	orgScopedIdentifiers := map[string][]string{}
	for _, o := range tq.Orgs {
		if _, ok := orgScopedIdentifiers[o]; !ok {
			orgScopedIdentifiers[o] = []string{fmt.Sprintf(`org:"%s"`, o)}
		}
	}
	for _, r := range tq.Repos {
		if org, _, ok := splitOrgRepoString(r); ok {
			orgScopedIdentifiers[org] = append(orgScopedIdentifiers[org], fmt.Sprintf("repo:\"%s\"", r))
		}
	}

	for _, r := range tq.ExcludedRepos {
		if org, _, ok := splitOrgRepoString(r); ok {
			orgScopedIdentifiers[org] = append(orgScopedIdentifiers[org], fmt.Sprintf("-repo:\"%s\"", r))
		}
	}

	queryString := []string{"is:pr", "state:open", "archived:false"}
	if tq.Author != "" {
		queryString = append(queryString, fmt.Sprintf("author:\"%s\"", tq.Author))
	}
	for _, b := range tq.ExcludedBranches {
		queryString = append(queryString, fmt.Sprintf("-base:\"%s\"", b))
	}
	for _, b := range tq.IncludedBranches {
		queryString = append(queryString, fmt.Sprintf("base:\"%s\"", b))
	}
	for _, l := range tq.Labels {
		var orOperands []string
		for _, alt := range strings.Split(l, ",") {
			orOperands = append(orOperands, fmt.Sprintf("\"%s\"", alt))
		}
		queryString = append(queryString, fmt.Sprintf("label:%s", strings.Join(orOperands, ",")))
	}
	for _, l := range tq.MissingLabels {
		queryString = append(queryString, fmt.Sprintf("-label:\"%s\"", l))
	}
	if tq.Milestone != "" {
		queryString = append(queryString, fmt.Sprintf("milestone:\"%s\"", tq.Milestone))
	}
	if tq.ReviewApprovedRequired {
		queryString = append(queryString, "review:approved")
	}

	return orgScopedIdentifiers, strings.Join(queryString, " ")
}

func splitOrgRepoString(orgRepo string) (string, string, bool) {
	split := strings.Split(orgRepo, "/")
	if len(split) != 2 {
		// Just do it like the github search itself and ignore invalid orgRepo identifiers
		return "", "", false
	}
	return split[0], split[1], true
}

// OrgQueries returns the GitHub search string for the query, sharded
// by org.
func (tq *TideQuery) OrgQueries() map[string]string {
	orgRepoIdentifiers, queryString := tq.constructQuery()
	result := map[string]string{}
	for org, repoIdentifiers := range orgRepoIdentifiers {
		result[org] = queryString + " " + strings.Join(repoIdentifiers, " ")
	}

	return result
}

// Query returns the corresponding github search string for the tide query.
func (tq *TideQuery) Query() string {
	orgRepoIdentifiers, queryString := tq.constructQuery()
	toks := []string{queryString}
	for _, repoIdentifiers := range orgRepoIdentifiers {
		toks = append(toks, repoIdentifiers...)
	}
	return strings.Join(toks, " ")
}

// ForRepo indicates if the tide query applies to the specified repo.
func (tq TideQuery) ForRepo(repo OrgRepo) bool {
	for _, queryOrg := range tq.Orgs {
		if queryOrg != repo.Org {
			continue
		}
		// Check for repos excluded from the org.
		for _, excludedRepo := range tq.ExcludedRepos {
			if excludedRepo == repo.String() {
				return false
			}
		}
		return true
	}
	for _, queryRepo := range tq.Repos {
		if queryRepo == repo.String() {
			return true
		}
	}
	return false
}

func reposInOrg(org string, repos []string) []string {
	prefix := org + "/"
	var res []string
	for _, repo := range repos {
		if strings.HasPrefix(repo, prefix) {
			res = append(res, repo)
		}
	}
	return res
}

// OrgExceptionsAndRepos determines which orgs and repos a set of queries cover.
// Output is returned as a mapping from 'included org'->'repos excluded in the org'
// and a set of included repos.
func (tqs TideQueries) OrgExceptionsAndRepos() (map[string]sets.Set[string], sets.Set[string]) {
	orgs := make(map[string]sets.Set[string])
	for i := range tqs {
		for _, org := range tqs[i].Orgs {
			applicableRepos := sets.New[string](reposInOrg(org, tqs[i].ExcludedRepos)...)
			if excepts, ok := orgs[org]; !ok {
				// We have not seen this org so the exceptions are just applicable
				// members of 'excludedRepos'.
				orgs[org] = applicableRepos
			} else {
				// We have seen this org so the exceptions are the applicable
				// members of 'excludedRepos' intersected with existing exceptions.
				orgs[org] = excepts.Intersection(applicableRepos)
			}
		}
	}
	repos := sets.New[string]()
	for i := range tqs {
		repos.Insert(tqs[i].Repos...)
	}
	// Remove any org exceptions that are explicitly included in a different query.
	reposList := repos.UnsortedList()
	for _, excepts := range orgs {
		excepts.Delete(reposList...)
	}
	return orgs, repos
}

// QueryMap is a struct mapping from "org/repo" -> TideQueries that
// apply to that org or repo. It is lazily populated, but threadsafe.
type QueryMap struct {
	queries TideQueries

	cache map[string]TideQueries
	sync.Mutex
}

// QueryMap creates a QueryMap from TideQueries
func (tqs TideQueries) QueryMap() *QueryMap {
	return &QueryMap{
		queries: tqs,
		cache:   make(map[string]TideQueries),
	}
}

// ForRepo returns the tide queries that apply to a repo.
func (qm *QueryMap) ForRepo(repo OrgRepo) TideQueries {
	res := TideQueries(nil)

	qm.Lock()
	defer qm.Unlock()

	if qs, ok := qm.cache[repo.String()]; ok {
		return append(res, qs...) // Return a copy.
	}
	// Cache miss. Need to determine relevant queries.

	for _, query := range qm.queries {
		if query.ForRepo(repo) {
			res = append(res, query)
		}
	}
	qm.cache[repo.String()] = res
	return res
}

// Validate returns an error if the query has any errors.
//
// Examples include:
// * an org name that is empty or includes a /
// * repos that are not org/repo
// * a label that is in both the labels and missing_labels section
// * a branch that is in both included and excluded branch set.
func (tq *TideQuery) Validate() error {
	duplicates := func(field string, list []string) error {
		dups := sets.New[string]()
		seen := sets.New[string]()
		for _, elem := range list {
			if seen.Has(elem) {
				dups.Insert(elem)
			} else {
				seen.Insert(elem)
			}
		}
		dupCount := len(list) - seen.Len()
		if dupCount == 0 {
			return nil
		}
		return fmt.Errorf("%q contains %d duplicate entries: %s", field, dupCount, strings.Join(sets.List(dups), ", "))
	}

	orgs := sets.New[string]()
	for o := range tq.Orgs {
		if strings.Contains(tq.Orgs[o], "/") {
			return fmt.Errorf("orgs[%d]: %q contains a '/' which is not valid", o, tq.Orgs[o])
		}
		if len(tq.Orgs[o]) == 0 {
			return fmt.Errorf("orgs[%d]: is an empty string", o)
		}
		orgs.Insert(tq.Orgs[o])
	}
	if err := duplicates("orgs", tq.Orgs); err != nil {
		return err
	}

	for r := range tq.Repos {
		parts := strings.Split(tq.Repos[r], "/")
		if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
			return fmt.Errorf("repos[%d]: %q is not of the form \"org/repo\"", r, tq.Repos[r])
		}
		if orgs.Has(parts[0]) {
			return fmt.Errorf("repos[%d]: %q is already included via org: %q", r, tq.Repos[r], parts[0])
		}
	}
	if err := duplicates("repos", tq.Repos); err != nil {
		return err
	}

	if len(tq.Orgs) == 0 && len(tq.Repos) == 0 {
		return errors.New("'orgs' and 'repos' cannot both be empty")
	}

	for er := range tq.ExcludedRepos {
		parts := strings.Split(tq.ExcludedRepos[er], "/")
		if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
			return fmt.Errorf("excludedRepos[%d]: %q is not of the form \"org/repo\"", er, tq.ExcludedRepos[er])
		}
		if !orgs.Has(parts[0]) {
			return fmt.Errorf("excludedRepos[%d]: %q has no effect because org %q is not included", er, tq.ExcludedRepos[er], parts[0])
		}
		// Note: At this point we also know that this excludedRepo is not found in 'repos'.
	}
	if err := duplicates("excludedRepos", tq.ExcludedRepos); err != nil {
		return err
	}

	if invalids := sets.New[string](tq.Labels...).Intersection(sets.New[string](tq.MissingLabels...)); len(invalids) > 0 {
		return fmt.Errorf("the labels: %q are both required and forbidden", sets.List(invalids))
	}
	if err := duplicates("labels", tq.Labels); err != nil {
		return err
	}
	if err := duplicates("missingLabels", tq.MissingLabels); err != nil {
		return err
	}

	if len(tq.ExcludedBranches) > 0 && len(tq.IncludedBranches) > 0 {
		return errors.New("both 'includedBranches' and 'excludedBranches' are specified ('excludedBranches' have no effect)")
	}
	if err := duplicates("includedBranches", tq.IncludedBranches); err != nil {
		return err
	}
	if err := duplicates("excludedBranches", tq.ExcludedBranches); err != nil {
		return err
	}

	return nil
}

// Validate returns an error if any contexts are listed more than once in the config.
func (cp *TideContextPolicy) Validate() error {
	if inter := sets.New[string](cp.RequiredContexts...).Intersection(sets.New[string](cp.OptionalContexts...)); inter.Len() > 0 {
		return fmt.Errorf("contexts %s are defined as required and optional", strings.Join(sets.List(inter), ", "))
	}
	if inter := sets.New[string](cp.RequiredContexts...).Intersection(sets.New[string](cp.RequiredIfPresentContexts...)); inter.Len() > 0 {
		return fmt.Errorf("contexts %s are defined as required and required if present", strings.Join(sets.List(inter), ", "))
	}
	if inter := sets.New[string](cp.OptionalContexts...).Intersection(sets.New[string](cp.RequiredIfPresentContexts...)); inter.Len() > 0 {
		return fmt.Errorf("contexts %s are defined as optional and required if present", strings.Join(sets.List(inter), ", "))
	}
	return nil
}

func mergeTideContextPolicy(a, b TideContextPolicy) TideContextPolicy {
	mergeBool := func(a, b *bool) *bool {
		if b == nil {
			return a
		}
		return b
	}
	c := TideContextPolicy{}
	c.FromBranchProtection = mergeBool(a.FromBranchProtection, b.FromBranchProtection)
	c.SkipUnknownContexts = mergeBool(a.SkipUnknownContexts, b.SkipUnknownContexts)
	required := sets.New[string](a.RequiredContexts...)
	requiredIfPresent := sets.New[string](a.RequiredIfPresentContexts...)
	optional := sets.New[string](a.OptionalContexts...)
	required.Insert(b.RequiredContexts...)
	requiredIfPresent.Insert(b.RequiredIfPresentContexts...)
	optional.Insert(b.OptionalContexts...)
	if required.Len() > 0 {
		c.RequiredContexts = sets.List(required)
	}
	if requiredIfPresent.Len() > 0 {
		c.RequiredIfPresentContexts = sets.List(requiredIfPresent)
	}
	if optional.Len() > 0 {
		c.OptionalContexts = sets.List(optional)
	}
	return c
}

func parseTideContextPolicyOptions(org, repo, branch string, options TideContextPolicyOptions) TideContextPolicy {
	option := options.TideContextPolicy
	if o, ok := options.Orgs[org]; ok {
		option = mergeTideContextPolicy(option, o.TideContextPolicy)
		if r, ok := o.Repos[repo]; ok {
			option = mergeTideContextPolicy(option, r.TideContextPolicy)
			if b, ok := r.Branches[branch]; ok {
				option = mergeTideContextPolicy(option, b)
			}
		}
	}
	return option
}

// GetTideContextPolicy parses the prow config to find context merge options.
// If none are set, it will use the prow jobs configured and use the default github combined status.
// Otherwise if set it will use the branch protection setting, or the listed jobs.
func (c Config) GetTideContextPolicy(gitClient git.ClientFactory, org, repo, branch string, baseSHAGetter RefGetter, headSHA string) (*TideContextPolicy, error) {
	var requireManuallyTriggeredJobs *bool
	options := parseTideContextPolicyOptions(org, repo, branch, c.Tide.ContextOptions)
	// Adding required and optional contexts from options
	required := sets.New[string](options.RequiredContexts...)
	requiredIfPresent := sets.New[string](options.RequiredIfPresentContexts...)
	optional := sets.New[string](options.OptionalContexts...)

	headSHAGetter := func() (string, error) {
		return headSHA, nil
	}
	presubmits, err := c.GetPresubmits(gitClient, org+"/"+repo, branch, baseSHAGetter, headSHAGetter)
	if err != nil {
		return nil, fmt.Errorf("failed to get presubmits: %w", err)
	}

	// Using Branch protection configuration
	if options.FromBranchProtection != nil && *options.FromBranchProtection {
		bp, err := c.GetBranchProtection(org, repo, branch, presubmits)
		if err != nil {
			logrus.WithError(err).Warningf("Error getting branch protection for %s/%s+%s", org, repo, branch)
		} else if bp != nil {
			requireManuallyTriggeredJobs = bp.RequireManuallyTriggeredJobs
			if bp.Protect != nil && *bp.Protect && bp.RequiredStatusChecks != nil {
				required.Insert(bp.RequiredStatusChecks.Contexts...)
			}
		}
	}

	// automatically generate required and optional entries for Prow Jobs
	prowRequired, prowRequiredIfPresent, prowOptional := BranchRequirements(branch, presubmits, requireManuallyTriggeredJobs)
	required.Insert(prowRequired...)
	requiredIfPresent.Insert(prowRequiredIfPresent...)
	optional.Insert(prowOptional...)

	t := &TideContextPolicy{
		RequiredContexts:          sets.List(required),
		RequiredIfPresentContexts: sets.List(requiredIfPresent),
		OptionalContexts:          sets.List(optional),
		SkipUnknownContexts:       options.SkipUnknownContexts,
	}
	if err := t.Validate(); err != nil {
		return t, err
	}
	return t, nil
}

// IsOptional checks whether a context can be ignored.
// Will return true if
// - context is registered as optional
// - required contexts are registered and the context provided is not required
// Will return false otherwise. Every context is required.
func (cp *TideContextPolicy) IsOptional(c string) bool {
	if sets.New[string](cp.OptionalContexts...).Has(c) {
		return true
	}
	if sets.New[string](cp.RequiredContexts...).Has(c) {
		return false
	}
	// assume if we're asking that the context is present on the PR
	if sets.New[string](cp.RequiredIfPresentContexts...).Has(c) {
		return false
	}
	if cp.SkipUnknownContexts != nil && *cp.SkipUnknownContexts {
		return true
	}
	return false
}

// MissingRequiredContexts discard the optional contexts and only look of extra required contexts that are not provided.
func (cp *TideContextPolicy) MissingRequiredContexts(contexts []string) []string {
	if len(cp.RequiredContexts) == 0 {
		return nil
	}
	existingContexts := sets.New[string]()
	for _, c := range contexts {
		existingContexts.Insert(c)
	}
	var missingContexts []string
	for c := range sets.New[string](cp.RequiredContexts...).Difference(existingContexts) {
		missingContexts = append(missingContexts, c)
	}
	return missingContexts
}
