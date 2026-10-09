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

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
)

type rationaleCommand struct {
	matcher *regexp.Regexp
	example string
}

var rationaleCommands = map[string]rationaleCommand{
	"retest": {
		// Keep this syntax in sync with pjutil.RetestRe.
		matcher: regexp.MustCompile(`^/retest\s*$`),
		example: "/retest\nReason: Retrying after the CI setup network timeout was resolved; failure is unrelated to PR changes.",
	},
	"override": {
		// Keep this syntax in sync with override.overrideRe.
		matcher: regexp.MustCompile(`(?i)^/override( ([^\r\n]+))?[\r\n]?$`),
		example: "/override \"ci/prow/e2e-aws\"\nReason: Overriding after the e2e infrastructure outage was confirmed.",
	},
}

// Rationale contains the fields parsed from a protected command's comment block.
type Rationale struct {
	Reason    string
	Emergency bool
}

// RationaleContext identifies the actor and command being validated.
type RationaleContext struct {
	Command     string
	Actor       string
	Org         string
	Repo        string
	PullRequest int
}

// RationaleTeamClient checks membership in a configured GitHub team.
type RationaleTeamClient interface {
	ListTeamMembersBySlug(org, teamSlug, role string) ([]github.TeamMember, error)
}

// RationaleErrorKind describes why rationale validation failed.
type RationaleErrorKind string

