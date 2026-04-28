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

// Package junit provides a junit viewer for Spyglass
package junit

import (
	"bytes"
	"context"
	"html/template"
	"io"
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/testgrid/metadata/junit"
	"github.com/google/go-cmp/cmp"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/prow/pkg/spyglass/api"
	"sigs.k8s.io/prow/pkg/spyglass/lenses"
)

const (
	fakeCanonicalLink = "linknotfound.io/404"
)

// FakeArtifact implements lenses.Artifact.
// This is pretty much copy/pasted from prow/spyglass/lenses/lenses_test.go, if
// another package needs to reuse, should think about refactor this into it's
// own package
type FakeArtifact struct {
	path      string
	content   []byte
	sizeLimit int64
}

func (fa *FakeArtifact) JobPath() string {
	return fa.path
}

func (fa *FakeArtifact) Size() (int64, error) {
	return int64(len(fa.content)), nil
}

func (fa *FakeArtifact) CanonicalLink() string {
	return fakeCanonicalLink
}

func (fa *FakeArtifact) Metadata() (map[string]string, error) {
	return nil, nil
}

func (fa *FakeArtifact) UpdateMetadata(map[string]string) error {
	return nil
}

func (fa *FakeArtifact) ReadAt(b []byte, off int64) (int, error) {
	r := bytes.NewReader(fa.content)
	return r.ReadAt(b, off)
}

func (fa *FakeArtifact) ReadAll() ([]byte, error) {
	size, err := fa.Size()
	if err != nil {
		return nil, err
	}
	if size > fa.sizeLimit {
		return nil, lenses.ErrFileTooLarge
	}
	r := bytes.NewReader(fa.content)
	return io.ReadAll(r)
}

func (fa *FakeArtifact) ReadTail(n int64) ([]byte, error) {
	return nil, nil
}

func (fa *FakeArtifact) UseContext(ctx context.Context) error {
	return nil
}

func (fa *FakeArtifact) ReadAtMost(n int64) ([]byte, error) {
	return nil, nil
}

func TestGetJvd(t *testing.T) {
	emptySkipped := junit.Skipped{}
	failures := []junit.Failure{
		{
			Type:    "failure",
			Message: "failure message 0",
			Value:   " failure value 0 ",
		},
		{
			Type:    "failure",
			Message: "failure message 1",
			Value:   " failure value 1 ",
		},
	}
	errors := []junit.Errored{
		{
			Type:    "error",
			Message: "error message 0",
			Value:   " error value 0 ",
		},
		{
			Type:    "error",
			Message: "error message 1",
			Value:   " error value 1 ",
		},
	}

	tests := []struct {
		name       string
		rawResults [][]byte
		exp        JVD
	}{
		{
			"Failed",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Errored",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<error message="error message 0" type="error"> error value 0 </error>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Errored:   &errors[0],
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Passed",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Failed:  nil,
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Skipped",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<skipped/>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed:   nil,
				Skipped: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
									Skipped:   &emptySkipped,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Flaky: nil,
			},
		}, {
			"Failed and Skipped",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					<testcase classname="fake_class_0" name="fake_test_0">
							<skipped/>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
									Skipped:   &emptySkipped,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Skipped: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
									Skipped:   &emptySkipped,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Flaky: nil,
			},
		}, {
			"Multiple tests in same file",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
						<testcase classname="fake_class_1" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 2,
				Passed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_1",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Failed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Multiple tests in different files",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
				</testsuites>
				`),
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_1" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 2,
				Passed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_1",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Failed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Fail multiple times in same file",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 1" type="failure"> failure value 1 </failure>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[1],
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Passed multiple times in same file",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Failed:  nil,
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			// This is the case where `go test --count=N`, where N>1
			"Passed multiple times in same suite",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Failed:  nil,
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Failed then pass in same file (flaky)",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed:   nil,
				Skipped:  nil,
				Flaky: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
			},
		}, {
			"Pass then fail in same file (flaky)",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed:   nil,
				Skipped:  nil,
				Flaky: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
			},
		}, {
			"Fail multiple times then pass in same file (flaky)",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 1" type="failure"> failure value 1 </failure>
						</testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 1,
				Passed:   nil,
				Failed:   nil,
				Skipped:  nil,
				Flaky: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[1],
								},
							},
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
			},
		}, {
			"Same test in different file",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
				</testsuites>
				`),
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 2,
				Passed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Failed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   &failures[0],
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Skipped: nil,
				Flaky:   nil,
			},
		}, {
			"Sequence of test cases in the artifact file is reflected in the lens",
			[][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="fake_class_0" name="fake_test_0"></testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_1" name="fake_test_1"></testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_2" name="fake_test_2"></testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_3" name="fake_test_3"></testcase>
					</testsuite>
					<testsuite>
						<testcase classname="fake_class_4" name="fake_test_4"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			JVD{
				NumTests: 5,
				Passed: []TestResult{
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_0",
									ClassName: "fake_class_0",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_1",
									ClassName: "fake_class_1",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_2",
									ClassName: "fake_class_2",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_3",
									ClassName: "fake_class_3",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
					{
						Junit: []JunitResult{
							{
								junit.Result{
									Name:      "fake_test_4",
									ClassName: "fake_class_4",
									Failure:   nil,
								},
							},
						},
						Link: "linknotfound.io/404",
					},
				},
				Failed:  nil,
				Skipped: nil,
				Flaky:   nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifacts := make([]api.Artifact, 0)
			for _, rr := range tt.rawResults {
				artifacts = append(artifacts, &FakeArtifact{
					path:      "log.txt",
					content:   rr,
					sizeLimit: 500e6,
				})
			}
			l := Lens{}
			got := l.getJvd(artifacts)
			if diff := cmp.Diff(tt.exp, got); diff != "" {
				t.Fatalf("JVD mismatch, want(-), got(+): \n%s", diff)
			}
		})
	}
}

