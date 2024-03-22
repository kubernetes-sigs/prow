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

package entrypoint

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/pod-utils/wrapper"
)

func TestOptions_Run(t *testing.T) {
	var testCases = []struct {
		name           string
		args           []string
		alwaysZero     bool
		interrupt      bool
		propagate      bool
		invalidMarker  bool
		previousMarker string
		timeout        time.Duration
		gracePeriod    time.Duration
		expectedLog    string
		expectedMarker string
		expectedCode   int
	}{
		{
			name:           "successful command",
			args:           []string{"sh", "-c", "exit 0"},
			expectedLog:    "",
			expectedMarker: "0",
			expectedCode:   0,
		},
		{
			name:           "successful command with output",
			args:           []string{"echo", "test"},
			expectedLog:    "test\n",
			expectedMarker: "0",
			expectedCode:   0,
		},
		{
			name:           "unsuccessful command",
			args:           []string{"sh", "-c", "exit 12"},
			expectedLog:    "",
			expectedMarker: "12",
			expectedCode:   12,
		},
		{
			name:           "unsuccessful command with output",
			args:           []string{"sh", "-c", "echo test && exit 12"},
			expectedLog:    "test\n",
			expectedMarker: "12",
			expectedCode:   12,
		},
		{
			name:           "command times out",
			args:           []string{"sleep", "10"},
			timeout:        1 * time.Second,
			gracePeriod:    1 * time.Second,
			expectedLog:    "level=error msg=\"Process did not finish before 1s timeout\"\nlevel=error msg=\"Process gracefully exited before 1s grace period\"\n",
			expectedMarker: strconv.Itoa(InternalErrorCode),
			expectedCode:   InternalErrorCode,
		},
		{
			name:           "command times out and ignores interrupt",
			args:           []string{"bash", "-c", "trap 'sleep 10' EXIT; sleep 10"},
			timeout:        1 * time.Second,
			gracePeriod:    1 * time.Second,
			expectedLog:    "level=error msg=\"Process did not finish before 1s timeout\"\nlevel=error msg=\"Process did not exit before 1s grace period\"\n",
			expectedMarker: strconv.Itoa(InternalErrorCode),
			expectedCode:   InternalErrorCode,
		},
		{
			// Ensure that environment variables get passed through
			name:           "$PATH is set",
			args:           []string{"sh", "-c", "echo $PATH"},
			expectedLog:    os.Getenv("PATH") + "\n",
			expectedMarker: "0",
			expectedCode:   0,
		},
		{
			name:           "failures return 0 when AlwaysZero is set",
			alwaysZero:     true,
			args:           []string{"sh", "-c", "exit 7"},
			expectedMarker: "7",
			expectedCode:   0,
		},
		{
			name:           "return non-zero when writing marker fails even when AlwaysZero is set",
			alwaysZero:     true,
			timeout:        1 * time.Second,
			gracePeriod:    1 * time.Second,
			args:           []string{"echo", "test"},
			invalidMarker:  true,
			expectedLog:    "test\n",
			expectedMarker: strconv.Itoa(InternalErrorCode),
			expectedCode:   InternalErrorCode,
		},
		{
			name:           "return PreviousErrorCode without running anything if previous marker failed",
			previousMarker: "9",
			args:           []string{"echo", "test"},
			expectedLog:    "level=info msg=\"Skipping as previous step exited 9\"\n",
			expectedCode:   PreviousErrorCode,
			expectedMarker: strconv.Itoa(PreviousErrorCode),
		},
		{
			name:           "run passing command as normal if previous marker passed",
			previousMarker: "0",
			args:           []string{"sh", "-c", "exit 0"},
			expectedMarker: "0",
			expectedCode:   0,
		},

		{
			name:      "interrupt, propagate child error",
			interrupt: true,
			propagate: true,
			args: []string{"bash", "-c", `function cleanup() {
CHILDREN=$(jobs -p)
if test -n "${CHILDREN}"
then
kill ${CHILDREN} && wait
fi
exit 3
}
trap cleanup SIGINT SIGTERM EXIT
echo process started
sleep infinity &
wait`},
			expectedLog:    "process started\nlevel=error msg=\"Entrypoint received interrupt: terminated\"\nlevel=error msg=\"Process gracefully exited before 15s grace period\"\n",
			expectedMarker: "3",
			expectedCode:   3,
		},
		{
			name:      "interrupt, do not propagate child error",
			interrupt: true,
			args: []string{"bash", "-c", `function cleanup() {
CHILDREN=$(jobs -p)
if test -n "${CHILDREN}"
then
kill ${CHILDREN} && wait
fi
exit 3
}
trap cleanup SIGINT SIGTERM EXIT
echo process started
sleep infinity &
wait`},
			expectedLog:    "process started\nlevel=error msg=\"Entrypoint received interrupt: terminated\"\nlevel=error msg=\"Process gracefully exited before 15s grace period\"\n",
			expectedMarker: "130",
			expectedCode:   130,
		},
		{
			name:           "run failing command as normal if previous marker passed",
			previousMarker: "0",
			args:           []string{"sh", "-c", "exit 4"},
			expectedMarker: "4",
			expectedCode:   4,
		},
		{
			name:           "start error is written to log",
			args:           []string{"./this-command-does-not-exist"},
			expectedLog:    "could not start the process: fork/exec ./this-command-does-not-exist: no such file or directory",
			expectedMarker: "127",
			expectedCode:   InternalErrorCode,
		},
	}

	// we write logs to the process log if wrapping fails
	// and cannot write timestamps or we can't match text
	logrus.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			interrupt := make(chan os.Signal, 1)

			options := Options{
				AlwaysZero:         testCase.alwaysZero,
				PropagateErrorCode: testCase.propagate,
				Timeout:            testCase.timeout,
				GracePeriod:        testCase.gracePeriod,
				Options: &wrapper.Options{
					Args:       testCase.args,
					ProcessLog: path.Join(tmpDir, "process-log.txt"),
					MarkerFile: path.Join(tmpDir, "marker-file.txt"),
				},
			}

			if testCase.previousMarker != "" {
				p := path.Join(tmpDir, "previous-marker.txt")
				options.PreviousMarker = p
				if err := os.WriteFile(p, []byte(testCase.previousMarker), 0600); err != nil {
					t.Fatalf("could not create previous marker: %v", err)
				}
			}

			if testCase.invalidMarker {
				options.MarkerFile = "/this/had/better/not/be/a/real/file!@!#$%#$^#%&*&&*()*"
			}

			if testCase.interrupt {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					// sync with ExecuteProcess func to ensure that process has already started
					if err := waitForFileToBeWritten(ctx, options.ProcessLog); err != nil {
						t.Errorf("failed to wait for file: %v", err)
					}
					time.Sleep(200 * time.Millisecond)
					interrupt <- syscall.SIGTERM
				}()
			}

			if code := options.internalRun(interrupt); code != testCase.expectedCode {
				t.Errorf("%s: expected exit code %d != actual %d", testCase.name, testCase.expectedCode, code)
			}

			compareFileContents(testCase.name, options.ProcessLog, testCase.expectedLog, t)
			if !testCase.invalidMarker {
				compareFileContents(testCase.name, options.MarkerFile, testCase.expectedMarker, t)
			}
		})
	}
}

func compareFileContents(name, file, expected string, t *testing.T) {
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s: could not read file: %v", name, err)
	}
	if string(data) != expected {
		t.Errorf("%s: expected contents: %q, got %q", name, expected, data)
	}
}

func waitForFileToBeWritten(ctx context.Context, file string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fileInfo, _ := os.Stat(file)
			if fileInfo.Size() != 0 {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("cancelled while waiting for file %s to exist", file)
		}
	}
}
