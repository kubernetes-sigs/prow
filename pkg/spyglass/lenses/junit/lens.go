/*
Copyright 2018 The Kubernetes Authors.

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

// Package junit provides a junit viewer for Spyglass
package junit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/testgrid/metadata/junit"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/spyglass/api"
	"sigs.k8s.io/prow/pkg/spyglass/lenses"
)

const (
	name                     = "junit"
	title                    = "JUnit"
	priority                 = 5
	passedStatus  testStatus = "Passed"
	failedStatus  testStatus = "Failed"
	skippedStatus testStatus = "Skipped"
)

func init() {
	lenses.RegisterLens(Lens{})
}

type testStatus string

// Lens is the implementation of a JUnit-rendering Spyglass lens.
type Lens struct{}

// GroupConfig defines a test group that segregates matching tests into a
// separate section with its own pass/fail/skip/flaky breakdown. The Selector
// is an XPath-like expression evaluated against each <testcase> element;
// currently only property predicates are supported, e.g.:
//
//	properties/property[@name='lifecycle' and @value='informing']
type GroupConfig struct {
	Name      string `json:"name"`
	Selector  string `json:"selector"`
	Collapsed bool   `json:"collapsed"`
}

// LensConfig is the configuration for the JUnit lens.
type LensConfig struct {
	Groups []GroupConfig `json:"groups,omitempty"`
}

// GroupResult holds the complete test breakdown for a configured group.
type GroupResult struct {
	Name      string
	Collapsed bool
	NumTests  int
	Passed    []TestResult
	Failed    []TestResult
	Skipped   []TestResult
	Flaky     []TestResult
}

type JVD struct {
	NumTests int
	Passed   []TestResult
	Failed   []TestResult
	Skipped  []TestResult
	Flaky    []TestResult
	Groups   []GroupResult
}

// TotalTests returns the total number of tests across all groups and the
// default section, for use in the template's empty-check.
func (j JVD) TotalTests() int {
	total := j.NumTests
	for _, g := range j.Groups {
		total += g.NumTests
	}
	return total
}

// propertyPredicate represents a parsed property[@name='X' and @value='Y']
// selector. Either field may be empty if not constrained.
type propertyPredicate struct {
	name  string
	value string
}

// selectorPredicate is a compiled selector that can match a junit.Result.
type selectorPredicate struct {
	properties []propertyPredicate
}

var propertyPredicateRE = regexp.MustCompile(
	`properties/property\[([^\]]+)\]`,
)

func parseSelector(selector string) (selectorPredicate, error) {
	selector = strings.TrimSpace(selector)
	var pred selectorPredicate
	matches := propertyPredicateRE.FindAllStringSubmatch(selector, -1)
	if len(matches) == 0 {
		return pred, fmt.Errorf("unsupported selector syntax: %s", selector)
	}
	for _, m := range matches {
		pp, err := parsePropertyPredicate(m[1])
		if err != nil {
			return pred, err
		}
		pred.properties = append(pred.properties, pp)
	}
	return pred, nil
}

var attrRE = regexp.MustCompile(`@(\w+)\s*=\s*'([^']*)'`)

func parsePropertyPredicate(expr string) (propertyPredicate, error) {
	var pp propertyPredicate
	attrs := attrRE.FindAllStringSubmatch(expr, -1)
	if len(attrs) == 0 {
		return pp, fmt.Errorf("no attribute predicates found in: %s", expr)
	}
	for _, a := range attrs {
		switch a[1] {
		case "name":
			pp.name = a[2]
		case "value":
			pp.value = a[2]
		default:
			return pp, fmt.Errorf("unsupported attribute: @%s", a[1])
		}
	}
	return pp, nil
}

func (sp selectorPredicate) matches(r junit.Result) bool {
	if r.Properties == nil {
		return false
	}
	for _, pp := range sp.properties {
		if !pp.matchesAny(r.Properties.PropertyList) {
			return false
		}
	}
	return true
}

func (pp propertyPredicate) matchesAny(props []junit.Property) bool {
	for _, p := range props {
		if pp.name != "" && p.Name != pp.name {
			continue
		}
		if pp.value != "" && p.Value != pp.value {
			continue
		}
		return true
	}
	return false
}

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

type JunitResult struct {
	junit.Result
}

func (jr JunitResult) Duration() time.Duration {
	return time.Duration(jr.Time * float64(time.Second)).Round(time.Second)
}

func (jr JunitResult) Status() testStatus {
	res := passedStatus
	if jr.Skipped != nil {
		res = skippedStatus
	} else if jr.Failure != nil || jr.Errored != nil {
		res = failedStatus
	}
	return res
}

func (jr JunitResult) SkippedReason() string {
	res := ""
	if jr.Skipped != nil {
		res = jr.Message(-1) // Don't truncate
	}
	return res
}

// TestResult holds data about a test extracted from junit output
type TestResult struct {
	Junit []JunitResult
	Link  string
}

func parseLensConfig(rawConfig json.RawMessage) LensConfig {
	var cfg LensConfig
	if len(rawConfig) == 0 {
		return cfg
	}
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		logrus.WithError(err).Error("Failed to decode junit lens config")
	}
	return cfg
}

// Body renders the <body> for JUnit tests
func (lens Lens) Body(artifacts []api.Artifact, resourceDir string, data string, rawConfig json.RawMessage, spyglassConfig config.Spyglass) string {
	cfg := parseLensConfig(rawConfig)
	jvd := lens.getJvd(artifacts, cfg.Groups)

	junitTemplate, err := template.ParseFiles(filepath.Join(resourceDir, "template.html"))
	if err != nil {
		logrus.WithError(err).Error("Error executing template.")
		return fmt.Sprintf("Failed to load template file: %v", err)
	}

	var buf bytes.Buffer
	if err := junitTemplate.ExecuteTemplate(&buf, "body", jvd); err != nil {
		logrus.WithError(err).Error("Error executing template.")
	}

	return buf.String()
}

// testBucket is an interface for appending classified test results.
type testBucket struct {
	numTests *int
	passed   *[]TestResult
	failed   *[]TestResult
	skipped  *[]TestResult
	flaky    *[]TestResult
}

func jvdBucket(jvd *JVD) testBucket {
	return testBucket{&jvd.NumTests, &jvd.Passed, &jvd.Failed, &jvd.Skipped, &jvd.Flaky}
}

func groupBucket(g *GroupResult) testBucket {
	return testBucket{&g.NumTests, &g.Passed, &g.Failed, &g.Skipped, &g.Flaky}
}

// classifyTests takes a list of deduplicated test run groups and classifies
// each into passed/failed/skipped/flaky, appending to the given bucket.
// It updates numTests accounting for tests that appear in both skipped and
// failed (which are counted once, not twice).
func classifyTests(b testBucket, testGroups [][]JunitResult, link string) {
	prevTotal := len(*b.passed) + len(*b.failed) + len(*b.flaky) + len(*b.skipped)
	var duplicates int
	for _, tests := range testGroups {
		var (
			skipped bool
			passed  bool
			failed  bool
			flaky   bool
		)
		for _, test := range tests {
			if test.Status() == skippedStatus {
				skipped = true
			} else if test.Status() == failedStatus {
				if passed {
					passed = false
					failed = false
					flaky = true
				}
				if !flaky {
					failed = true
				}
			} else if failed {
				passed = false
				failed = false
				flaky = true
			} else if !flaky {
				passed = true
			}
		}

		tr := TestResult{Junit: tests, Link: link}
		if skipped {
			*b.skipped = append(*b.skipped, tr)
			if failed {
				*b.failed = append(*b.failed, tr)
				duplicates++
			}
		} else if failed {
			*b.failed = append(*b.failed, tr)
		} else if flaky {
			*b.flaky = append(*b.flaky, tr)
		} else {
			*b.passed = append(*b.passed, tr)
		}
	}
	newTotal := len(*b.passed) + len(*b.failed) + len(*b.flaky) + len(*b.skipped)
	*b.numTests += (newTotal - prevTotal) - duplicates
}

func (lens Lens) getJvd(artifacts []api.Artifact, groups []GroupConfig) JVD {
	// Compile group selectors.
	type compiledGroup struct {
		config    GroupConfig
		predicate selectorPredicate
	}
	var compiled []compiledGroup
	for _, gc := range groups {
		pred, err := parseSelector(gc.Selector)
		if err != nil {
			logrus.WithError(err).WithField("selector", gc.Selector).Warn("Invalid group selector, skipping group")
			continue
		}
		compiled = append(compiled, compiledGroup{config: gc, predicate: pred})
	}

	type testResults struct {
		junit [][]JunitResult
		link  string
		path  string
		err   error
	}
	type testIdentifier struct {
		suite string
		class string
		name  string
	}
	resultChan := make(chan testResults)
	for _, artifact := range artifacts {
		go func(artifact api.Artifact) {
			dedupe := make(map[testIdentifier][]JunitResult)
			var testsSequence []testIdentifier
			result := testResults{
				link: artifact.CanonicalLink(),
				path: artifact.JobPath(),
			}
			var contents []byte
			contents, result.err = artifact.ReadAll()
			if result.err != nil {
				logrus.WithError(result.err).WithField("artifact", artifact.CanonicalLink()).Warn("Error reading artifact")
				resultChan <- result
				return
			}
			var suites *junit.Suites
			suites, result.err = junit.Parse(contents)
			if result.err != nil {
				logrus.WithError(result.err).WithField("artifact", artifact.CanonicalLink()).Info("Error parsing junit file.")
				resultChan <- result
				return
			}
			var record func(suite junit.Suite)
			record = func(suite junit.Suite) {
				for _, subSuite := range suite.Suites {
					record(subSuite)
				}

				for _, test := range suite.Results {
					// There are cases where multiple entries of exactly the same
					// testcase in a single junit result file, this could result
					// from reruns of test cases by `go test --count=N` where N>1.
					// Deduplicate them here in this case, and classify a test as being
					// flaky if it both succeeded and failed
					k := testIdentifier{suite.Name, test.ClassName, test.Name}
					dedupe[k] = append(dedupe[k], JunitResult{Result: test})
					if len(dedupe[k]) == 1 {
						testsSequence = append(testsSequence, k)
					}
				}
			}
			for _, suite := range suites.Suites {
				record(suite)
			}
			for _, identifier := range testsSequence {
				result.junit = append(result.junit, dedupe[identifier])
			}
			resultChan <- result
		}(artifact)
	}
	results := make([]testResults, 0, len(artifacts))
	for range artifacts {
		results = append(results, <-resultChan)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].path < results[j].path })

	var jvd JVD

	// Initialize group results.
	for _, cg := range compiled {
		jvd.Groups = append(jvd.Groups, GroupResult{
			Name:      cg.config.Name,
			Collapsed: cg.config.Collapsed,
		})
	}

	// Partition each test into its matching group, or the default bucket.
	for _, result := range results {
		if result.err != nil {
			continue
		}

		// If no groups configured, classify everything into the default bucket.
		if len(compiled) == 0 {
			classifyTests(jvdBucket(&jvd), result.junit, result.link)
			continue
		}

		// Partition tests by group membership.
		groupPartitions := make([][][]JunitResult, len(compiled))
		var defaultPartition [][]JunitResult
		for _, tests := range result.junit {
			matched := false
			for i := range compiled {
				if compiled[i].predicate.matches(tests[0].Result) {
					groupPartitions[i] = append(groupPartitions[i], tests)
					matched = true
					break
				}
			}
			if !matched {
				defaultPartition = append(defaultPartition, tests)
			}
		}

		classifyTests(jvdBucket(&jvd), defaultPartition, result.link)
		for i := range compiled {
			classifyTests(groupBucket(&jvd.Groups[i]), groupPartitions[i], result.link)
		}
	}

	return jvd
}
