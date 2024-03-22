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
	utilpointer "k8s.io/utils/pointer"

	"k8s.io/test-infra/prow/spyglass/api"
	"k8s.io/test-infra/prow/spyglass/lenses"
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
						Output: utilpointer.String("output"),
						Error:  utilpointer.String("error"),
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
							Output: utilpointer.String("output"),
							Error:  utilpointer.String("error"),
						},
					},
					{
						Result: junit.Result{
							Output: utilpointer.String("output"),
							Error:  utilpointer.String("error"),
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
						Output: utilpointer.String("output"),
						Error:  utilpointer.String("error"),
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
