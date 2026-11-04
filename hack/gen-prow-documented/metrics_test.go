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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, root, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

type collectionTestCase struct {
	name          string
	files         map[string]string
	want          []metric
	wantError     string
	wantTableText []string
}

func TestCollect(t *testing.T) {
	testCases := []collectionTestCase{
		{
			name: "metric definitions and aliases",
			files: map[string]string{
				"cmd/example/main.go": `package main
import "github.com/prometheus/client_golang/prometheus"
import "github.com/prometheus/client_golang/prometheus/promauto"

func currentLoad() float64 {
	return 0
}

var gauge = promauto.With(prometheus.DefaultRegisterer).NewGaugeFunc(
	prometheus.GaugeOpts{
		Name: "load",
	},
	currentLoad,
)
`,
				"pkg/example/labels.go": `package example
const namespace = "prow"
const prefix = "x"
var metricLabels = []string{"method", "status"}
`,
				"pkg/example/metrics.go": `package example
import prom "github.com/prometheus/client_golang/prometheus"

var options = prom.CounterOpts{
	Namespace: namespace,
	Subsystem: "api",
	Name:      prefix + prefix + "requests",
	Help:      "GET | POST\n<requests>",
	ConstLabels: prom.Labels{
		"zone":    "east",
		"cluster": "test",
	},
}
var counter = prom.NewCounterVec(options, metricLabels)
var histogram = prom.NewHistogram(prom.HistogramOpts{
	Name: "duration",
	Help: "Duration.",
})
var summary = prom.NewSummaryVec(prom.SummaryOpts{
	Name: "size",
}, nil)
var gauge = prom.NewGauge(prom.GaugeOpts{
	Name: "queue",
})
`,
				"pkg/other/metrics.go": `package other
import prometheus "example.com/not-prometheus"
var unrelated = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "ignored",
})
`,
				"pkg/example/metrics_test.go":     "deliberately invalid Go, which must not be parsed",
				"pkg/example/testdata/ignored.go": "deliberately invalid Go, which must not be parsed",
			},
			want: []metric{
				{
					source: "cmd/example/main.go",
					kind:   "Gauge",
					name:   "load",
				},
				{
					source: "pkg/example/metrics.go",
					kind:   "Histogram",
					name:   "duration",
					help:   "Duration.",
				},
				{
					source: "pkg/example/metrics.go",
					kind:   "Counter",
					name:   "prow_api_xxrequests",
					help:   "GET | POST\n<requests>",
					labels: []string{"method", "status", "cluster", "zone"},
				},
				{
					source: "pkg/example/metrics.go",
					kind:   "Gauge",
					name:   "queue",
				},
				{
					source: "pkg/example/metrics.go",
					kind:   "Summary",
					name:   "size",
				},
			},
			wantTableText: []string{
				"GET &#124; POST &lt;requests&gt;",
				"method, status, cluster, zone",
				"https://github.com/kubernetes-sigs/prow/blob/main/pkg/example/metrics.go",
			},
		},
		{
			name: "skip HTTP helpers but retain static metrics in the same file",
			files: map[string]string{
				"pkg/metrics/http.go": `package metrics
import "github.com/prometheus/client_golang/prometheus"

func ErrorRate(prefix string) {
	prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_errors",
	}, []string{"error"})
}

func histogram(name, help string) {
	prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: name,
		Help: help,
	}, []string{"path"})
}

var metric = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "static_metric",
})
`,
			},
			want: []metric{
				{
					source: "pkg/metrics/http.go",
					kind:   "Counter",
					name:   "static_metric",
				},
			},
		},
		{
			name: "dynamic name",
			files: map[string]string{
				"cmd/example/main.go": `package main
import "github.com/prometheus/client_golang/prometheus"

func metric(name string) {
	prometheus.NewCounter(prometheus.CounterOpts{
		Name: name,
	})
}
`,
			},
			wantError: `Name: cannot resolve "name" statically`,
		},
		{
			name: "dynamic labels",
			files: map[string]string{
				"cmd/example/main.go": `package main
import "github.com/prometheus/client_golang/prometheus"

func metric(labels []string) {
	prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "requests",
	}, labels)
}
`,
			},
			wantError: `labels: cannot resolve "labels" statically`,
		},
		{
			name: "cyclic name",
			files: map[string]string{
				"cmd/example/main.go": `package main
import "github.com/prometheus/client_golang/prometheus"

var name = name + "suffix"
var metric = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: name,
})
`,
			},
			wantError: "cyclic metric definition",
		},
		{
			name: "empty name",
			files: map[string]string{
				"cmd/example/main.go": `package main
import "github.com/prometheus/client_golang/prometheus"

var metric = prometheus.NewGauge(prometheus.GaugeOpts{})
`,
			},
			wantError: "empty metric name",
		},
		{
			name: "options function",
			files: map[string]string{
				"cmd/example/main.go": `package main
import "github.com/prometheus/client_golang/prometheus"

var metric = prometheus.NewGauge(options())
`,
			},
			wantError: "expected a literal metric definition",
		},
		{
			name: "shadowed package name",
			files: map[string]string{
				"cmd/example/main.go": `package main
import "github.com/prometheus/client_golang/prometheus"

const name = "package"

func metric(name string) {
	prometheus.NewGauge(prometheus.GaugeOpts{
		Name: name,
	})
}
`,
			},
			wantError: `cannot resolve "name" statically`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.run)
	}
}

