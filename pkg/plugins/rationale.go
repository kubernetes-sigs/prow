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

package plugins

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type rationaleCommand struct {
	// matcher matches the command line. Its first group captures any
	// arguments that follow the command on the same line.
	matcher *regexp.Regexp
	// splitArgs separates the command's own arguments from a same-line
	// rationale.
	splitArgs func(args string) (commandArgs, rationale string)
	example   string
	hint      string
}

var rationaleCommands = map[string]rationaleCommand{
	"retest": {
		// Without a policy, pjutil.RetestRe rejects any trailing text.
		matcher:   regexp.MustCompile(`^/retest(?:[ \t]+([^\r\n]*))?\s*$`),
		splitArgs: allArgsAreRationale,
		example:   "/retest Retrying after the CI setup network timeout was resolved; failure is unrelated to PR changes.",
	},
	"retest-required": {
		// Without a policy, pjutil.RetestRequiredRe rejects any trailing text.
		matcher:   regexp.MustCompile(`^/retest-required(?:[ \t]+([^\r\n]*))?\s*$`),
		splitArgs: allArgsAreRationale,
		example:   "/retest-required Retrying required jobs after the image registry outage was resolved.",
	},
	"override": {
		// Keep this syntax in sync with override.overrideRe.
		matcher:   regexp.MustCompile(`(?i)^/override(?: ([^\r\n]+))?[\r\n]?$`),
		splitArgs: splitOverrideArgs,
		example:   "/override pull-e2e-aws -- Overriding after the e2e infrastructure outage was confirmed.",
		hint:      "Separate the contexts from the rationale with ` -- `.",
	},
}

// RationaleErrorKind describes why rationale validation failed.
type RationaleErrorKind string

const (
	RationaleErrorMissing         RationaleErrorKind = "missing"
	RationaleErrorTooShort        RationaleErrorKind = "too_short"
	RationaleErrorStartsWithSlash RationaleErrorKind = "starts_with_slash"
	RationaleErrorValidation      RationaleErrorKind = "validation_error"
)

// RationaleValidationError describes a user-facing rationale validation failure.
type RationaleValidationError struct {
	Kind      RationaleErrorKind
	Count     int
	MinLength int
	Cause     error
}

func (e *RationaleValidationError) Error() string {
	switch e.Kind {
	case RationaleErrorMissing:
		return "a non-empty rationale is required"
	case RationaleErrorTooShort:
		return fmt.Sprintf("rationale has %d non-whitespace code points, need at least %d", e.Count, e.MinLength)
	case RationaleErrorStartsWithSlash:
		return "rationale must not start with /"
	case RationaleErrorValidation:
		if e.Cause != nil {
			return fmt.Sprintf("rationale validation failed: %v", e.Cause)
		}
		return "rationale validation failed"
	default:
		return "unknown rationale validation error"
	}
}

func (e *RationaleValidationError) Unwrap() error {
	return e.Cause
}

// ParseRationale returns the rationale for every line that matches the
// protected command, because the plugins act on every occurrence. A rationale
// on the same line as the command wins. Otherwise the rationale is the
// non-blank text on the following lines, up to the next line that starts with
// "/" or a code fence. A rationale must not start with "/", so other commands
// cannot pass as one.
func ParseRationale(body, command string) ([]string, error) {
	rationaleCommand, ok := rationaleCommandByToken(command)
	if !ok {
		return nil, &RationaleValidationError{
			Kind:  RationaleErrorValidation,
			Cause: fmt.Errorf("unsupported protected command %q", command),
		}
	}

	var rationales []string
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		match := rationaleCommand.matcher.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if match == nil {
			continue
		}
		_, rationale := rationaleCommand.splitArgs(match[1])
		if rationale == "" {
			var following []string
			for _, next := range lines[i+1:] {
				if strings.HasPrefix(next, "/") || isCodeFenceLine(next) {
					break
				}
				if next = strings.TrimSpace(next); next != "" {
					following = append(following, next)
				}
			}
			rationale = strings.Join(following, " ")
		}
		switch {
		case NonWhitespaceCodePoints(rationale) == 0:
			return nil, &RationaleValidationError{Kind: RationaleErrorMissing}
		case strings.HasPrefix(rationale, "/"):
			return nil, &RationaleValidationError{Kind: RationaleErrorStartsWithSlash}
		}
		rationales = append(rationales, rationale)
	}
	if len(rationales) == 0 {
		return nil, &RationaleValidationError{
			Kind:  RationaleErrorValidation,
			Cause: fmt.Errorf("command %q was not found in comment", command),
		}
	}
	return rationales, nil
}

