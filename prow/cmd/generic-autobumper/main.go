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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	flag "github.com/spf13/pflag"
	"golang.org/x/oauth2/google"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"
	"k8s.io/test-infra/prow/cmd/generic-autobumper/imagebumper"

	"sigs.k8s.io/yaml"
)

const (
	latestVersion           = "latest"
	upstreamVersion         = "upstream"
	upstreamStagingVersion  = "upstream-staging"
	tagVersion              = "vYYYYMMDD-deadbeef"
	defaultUpstreamURLBase  = "https://raw.githubusercontent.com/kubernetes/test-infra/master"
	googleImageRegistryAuth = "google"
	cloudPlatformScope      = "https://www.googleapis.com/auth/cloud-platform"

	defaultOncallGroup = "testinfra"
	errOncallMsgTempl  = "An error occurred while finding an assignee: `%s`.\nFalling back to Blunderbuss."
	noOncallMsg        = "Nobody is currently oncall, so falling back to Blunderbuss."
)

var (
	tagRegexp    = regexp.MustCompile("v[0-9]{8}-[a-f0-9]{6,9}")
	imageMatcher = regexp.MustCompile(`(?s)^.+image:(.+):(v[a-zA-Z0-9_.-]+)`)
)

var _ bumper.PRHandler = (*client)(nil)

type client struct {
	o        *options
	images   map[string]string
	versions map[string][]string
}

// Changes returns a slice of functions, each one does some stuff, and
// returns commit message for the changes
func (c *client) Changes() []func(context.Context) (string, error) {
	return []func(context.Context) (string, error){
		func(ctx context.Context) (string, error) {
			var err error
			if c.images, err = updateReferencesWrapper(ctx, c.o); err != nil {
				return "", fmt.Errorf("failed to update image references: %w", err)
			}

			if c.versions, err = getVersionsAndCheckConsistency(c.o.Prefixes, c.images); err != nil {
				return "", err
			}

			var body string
			var prefixNames []string
			for _, prefix := range c.o.Prefixes {
				prefixNames = append(prefixNames, prefix.Name)
				body = body + generateSummary(prefix.Name, prefix.Repo, prefix.Prefix, prefix.Summarise, c.images) + "\n\n"
			}

			return fmt.Sprintf("Bumping %s\n\n%s", strings.Join(prefixNames, " and "), body), nil
		},
	}
}

// PRTitleBody returns the body of the PR, this function runs after each commit
func (c *client) PRTitleBody() (string, string) {
	body := generatePRBody(c.images, c.o.Prefixes) +
		getAssignment(c.o.OncallAddress, c.o.OncallGroup, c.o.SkipOncallAssignment, c.o.SelfAssign) + "\n"
	if c.o.AdditionalPRBody != "" {
		body += c.o.AdditionalPRBody + "\n"
	}
	return makeCommitSummary(c.o.Prefixes, c.versions), body
}

func generatePRBody(images map[string]string, prefixes []prefix) (body string) {
	body = ""
	for _, prefix := range prefixes {
		body = body + generateSummary(prefix.Name, prefix.Repo, prefix.Prefix, prefix.Summarise, images) + "\n\n"
	}
	return body + "\n"
}