func (tc collectionTestCase) run(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"cmd", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range tc.files {
		writeFixture(t, root, name, content)
	}
	rows, err := collect(root)
	if tc.wantError != "" {
		if err == nil {
			t.Fatalf("expected error containing %q", tc.wantError)
		}
		if !strings.Contains(err.Error(), tc.wantError) {
			t.Fatalf("error = %v, want %q", err, tc.wantError)
		}
		if !strings.Contains(err.Error(), "main.go:") {
			t.Errorf("error does not include the source location: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		if rows[i].line == 0 {
			t.Errorf("metric %q has no source line", rows[i].name)
		}
		rows[i].line = 0
	}
	if diff := cmp.Diff(tc.want, rows, cmp.AllowUnexported(metric{})); diff != "" {
		t.Fatalf("unexpected metrics (-want +got):\n%s", diff)
	}
	table := render(rows)
	for _, text := range tc.wantTableText {
		if !strings.Contains(table, text) {
			t.Errorf("table does not contain %q:\n%s", text, table)
		}
	}
}

func TestGenMetrics(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "cmd/example/main.go", `package main
import "github.com/prometheus/client_golang/prometheus"

var counter = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "requests",
	Help: "Requests.",
})
`)
	writeFixture(t, root, "pkg/empty/empty.go", "package empty")
	prefix := "---\ntitle: Metrics\n---\nProse before.\n"
	suffix := "\nProse after.\n"
	original := prefix + startMarker + "\nold table\n" + endMarker + suffix
	writeFixture(t, root, docPath, original)
	if err := genMetrics(root); err != nil {
		t.Fatal(err)
	}
	generated := readFixture(t, root, docPath)
	if !bytes.HasPrefix(generated, []byte(prefix+startMarker)) {
		t.Fatalf("generated document lost its preceding prose:\n%s", generated)
	}
	if !bytes.HasSuffix(generated, []byte(endMarker+suffix)) {
		t.Fatalf("generated document lost its following prose:\n%s", generated)
	}
	if !bytes.Contains(generated, []byte("`requests`")) {
		t.Fatalf("generated document is missing the metric:\n%s", generated)
	}
	if err := genMetrics(root); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFixture(t, root, docPath), generated) {
		t.Fatal("generation is not idempotent")
	}
}

type invalidMarkersTestCase struct {
	name      string
	document  string
	wantError bool
}

func TestReplaceTableRejectsInvalidMarkers(t *testing.T) {
	testCases := []invalidMarkersTestCase{
		{
			name:      "missing markers",
			document:  "",
			wantError: true,
		},
		{
			name:      "reversed markers",
			document:  endMarker + startMarker,
			wantError: true,
		},
		{
			name:      "duplicate start marker",
			document:  startMarker + endMarker + startMarker,
			wantError: true,
		},
		{
			name:      "duplicate end marker",
			document:  startMarker + endMarker + endMarker,
			wantError: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.run)
	}
}

func (tc invalidMarkersTestCase) run(t *testing.T) {
	_, err := replaceTable([]byte(tc.document), "table")
	if (err != nil) != tc.wantError {
		t.Fatalf("replaceTable() error = %v, want error = %t", err, tc.wantError)
	}
}