func TestParseSelector(t *testing.T) {
	tests := []struct {
		name      string
		selector  string
		wantErr   bool
		wantProps []propertyPredicate
	}{
		{
			name:     "name and value",
			selector: "properties/property[@name='lifecycle' and @value='informing']",
			wantProps: []propertyPredicate{
				{name: "lifecycle", value: "informing"},
			},
		},
		{
			name:     "name only",
			selector: "properties/property[@name='lifecycle']",
			wantProps: []propertyPredicate{
				{name: "lifecycle"},
			},
		},
		{
			name:     "value only",
			selector: "properties/property[@value='informing']",
			wantProps: []propertyPredicate{
				{value: "informing"},
			},
		},
		{
			name:     "unsupported selector",
			selector: "classname='foo'",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred, err := parseSelector(tt.selector)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.wantProps, pred.properties, cmp.AllowUnexported(propertyPredicate{})); diff != "" {
				t.Fatalf("predicate mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelectorMatches(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		result   junit.Result
		want     bool
	}{
		{
			name:     "matches property name and value",
			selector: "properties/property[@name='lifecycle' and @value='informing']",
			result: junit.Result{
				Properties: &junit.Properties{
					PropertyList: []junit.Property{
						{Name: "lifecycle", Value: "informing"},
					},
				},
			},
			want: true,
		},
		{
			name:     "does not match different value",
			selector: "properties/property[@name='lifecycle' and @value='informing']",
			result: junit.Result{
				Properties: &junit.Properties{
					PropertyList: []junit.Property{
						{Name: "lifecycle", Value: "blocking"},
					},
				},
			},
			want: false,
		},
		{
			name:     "does not match nil properties",
			selector: "properties/property[@name='lifecycle' and @value='informing']",
			result:   junit.Result{},
			want:     false,
		},
		{
			name:     "matches name only selector",
			selector: "properties/property[@name='lifecycle']",
			result: junit.Result{
				Properties: &junit.Properties{
					PropertyList: []junit.Property{
						{Name: "lifecycle", Value: "anything"},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred, err := parseSelector(tt.selector)
			if err != nil {
				t.Fatalf("failed to parse selector: %v", err)
			}
			got := pred.matches(tt.result)
			if got != tt.want {
				t.Fatalf("matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetJvdWithGroups(t *testing.T) {
	failures := []junit.Failure{
		{
			Type:    "failure",
			Message: "failure message 0",
			Value:   " failure value 0 ",
		},
		{
			Type:    "failure",
			Message: "failure message 1",
			Value:   " failure value 1 ",
		},
	}

	tests := []struct {
		name       string
		rawResults [][]byte
		groups     []GroupConfig
		expFailed  int
		expPassed  int
		expGroups  []struct {
			name    string
			failed  int
			passed  int
			skipped int
		}
	}{
		{
			name: "informing tests go to group with full breakdown",
			rawResults: [][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="cls" name="blocking_test">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
						<testcase classname="cls" name="blocking_pass"></testcase>
						<testcase classname="cls" name="informing_fail">
							<properties>
								<property name="lifecycle" value="informing"/>
							</properties>
							<failure message="failure message 1" type="failure"> failure value 1 </failure>
						</testcase>
						<testcase classname="cls" name="informing_pass">
							<properties>
								<property name="lifecycle" value="informing"/>
							</properties>
						</testcase>
						<testcase classname="cls" name="informing_skip">
							<properties>
								<property name="lifecycle" value="informing"/>
							</properties>
							<skipped/>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			groups: []GroupConfig{
				{
					Name:      "Informing",
					Selector:  "properties/property[@name='lifecycle' and @value='informing']",
					Collapsed: true,
				},
			},
			expFailed: 1,
			expPassed: 1,
			expGroups: []struct {
				name    string
				failed  int
				passed  int
				skipped int
			}{
				{name: "Informing", failed: 1, passed: 1, skipped: 1},
			},
		},
		{
			name: "no groups configured, all tests stay in default",
			rawResults: [][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="cls" name="test1">
							<properties>
								<property name="lifecycle" value="informing"/>
							</properties>
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
						<testcase classname="cls" name="test2"></testcase>
					</testsuite>
				</testsuites>
				`),
			},
			groups:    nil,
			expFailed: 1,
			expPassed: 1,
			expGroups: nil,
		},
		{
			name: "group with no matches produces empty group",
			rawResults: [][]byte{
				[]byte(`
				<testsuites>
					<testsuite>
						<testcase classname="cls" name="test1">
							<failure message="failure message 0" type="failure"> failure value 0 </failure>
						</testcase>
					</testsuite>
				</testsuites>
				`),
			},
			groups: []GroupConfig{
				{
					Name:     "Informing",
					Selector: "properties/property[@name='lifecycle' and @value='informing']",
				},
			},
			expFailed: 1,
			expPassed: 0,
			expGroups: []struct {
				name    string
				failed  int
				passed  int
				skipped int
			}{
				{name: "Informing", failed: 0, passed: 0, skipped: 0},
			},
		},
	}

	_ = failures

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifacts := make([]api.Artifact, 0)
			for _, rr := range tt.rawResults {
				artifacts = append(artifacts, &FakeArtifact{
					path:      "log.txt",
					content:   rr,
					sizeLimit: 500e6,
				})
			}
			l := Lens{}
			got := l.getJvd(artifacts, tt.groups)
			if len(got.Failed) != tt.expFailed {
				t.Errorf("Failed count = %d, want %d", len(got.Failed), tt.expFailed)
			}
			if len(got.Passed) != tt.expPassed {
				t.Errorf("Passed count = %d, want %d", len(got.Passed), tt.expPassed)
			}
			if tt.expGroups == nil {
				if len(got.Groups) != 0 {
					t.Errorf("Groups count = %d, want 0", len(got.Groups))
				}
			} else {
				if len(got.Groups) != len(tt.expGroups) {
					t.Fatalf("Groups count = %d, want %d", len(got.Groups), len(tt.expGroups))
				}
				for i, eg := range tt.expGroups {
					g := got.Groups[i]
					if g.Name != eg.name {
						t.Errorf("Group[%d].Name = %q, want %q", i, g.Name, eg.name)
					}
					if len(g.Failed) != eg.failed {
						t.Errorf("Group[%d].Failed = %d, want %d", i, len(g.Failed), eg.failed)
					}
					if len(g.Passed) != eg.passed {
						t.Errorf("Group[%d].Passed = %d, want %d", i, len(g.Passed), eg.passed)
					}
					if len(g.Skipped) != eg.skipped {
						t.Errorf("Group[%d].Skipped = %d, want %d", i, len(g.Skipped), eg.skipped)
					}
				}
			}
		})
	}
}

func TestTemplate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name               string
		input              JVD
		expectedSubstrings []string
	}{
		{
			name: "Both stdout and stderr get rendered when there is one test",
			input: JVD{NumTests: 1, Failed: []TestResult{{
				Junit: []JunitResult{{
					Result: junit.Result{
						Output: ptr.To("output"),
						Error:  ptr.To("error"),
					},
				}},
			}}},
			expectedSubstrings: []string{
				`<a href="#" class="open-stdout-stderr">open stdout<i class="material-icons" `,
				`<a href="#" class="open-stdout-stderr">open stderr<i class="material-icons"`,
				`output`,
				`error`,
			},
		},
		{
			name: "Both stdout and stderr get rendered when there are multiple tests",
			input: JVD{NumTests: 1, Failed: []TestResult{{
				Junit: []JunitResult{
					{
						Result: junit.Result{
							Output: ptr.To("output"),
							Error:  ptr.To("error"),
						},
					},
					{
						Result: junit.Result{
							Output: ptr.To("output"),
							Error:  ptr.To("error"),
						},
					},
				},
			}}},
			expectedSubstrings: []string{
				`<a href="#" class="open-stdout-stderr">open stdout<i class="material-icons" `,
				`<a href="#" class="open-stdout-stderr">open stderr<i class="material-icons"`,
				`<td class="mdl-data-table__cell--non-numeric test-name">Run #0`,
				`<td class="mdl-data-table__cell--non-numeric test-name">Run #1`,
				`output`,
				`error`,
			},
		},
		{
			name: "Both stdout and stderr get rendered for flaky tests",
			input: JVD{NumTests: 1, Flaky: []TestResult{{
				Junit: []JunitResult{{
					Result: junit.Result{
						Output: ptr.To("output"),
						Error:  ptr.To("error"),
					},
				}},
			}}},
			expectedSubstrings: []string{
				`<a href="#" class="open-stdout-stderr">open stdout<i class="material-icons" `,
				`<a href="#" class="open-stdout-stderr">open stderr<i class="material-icons"`,
				`output`,
				`error`,
			},
		}, {
			name: "Group section shows full breakdown",
			input: JVD{Groups: []GroupResult{{
				Name:      "Informing",
				Collapsed: true,
				NumTests:  2,
				Failed: []TestResult{{
					Junit: []JunitResult{{
						Result: junit.Result{
							Name:      "informing_fail",
							ClassName: "fake_class",
							Failure:   &junit.Failure{Value: "non-blocking failure"},
						},
					}},
				}},
				Passed: []TestResult{{
					Junit: []JunitResult{{
						Result: junit.Result{
							Name:      "informing_pass",
							ClassName: "fake_class",
						},
					}},
				}},
			}}},
			expectedSubstrings: []string{
				`Informing`,
				`1/2 Failed.`,
				`1/2 Passed.`,
				`group-layout`,
				`fake_class: informing_fail`,
				`hidden-tests`,
			},
		},
		{
			name: "Group section expanded when collapsed is false",
			input: JVD{Groups: []GroupResult{{
				Name:      "MyGroup",
				Collapsed: false,
				NumTests:  1,
				Failed: []TestResult{{
					Junit: []JunitResult{{
						Result: junit.Result{
							Name:      "test_1",
							ClassName: "cls",
							Failure:   &junit.Failure{Value: "fail"},
						},
					}},
				}},
			}}},
			expectedSubstrings: []string{
				`1/1 Failed.`,
				`expand_less`,
			},
		},
	}

	tmpl, err := template.ParseFiles("template.html")
	if err != nil {
		t.Fatalf("failed to parse template: %v", err)
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "body", tc.input); err != nil {
				t.Fatalf("failed to execute template: %v", err)
			}
			result := buf.String()

			for _, substring := range tc.expectedSubstrings {
				if !strings.Contains(result, substring) {
					t.Errorf("expected to find substring '%s' in rendered template '%s', wasn't the case", substring, result)
				}
			}
		})
	}
}
