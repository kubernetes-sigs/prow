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
	"net/http"
	"path"
	"strings"
)

// normalizeBasePath sanitizes the configured base path to always start with a slash and omit
// trailing slashes (except for the root path).
func normalizeBasePath(basePath string) string {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" {
		return "/"
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	basePath = path.Clean(basePath)
	if basePath == "." {
		return "/"
	}
	return basePath
}

// withBasePath ensures that only requests that begin with the configured base path are handled
// and that downstream handlers only see paths relative to that base path.
func withBasePath(basePath string, handler http.Handler) http.Handler {
	basePath = normalizeBasePath(basePath)
	if basePath == "/" {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed, ok := stripBasePath(basePath, r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = trimmed
		if r.URL.RawPath != "" {
			r.URL.RawPath = trimmed
		}
		handler.ServeHTTP(w, r)
	})
}

func stripBasePath(basePath, requestPath string) (string, bool) {
	if basePath == "/" {
		return requestPath, true
	}
	if requestPath == basePath {
		return "/", true
	}
	if strings.HasPrefix(requestPath, basePath+"/") {
		trimmed := strings.TrimPrefix(requestPath, basePath)
		if trimmed == "" {
			return "/", true
		}
		return trimmed, true
	}
	return "", false
}

// absolutePath returns the absolute URL path for a given relative path by prepending the
// configured base path. The input is expected to be either "/" or begin with a slash.
func (o options) absolutePath(rel string) string {
	if rel == "" {
		return ""
	}

	pathPart := rel
	suffix := ""
	if idx := strings.IndexAny(rel, "?#"); idx >= 0 {
		pathPart = rel[:idx]
		suffix = rel[idx:]
	}
	if pathPart == "" {
		pathPart = "/"
	}
	if !strings.HasPrefix(pathPart, "/") {
		pathPart = "/" + pathPart
	}
	if o.basePath == "/" {
		return pathPart + suffix
	}
	if pathPart == "/" {
		return o.basePath + suffix
	}
	return o.basePath + pathPart + suffix
}

func (o options) absolutePathIfRelative(rel string) string {
	if rel == "" || !strings.HasPrefix(rel, "/") {
		return rel
	}
	return o.absolutePath(rel)
}