// options is the options for autobumper operations.
type options struct {
	// The URL where upstream image references are located. Only required if Target Version is "upstream" or "upstreamStaging". Use "https://raw.githubusercontent.com/{ORG}/{REPO}"
	// Images will be bumped based off images located at the address using this URL and the refConfigFile or stagingRefConigFile for each Prefix.
	UpstreamURLBase string `yaml:"upstreamURLBase"`
	// The config paths to be included in this bump, in which only .yaml files will be considered. By default all files are included.
	IncludedConfigPaths []string `yaml:"includedConfigPaths"`
	// The config paths to be excluded in this bump, in which only .yaml files will be considered.
	ExcludedConfigPaths []string `yaml:"excludedConfigPaths"`
	// The extra non-yaml file to be considered in this bump.
	ExtraFiles []string `yaml:"extraFiles"`
	// The target version to bump images version to, which can be one of latest, upstream, upstream-staging and vYYYYMMDD-deadbeef.
	TargetVersion string `yaml:"targetVersion"`
	// List of prefixes that the autobumped is looking for, and other information needed to bump them. Must have at least 1 prefix.
	Prefixes []prefix `yaml:"prefixes"`
	// The oncall address where we can get the JSON file that stores the current oncall information.
	OncallAddress string `json:"onCallAddress"`
	// The oncall group that is responsible for reviewing the change, i.e. "test-infra".
	OncallGroup string `json:"onCallGroup"`
	// Whether skip if no oncall is discovered
	SkipIfNoOncall bool `yaml:"skipIfNoOncall"`
	// SkipOncallAssignment skips assigning to oncall.
	// The OncallAddress and OncallGroup are required for auto-bumper to figure out whether there are active oncall,
	// which is used to avoid bumping when there is no active oncall.
	SkipOncallAssignment bool `yaml:"skipOncallAssignment"`
	// SelfAssign is used to comment `/assign` and `/cc` so that blunderbuss wouldn't assign
	// bump PR to someone else.
	SelfAssign bool `yaml:"selfAssign"`
	// ImageRegistryAuth determines a way the autobumper with authenticate when talking to image registry.
	// Allowed values:
	// * "" (empty) -- uses no auth token
	// * "google" -- uses Google's "Application Default Credentials" as defined on https://pkg.go.dev/golang.org/x/oauth2/google#hdr-Credentials.
	ImageRegistryAuth string `yaml:"imageRegistryAuth"`
	// AdditionalPRBody allows for generic, additional content in the body of the PR
	AdditionalPRBody string `yaml:"additionalPRBody"`
}

// prefix is the information needed for each prefix being bumped.
type prefix struct {
	// Name of the tool being bumped
	Name string `yaml:"name"`
	// The image prefix that the autobumper should look for
	Prefix string `yaml:"prefix"`
	// File that is looked at to determine current upstream image when bumping to upstream. Required only if targetVersion is "upstream"
	RefConfigFile string `yaml:"refConfigFile"`
	// File that is looked at to determine current upstream staging image when bumping to upstream staging. Required only if targetVersion is "upstream-staging"
	StagingRefConfigFile string `yaml:"stagingRefConfigFile"`
	// The repo where the image source resides for the images with this prefix. Used to create the links to see comparisons between images in the PR summary.
	Repo string `yaml:"repo"`
	// Whether or not the format of the PR summary for this prefix should be summarised.
	Summarise bool `yaml:"summarise"`
	// Whether the prefix tags should be consistent after the bump
	ConsistentImages bool `yaml:"consistentImages"`
	// A list of images whose tags are not required to be consistent after the bump. Requires `consistentImages: true`.
	ConsistentImageExceptions []string `yaml:"consistentImageExceptions"`
}

func parseOptions() (*options, *bumper.Options, error) {
	var config string
	var labelsOverride []string
	var skipPullRequest bool
	var signoff bool

	var o options
	flag.StringVar(&config, "config", "", "The path to the config file for the autobumber.")
	flag.StringSliceVar(&labelsOverride, "labels-override", nil, "Override labels to be added to PR.")
	flag.BoolVar(&skipPullRequest, "skip-pullrequest", false, "")
	flag.BoolVar(&signoff, "signoff", false, "Signoff the commits.")
	flag.BoolVar(&o.SkipIfNoOncall, "skip-if-no-oncall", false, "Don't run anything if no oncall is discovered")
	flag.Parse()

	var pro bumper.Options
	data, err := os.ReadFile(config)
	if err != nil {
		return nil, nil, fmt.Errorf("read %q: %w", config, err)
	}

	if err = yaml.Unmarshal(data, &o); err != nil {
		return nil, nil, fmt.Errorf("unmarshal %q: %w", config, err)
	}

	if err := yaml.Unmarshal(data, &pro); err != nil {
		return nil, nil, fmt.Errorf("unmarshal %q: %w", config, err)
	}

	if labelsOverride != nil {
		pro.Labels = labelsOverride
	}
	if o.OncallGroup == "" {
		o.OncallGroup = defaultOncallGroup
	}
	pro.SkipPullRequest = skipPullRequest
	pro.Signoff = signoff
	return &o, &pro, nil
}

