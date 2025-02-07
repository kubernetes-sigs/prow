package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/configuredjobs"
	"sigs.k8s.io/yaml"
)

// GetIndex returns the necessary information for the configured jobs index page, including all the potential orgs and repos
func GetIndex(jobConfig config.JobConfig) configuredjobs.Index {
	repos := jobConfig.AllRepos

	orgs := make(map[string]*configuredjobs.Org)
	for _, r := range sets.List(repos) {
		orgRepo := strings.Split(r, "/")
		org, repo := orgRepo[0], orgRepo[1]
		if org == "" || repo == "" {
			continue
		}
		existingOrg := orgs[org]
		if existingOrg != nil {
			existingOrg.Repos = append(existingOrg.Repos, repo)
			orgs[org] = existingOrg
		} else {
			orgs[org] = &configuredjobs.Org{
				Name:  org,
				Repos: []string{repo},
			}
		}
	}

	// Sort the orgs
	orgNames := make([]string, 0, len(orgs))
	for orgName := range orgs {
		orgNames = append(orgNames, orgName)
	}
	sort.Strings(orgNames)
	orgList := make([]configuredjobs.Org, 0, len(orgNames))
	for _, orgName := range orgNames {
		orgList = append(orgList, *orgs[orgName])
	}
	return configuredjobs.Index{Orgs: orgList}
}

// GetConfiguredJobs returns the information for the configured jobs page for a given repo, or the org if the repo is empty
func GetConfiguredJobs(cfg config.Getter, org, repo string) (*configuredjobs.JobsByRepo, error) {
	jobConfig := cfg().JobConfig
	configuredJobs := &configuredjobs.JobsByRepo{
		AllRepos: sets.List(jobConfig.AllRepos),
	}

	var orgRepos []string
	if repo != "" {
		orgRepos = []string{fmt.Sprintf("%s/%s", org, repo)}
	} else {
		for _, r := range sets.List(jobConfig.AllRepos) {
			o := strings.Split(r, "/")[0]
			if o == org {
				orgRepos = append(orgRepos, r)
			}
		}
	}

	// If there are more than 10 repos in the org, the page will be slow and not particularly useful,
	// Instead, just list the repos so they can be drilled down into
	includeJobs := len(orgRepos) <= 10

	for _, orgRepo := range orgRepos {
		r := strings.Split(orgRepo, "/")[1]
		cjRepo := configuredjobs.RepoInfo{
			Org:      configuredjobs.Org{Name: org},
			Name:     r,
			SafeName: safeName(r),
		}

		if includeJobs {
			presubmits := jobConfig.AllStaticPresubmits([]string{orgRepo})
			for _, presubmit := range presubmits {
				definition, err := yaml.Marshal(presubmit)
				if err != nil {
					return nil, fmt.Errorf("could not marshal presubmit: %w", err)
				}

				provider, bucket, err := getStorageProviderAndBucket(cfg, org, r, presubmit.JobBase)
				if err != nil {
					return nil, fmt.Errorf("could not get storage provider and bucket: %w", err)
				}
				cjRepo.Jobs = append(cjRepo.Jobs, configuredjobs.JobInfo{
					Name:           presubmit.Name,
					Type:           v1.PresubmitJob,
					JobHistoryLink: jobHistoryLink(provider, bucket, presubmit.Name, true),
					YAMLDefinition: string(definition),
				})
			}
			postsubmits := jobConfig.AllStaticPostsubmits([]string{orgRepo})
			for _, postsubmit := range postsubmits {
				definition, err := yaml.Marshal(postsubmit)
				if err != nil {
					return nil, fmt.Errorf("could not marshal postsubmit: %w", err)
				}

				provider, bucket, err := getStorageProviderAndBucket(cfg, org, r, postsubmit.JobBase)
				if err != nil {
					return nil, fmt.Errorf("could not get storage provider and bucket: %w", err)
				}
				cjRepo.Jobs = append(cjRepo.Jobs, configuredjobs.JobInfo{
					Name:           postsubmit.Name,
					Type:           v1.PostsubmitJob,
					JobHistoryLink: jobHistoryLink(provider, bucket, postsubmit.Name, false),
					YAMLDefinition: string(definition),
				})
			}
			periodics := jobConfig.PeriodicsMatchingExtraRefs(org, r)
			for _, periodic := range periodics {
				definition, err := yaml.Marshal(periodic)
				if err != nil {
					return nil, fmt.Errorf("could not marshal periodic: %w", err)
				}

				provider, bucket, err := getStorageProviderAndBucket(cfg, org, r, periodic.JobBase)
				if err != nil {
					return nil, fmt.Errorf("could not get storage provider and bucket: %w", err)
				}
				cjRepo.Jobs = append(cjRepo.Jobs, configuredjobs.JobInfo{
					Name:           periodic.Name,
					Type:           v1.PeriodicJob,
					JobHistoryLink: jobHistoryLink(provider, bucket, periodic.Name, false),
					YAMLDefinition: string(definition),
				})
			}
		}

		configuredJobs.IncludedRepos = append(configuredJobs.IncludedRepos, cjRepo)
	}

	return configuredJobs, nil
}

func safeName(name string) string {
	return strings.Replace(name, ".", "-", -1)
}

func getStorageProviderAndBucket(cfg config.Getter, org, repo string, job config.JobBase) (provider string, bucket string, err error) {
	var gcsConfig *v1.GCSConfiguration
	if job.DecorationConfig != nil && job.DecorationConfig.GCSConfiguration != nil {
		gcsConfig = job.DecorationConfig.GCSConfiguration
	} else {
		// for undecorated jobs assume the default
		def := cfg().Plank.GuessDefaultDecorationConfig(fmt.Sprintf("%s/%s", org, repo), job.Cluster)
		if def == nil || def.GCSConfiguration == nil {
			return "", "", fmt.Errorf("failed to guess gcs config based on default decoration config")
		}
		gcsConfig = def.GCSConfiguration
	}

	b := gcsConfig.Bucket
	// If no provider is included, default to gs
	if !strings.Contains(b, "://") {
		b = "gs://" + b
	}
	parsedBucket, err := url.Parse(b)
	if err != nil {
		return "", "", fmt.Errorf("parse bucket %s: %w", bucket, err)
	}

	return parsedBucket.Scheme, parsedBucket.Host, nil
}
