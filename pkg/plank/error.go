/*
Copyright 2020 The Kubernetes Authors.

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

package plank

import (
	"errors"
	"fmt"
)

type nonRetryableError struct {
	err error
}

func (ne nonRetryableError) Error() string {
	return fmt.Sprintf("nonretryable error: %s", ne.err.Error())
}

func (nonRetryableError) Is(err error) bool {
	_, ok := err.(nonRetryableError)
	return ok
}

// TerminalError wraps an error and return a nonRetryableError error
func TerminalError(err error) error {
	return &nonRetryableError{err: err}
}

func IsTerminalError(err error) bool {
	return errors.Is(err, nonRetryableError{})
}
