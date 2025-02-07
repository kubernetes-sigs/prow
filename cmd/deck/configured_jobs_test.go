package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/configuredjobs"
)

func TestGetIndex(t *testing.T) {
	testCases := []struct {
		name     string
		repos    []string
		expected configuredjobs.Index
	}{
		{
			name:  "basic case",
			repos: []string{"kubernetes/kubernetes", "kubernetes-sigs/prow", "kubernetes/test-infra"},
			expected: configuredjobs.Index{
				Orgs: []configuredjobs.Org{
					{
						Name:  "kubernetes",
						Repos: []string{"kubernetes", "test-infra"},
					},
					{
						Name:  "kubernetes-sigs",
						Repos: []string{"prow"},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jobConfig := config.JobConfig{AllRepos: sets.New(tc.repos...)}
			index := GetIndex(jobConfig)
			if diff := cmp.Diff(tc.expected, index); diff != "" {
				t.Errorf("GetIndex() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetConfiguredJobs(t *testing.T) {
	testCases := []struct {
		name     string
		repos    []string
		org      string
		repo     string
		expected configuredjobs.JobsByRepo
	}{
		{
			name:  "for only a specific repo",
			repos: []string{"kubernetes/kubernetes", "kubernetes-sigs/prow", "kubernetes/test-infra"},
			org:   "kubernetes-sigs",
			repo:  "prow",
			expected: configuredjobs.JobsByRepo{
				IncludedRepos: []configuredjobs.RepoInfo{
					{
						Name:     "prow",
						SafeName: "prow",
						Org:      configuredjobs.Org{Name: "kubernetes-sigs"},
						Jobs: []configuredjobs.JobInfo{
							{
								Name:           "some-presubmit-with-special-decoration-config",
								Type:           "presubmit",
								JobHistoryLink: "/job-history/gs/special-results/pr-logs/directory/some-presubmit-with-special-decoration-config",
								YAMLDefinition: "always_run: false\ndecoration_config:\n  gcs_configuration:\n    bucket: special-results\nname: some-presubmit-with-special-decoration-config\n",
							},
							{
								Name:           "other-presubmit",
								Type:           "presubmit",
								JobHistoryLink: "/job-history/gs/prow-results/pr-logs/directory/other-presubmit",
								YAMLDefinition: "always_run: false\nname: other-presubmit\n",
							},
							{
								Name:           "some-postsubmit",
								Type:           "postsubmit",
								JobHistoryLink: "/job-history/gs/prow-results/logs/some-postsubmit",
								YAMLDefinition: "name: some-postsubmit\n",
							},
							{
								Name:           "other-postsubmit",
								Type:           "postsubmit",
								JobHistoryLink: "/job-history/gs/prow-results/logs/other-postsubmit",
								YAMLDefinition: "name: other-postsubmit\n",
							},
							{
								Name:           "some-prow-periodic",
								Type:           "periodic",
								JobHistoryLink: "/job-history/gs/prow-results/logs/some-prow-periodic",
								YAMLDefinition: "extra_refs:\n- org: kubernetes-sigs\n  repo: prow\nname: some-prow-periodic\n",
							},
						},
					},
				},
				AllRepos: []string{"kubernetes-sigs/prow", "kubernetes/kubernetes", "kubernetes/test-infra"},
			},
		},
		{
			name:  "for an org",
			repos: []string{"kubernetes/kubernetes", "kubernetes-sigs/prow", "kubernetes/test-infra"},
			org:   "kubernetes",
			expected: configuredjobs.JobsByRepo{
				IncludedRepos: []configuredjobs.RepoInfo{
					{
						Org:      configuredjobs.Org{Name: "kubernetes"},
						SafeName: "kubernetes",
						Name:     "kubernetes",
						Jobs: []configuredjobs.JobInfo{
							{
								Name:           "some-k8s-presubmit",
								Type:           "presubmit",
								JobHistoryLink: "/job-history/gs/results/pr-logs/directory/some-k8s-presubmit",
								YAMLDefinition: "always_run: false\nname: some-k8s-presubmit\n",
							},
							{
								Name:           "other-k8s-presubmit",
								Type:           "presubmit",
								JobHistoryLink: "/job-history/gs/results/pr-logs/directory/other-k8s-presubmit",
								YAMLDefinition: "always_run: false\nname: other-k8s-presubmit\n",
							},
							{
								Name:           "some-k8s-postsubmit",
								Type:           "postsubmit",
								JobHistoryLink: "/job-history/gs/results/logs/some-k8s-postsubmit",
								YAMLDefinition: "name: some-k8s-postsubmit\n",
							},
							{
								Name:           "other-k8s-postsubmit",
								Type:           "postsubmit",
								JobHistoryLink: "/job-history/gs/results/logs/other-k8s-postsubmit",
								YAMLDefinition: "name: other-k8s-postsubmit\n",
							},
							{
								Name:           "some-k8s-periodic",
								Type:           "periodic",
								JobHistoryLink: "/job-history/gs/results/logs/some-k8s-periodic",
								YAMLDefinition: "extra_refs:\n- org: kubernetes\n  repo: kubernetes\nname: some-k8s-periodic\n",
							},
							{
								Name:           "other-k8s-periodic",
								Type:           "periodic",
								JobHistoryLink: "/job-history/gs/results/logs/other-k8s-periodic",
								YAMLDefinition: "extra_refs:\n- org: kubernetes\n  repo: kubernetes\nname: other-k8s-periodic\n",
							},
						},
					},
					{
						Org:      configuredjobs.Org{Name: "kubernetes"},
						SafeName: "test-infra",
						Name:     "test-infra",
						Jobs: []configuredjobs.JobInfo{
							{
								Name:           "some-test-infra-presubmit",
								Type:           "presubmit",
								JobHistoryLink: "/job-history/gs/test-infra-results/pr-logs/directory/some-test-infra-presubmit",
								YAMLDefinition: "always_run: false\nname: some-test-infra-presubmit\n",
							},
							{
								Name:           "other-test-infra-presubmit",
								Type:           "presubmit",
								JobHistoryLink: "/job-history/gs/test-infra-results/pr-logs/directory/other-test-infra-presubmit",
								YAMLDefinition: "always_run: false\nname: other-test-infra-presubmit\n",
							},
							{
								Name:           "some-test-infra-postsubmit",
								Type:           "postsubmit",
								JobHistoryLink: "/job-history/gs/test-infra-results/logs/some-test-infra-postsubmit",
								YAMLDefinition: "name: some-test-infra-postsubmit\n",
							},
							{
								Name:           "other-test-infra-postsubmit",
								Type:           "postsubmit",
								JobHistoryLink: "/job-history/gs/test-infra-results/logs/other-test-infra-postsubmit",
								YAMLDefinition: "name: other-test-infra-postsubmit\n",
							},
						},
					},
				},
				AllRepos: []string{"kubernetes-sigs/prow", "kubernetes/kubernetes", "kubernetes/test-infra"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configGetter := getConfigGetter(tc.repos)
			configuredJobs, err := GetConfiguredJobs(configGetter, tc.org, tc.repo)
			if err != nil {
				t.Fatalf("GetConfiguredJobs returned unexpected err: %v", err)
			}
			if diff := cmp.Diff(&tc.expected, configuredJobs); diff != "" {
				t.Errorf("GetConfiguredJobs() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}

func getConfigGetter(repos []string) config.Getter {
	ca := config.Agent{}
	ca.Set(&config.Config{
		ProwConfig: config.ProwConfig{
			Plank: config.Plank{
				DefaultDecorationConfigs: []*config.DefaultDecorationConfigEntry{
					{
						OrgRepo: "kubernetes/kubernetes",
						Config: &prowapi.DecorationConfig{
							GCSConfiguration: &prowapi.GCSConfiguration{
								Bucket: "results",
							},
						},
					},
					{
						OrgRepo: "kubernetes/test-infra",
						Config: &prowapi.DecorationConfig{
							GCSConfiguration: &prowapi.GCSConfiguration{
								Bucket: "test-infra-results",
							},
						},
					},
					{
						OrgRepo: "kubernetes-sigs/prow",
						Config: &prowapi.DecorationConfig{
							GCSConfiguration: &prowapi.GCSConfiguration{
								Bucket: "prow-results",
							},
						},
					},
				},
			},
		},
		JobConfig: config.JobConfig{
			AllRepos: sets.New(repos...),
			PresubmitsStatic: map[string][]config.Presubmit{
				"kubernetes-sigs/prow": {
					{
						JobBase: config.JobBase{
							Name: "some-presubmit-with-special-decoration-config",
							UtilityConfig: config.UtilityConfig{
								DecorationConfig: &prowapi.DecorationConfig{
									GCSConfiguration: &prowapi.GCSConfiguration{
										Bucket: "special-results",
									},
								},
							},
						},
					},
					{
						JobBase: config.JobBase{
							Name: "other-presubmit",
						},
					},
				},
				"kubernetes/test-infra": {
					{
						JobBase: config.JobBase{
							Name: "some-test-infra-presubmit",
						},
					},
					{
						JobBase: config.JobBase{
							Name: "other-test-infra-presubmit",
						},
					},
				},
				"kubernetes/kubernetes": {
					{
						JobBase: config.JobBase{
							Name: "some-k8s-presubmit",
						},
					},
					{
						JobBase: config.JobBase{
							Name: "other-k8s-presubmit",
						},
					},
				},
			},
			PostsubmitsStatic: map[string][]config.Postsubmit{
				"kubernetes-sigs/prow": {
					{
						JobBase: config.JobBase{
							Name: "some-postsubmit",
						},
					},
					{
						JobBase: config.JobBase{
							Name: "other-postsubmit",
						},
					},
				},
				"kubernetes/test-infra": {
					{
						JobBase: config.JobBase{
							Name: "some-test-infra-postsubmit",
						},
					},
					{
						JobBase: config.JobBase{
							Name: "other-test-infra-postsubmit",
						},
					},
				},
				"kubernetes/kubernetes": {
					{
						JobBase: config.JobBase{
							Name: "some-k8s-postsubmit",
						},
					},
					{
						JobBase: config.JobBase{
							Name: "other-k8s-postsubmit",
						},
					},
				},
			},
			Periodics: []config.Periodic{
				{
					JobBase: config.JobBase{
						Name: "some-k8s-periodic",
						UtilityConfig: config.UtilityConfig{
							ExtraRefs: []prowapi.Refs{
								{Org: "kubernetes", Repo: "kubernetes"},
							},
						},
					},
				},
				{
					JobBase: config.JobBase{
						Name: "other-k8s-periodic",
						UtilityConfig: config.UtilityConfig{
							ExtraRefs: []prowapi.Refs{
								{Org: "kubernetes", Repo: "kubernetes"},
							},
						},
					},
				},
				{
					JobBase: config.JobBase{
						Name: "some-prow-periodic",
						UtilityConfig: config.UtilityConfig{
							ExtraRefs: []prowapi.Refs{
								{Org: "kubernetes-sigs", Repo: "prow"},
							},
						},
					},
				},
			},
		},
	})

	return ca.Config
}
