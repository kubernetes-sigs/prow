/*
Copyright 2022 The Kubernetes Authors.

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
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/genyaml"
	"sigs.k8s.io/prow/pkg/plugins"
)

const (
	// Pretending that it runs from root of the repo
	defaultRootDir = "."
)

var genConfigs = []genConfig{
	{
		in: []string{
			"pkg/config/*.go",
			"pkg/apis/prowjobs/v1/*.go",
		},
		format: &config.ProwConfig{},
		out:    "pkg/config/prow-config-documented.yaml",
	},
	{
		in: []string{
			"pkg/plugins/*.go",
		},
		format: &plugins.Configuration{},
		out:    "pkg/plugins/plugin-config-documented.yaml",
	},
}

type genConfig struct {
	in     []string
	format interface{}
	out    string
}

func (g *genConfig) gen(rootDir string, importPathResolver genyaml.ImportPathResolver) error {
	var inputFiles []string
	for _, goGlob := range g.in {
		ifs, err := filepath.Glob(path.Join(rootDir, goGlob))
		if err != nil {
			return fmt.Errorf("filepath glob: %w", err)
		}
		inputFiles = append(inputFiles, ifs...)
	}

	commentMap, err := genyaml.NewCommentMap(importPathResolver, nil, inputFiles...)
	if err != nil {
		return fmt.Errorf("failed to construct commentMap: %w", err)
	}
	actualYaml, err := commentMap.GenYaml(genyaml.PopulateStruct(g.format))
	if err != nil {
		return fmt.Errorf("genyaml errored: %w", err)
	}
	if err := os.WriteFile(path.Join(rootDir, g.out), []byte(actualYaml), 0644); err != nil {
		return fmt.Errorf("failed to write fixture: %w", err)
	}
	return nil
}

func importPathResolverFunc(root string) (genyaml.ImportPathResolver, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, errors.New("module support needed")
	}
	modName := info.Main.Path
	resolved := make(map[string]string)

	return func(dir string) (string, error) {
		if importPath, ok := resolved[dir]; ok {
			return importPath, nil
		}
		packageName := strings.TrimPrefix(dir, root)
		importPath := path.Join(modName, packageName)
		resolved[dir] = importPath
		return importPath, nil
	}, nil
}

func main() {
	rootDir := flag.String("root-dir", defaultRootDir, "Repo root dir.")
	flag.Parse()

	importPathResolver, err := importPathResolverFunc(*rootDir)
	if err != nil {
		logrus.WithError(err).Error("Failed to create the import path resolver.")
		os.Exit(1)
	}

	for _, g := range genConfigs {
		if err := g.gen(*rootDir, importPathResolver); err != nil {
			logrus.WithError(err).WithField("fixture", g.out).Error("Failed generating.")
			os.Exit(1)
		}
	}
}
