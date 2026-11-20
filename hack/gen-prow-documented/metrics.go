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

// Metric documentation is extracted from Go source without running metric constructors.
package main

import (
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	docPath     = "site/content/en/docs/metrics/_index.md"
	startMarker = "<!-- BEGIN GENERATED METRICS -->"
	endMarker   = "<!-- END GENERATED METRICS -->"
)

type metric struct {
	source string
	kind   string
	name   string
	help   string
	line   int
	labels []string
}

func genMetrics(root string) error {
	metrics, err := collect(root)
	if err != nil {
		return err
	}
	file := filepath.Join(root, docPath)
	old, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	updated, err := replaceTable(old, render(metrics))
	if err != nil {
		return err
	}
	return os.WriteFile(file, updated, 0644)
}

func collect(root string) ([]metric, error) {
	sources := metricSourceFiles{
		fset:     token.NewFileSet(),
		packages: make(map[string][]*ast.File),
	}
	for _, subtree := range []string{"cmd", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, subtree), sources.walk)
		if err != nil {
			return nil, err
		}
	}

	var metrics []metric
	// Sort packages as well as rows so errors are reproducible too.
	dirs := make([]string, 0, len(sources.packages))
	for dir := range sources.packages {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		definitions := map[string]ast.Expr{}
		for _, file := range sources.packages[dir] {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range value.Names {
						if i < len(value.Values) {
							definitions[name.Name] = value.Values[i]
						}
					}
				}
			}
		}
		for _, file := range sources.packages[dir] {
			rows, err := fileMetrics(root, sources.fset, file, definitions)
			if err != nil {
				return nil, err
			}
			metrics = append(metrics, rows...)
		}
	}
	slices.SortFunc(metrics, compareMetrics)
	return metrics, nil
}

type metricSourceFiles struct {
	fset     *token.FileSet
	packages map[string][]*ast.File
}

func (sources *metricSourceFiles) walk(file string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if entry.IsDir() {
		if entry.Name() == "testdata" || entry.Name() == "vendor" {
			return filepath.SkipDir
		}
		return nil
	}
	if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
		return nil
	}
	parsed, err := parser.ParseFile(sources.fset, file, nil, 0)
	if err != nil {
		return err
	}
	dir := filepath.Dir(file)
	sources.packages[dir] = append(sources.packages[dir], parsed)
	return nil
}

func compareMetrics(a, b metric) int {
	if order := cmp.Compare(filepath.Dir(a.source), filepath.Dir(b.source)); order != 0 {
		return order
	}
	if order := cmp.Compare(a.name, b.name); order != 0 {
		return order
	}
	if order := cmp.Compare(a.source, b.source); order != 0 {
		return order
	}
	return cmp.Compare(a.line, b.line)
}

func fileMetrics(root string, fset *token.FileSet, file *ast.File, definitions map[string]ast.Expr) ([]metric, error) {
	imports := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid import path: %w", fset.Position(spec.Pos()), err)
		}
		if path != "github.com/prometheus/client_golang/prometheus" && path != "github.com/prometheus/client_golang/prometheus/promauto" {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." {
			return nil, fmt.Errorf("%s: dot imports of Prometheus are not supported", fset.Position(spec.Pos()))
		}
		imports[name] = true
	}
	visitor := metricVisitor{
		root:        root,
		fset:        fset,
		imports:     imports,
		definitions: definitions,
	}
	ast.Inspect(file, visitor.visit)
	return visitor.rows, visitor.err
}

type metricVisitor struct {
	root        string
	fset        *token.FileSet
	imports     map[string]bool
	definitions map[string]ast.Expr
	rows        []metric
	err         error
}

func (visitor *metricVisitor) visit(node ast.Node) bool {
	if visitor.err != nil {
		return false
	}
	if function, ok := node.(*ast.FuncDecl); ok {
		source, err := filepath.Rel(visitor.root, visitor.fset.Position(function.Pos()).Filename)
		if err != nil {
			visitor.err = err
			return false
		}
		// These two helpers take metric names from their callers and are
		// documented separately. Still inspect other declarations in this file.
		if filepath.ToSlash(source) == "pkg/metrics/http.go" && (function.Name.Name == "ErrorRate" || function.Name.Name == "histogram") {
			return false
		}
	}
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return true
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return true
	}
	if !visitor.imports[importQualifier(selector.X)] {
		return true
	}
	kind := metricKind(selector.Sel.Name)
	if kind == "" {
		return true
	}
	row, err := parseMetric(kind, call, visitor.definitions)
	position := visitor.fset.Position(call.Pos())
	if err != nil {
		visitor.err = fmt.Errorf("%s: %w", position, err)
		return false
	}
	rel, err := filepath.Rel(visitor.root, position.Filename)
	if err != nil {
		visitor.err = err
		return false
	}
	row.source = filepath.ToSlash(rel)
	row.line = position.Line
	visitor.rows = append(visitor.rows, row)
	return true
}

func metricKind(constructor string) string {
	switch constructor {
	case "NewCounter", "NewCounterVec", "NewCounterFunc":
		return "Counter"
	case "NewGauge", "NewGaugeVec", "NewGaugeFunc":
		return "Gauge"
	case "NewHistogram", "NewHistogramVec":
		return "Histogram"
	case "NewSummary", "NewSummaryVec":
		return "Summary"
	default:
		return ""
	}
}

// Follow selectors and factory calls, including promauto.With(registry).NewGauge
// and prometheus.V2.NewGaugeVec. Unsupported options must fail rather than be lost.
func importQualifier(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return importQualifier(value.X)
	case *ast.CallExpr:
		return importQualifier(value.Fun)
	}
	return ""
}

