/*
Copyright The Kubernetes Authors.

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

package markdown

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/spyglass/api"
	"sigs.k8s.io/prow/pkg/spyglass/lenses"
)

func init() {
	lenses.RegisterLens(Lens{})
}

// Lens is the implementation of a markdown lens.
type Lens struct{}

type document struct {
	Title   string
	Content template.HTML
}

// Config returns the lens's configuration.
func (lens Lens) Config() lenses.LensConfig {
	return lenses.LensConfig{
		Name:      "markdown",
		Title:     "Summary",
		Priority:  5,
	}
}

// Header renders the content of <head> from template.html.
func (lens Lens) Header(artifacts []api.Artifact, resourceDir string, config json.RawMessage, spyglassConfig config.Spyglass) string {
	t, err := template.ParseFiles(filepath.Join(resourceDir, "template.html"))
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
func (lens Lens) Body(artifacts []api.Artifact, resourceDir string, data string, config json.RawMessage, spyglassConfig config.Spyglass) string {
	if len(artifacts) == 0 {
		logrus.Error("markdown Body() called with no artifacts, which should never happen.")
		return "No markdown file found."
	}

	var targetFile string
	var customTitle string
	if len(config) > 0 {
		var lConf struct {
			Title string `json:"title,omitempty"`
			File  string `json:"file,omitempty"`
		}
		if err := json.Unmarshal(config, &lConf); err == nil {
			targetFile = lConf.File
			customTitle = lConf.Title
		}
	}

	var documents []document
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
	)
	for _, artifact := range artifacts {
		if targetFile != "" && !strings.HasSuffix(artifact.JobPath(), targetFile) {
			continue
		}

		content, err := artifact.ReadAll()
		if err != nil {
			logrus.WithError(err).WithField("artifact_url", artifact.CanonicalLink()).Warn("failed to read content")
			continue
		}
		
		var buf bytes.Buffer
		if err := md.Convert(content, &buf); err != nil {
			logrus.WithError(err).WithField("artifact_url", artifact.CanonicalLink()).Warn("failed to convert markdown")
			continue
		}
		htmlContent := buf.String()

		title := filepath.Base(artifact.CanonicalLink())
		if customTitle != "" {
			title = customTitle
		}

		documents = append(documents, document{
			Title:   title,
			Content: template.HTML(htmlContent),
		})
	}

	if len(documents) == 0 {
		return `<script>
			if (window.frameElement) {
				const container = window.frameElement.parentElement.parentElement;
				if (container) {
					container.style.display = 'none';
				}
			}
		</script>`
	}

	t, err := template.ParseFiles(filepath.Join(resourceDir, "template.html"))
	if err != nil {
		logrus.WithError(err).Error("Error parsing template.")
		return fmt.Sprintf("Failed to load template file: %v", err)
	}

	buf := &bytes.Buffer{}
	if err := t.ExecuteTemplate(buf, "body", documents); err != nil {
		return fmt.Sprintf("Failed to execute template: %v", err)
	}
	return buf.String()
}
