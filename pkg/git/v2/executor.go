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

package git

import (
	"net/url"
	"os/exec"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
)

// executor knows how to execute Git commands
type executor interface {
	Run(args ...string) ([]byte, error)
}

// Censor censors content to remove secrets
type Censor func(content []byte) []byte

// credentialURLRegex is a pre-compiled regex for censoring URL credentials
// Match URLs like git clone https://username:token@gitlab.com:443/group/project.git
// Groups: 1:(https://username:) 2:(token) 3:(@gitlab.com:443)
var credentialURLRegex = regexp.MustCompile(`(https?://[^:]+:)([^@]+)(@[^/\s:]+(?::[0-9]+)?)`)

// censorURLCredentials censors credentials in URLs
// It replaces the password/token part of URLs with "xxxxx"
func censorURLCredentials(s string) string {
	if u, err := url.Parse(s); err == nil && u.User != nil {
		return u.Redacted()
	}

	// Fallback to regex for URLs embedded in command arguments
	return credentialURLRegex.ReplaceAllString(s, "${1}xxxxx${3}")
}

func NewCensoringExecutor(dir string, censor Censor, logger *logrus.Entry) (executor, error) {
	g, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	return &censoringExecutor{
		logger: logger.WithField("client", "git"),
		dir:    dir,
		git:    g,
		censor: censor,
		execute: func(dir, command string, args ...string) ([]byte, error) {
			c := exec.Command(command, args...)
			c.Dir = dir
			return c.CombinedOutput()
		},
	}, nil
}

type censoringExecutor struct {
	// logger will be used to log git operations
	logger *logrus.Entry
	// dir is the location of this repo.
	dir string
	// git is the path to the git binary.
	git string
	// censor removes sensitive data from output
	censor Censor
	// execute executes a command
	execute func(dir, command string, args ...string) ([]byte, error)
}

func (e *censoringExecutor) Run(args ...string) ([]byte, error) {
	censoredArgs := make([]string, len(args))
	for i, arg := range args {
		censoredArgs[i] = censorURLCredentials(arg)
	}

	logger := e.logger.WithField("args", strings.Join(censoredArgs, " "))

	// Execute with the original arguments
	b, err := e.execute(e.dir, e.git, args...)
	b = e.censor(b)
	if err != nil {
		logger.WithError(err).WithField("output", string(b)).Debug("Running command failed.")
	} else {
		logger.Debug("Running command succeeded.")
	}
	return b, err
}
