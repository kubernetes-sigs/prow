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
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`repos:
  kubernetes/website:
    site_id: site-123
`), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	repo, ok := cfg.Repo("kubernetes", "website")
	if !ok {
		t.Fatal("expected kubernetes/website mapping")
	}
	if repo.SiteID != "site-123" {
		t.Fatalf("expected site id %q, got %q", "site-123", repo.SiteID)
	}
}

func TestLoadRejectsMissingSiteID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`repos:
  kubernetes/website: {}
`), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing site id")
	}
}
