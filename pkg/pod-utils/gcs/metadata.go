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

package gcs

import (
	"mime"
	"strings"

	"sigs.k8s.io/prow/pkg/io"
)

// WriterOptionsFromFileName guesses file attributes from the filename
// and returns the writerOptions and a simplified filename.  For example,
// build-log.txt.gz would be:
//
//	Content-Type: text/plain; charset=utf-8
//	Content-Encoding: gzip
//
// and the simplified filename would be build-log.txt (excluding the
// content encoding extension).
func WriterOptionsFromFileName(filename string) (string, io.WriterOptions) {
	attrs := io.WriterOptions{}
	segments := strings.Split(filename, ".")
	index := len(segments) - 1
	segment := segments[index]

	// https://www.iana.org/assignments/http-parameters/http-parameters.xhtml#content-coding
	isGzip := segment == "gz" || segment == "gzip"
	// A .tar.gz is a gzipped archive, not a gzip-encoded .tar: declaring
	// Content-Encoding: gzip would store it as a bare .tar and have GCS
	// decompress it on read, so leave both the name and the bytes alone.
	isTarball := isGzip && index > 0 && segments[index-1] == "tar"

	if isGzip && !isTarball {
		attrs.ContentEncoding = new("gzip")

		if index == 0 {
			segment = ""
		} else {
			filename = filename[:len(filename)-len(segment)-1]
			index -= 1
			segment = segments[index]
		}
	}

	if !isTarball && segment != "" {
		if mediaType := mime.TypeByExtension("." + segment); mediaType != "" {
			attrs.ContentType = new(mediaType)
		}
	}

	if attrs.ContentType == nil && isGzip {
		attrs.ContentType = new("application/gzip")
		attrs.ContentEncoding = nil
	}

	return filename, attrs
}