func validateOptions(o *options) error {
	if len(o.Prefixes) == 0 {
		return errors.New("must have at least one Prefix specified")
	}
	for _, prefix := range o.Prefixes {
		if len(prefix.ConsistentImageExceptions) > 0 && !prefix.ConsistentImages {
			return fmt.Errorf("consistentImageExceptions requires consistentImages to be true, found in prefix %q", prefix.Name)
		}
	}
	if len(o.IncludedConfigPaths) == 0 {
		return errors.New("includedConfigPaths is mandatory")
	}
	if o.TargetVersion != latestVersion && o.TargetVersion != upstreamVersion &&
		o.TargetVersion != upstreamStagingVersion && !tagRegexp.MatchString(o.TargetVersion) {
		logrus.WithField("allowed", []string{latestVersion, upstreamVersion, upstreamStagingVersion, tagVersion}).Warn(
			"Warning: targetVersion mot in allowed so it might not work properly.")
	}
	if o.TargetVersion == upstreamVersion {
		for _, prefix := range o.Prefixes {
			if prefix.RefConfigFile == "" {
				return fmt.Errorf("targetVersion can't be %q without refConfigFile for each prefix. %q is missing one", upstreamVersion, prefix.Name)
			}
		}
	}
	if o.TargetVersion == upstreamStagingVersion {
		for _, prefix := range o.Prefixes {
			if prefix.StagingRefConfigFile == "" {
				return fmt.Errorf("targetVersion can't be %q without stagingRefConfigFile for each prefix. %q is missing one", upstreamStagingVersion, prefix.Name)
			}
		}
	}
	if (o.TargetVersion == upstreamVersion || o.TargetVersion == upstreamStagingVersion) && o.UpstreamURLBase == "" {
		o.UpstreamURLBase = defaultUpstreamURLBase
		logrus.Warnf("targetVersion can't be 'upstream' or 'upstreamStaging` without upstreamURLBase set. Default upstreamURLBase is %q", defaultUpstreamURLBase)
	}

	if o.ImageRegistryAuth != "" && o.ImageRegistryAuth != googleImageRegistryAuth {
		return fmt.Errorf("imageRegistryAuth has incorrect value: %q. Only \"\" and %q are allowed", o.ImageRegistryAuth, googleImageRegistryAuth)
	}

	return nil
}

func isOncallActive(oncallAddress, oncallGroup string) bool {
	_, oncallActive, _ := getOncallInfo(oncallAddress, oncallGroup)
	return oncallActive
}

func getAssignment(oncallAddress, oncallGroup string, skipOncallAssignment, selfAssign bool) string {
	// No reason to self assign if wants to assign to oncall
	if selfAssign {
		return "/cc"
	}
	if skipOncallAssignment {
		return ""
	}
	// Processing oncall info now
	curtOncall, _, err := getOncallInfo(oncallAddress, oncallGroup)
	if err != nil {
		return fmt.Sprintf(errOncallMsgTempl, err.Error())
	}
	if curtOncall == "" {
		return noOncallMsg
	}
	return curtOncall
}

func getOncallInfo(oncallAddress, oncallGroup string) (string, bool, error) {
	if oncallAddress == "" {
		return "", false, nil
	}

	req, err := http.Get(oncallAddress)
	if err != nil {
		return "", false, err
	}
	defer req.Body.Close()
	if req.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("requesting oncall address: HTTP error %d: %q", req.StatusCode, req.Status)
	}
	oncall := struct {
		Oncall map[string]string `json:"Oncall"`
		Active map[string]bool   `json:"Active"`
	}{}
	if err := json.NewDecoder(req.Body).Decode(&oncall); err != nil {
		return "", false, err
	}
	curtOncall, ok := oncall.Oncall[oncallGroup]
	if !ok {
		return "", false, fmt.Errorf("oncall map doesn't contain group '%s'", oncallGroup)
	}
	oncallActive, ok := oncall.Active[oncallGroup]
	if !ok {
		return "", false, fmt.Errorf("oncall map doesn't contain group '%s'", oncallGroup)
	}
	if curtOncall != "" {
		return "/cc @" + curtOncall, oncallActive, nil
	}
	return "", false, nil
}

