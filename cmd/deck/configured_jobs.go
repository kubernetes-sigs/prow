package main

import (
	"fmt"
	"strings"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/configuredjobs"
	"sigs.k8s.io/yaml"
)

func GetIndex(jobConfig config.JobConfig) configuredjobs.Index {
	repos := jobConfig.AllRepos

	orgs := make(map[string]*configuredjobs.Org)
	for _, r := range repos.UnsortedList() {
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

	orgList := make([]configuredjobs.Org, 0, len(orgs))
	for _, org := range orgs {
		orgList = append(orgList, *org)
	}
	return configuredjobs.Index{Orgs: orgList}
}

func GetConfiguredJobs(jobConfig config.JobConfig, org, repo string) (*configuredjobs.ConfiguredJobs, error) {
	configuredJobs := &configuredjobs.ConfiguredJobs{}

	var orgRepos []string
	if repo != "" {
		orgRepos = []string{fmt.Sprintf("%s/%s", org, repo)}
	} else {
		for _, r := range jobConfig.AllRepos.UnsortedList() {
			o := strings.Split(r, "/")[0]
			if o == org {
				orgRepos = append(orgRepos, r)
			}
		}
	}

	for _, orgRepo := range orgRepos {
		r := strings.Split(orgRepo, "/")[1]
		cjRepo := configuredjobs.Repo{
			Org:  org,
			Name: r,
		}
		//TODO: I think we could improve the performance here by including all the repos in each search and then sorting jobs later
		// not sure if it is worth the decreased readability though
		presubmits := jobConfig.AllStaticPresubmits([]string{orgRepo})
		for _, presubmit := range presubmits {
			definition, err := yaml.Marshal(presubmit)
			if err != nil {
				return nil, fmt.Errorf("could not marshal presubmit: %w", err)
			}
			cjRepo.Presubmits = append(cjRepo.Presubmits, configuredjobs.Presubmit{
				Name:              presubmit.Name,
				ConfigurationLink: "TODO", //TODO: is it even possible to do this???
				YAMLDefinition:    string(definition),
			})
		}
		postsubmits := jobConfig.AllStaticPostsubmits([]string{orgRepo})
		for _, postsubmit := range postsubmits {
			definition, err := yaml.Marshal(postsubmit)
			if err != nil {
				return nil, fmt.Errorf("could not marshal postsubmit: %w", err)
			}
			cjRepo.Postsubmits = append(cjRepo.Postsubmits, configuredjobs.Postsubmit{
				Name:              postsubmit.Name,
				ConfigurationLink: "TODO", //TODO: is it even possible to do this???
				YAMLDefinition:    string(definition),
			})
		}
		periodics := jobConfig.PeriodicsMatchingExtraRefs(org, repo)
		for _, periodic := range periodics {
			definition, err := yaml.Marshal(periodic)
			if err != nil {
				return nil, fmt.Errorf("could not marshal periodic: %w", err)
			}
			cjRepo.Periodics = append(cjRepo.Periodics, configuredjobs.Periodic{
				Name:              periodic.Name,
				ConfigurationLink: "TODO", //TODO
				YAMLDefinition:    string(definition),
			})
		}

		configuredJobs.Repos = append(configuredJobs.Repos, cjRepo)
	}

	return configuredJobs, nil
}