func parseMetric(kind string, call *ast.CallExpr, definitions map[string]ast.Expr) (metric, error) {
	row := metric{kind: kind}
	if len(call.Args) == 0 {
		return row, fmt.Errorf("missing metric options")
	}
	options, err := literal(call.Args[0], definitions)
	if err != nil {
		return row, err
	}
	fields := map[string]ast.Expr{}
	for _, element := range options.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return row, fmt.Errorf("metric options must use named fields")
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok {
			return row, fmt.Errorf("invalid metric option name")
		}
		fields[key.Name] = field.Value
	}
	var parts []string
	for _, field := range []string{"Namespace", "Subsystem", "Name", "Help"} {
		value := ""
		if expression, ok := fields[field]; ok {
			value, err = stringValue(expression, definitions, map[ast.Expr]bool{})
			if err != nil {
				return row, fmt.Errorf("%s: %w", field, err)
			}
		}
		if field == "Help" {
			row.help = value
		} else if value != "" {
			parts = append(parts, value)
		}
		if field == "Name" && value == "" {
			return row, fmt.Errorf("empty metric name")
		}
	}
	row.name = strings.Join(parts, "_")
	if strings.HasSuffix(call.Fun.(*ast.SelectorExpr).Sel.Name, "Vec") {
		if len(call.Args) < 2 {
			return row, fmt.Errorf("missing vector labels")
		}
		labels, err := literal(call.Args[1], definitions)
		if err != nil {
			return row, fmt.Errorf("labels: %w", err)
		}
		for _, expression := range labels.Elts {
			label, err := stringValue(expression, definitions, map[ast.Expr]bool{})
			if err != nil {
				return row, fmt.Errorf("label: %w", err)
			}
			row.labels = append(row.labels, label)
		}
	}
	if expression, ok := fields["ConstLabels"]; ok {
		labels, err := literal(expression, definitions)
		if err != nil {
			return row, fmt.Errorf("ConstLabels: %w", err)
		}
		var keys []string
		for _, element := range labels.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				return row, fmt.Errorf("invalid ConstLabels entry")
			}
			key, err := stringValue(field.Key, definitions, map[ast.Expr]bool{})
			if err != nil {
				return row, err
			}
			keys = append(keys, key)
		}
		slices.Sort(keys)
		row.labels = append(row.labels, keys...)
	}
	return row, nil
}

// Resolve aliases, using parser scope information for local declarations and
// package definitions for references to declarations in other source files.
func resolve(expression ast.Expr, definitions map[string]ast.Expr, seen map[ast.Expr]bool) (ast.Expr, error) {
	if seen[expression] {
		return nil, fmt.Errorf("cyclic metric definition")
	}
	seen[expression] = true
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return resolve(value.X, definitions, seen)
	case *ast.Ident:
		if value.Name == "nil" {
			return &ast.CompositeLit{}, nil
		}
		if value.Obj != nil {
			if spec, ok := value.Obj.Decl.(*ast.ValueSpec); ok {
				for i, name := range spec.Names {
					if name.Name == value.Name && i < len(spec.Values) {
						return resolve(spec.Values[i], definitions, seen)
					}
				}
			}
		} else if definition, ok := definitions[value.Name]; ok {
			return resolve(definition, definitions, seen)
		}
		return nil, fmt.Errorf("cannot resolve %q statically", value.Name)
	default:
		return expression, nil
	}
}

func literal(expression ast.Expr, definitions map[string]ast.Expr) (*ast.CompositeLit, error) {
	resolved, err := resolve(expression, definitions, map[ast.Expr]bool{})
	if err != nil {
		return nil, err
	}
	value, ok := resolved.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("expected a literal metric definition")
	}
	return value, nil
}

func stringValue(expression ast.Expr, definitions map[string]ast.Expr, seen map[ast.Expr]bool) (string, error) {
	resolved, err := resolve(expression, definitions, seen)
	if err != nil {
		return "", err
	}
	switch value := resolved.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			return strconv.Unquote(value.Value)
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			// Each operand has its own path through the definitions: using the
			// same constant in both operands is not a cycle.
			left, err := stringValue(value.X, definitions, maps.Clone(seen))
			if err != nil {
				return "", err
			}
			right, err := stringValue(value.Y, definitions, maps.Clone(seen))
			if err != nil {
				return "", err
			}
			return left + right, nil
		}
	}
	return "", fmt.Errorf("expected a static string")
}

func render(metrics []metric) string {
	var table strings.Builder
	table.WriteString("| Source | Type | Metric | Labels | Description |\n|---|---|---|---|---|\n")
	for _, row := range metrics {
		fmt.Fprintf(&table, "| [%s](https://github.com/kubernetes-sigs/prow/blob/main/%s#L%d) | %s | `%s` | %s | %s |\n",
			filepath.ToSlash(filepath.Dir(row.source)), row.source, row.line, row.kind, row.name,
			escapeCell(strings.Join(row.labels, ", ")), escapeCell(row.help))
	}
	return table.String()
}

func escapeCell(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = html.EscapeString(value)
	return strings.ReplaceAll(value, "|", "&#124;")
}

func replaceTable(doc []byte, table string) ([]byte, error) {
	start, end := bytes.Index(doc, []byte(startMarker)), bytes.Index(doc, []byte(endMarker))
	if start < 0 || end < start || bytes.Count(doc, []byte(startMarker)) != 1 || bytes.Count(doc, []byte(endMarker)) != 1 {
		return nil, fmt.Errorf("metrics documentation must contain exactly one ordered pair of generation markers")
	}
	updated := append([]byte{}, doc[:start+len(startMarker)]...)
	updated = append(updated, []byte("\n\n<!-- Generated by go run ./hack/gen-prow-documented; do not edit this table. -->\n\n"+table+"\n")...)
	return append(updated, doc[end:]...), nil
}
