/*
Copyright 2026 The Kubernetes Authors.

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
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

type Config struct {
	Repos map[string]Repo `json:"repos,omitempty"`
}

type Repo struct {
	SiteID string `json:"site_id,omitempty"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	for repo, cfg := range c.Repos {
		if cfg.SiteID == "" {
			return fmt.Errorf("missing site_id for repo %q", repo)
		}
	}
	return nil
}

func (c *Config) Repo(org, repo string) (Repo, bool) {
	if c == nil {
		return Repo{}, false
	}
	cfg, ok := c.Repos[org+"/"+repo]
	return cfg, ok
}
