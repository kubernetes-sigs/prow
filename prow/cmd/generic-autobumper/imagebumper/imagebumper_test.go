/*
Copyright 2019 The Kubernetes Authors.

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

package imagebumper

import (
	"fmt"
	"regexp"
	"testing"
)

func TestDeconstructCommit(t *testing.T) {
	cases := []struct {
		name           string
		commit         string
		tag            string
		num            int
		expectedCommit string
	}{
		{
			name: "basically works",
		},
		{
			name:           "just commit works",
			commit:         "deadbeef",
			expectedCommit: "deadbeef",
		},
		{
			name:           "commit drops leading g",
			commit:         "gdeadbeef",
			expectedCommit: "deadbeef",
		},
		{
			name:   "just tag works",
			commit: "v0.0.30",
			tag:    "v0.0.30",
		},
		{
			name:           "commits past tags work",
			commit:         "v0.0.30-14-gdeadbeef",
			tag:            "v0.0.30",
			num:            14,
			expectedCommit: "deadbeef",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, num, commit := DeconstructCommit(tc.commit)
			if tag != tc.tag {
				t.Errorf("DeconstructCommit(%s) got tag %q, want %q", tc.commit, tag, tc.tag)
			}
			if num != tc.num {
				t.Errorf("DeconstructCommit(%s) got tag %d, want %d", tc.commit, num, tc.num)
			}
			if commit != tc.expectedCommit {
				t.Errorf("DeconstructCommit(%s) got commit %q, want %q", tc.commit, commit, tc.expectedCommit)
			}

		})
	}
}

func TestDeconstructTag(t *testing.T) {
	cases := []struct {
		tag     string
		date    string
		commit  string
		variant string
	}{
		{
			tag: "deadbeef",
			// TODO(fejta): commit: "deadbeef",
		},
		{
			tag: "v0.0.30",
			// TODO(fejta): commit: "v0.0.30",
		},
		{
			tag:    "v20190404-65af07d",
			date:   "20190404",
			commit: "65af07d",
		},
		{
			tag:     "v20190330-811f79999-experimental",
			date:    "20190330",
			commit:  "811f79999",
			variant: "-experimental",
		},
		{
			tag:    "latest",
			date:   "atest", // TODO(fejta): empty
			commit: "latest",
		},
		{
			tag:     "latest-experimental",
			date:    "atest", // TODO(fejta): empty
			commit:  "latest",
			variant: "-experimental", // TODO(fejta): no -
		},
		{
			tag:    "v20210125-v0.0.41-8-gcb960c8",
			date:   "20210125",
			commit: "gcb960c8", // TODO(fejta): "v0.0.41-8-gcb960c8",
		},
		{
			tag:     "v20210125-v0.0.41-8-gcb960c8-fancy",
			date:    "20210125",
			commit:  "gcb960c8", // TODO(fejta): "v0.0.41-8-gcb960c8",
			variant: "-fancy",   // TODO(fejta): no -
		},
	}

	for _, tc := range cases {
		t.Run(tc.tag, func(t *testing.T) {
			date, commit, variant := DeconstructTag(tc.tag)
			if date != tc.date {
				t.Errorf("DeconstructTag(%q) got date %s, want %s", tc.tag, date, tc.date)
			}
			if commit != tc.commit {
				t.Errorf("DeconstructTag(%q) got commit %s, want %s", tc.tag, commit, tc.commit)
			}
			if variant != tc.variant {
				t.Errorf("DeconstructTag(%q) got variant %s, want %s", tc.tag, variant, tc.variant)
			}
		})
	}
}

func TestPickBestTag(t *testing.T) {
	tests := []struct {
		name      string
		tag       string
		manifest  manifest
		bestTag   string
		expectErr bool
	}{
		{
			name: "simple lookup",
			tag:  "v20190329-811f7954b",
			manifest: manifest{
				"image1": {
					TimeCreatedMs: "2000",
					Tags:          []string{"v20190404-65af07d"},
				},
				"image2": {
					TimeCreatedMs: "1000",
					Tags:          []string{"v20190329-811f7954b"},
				},
			},
			bestTag: "v20190404-65af07d",
		},
		{
			name: "'latest' overrides date",
			tag:  "v20190329-811f7954b",
			manifest: manifest{
				"image1": {
					TimeCreatedMs: "2000",
					Tags:          []string{"v20190404-65af07d"},
				},
				"image2": {
					TimeCreatedMs: "1000",
					Tags:          []string{"v20190330-811f79999", "latest"},
				},
			},
			bestTag: "v20190330-811f79999",
		},
		{
			name: "tags with suffixes only match other tags with the same suffix",
			tag:  "v20190329-811f7954b-experimental",
			manifest: manifest{
				"image1": {
					TimeCreatedMs: "2000",
					Tags:          []string{"v20190404-65af07d"},
				},
				"image2": {
					TimeCreatedMs: "1000",
					Tags:          []string{"v20190330-811f79999-experimental"},
				},
			},
			bestTag: "v20190330-811f79999-experimental",
		},
		{
			name: "unsuffixed 'latest' has no effect on suffixed tags",
			tag:  "v20190329-811f7954b-experimental",
			manifest: manifest{
				"image1": {
					TimeCreatedMs: "2000",
					Tags:          []string{"v20190404-65af07d", "latest"},
				},
				"image2": {
					TimeCreatedMs: "1000",
					Tags:          []string{"v20190330-811f79999-experimental"},
				},
			},
			bestTag: "v20190330-811f79999-experimental",
		},
		{
			name: "suffixed 'latest' has no effect on unsuffixed tags",
			tag:  "v20190329-811f7954b",
			manifest: manifest{
				"image1": {
					TimeCreatedMs: "2000",
					Tags:          []string{"v20190404-65af07d"},
				},
				"image2": {
					TimeCreatedMs: "1000",
					Tags:          []string{"v20190330-811f79999-experimental", "latest-experimental"},
				},
			},
			bestTag: "v20190404-65af07d",
		},
		{
			name: "'latest' with the correct suffix overrides date",
			tag:  "v20190329-811f7954b-experimental",
			manifest: manifest{
				"image1": {
					TimeCreatedMs: "2000",
					Tags:          []string{"v20190404-65af07d-experimental"},
				},
				"image2": {
					TimeCreatedMs: "1000",
					Tags:          []string{"v20190330-811f79999-experimental", "latest-experimental"},
				},
			},
			bestTag: "v20190330-811f79999-experimental",
		},
		{
			name: "it is an error when no tags are found",
			tag:  "v20190329-811f7954b-master",
			manifest: manifest{
				"image1": {
					TimeCreatedMs: "2000",
					Tags:          []string{"v20190404-65af07d-experimental"},
				},
				"image2": {
					TimeCreatedMs: "1000",
					Tags:          []string{"v20190330-811f79999-experimental", "latest-experimental"},
				},
			},
			expectErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tagParts := tagRegexp.FindStringSubmatch(test.tag)
			bestTag, err := pickBestTag(tagParts, test.manifest)
			if err != nil {
				if !test.expectErr {
					t.Fatalf("Unexpected error: %v", err)
				}
				return
			}
			if test.expectErr {
				t.Fatalf("Expected an error, but got result %q", bestTag)
			}
			if bestTag != test.bestTag {
				t.Fatalf("Expected tag %q, but got %q instead", test.bestTag, bestTag)
			}
		})
	}
}

func TestUpdateAllTags(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		expectedResult string
		imageFilter    *regexp.Regexp
		newTags        map[string]string
	}{
		{
			name:           "file with no images does nothing",
			content:        "this is just a normal file",
			expectedResult: "this is just a normal file",
		},
		{
			name:           "file that has only an image replaces the image",
			content:        "gcr.io/k8s-testimages/some-image:v20190404-12345678",
			expectedResult: "gcr.io/k8s-testimages/some-image:v20190405-123456789",
			newTags: map[string]string{
				"gcr.io/k8s-testimages/some-image:v20190404-12345678": "v20190405-123456789",
			},
		},
		{
			name:           "file that has content before and after an image still has it later",
			content:        `{"image": "gcr.io/k8s-testimages/some-image:v20190404-12345678"}`,
			expectedResult: `{"image": "gcr.io/k8s-testimages/some-image:v20190405-123456789"}`,
			newTags: map[string]string{
				"gcr.io/k8s-testimages/some-image:v20190404-12345678": "v20190405-123456789",
			},
		},
		{
			name:           "file that has multiple different images replaces both of them",
			content:        `{"images": ["gcr.io/k8s-testimages/some-image:v20190404-12345678-master", "gcr.io/k8s-testimages/some-image:v20190404-12345678-experimental"]}`,
			expectedResult: `{"images": ["gcr.io/k8s-testimages/some-image:v20190405-123456789-master", "gcr.io/k8s-testimages/some-image:v20190405-123456789-experimental"]}`,
			newTags: map[string]string{
				"gcr.io/k8s-testimages/some-image:v20190404-12345678-master":       "v20190405-123456789-master",
				"gcr.io/k8s-testimages/some-image:v20190404-12345678-experimental": "v20190405-123456789-experimental",
			},
		},
		{
			name:           "file with an error image is still otherwise updated",
			content:        `{"images": ["gcr.io/k8s-testimages/some-image:0.2", "gcr.io/k8s-testimages/some-image:v20190404-12345678"]}`,
			expectedResult: `{"images": ["gcr.io/k8s-testimages/some-image:0.2", "gcr.io/k8s-testimages/some-image:v20190405-123456789"]}`,
			newTags: map[string]string{
				"gcr.io/k8s-testimages/some-image:v20190404-12345678": "v20190405-123456789",
			},
		},
		{
			name:           "gcr subdomains are supported",
			content:        `{"images": ["eu.gcr.io/k8s-testimages/some-image:v20190404-12345678"]}`,
			expectedResult: `{"images": ["eu.gcr.io/k8s-testimages/some-image:v20190405-123456789"]}`,
			newTags: map[string]string{
				"eu.gcr.io/k8s-testimages/some-image:v20190404-12345678": "v20190405-123456789",
			},
		},
		{
			name:           "AR multi-regional subdomains are supported",
			content:        `{"images": ["us-docker.pkg.dev/k8s-testimages/some-image:v20190404-12345678"]}`,
			expectedResult: `{"images": ["us-docker.pkg.dev/k8s-testimages/some-image:v20190405-123456789"]}`,
			newTags: map[string]string{
				"us-docker.pkg.dev/k8s-testimages/some-image:v20190404-12345678": "v20190405-123456789",
			},
		},
		{
			name:           "AR regional subdomains are supported",
			content:        `{"images": ["us-central1-docker.pkg.dev/k8s-testimages/some-image:v20190404-12345678"]}`,
			expectedResult: `{"images": ["us-central1-docker.pkg.dev/k8s-testimages/some-image:v20190405-123456789"]}`,
			newTags: map[string]string{
				"us-central1-docker.pkg.dev/k8s-testimages/some-image:v20190404-12345678": "v20190405-123456789",
			},
		},
		{
			name:           "images not matching the filter regex are not updated",
			content:        `{"images": ["gcr.io/k8s-prow/prow-thing:v20190404-12345678", "gcr.io/k8s-testimages/some-image:v20190404-12345678"]}`,
			expectedResult: `{"images": ["gcr.io/k8s-prow/prow-thing:v20190404-12345678", "gcr.io/k8s-testimages/some-image:v20190405-123456789"]}`,
			newTags: map[string]string{
				"gcr.io/k8s-prow/prow-thing:v20190404-12345678":       "v20190405-123456789",
				"gcr.io/k8s-testimages/some-image:v20190404-12345678": "v20190405-123456789",
			},
			imageFilter: regexp.MustCompile("gcr.io/k8s-testimages"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tagPicker := func(imageHost string, imageName string, imageTag string) (string, error) {
				result, ok := test.newTags[imageHost+"/"+imageName+":"+imageTag]
				if !ok {
					return "", fmt.Errorf("unknown image %s/%s:%s", imageHost, imageName, imageTag)
				}
				return result, nil
			}

			newContent := updateAllTags(tagPicker, []byte(test.content), test.imageFilter)
			if test.expectedResult != string(newContent) {
				t.Fatalf("Expected content:\n%s\n\nActual content:\n%s\n\n", test.expectedResult, string(newContent))
			}
		})
	}
}
