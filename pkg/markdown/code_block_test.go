/*
Copyright 2025 The Kubernetes Authors.

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

package markdown_test

import (
	"strings"
	"testing"

	"sigs.k8s.io/prow/pkg/markdown"
)

type codeBlockTestCase struct {
	testName     string
	origText     string
	expectedText string
}

const (
	singleLineToStay = "should stay"
	multiLinesToStay = `should stay
this one should stay as well
and this one too
`

	singleLineCodeBlock = "```" + `
should be filtered out
` + "```"
	multiLinesCodeBlock = "```" + `
should be filtered out
this one should be filtered out as well
and this one too
` + "```\n"
	codeBlockWithLanguage = "```shell" + `
should be filtered out
this one should be filtered out as well
and this one too
` + "```\n"
)

func TestDropCodeBlock(t *testing.T) {
	tests := []codeBlockTestCase{
		{
			testName:     "no code block, single line",
			origText:     singleLineToStay,
			expectedText: singleLineToStay,
		},
		{
			testName:     "no code block, multi-lines",
			origText:     multiLinesToStay,
			expectedText: multiLinesToStay,
		},
		{
			testName:     "one single-line block",
			origText:     singleLineCodeBlock,
			expectedText: "",
		},
		{
			testName:     "one multi-line block",
			origText:     multiLinesCodeBlock,
			expectedText: "",
		},
		{
			testName:     "one multi-line tilda block",
			origText:     strings.ReplaceAll(multiLinesCodeBlock, "```", "~~~"),
			expectedText: "",
		},
		{
			testName:     "one multi-line block, wrapped with text",
			origText:     multiLinesToStay + multiLinesCodeBlock + multiLinesToStay,
			expectedText: multiLinesToStay + multiLinesToStay,
		},
		{
			testName: "few multi-line blocks",
			origText: multiLinesCodeBlock +
				multiLinesCodeBlock +
				multiLinesCodeBlock +
				multiLinesCodeBlock,
			expectedText: "",
		},
		{
			testName: "few multi-line blocks, no new line at the end",
			origText: multiLinesCodeBlock +
				multiLinesCodeBlock +
				multiLinesCodeBlock +
				multiLinesCodeBlock[:len(multiLinesCodeBlock)-1],
			expectedText: "",
		},
		{
			testName: "few multi-line blocks, some wrapped with test",
			origText: singleLineToStay + "\n" +
				multiLinesCodeBlock +
				multiLinesCodeBlock +
				multiLinesToStay +
				multiLinesCodeBlock +
				multiLinesToStay +
				multiLinesCodeBlock,
			expectedText: singleLineToStay + "\n" +
				multiLinesToStay +
				multiLinesToStay,
		},
		{
			testName: "few multi-line tilda blocks, some wrapped with test",
			origText: strings.ReplaceAll(singleLineToStay+"\n"+
				multiLinesCodeBlock+
				multiLinesCodeBlock+
				multiLinesToStay+
				multiLinesCodeBlock+
				multiLinesToStay+
				multiLinesCodeBlock, "```", "~~~"),
			expectedText: singleLineToStay + "\n" +
				multiLinesToStay +
				multiLinesToStay,
		},
		{
			testName: "should filter out code blocks with language",
			origText: singleLineToStay + "\n" +
				multiLinesCodeBlock +
				codeBlockWithLanguage +
				multiLinesToStay +
				multiLinesCodeBlock +
				multiLinesToStay +
				codeBlockWithLanguage,
			expectedText: singleLineToStay + "\n" +
				multiLinesToStay +
				multiLinesToStay,
		},
		{
			testName: "invalid block: extra test at the end",
			origText: "```\n" +
				"this one is here to stay\n" +
				"``` extra text",
			expectedText: "```\n" +
				"this one is here to stay\n" +
				"``` extra text",
		},
		{
			testName: "invalid block: extra test at the end",
			origText: "```\n" +
				"this one is here to stay\n" +
				"``` extra text\n",
			expectedText: "```\n" +
				"this one is here to stay\n" +
				"``` extra text\n",
		},
		{
			testName: "invalid block: not closed",
			origText: "```\n" +
				"this one is here to stay",
			expectedText: "```\n" +
				"this one is here to stay",
		},
	}

	for _, test := range tests {
		t.Run(test.testName, func(t *testing.T) {
			result := markdown.DropCodeBlock(test.origText)
			if result != test.expectedText {
				t.Errorf("for the original text of\n%s\n\nexpected: %q\ngot:      %q", test.origText, test.expectedText, result)
			}
		})
	}
}