// StripSameLineRationale removes same-line rationales from every line that
// matches the protected command, so the plugin's own command parsing never
// sees rationale text.
func StripSameLineRationale(body, command string) string {
	rationaleCommand, ok := rationaleCommandByToken(command)
	if !ok {
		return body
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSuffix(line, "\r")
		match := rationaleCommand.matcher.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		commandArgs, rationale := rationaleCommand.splitArgs(match[1])
		if rationale == "" {
			continue
		}
		// Keep the command as the user typed it, including its case.
		stripped := trimmed[:len(command)]
		if commandArgs != "" {
			stripped += " " + commandArgs
		}
		lines[i] = stripped + strings.TrimPrefix(line, trimmed)
	}
	return strings.Join(lines, "\n")
}

// NonWhitespaceCodePoints counts Unicode code points other than Unicode whitespace.
func NonWhitespaceCodePoints(text string) int {
	count := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return count
}

// ValidateRationale checks that every occurrence of a protected command has a
// rationale that meets the configured minimum length.
func ValidateRationale(body, command string, settings RationaleSettings) error {
	rationales, err := ParseRationale(body, command)
	if err != nil {
		return err
	}
	minLength := settings.EffectiveMinLength()
	for _, rationale := range rationales {
		if count := NonWhitespaceCodePoints(rationale); count < minLength {
			return &RationaleValidationError{
				Kind:      RationaleErrorTooShort,
				Count:     count,
				MinLength: minLength,
			}
		}
	}
	return nil
}

// RationaleErrorComment formats a fail-closed response for a protected command.
func RationaleErrorComment(command, noAction string, settings RationaleSettings, err error) string {
	var rationaleErr *RationaleValidationError
	if !errors.As(err, &rationaleErr) {
		rationaleErr = &RationaleValidationError{Kind: RationaleErrorValidation}
	}

	hint := ""
	if rationaleCommand, ok := rationaleCommandByToken(command); ok && rationaleCommand.hint != "" {
		hint = " " + rationaleCommand.hint
	}
	switch rationaleErr.Kind {
	case RationaleErrorMissing:
		return fmt.Sprintf("Rationale required for `%s` in this repository. %s\n\nAdd a rationale of at least %d non-whitespace Unicode code points after the command on the same line, or on the next line.%s\n\nExample:\n%s", command, noAction, settings.EffectiveMinLength(), hint, rationaleExample(command))
	case RationaleErrorStartsWithSlash:
		return fmt.Sprintf("Rationale for `%s` must not start with `/`. %s\n\nStart the rationale with a plain-text explanation.%s Example:\n%s", command, noAction, hint, rationaleExample(command))
	case RationaleErrorTooShort:
		return fmt.Sprintf("Rationale for `%s` is too short (got %d non-whitespace Unicode code points; need at least %d). %s\n\nPlease provide a meaningful explanation.%s Example:\n%s", command, rationaleErr.Count, rationaleErr.MinLength, noAction, hint, rationaleExample(command))
	default:
		return fmt.Sprintf("An internal error occurred while validating the rationale policy for `%s`. The command was blocked as a precaution. %s\n\nPlease try again. If this persists, contact your CI administrators.", command, noAction)
	}
}

// rationaleExample renders a command example as a quoted code block. Quoting
// keeps every line from starting with a slash command, so plugins that do not
// ignore the bot's own comments never act on the example.
func rationaleExample(command string) string {
	example := command + " Provide a meaningful explanation for this command."
	if rationaleCommand, ok := rationaleCommandByToken(command); ok {
		example = rationaleCommand.example
	}
	lines := []string{">```"}
	for line := range strings.SplitSeq(example, "\n") {
		lines = append(lines, ">"+line)
	}
	return strings.Join(append(lines, ">```"), "\n")
}

func allArgsAreRationale(args string) (string, string) {
	return "", strings.TrimSpace(args)
}

// splitOverrideArgs splits /override arguments at the first " -- " outside
// double quotes, because quoted context names may contain spaces.
func splitOverrideArgs(args string) (string, string) {
	inQuotes := false
	for i := 0; i < len(args); i++ {
		if args[i] == '"' {
			inQuotes = !inQuotes
			continue
		}
		if inQuotes || !strings.HasPrefix(args[i:], "--") || (i > 0 && args[i-1] != ' ') {
			continue
		}
		if end := i + len("--"); end == len(args) || args[end] == ' ' {
			return strings.TrimSpace(args[:i]), strings.TrimSpace(args[end:])
		}
	}
	return strings.TrimSpace(args), ""
}

func isCodeFenceLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

func rationaleCommandByToken(command string) (rationaleCommand, bool) {
	name := strings.TrimPrefix(strings.ToLower(command), "/")
	rationaleCommand, ok := rationaleCommands[name]
	return rationaleCommand, ok
}

func isSupportedRationalePolicyCommand(command string) bool {
	_, ok := rationaleCommands[command]
	return ok
}