// updateReferencesWrapper update the references of prow-images and/or boskos-images and/or testimages
// in the files in any of "subfolders" of the includeConfigPaths but not in excludeConfigPaths
// if the file is a yaml file (*.yaml) or extraFiles[file]=true
func updateReferencesWrapper(ctx context.Context, o *options) (map[string]string, error) {
	logrus.Info("Bumping image references...")
	var allPrefixes []string
	for _, prefix := range o.Prefixes {
		allPrefixes = append(allPrefixes, prefix.Prefix)
	}
	filterRegexp, err := regexp.Compile(strings.Join(allPrefixes, "|"))
	if err != nil {
		return nil, fmt.Errorf("bad regexp %q: %w", strings.Join(allPrefixes, "|"), err)
	}
	var client *http.Client = http.DefaultClient
	if o.ImageRegistryAuth == googleImageRegistryAuth {
		var err error
		client, err = google.DefaultClient(ctx, cloudPlatformScope)
		if err != nil {
			return nil, fmt.Errorf("failed to create authed client: %v", err)
		}
	}
	imageBumperCli := imagebumper.NewClient(client)
	return updateReferences(imageBumperCli, filterRegexp, o)
}

type imageBumper interface {
	FindLatestTag(imageHost, imageName, currentTag string) (string, error)
	UpdateFile(tagPicker func(imageHost, imageName, currentTag string) (string, error), path string, imageFilter *regexp.Regexp) error
	GetReplacements() map[string]string
	AddToCache(image, newTag string)
	TagExists(imageHost, imageName, currentTag string) (bool, error)
}

func updateReferences(imageBumperCli imageBumper, filterRegexp *regexp.Regexp, o *options) (map[string]string, error) {
	var tagPicker func(string, string, string) (string, error)

	switch o.TargetVersion {
	case latestVersion:
		tagPicker = imageBumperCli.FindLatestTag
	case upstreamVersion, upstreamStagingVersion:
		var err error
		if tagPicker, err = upstreamImageVersionResolver(o, o.TargetVersion, parseUpstreamImageVersion, imageBumperCli); err != nil {
			return nil, fmt.Errorf("failed to resolve the %s image version: %w", o.TargetVersion, err)
		}
	default:
		tagPicker = func(imageHost, imageName, currentTag string) (string, error) { return o.TargetVersion, nil }
	}

	updateFile := func(name string) error {
		logrus.WithField("file", name).Info("Updating file")
		if err := imageBumperCli.UpdateFile(tagPicker, name, filterRegexp); err != nil {
			return fmt.Errorf("failed to update the file: %w", err)
		}
		return nil
	}
	updateYAMLFile := func(name string) error {
		if strings.HasSuffix(name, ".yaml") && !isUnderPath(name, o.ExcludedConfigPaths) {
			return updateFile(name)
		}
		return nil
	}

	// Updated all .yaml files under the included config paths but not under excluded config paths.
	for _, path := range o.IncludedConfigPaths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("failed to get the file info for %q: %w", path, err)
		}
		if info.IsDir() {
			err := filepath.Walk(path, func(subpath string, info os.FileInfo, err error) error {
				return updateYAMLFile(subpath)
			})
			if err != nil {
				return nil, fmt.Errorf("failed to update yaml files under %q: %w", path, err)
			}
		} else {
			if err := updateYAMLFile(path); err != nil {
				return nil, fmt.Errorf("failed to update the yaml file %q: %w", path, err)
			}
		}
	}

	// Update the extra files in any case.
	for _, file := range o.ExtraFiles {
		if err := updateFile(file); err != nil {
			return nil, fmt.Errorf("failed to update the extra file %q: %w", file, err)
		}
	}

	return imageBumperCli.GetReplacements(), nil
}

