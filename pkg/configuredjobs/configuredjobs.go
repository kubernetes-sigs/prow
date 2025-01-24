package configuredjobs

//TODO: documentation for everything here

type ConfiguredJobs struct {
	Repos []Repo `json:"repos"`
}

type Index struct {
	Orgs []Org `json:"orgs"`
}

type Org struct {
	Name string `json:"name"`
	// SafeName is the Name with unsupported chars stripped
	SafeName string   `json:"safeName"` //TODO: do we use this anywhere?
	Repos    []string `json:"repos"`
}

type Repo struct {
	Org Org `json:"org"`
	// SafeName is the Name with unsupported chars stripped
	SafeName string    `json:"safeName"`
	Name     string    `json:"name"`
	Jobs     []JobInfo `json:"jobs"`
}

type JobInfo struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	JobHistoryLink string `json:"jobHistoryLink"`
	YAMLDefinition string `json:"yamlDefinition"`
}
