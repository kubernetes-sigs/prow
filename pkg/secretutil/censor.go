/*
Copyright 2021 The Kubernetes Authors.

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

// Package secretutil implements utilities to operate on secret data.
package secretutil

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"

	"go4.org/bytereplacer"
	"gopkg.in/ini.v1"
)

// Censorer knows how to replace sensitive data from input.
type Censorer interface {
	// Censor will remove sensitive data previously registered with the Censorer
	// from the input. This is thread-safe, will mutate the input and will never
	// change the overall size of the input.
	Censor(input *[]byte)
}

func NewCensorer() *ReloadingCensorer {
	return NewCensorerWithMinLength(0)
}

func NewCensorerWithMinLength(minLength int) *ReloadingCensorer {
	return &ReloadingCensorer{
		RWMutex:             &sync.RWMutex{},
		Replacer:            bytereplacer.New(),
		minimumSecretLength: minLength,
	}
}

type ReloadingCensorer struct {
	*sync.RWMutex
	*bytereplacer.Replacer
	largestSecret       int
	minimumSecretLength int
}

var _ Censorer = &ReloadingCensorer{}

// Censor will remove sensitive data previously registered with the Censorer
// from the input. This is thread-safe, will mutate the input and will never
// change the overall size of the input.
// Censoring will attempt to be intelligent about how content is removed from
// the input - when the ReloadingCensorer is given secrets to censor, we:
//   - handle the case where whitespace is needed to be trimmed
//   - censor not only the plaintext representation of the secret but also
//     the base64-encoded representation of it, as it's common for k8s
//     Secrets to contain information in this way
func (c *ReloadingCensorer) Censor(input *[]byte) {
	c.RLock()
	// we know our replacer will never have to allocate, as our replacements
	// are the same size as what they're replacing, so we can throw away
	// the return value from Replace()
	c.Replacer.Replace(*input)
	c.RUnlock()
}

// LargestSecret returns the size of the largest secret we will censor.
func (c *ReloadingCensorer) LargestSecret() int {
	c.RLock()
	defer c.RUnlock()
	return c.largestSecret
}

// RefreshBytes refreshes the set of secrets that we censor.
func (c *ReloadingCensorer) RefreshBytes(secrets ...[]byte) {
	var asStrings []string
	for _, secret := range secrets {
		asStrings = append(asStrings, string(secret))
	}
	c.Refresh(asStrings...)
}

// Refresh refreshes the set of secrets that we censor.
func (c *ReloadingCensorer) Refresh(secrets ...string) {
	var largestSecret int
	var replacements []string
	addReplacement := func(s string) {
		replacements = append(replacements, s, strings.Repeat(`X`, len(s)))
		if len(s) > largestSecret {
			largestSecret = len(s)
		}
	}
	for _, secret := range expandStructuredSecrets(secrets) {
		toEncode := []string{secret}
		if trimmed := strings.TrimSpace(secret); trimmed != secret {
			secret = trimmed
			toEncode = append(toEncode, trimmed)
		}
		if secret == "" {
			continue
		}
		if strings.EqualFold(secret, "true") || strings.EqualFold(secret, "false") {
			// true and false appear in secrets when there is configuration that gets changed at the same time actual secrets.
			// this happens when storing credentials to servers where capabilities are different.
			// while every other type could reasonably be secret (through secret floats would be weird), it's difficult
			// to justify secret booleans, since all values are known and replacing them carries a high price.
			continue
		}
		if len(secret) < c.minimumSecretLength {
			// Skip secrets that are shorter than the minimum length
			continue
		}

		addReplacement(secret)
		for _, item := range toEncode {
			encoded := base64.StdEncoding.EncodeToString([]byte(item))
			addReplacement(encoded)
		}
	}
	c.Lock()
	c.Replacer = bytereplacer.New(replacements...)
	c.largestSecret = largestSecret
	c.Unlock()
}

// expandStructuredSecrets returns the given secrets followed by the individual
// values found in the ones that are JSON documents or INI files. A job that
// prints just one field of such a file would otherwise leak it, since only the
// complete file content was registered. Secrets that are not valid JSON or INI
// are returned unchanged.
func expandStructuredSecrets(secrets []string) []string {
	expanded := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		expanded = append(expanded, secret)
		if values := jsonStringValues(secret); values != nil {
			expanded = append(expanded, values...)
		} else {
			expanded = append(expanded, iniValues(secret)...)
		}
	}
	return expanded
}

// jsonStringValues returns all string values of a JSON object or array, or nil
// if the input is not one.
func jsonStringValues(input string) []string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	var parsed interface{}
	if err := decoder.Decode(&parsed); err != nil || decoder.More() {
		return nil
	}
	var values []string
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch typed := v.(type) {
		case string:
			values = append(values, typed)
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		case map[string]interface{}:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(parsed)
	return values
}

// iniValues returns all values of an INI document, or nil if the input does not
// look like one: every non-empty line has to be a comment, a section header or
// a key=value pair.
func iniValues(input string) []string {
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "", strings.HasPrefix(line, "#"), strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
		case strings.Contains(line, "="):
		default:
			return nil
		}
	}
	cfg, err := ini.Load([]byte(input))
	if err != nil {
		return nil
	}
	var values []string
	for _, section := range cfg.Sections() {
		for _, key := range section.Keys() {
			values = append(values, key.Value())
		}
	}
	return values
}

// AdaptCensorer returns a func that censors without touching the input, to
// be used in places where the previous behavior is required while migrations
// occur.
func AdaptCensorer(censorer Censorer) func(input []byte) []byte {
	return func(input []byte) []byte {
		output := make([]byte, len(input))
		copy(output, input)
		censorer.Censor(&output)
		return output
	}
}