// used by updateReferences
func upstreamImageVersionResolver(
	o *options, upstreamVersionType string, parse func(upstreamAddress, prefix string) (string, error), imageBumperCli imageBumper) (func(imageHost, imageName, currentTag string) (string, error), error) {
	upstreamVersions, err := upstreamConfigVersions(upstreamVersionType, o, parse)
	if err != nil {
		return nil, err
	}

	return func(imageHost, imageName, currentTag string) (string, error) {
		imageFullPath := imageHost + "/" + imageName + ":" + currentTag
		for prefix, version := range upstreamVersions {
			if !strings.HasPrefix(imageFullPath, prefix) {
				continue
			}
			if exists, err := imageBumperCli.TagExists(imageHost, imageName, version); err != nil {
				return "", err
			} else if exists {
				imageBumperCli.AddToCache(imageFullPath, version)
				return version, nil
			}
			imageBumperCli.AddToCache(imageFullPath, currentTag)
			return "", fmt.Errorf("Unable to bump to %s, image tag %s does not exist for %s", imageFullPath, version, imageName)
		}
		return currentTag, nil
	}, nil
}

// used by upstreamImageVersionResolver
func upstreamConfigVersions(upstreamVersionType string, o *options, parse func(upstreamAddress, prefix string) (string, error)) (versions map[string]string, err error) {
	versions = make(map[string]string)
	var upstreamAddress string
	for _, prefix := range o.Prefixes {
		if upstreamVersionType == upstreamVersion {
			upstreamAddress = o.UpstreamURLBase + "/" + prefix.RefConfigFile
		} else if upstreamVersionType == upstreamStagingVersion {
			upstreamAddress = o.UpstreamURLBase + "/" + prefix.StagingRefConfigFile
		} else {
			return nil, fmt.Errorf("unsupported upstream version type: %s, must be one of %v",
				upstreamVersionType, []string{upstreamVersion, upstreamStagingVersion})
		}
		version, err := parse(upstreamAddress, prefix.Prefix)
		if err != nil {
			return nil, err
		}
		versions[prefix.Prefix] = version
	}

	return versions, nil
}

