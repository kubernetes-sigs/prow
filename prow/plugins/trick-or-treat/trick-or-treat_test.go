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

package trickortreat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/github/fakegithub"
)

type fakeSnicker struct {
	err error
}

func (c *fakeSnicker) readImage(log *logrus.Entry) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	return "![fake candy image](fake)", nil
}

func TestReadImage(t *testing.T) {
	img, err := trickOrTreat.readImage(logrus.WithContext(context.Background()))
	if err != nil {
		t.Errorf("Could not read candies from %#v: %v", trickOrTreat, err)
		return
	}
	var found bool
	for _, cand := range candiesImgs {
		if want := fmt.Sprintf("![candy image](%s)", cand); want == img {
			found = true
		}
	}
	if !found {
		t.Fatalf("Image %q not part of curated list of images", img)
	}
}

// Small, unit tests
func TestAll(t *testing.T) {
	var testcases = []struct {
		name          string
		action        github.GenericCommentEventAction
		body          string
		state         string
		pr            bool
		readImgErr    error
		shouldComment bool
		shouldError   bool
	}{
		{
			name:          "failed reading image",
			state:         "open",
			action:        github.GenericCommentActionCreated,
			body:          "/trick-or-treat",
			readImgErr:    errors.New("failed"),
			shouldComment: false,
			shouldError:   true,
		},
		{
			name:          "ignore edited comment",
			state:         "open",
			action:        github.GenericCommentActionEdited,
			body:          "/trick-or-treat",
			shouldComment: false,
			shouldError:   false,
		},
		{
			name:          "leave candy on pr",
			state:         "open",
			action:        github.GenericCommentActionCreated,
			body:          "/trick-or-treat",
			pr:            true,
			shouldComment: true,
			shouldError:   false,
		},
		{
			name:          "leave candy on issue",
			state:         "open",
			action:        github.GenericCommentActionCreated,
			body:          "/trick-or-treat",
			shouldComment: true,
			shouldError:   false,
		},
		{
			name:          "leave candy on issue, trailing space",
			state:         "open",
			action:        github.GenericCommentActionCreated,
			body:          "/trick-or-treat \r",
			shouldComment: true,
			shouldError:   false,
		},
		{
			name:          "Trailing random strings",
			state:         "open",
			action:        github.GenericCommentActionCreated,
			body:          "/trick-or-treat clothes",
			shouldComment: true,
			shouldError:   false,
		},
	}
	for _, tc := range testcases {
		fc := fakegithub.NewFakeClient()
		e := &github.GenericCommentEvent{
			Action:     tc.action,
			Body:       tc.body,
			Number:     5,
			IssueState: tc.state,
			IsPR:       tc.pr,
		}
		err := handle(fc, logrus.WithField("plugin", pluginName), e, &fakeSnicker{tc.readImgErr})
		if !tc.shouldError && err != nil {
			t.Errorf("%s: didn't expect error: %v", tc.name, err)
		} else if tc.shouldError && err == nil {
			t.Errorf("%s: expected an error to occur", tc.name)
		} else if tc.shouldComment && len(fc.IssueComments[5]) != 1 {
			t.Errorf("%s: should have commented.", tc.name)
		} else if tc.shouldComment {
			shouldImage := !tc.shouldError
			body := fc.IssueComments[5][0].Body
			hasImage := strings.Contains(body, "![")
			if hasImage && !shouldImage {
				t.Errorf("%s: unexpected image in %s", tc.name, body)
			} else if !hasImage && shouldImage {
				t.Errorf("%s: no image in %s", tc.name, body)
			}
		} else if !tc.shouldComment && len(fc.IssueComments[5]) != 0 {
			t.Errorf("%s: should not have commented.", tc.name)
		}
	}
}
