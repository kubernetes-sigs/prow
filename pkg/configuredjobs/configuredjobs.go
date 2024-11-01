package configuredjobs

//TODO: documentation for everything here

// TODO: we don't need all the json info
type ConfiguredJobs struct {
	Repos []Repo `json:"repos"`
}

type Index struct {
	Orgs []Org `json:"orgs"`
}

type Org struct {
	Name  string   `json:"name"`
	Repos []string `json:"repos"`
}

type Repo struct {
	Org         string       `json:"org"`
	Name        string       `json:"name"`
	Presubmits  []Presubmit  `json:"presubmits,omitempty"`
	Postsubmits []Postsubmit `json:"postsubmits,omitempty"`
	Periodics   []Periodic   `json:"periodics,omitempty"`
}

type Presubmit struct {
	Name              string `json:"name"`
	ConfigurationLink string `json:"configuration_link"`
	YAMLDefinition    string `json:"yaml_definition"`
}

type Postsubmit struct {
	Name              string `json:"name"`
	ConfigurationLink string `json:"configuration_link"`
	YAMLDefinition    string `json:"yaml_definition"`
}

type Periodic struct {
	Name              string `json:"name"`
	ConfigurationLink string `json:"configuration_link"`
	YAMLDefinition    string `json:"yaml_definition"`
}