// used by updateReferences
func parseUpstreamImageVersion(upstreamAddress, prefix string) (string, error) {
	resp, err := http.Get(upstreamAddress)
	if err != nil {
		return "", fmt.Errorf("error sending GET request to %q: %w", upstreamAddress, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP error %d (%q) fetching upstream config file", resp.StatusCode, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading the response body: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
		res := imageMatcher.FindStringSubmatch(string(line))
		if len(res) > 2 && strings.Contains(res[1], prefix) {
			return res[2], nil
		}
	}
	return "", fmt.Errorf("unable to find match for %s in upstream refConfigFile", prefix)
}

// getVersionsAndCheckConisistency takes a list of Prefixes and a map of
// all the images found in the code before the bump : their versions after the bump
// For example {"gcr.io/k8s-prow/test1:tag": "newtag", "gcr.io/k8s-prow/test2:tag": "newtag"},
// and returns a map of new versions resulted from bumping : the images using those versions.
// It will error if one of the Prefixes was bumped inconsistently when it was not supposed to
func getVersionsAndCheckConsistency(prefixes []prefix, images map[string]string) (map[string][]string, error) {
	// Key is tag, value is full image.
	versions := map[string][]string{}
	consistencyChecker := map[string]string{}
	for _, prefix := range prefixes {
		exceptions := sets.NewString(prefix.ConsistentImageExceptions...)
		for k, v := range images {
			if strings.HasPrefix(k, prefix.Prefix) {
				image := imageFromName(k)
				found, ok := consistencyChecker[prefix.Prefix]
				if prefix.ConsistentImages && !exceptions.Has(image) {
					if ok && (found != v) {
						return nil, fmt.Errorf("%s:%s not bumped consistently for prefix %s(%s), expected version: %s", k, v, prefix.Prefix, prefix.Name, found)
					}
					if !ok {
						consistencyChecker[prefix.Prefix] = v
					}
				}

				//Only add bumped images to the new versions map
				if !strings.Contains(k, v) {
					versions[v] = append(versions[v], k)
				}

			}
		}
	}
	return versions, nil
}

// makeCommitSummary takes a list of Prefixes and a map of new tags resulted
// from bumping : the images using those tags and returns a summary of what was
// bumped for use in the commit message
func makeCommitSummary(prefixes []prefix, versions map[string][]string) string {
	var allPrefixes []string
	for _, prefix := range prefixes {
		allPrefixes = append(allPrefixes, prefix.Name)
	}
	if len(versions) == 0 {
		return fmt.Sprintf("Update %s images as necessary", strings.Join(allPrefixes, ", "))
	}
	var inconsistentBumps []string
	var consistentBumps []string
	for _, prefix := range prefixes {
		tag, bumped := isBumpedPrefix(prefix, versions)
		if !prefix.ConsistentImages && bumped {
			inconsistentBumps = append(inconsistentBumps, prefix.Name)
		} else if prefix.ConsistentImages && bumped {
			consistentBumps = append(consistentBumps, fmt.Sprintf("%s to %s", prefix.Name, tag))
		}
	}
	var msgs []string
	if len(consistentBumps) != 0 {
		msgs = append(msgs, strings.Join(consistentBumps, ", "))
	}
	if len(inconsistentBumps) != 0 {
		msgs = append(msgs, fmt.Sprintf("%s as needed", strings.Join(inconsistentBumps, ", ")))
	}
	return fmt.Sprintf("Update %s", strings.Join(msgs, " and "))

}

// Generate PR summary for github
func generateSummary(name, repo, prefix string, summarise bool, images map[string]string) string {
	type delta struct {
		oldCommit string
		newCommit string
		oldDate   string
		newDate   string
		variant   string
		component string
	}
	versions := map[string][]delta{}
	for image, newTag := range images {
		if !strings.HasPrefix(image, prefix) {
			continue
		}
		if strings.HasSuffix(image, ":"+newTag) {
			continue
		}
		oldDate, oldCommit, oldVariant := imagebumper.DeconstructTag(tagFromName(image))
		newDate, newCommit, _ := imagebumper.DeconstructTag(newTag)
		oldCommit = commitToRef(oldCommit)
		newCommit = commitToRef(newCommit)
		k := oldCommit + ":" + newCommit
		d := delta{
			oldCommit: oldCommit,
			newCommit: newCommit,
			oldDate:   oldDate,
			newDate:   newDate,
			variant:   formatVariant(oldVariant),
			component: componentFromName(image),
		}
		versions[k] = append(versions[k], d)
	}

	switch {
	case len(versions) == 0:
		return fmt.Sprintf("No %s changes.", prefix)
	case len(versions) == 1 && summarise:
		for k, v := range versions {
			s := strings.Split(k, ":")
			return fmt.Sprintf("%s changes: %s/compare/%s...%s (%s → %s)", prefix, repo, s[0], s[1], formatTagDate(v[0].oldDate), formatTagDate(v[0].newDate))
		}
	default:
		changes := make([]string, 0, len(versions))
		for k, v := range versions {
			s := strings.Split(k, ":")
			names := make([]string, 0, len(v))
			for _, d := range v {
				names = append(names, d.component+d.variant)
			}
			sort.Strings(names)
			changes = append(changes, fmt.Sprintf("%s/compare/%s...%s | %s&nbsp;&#x2192;&nbsp;%s | %s",
				repo, s[0], s[1], formatTagDate(v[0].oldDate), formatTagDate(v[0].newDate), strings.Join(names, ", ")))
		}
		sort.Slice(changes, func(i, j int) bool { return strings.Split(changes[i], "|")[1] < strings.Split(changes[j], "|")[1] })
		return fmt.Sprintf("Multiple distinct %s changes:\n\nCommits | Dates | Images\n--- | --- | ---\n%s\n", prefix, strings.Join(changes, "\n"))
	}
	panic("unreachable!")
}

func main() {
	ctx := context.Background()
	logrus.SetLevel(logrus.DebugLevel)
	o, pro, err := parseOptions()
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to run the bumper tool")
	}

	if o.SkipIfNoOncall {
		if !isOncallActive(o.OncallAddress, o.OncallGroup) {

			logrus.Info("`skip-if-no-oncall` is configured and there is no active oncall. Skip bumping.")
			return
		}
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatalf("Failed validating flags")
	}

	if err := bumper.Run(ctx, pro, &client{o: o}); err != nil {
		logrus.WithError(err).Fatalf("failed to run the bumper tool")
	}
}