const (
	RationaleErrorMissingReason         RationaleErrorKind = "missing_reason"
	RationaleErrorTooShort              RationaleErrorKind = "too_short"
	RationaleErrorEmergencyBypassDenied RationaleErrorKind = "emergency_bypass_denied"
	RationaleErrorValidation            RationaleErrorKind = "validation_error"
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
	case RationaleErrorMissingReason:
		return "a non-empty Reason: field is required"
	case RationaleErrorTooShort:
		return fmt.Sprintf("reason has %d non-whitespace code points, need at least %d", e.Count, e.MinLength)
	case RationaleErrorEmergencyBypassDenied:
		return "emergency rationale bypass is not authorized"
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

// ParseRationale extracts the first matching command's Reason and Emergency fields.
// Metadata is associated with that command up to the next slash-command line.
func ParseRationale(body, command string) (Rationale, error) {
	if _, ok := rationaleCommandByToken(command); !ok {
		return Rationale{}, &RationaleValidationError{
			Kind:  RationaleErrorValidation,
			Cause: fmt.Errorf("unsupported protected command %q", command),
		}
	}

	lines := strings.Split(body, "\n")
	commandLine := -1
	for i, line := range lines {
		if isRationaleCommandLine(line, command) {
			commandLine = i
			break
		}
	}
	if commandLine == -1 {
		return Rationale{}, &RationaleValidationError{
			Kind:  RationaleErrorValidation,
			Cause: fmt.Errorf("command %q was not found in comment", command),
		}
	}

	var rationale Rationale
	hasReason := false
	hasEmergency := false
	for _, line := range lines[commandLine+1:] {
		line = strings.TrimSuffix(line, "\r")
		if isSlashCommandLine(line) {
			break
		}

		if reason, ok := rationaleFieldValue(line, "Reason"); ok {
			if hasReason {
				return Rationale{}, &RationaleValidationError{
					Kind:  RationaleErrorValidation,
					Cause: errors.New("multiple Reason: fields were provided"),
				}
			}
			rationale.Reason = strings.TrimSpace(reason)
			hasReason = true
			continue
		}

		if emergencyValue, ok := rationaleFieldValue(line, "Emergency"); ok {
			if hasEmergency {
				return Rationale{}, &RationaleValidationError{
					Kind:  RationaleErrorValidation,
					Cause: errors.New("multiple Emergency: fields were provided"),
				}
			}
			switch {
			case strings.EqualFold(emergencyValue, "true"):
				rationale.Emergency = true
			case strings.EqualFold(emergencyValue, "false"):
				rationale.Emergency = false
			default:
				return Rationale{}, &RationaleValidationError{
					Kind:  RationaleErrorValidation,
					Cause: fmt.Errorf("Emergency: value must be true or false"),
				}
			}
			hasEmergency = true
		}
	}

	if !hasReason || NonWhitespaceCodePoints(rationale.Reason) == 0 {
		return Rationale{}, &RationaleValidationError{Kind: RationaleErrorMissingReason}
	}
	return rationale, nil
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

// ValidateRationale parses and validates rationale for one protected command.
// Emergency bypasses only the minimum length and still requires a non-empty reason.
func ValidateRationale(body string, rationaleContext RationaleContext, settings RationaleSettings, teamClient RationaleTeamClient, logger *logrus.Entry) (Rationale, error) {
	rationale, err := ParseRationale(body, rationaleContext.Command)
	if err != nil {
		return Rationale{}, err
	}

	minLength := settings.EffectiveMinLength()
	count := NonWhitespaceCodePoints(rationale.Reason)

	if rationale.Emergency {
		team, authorized, err := authorizedRationaleBypassTeam(teamClient, rationaleContext, settings.EmergencyBypass)
		if err != nil {
			if logger != nil {
				logger.WithError(err).WithFields(logrus.Fields{
					"actor":        rationaleContext.Actor,
					"repo":         rationaleContext.Org + "/" + rationaleContext.Repo,
					"pull_request": rationaleContext.PullRequest,
					"command":      rationaleContext.Command,
				}).Error("Failed to validate emergency rationale bypass")
			}
			return Rationale{}, &RationaleValidationError{Kind: RationaleErrorValidation, Cause: err}
		}
		if !authorized {
			return Rationale{}, &RationaleValidationError{Kind: RationaleErrorEmergencyBypassDenied}
		}
		if logger != nil {
			logger.WithFields(logrus.Fields{
				"event":        "rationale_emergency_bypass",
				"actor":        rationaleContext.Actor,
				"repo":         rationaleContext.Org + "/" + rationaleContext.Repo,
				"pull_request": rationaleContext.PullRequest,
				"command":      rationaleContext.Command,
				"reason":       rationale.Reason,
				"team_matched": team,
			}).Info("Emergency rationale bypass used")
		}
		if count < minLength {
			return rationale, nil
		}
	}

	if count < minLength {
		return Rationale{}, &RationaleValidationError{
			Kind:      RationaleErrorTooShort,
			Count:     count,
			MinLength: minLength,
		}
	}
	return rationale, nil
}

func authorizedRationaleBypassTeam(client RationaleTeamClient, rationaleContext RationaleContext, bypass *EmergencyBypassConfig) (string, bool, error) {
	if bypass == nil || len(bypass.AllowedGitHubTeams) == 0 {
		return "", false, nil
	}
	if client == nil {
		return "", false, errors.New("GitHub team client is unavailable")
	}

	for _, configuredTeam := range bypass.AllowedGitHubTeams {
		teamOrg, teamSlug, ok := parseGitHubTeamSlug(configuredTeam)
		if !ok {
			return "", false, fmt.Errorf("configured bypass team %q is invalid", configuredTeam)
		}
		members, err := client.ListTeamMembersBySlug(teamOrg, teamSlug, github.RoleAll)
		if err != nil {
			return "", false, fmt.Errorf("failed to check membership in %s: %w", configuredTeam, err)
		}
		for _, member := range members {
			if strings.EqualFold(member.Login, rationaleContext.Actor) {
				return configuredTeam, true, nil
			}
		}
	}
	return "", false, nil
}

func parseGitHubTeamSlug(team string) (org, slug string, ok bool) {
	if strings.ContainsAny(team, " \t\r\n") {
		return "", "", false
	}
	org, slug, ok = strings.Cut(team, "/")
	if !ok || org == "" || slug == "" || strings.Contains(slug, "/") {
		return "", "", false
	}
	return org, slug, true
}

// RationaleErrorComment formats a fail-closed response for a protected command.
func RationaleErrorComment(command, noAction string, settings RationaleSettings, err error) string {
	var rationaleErr *RationaleValidationError
	if !errors.As(err, &rationaleErr) {
		rationaleErr = &RationaleValidationError{Kind: RationaleErrorValidation}
	}

	minLength := settings.EffectiveMinLength()
	switch rationaleErr.Kind {
	case RationaleErrorMissingReason:
		message := fmt.Sprintf("Rationale required for `%s` in this repository. %s\n\nAdd a separate `Reason:` line with at least %d non-whitespace Unicode code points.\n\nExample:\n```\n%s\n```", command, noAction, minLength, rationaleExample(command))
		if settings.EmergencyBypass != nil {
			message += "\n\nIf this is an emergency, authorized team members may add `Emergency: true` on a separate line."
		}
		return message
	case RationaleErrorTooShort:
		return fmt.Sprintf("Rationale for `%s` is too short (got %d non-whitespace Unicode code points; need at least %d). %s\n\nPlease provide a meaningful explanation. Example:\n```\n%s\n```", command, rationaleErr.Count, rationaleErr.MinLength, noAction, rationaleExample(command))
	case RationaleErrorEmergencyBypassDenied:
		if settings.EmergencyBypass == nil || len(settings.EmergencyBypass.AllowedGitHubTeams) == 0 {
			return fmt.Sprintf("`Emergency: true` was requested for `%s`, but no emergency bypass team is configured for this command. %s\n\nPlease provide a standard `Reason:` line with at least %d non-whitespace Unicode code points.", command, noAction, minLength)
		}
		return fmt.Sprintf("`Emergency: true` was requested for `%s`, but you are not a member of an authorized bypass team. %s\n\nPlease provide a standard `Reason:` line with at least %d non-whitespace Unicode code points, or ask a member of the authorized team to issue the command.", command, noAction, minLength)
	default:
		return fmt.Sprintf("An internal error occurred while validating the rationale policy for `%s`. The command was blocked as a precaution. %s\n\nPlease try again. If this persists, contact your CI administrators.", command, noAction)
	}
}

func rationaleExample(command string) string {
	if rationaleCommand, ok := rationaleCommandByToken(command); ok {
		return rationaleCommand.example
	}
	return command + "\nReason: Provide a meaningful explanation for this command."
}

func isRationaleCommandLine(line, command string) bool {
	rationaleCommand, ok := rationaleCommandByToken(command)
	return ok && rationaleCommand.matcher.MatchString(line)
}

func isSlashCommandLine(line string) bool {
	line = strings.TrimSuffix(line, "\r")
	if len(line) < 2 || line[0] != '/' {
		return false
	}
	command := strings.Fields(line)[0]
	if len(command) < 2 {
		return false
	}
	for _, r := range command[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
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

func rationaleFieldValue(line, key string) (string, bool) {
	line = strings.TrimLeft(line, " \t\r")
	if len(line) <= len(key) || !strings.EqualFold(line[:len(key)], key) || line[len(key)] != ':' {
		return "", false
	}
	return strings.TrimSpace(line[len(key)+1:]), true
}

var _ RationaleTeamClient = (github.Client)(nil)
