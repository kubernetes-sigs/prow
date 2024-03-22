/*
Copyright 2020 The Kubernetes Authors.

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

// Package podinfo provides a coverage viewer for Spyglass
package podinfo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/test-infra/prow/config"
	k8sreporter "k8s.io/test-infra/prow/crier/reporters/gcs/kubernetes"
	"k8s.io/test-infra/prow/entrypoint"
	"sigs.k8s.io/yaml"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"k8s.io/test-infra/prow/spyglass/api"
	"k8s.io/test-infra/prow/spyglass/lenses"
)

const (
	name     = "podinfo"
	title    = "Job Pod Info"
	priority = 20
)

func init() {
	lenses.RegisterLens(Lens{})
}

// ownConfig stores config specific to podinfo lens.
type ownConfig struct {
	// RunnerConfig defines the mapping between build cluster alias: <template>,
	// where the template is used for helping displaying url to pod.
	RunnerConfigs map[string]RunnerConfig `json:"runner_configs,omitempty"`
}

type RunnerConfig struct {
	PodLinkTemplate string `json:"pod_link_template,omitempty"`
}

// Lens is the implementation of a coverage-rendering Spyglass lens.
type Lens struct{}

// Config returns the lens's configuration.
func (lens Lens) Config() lenses.LensConfig {
	return lenses.LensConfig{
		Name:     name,
		Title:    title,
		Priority: priority,
	}
}

// Header renders the content of <head> from template.html.
func (lens Lens) Header(artifacts []api.Artifact, resourceDir string, config json.RawMessage, spyglassConfig config.Spyglass) string {
	t, err := loadTemplate(filepath.Join(resourceDir, "template.html"))
	if err != nil {
		return fmt.Sprintf("<!-- FAILED LOADING HEADER: %v -->", err)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "header", nil); err != nil {
		return fmt.Sprintf("<!-- FAILED EXECUTING HEADER TEMPLATE: %v -->", err)
	}
	return buf.String()
}

// Callback does nothing.
func (lens Lens) Callback(artifacts []api.Artifact, resourceDir string, data string, config json.RawMessage, spyglassConfig config.Spyglass) string {
	return ""
}

// Body renders the <body>
func (lens Lens) Body(artifacts []api.Artifact, resourceDir string, data string, rawConfig json.RawMessage, spyglassConfig config.Spyglass) string {
	if len(artifacts) == 0 {
		logrus.Error("podinfo Body() called with no artifacts, which should never happen.")
		return "Why am I here? There is no podinfo file."
	}

	var conf ownConfig
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &conf); err != nil {
			logrus.WithError(err).Error("Failed to decode podinfo config")
		}
	}

	var p k8sreporter.PodReport
	var pj prowapi.ProwJob
	for _, artifact := range artifacts {
		switch artifact.JobPath() {
		case "podinfo.json":
			content, err := artifact.ReadAll()
			if err != nil {
				logrus.WithError(err).Warn("Couldn't read a podinfo file that should exist.")
				return fmt.Sprintf("Failed to read the podinfo file: %v", err)
			}

			if err := json.Unmarshal(content, &p); err != nil {
				logrus.WithError(err).Infof("Error unmarshalling PodReport")
				return fmt.Sprintf("Couldn't unmarshal podinfo.json: %v", err)
			}
		case "prowjob.json":
			// Need to figure out which cluster this job runs on. But pod info
			// itself doesn't really know where it belongs to, so get it from prowjob.
			content, err := artifact.ReadAll()
			if err != nil {
				logrus.WithError(err).Warn("Couldn't read a prowjob file that should exist.")
				return fmt.Sprintf("Failed to read the prowjob file: %v", err)
			}

			if err := json.Unmarshal(content, &pj); err != nil {
				logrus.WithError(err).Infof("Error unmarshalling prowjob")
				return fmt.Sprintf("Couldn't unmarshal prowjob.json: %v", err)
			}
		default:
			logrus.WithField("artifact", artifact.JobPath()).Debug("Unsupported artifact by podinfo lens.")
		}
	}

	infoTemplate, err := loadTemplate(filepath.Join(resourceDir, "template.html"))
	if err != nil {
		logrus.WithError(err).Error("Error loading template.")
		return fmt.Sprintf("Failed to load template file: %v", err)
	}

	var podLink string
	if len(conf.RunnerConfigs) > 0 && p.Pod.Name != "" && pj.Spec.Cluster != "" {
		runnerConfig, ok := conf.RunnerConfigs[pj.Spec.Cluster]
		if ok {
			tmpl, err := template.New("tmp").Parse(runnerConfig.PodLinkTemplate)
			if err == nil {
				var b bytes.Buffer
				err = tmpl.Execute(&b, p.Pod)
				if err == nil {
					podLink = b.String()
				}
			}
			if err != nil {
				logrus.WithError(err).Info("Error parsing template")
			}
		}
	}

	t := struct {
		PodReport  k8sreporter.PodReport
		PodLink    string
		Containers []containerInfo
	}{
		PodReport:  p,
		PodLink:    podLink,
		Containers: append(assembleContainers(p.Pod.Spec.InitContainers, p.Pod.Status.InitContainerStatuses), assembleContainers(p.Pod.Spec.Containers, p.Pod.Status.ContainerStatuses)...),
	}

	var buf bytes.Buffer
	if err := infoTemplate.ExecuteTemplate(&buf, "body", t); err != nil {
		logrus.WithError(err).Error("Error executing template.")
	}

	return buf.String()
}

type containerInfo struct {
	// Container is a container spec
	Container *v1.Container
	// Status is a container status corresponding to the spec
	Status *v1.ContainerStatus
	// DecoratedArgs is the arguments the podutils entrypoint is invoking,
	// which is explicitly extracted because `/tools/entrypoint` is not a very
	// useful entrypoint to report.
	DecoratedArgs []string
}

func assembleContainers(containers []v1.Container, containerStatuses []v1.ContainerStatus) []containerInfo {
	var assembled []containerInfo
	for i, c := range containers {
		ci := containerInfo{
			Container: &containers[i],
			Status:    nil,
		}
		for _, env := range c.Env {
			if env.Name == entrypoint.JSONConfigEnvVar && env.Value != "" {
				entrypointOptions := entrypoint.NewOptions()
				if err := entrypointOptions.LoadConfig(env.Value); err != nil {
					logrus.WithError(err).Infof("Couldn't parse JSON config env var")
					break
				}
				ci.DecoratedArgs = entrypointOptions.Args
				break
			}
		}
		for j, s := range containerStatuses {
			if s.Name == c.Name {
				ci.Status = &containerStatuses[j]
				break
			}
		}
		if ci.Status != nil {
			assembled = append(assembled, ci)
		}
	}
	return assembled
}

func loadTemplate(path string) (*template.Template, error) {
	return template.New("template.html").Funcs(template.FuncMap{
		"isProw": func(s string) bool {
			return strings.HasPrefix(s, "prow.k8s.io/") || strings.HasPrefix(s, "testgrid-") || s == "created-by-prow"
		},
		"toYaml": func(o interface{}) (string, error) {
			result, err := yaml.Marshal(o)
			if err != nil {
				return "", err
			}
			return string(result), nil
		},
		"toAge": func(t time.Time) string {
			d := time.Since(t)
			if d < time.Minute {
				return d.Truncate(time.Second).String()
			}
			s := d.Truncate(time.Minute).String()
			// Chop off the 0s at the end.
			return s[:len(s)-2]
		},
	}).ParseFiles(path)
}
