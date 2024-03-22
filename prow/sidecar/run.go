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

package sidecar

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil/pprof"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/entrypoint"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	"k8s.io/test-infra/prow/pod-utils/gcs"
	"k8s.io/test-infra/prow/pod-utils/wrapper"

	testgridmetadata "github.com/GoogleCloudPlatform/testgrid/metadata"
)

const LogFileName = "sidecar-logs.json"

func LogSetup() (*os.File, error) {
	logrusutil.ComponentInit()
	logrus.SetLevel(logrus.DebugLevel)
	logFile, err := os.CreateTemp("", "sidecar-logs*.txt")
	if err == nil {
		logrus.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	return logFile, err
}

func nameEntry(idx int, opt wrapper.Options) string {
	return fmt.Sprintf("entry %d: %s", idx, strings.Join(opt.Args, " "))
}

func wait(ctx context.Context, entries []wrapper.Options) (bool, bool, int) {

	var paths []string

	for _, opt := range entries {
		paths = append(paths, opt.MarkerFile)
	}

	results := wrapper.WaitForMarkers(ctx, paths...)

	passed := true
	var aborted bool
	var failures int

	for _, res := range results {
		passed = passed && res.Err == nil && res.ReturnCode == 0
		aborted = aborted || res.ReturnCode == entrypoint.AbortedErrorCode
		if res.ReturnCode != 0 && res.ReturnCode != entrypoint.PreviousErrorCode {
			failures++
		}
	}

	return passed, aborted, failures

}

// Run will watch for the process being wrapped to exit
// and then post the status of that process and any artifacts
// to cloud storage.
func (o Options) Run(ctx context.Context, logFile *os.File) (int, error) {
	if o.WriteMemoryProfile {
		pprof.WriteMemoryProfiles(flagutil.DefaultMemoryProfileInterval)
	}
	spec, err := downwardapi.ResolveSpecFromEnv()
	if err != nil {
		return 0, fmt.Errorf("could not resolve job spec: %w", err)
	}

	entries := o.entries()
	var once sync.Once

	ctx, cancel := context.WithCancel(ctx)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case s := <-interrupt:
			if o.IgnoreInterrupts {
				logrus.Warnf("Received an interrupt: %s, ignoring...", s)
			} else {
				// If we are being asked to terminate by the kubelet but we have
				// NOT seen the test process exit cleanly, we need a to start
				// uploading artifacts to GCS immediately. If we notice the process
				// exit while doing this best-effort upload, we can race with the
				// second upload but we can tolerate this as we'd rather get SOME
				// data into GCS than attempt to cancel these uploads and get none.
				logrus.Errorf("Received an interrupt: %s, cancelling...", s)

				// perform pre upload tasks
				o.preUpload()

				buildLogs := logReadersFuncs(entries)
				metadata := combineMetadata(entries)

				//Peform best-effort upload
				err := o.doUpload(ctx, spec, false, true, metadata, buildLogs, logFile, &once)
				if err != nil {
					logrus.WithError(err).Error("Failed to perform best-effort upload")
				} else {
					logrus.Error("Best-effort upload was successful")
				}
			}
		case <-ctx.Done():
		}
	}()

	passed, aborted, failures := wait(ctx, entries)

	cancel()
	// If we are being asked to terminate by the kubelet but we have
	// seen the test process exit cleanly, we need a chance to upload
	// artifacts to GCS. The only valid way for this program to exit
	// after a SIGINT or SIGTERM in this situation is to finish
	// uploading, so we ignore the signals.
	signal.Ignore(os.Interrupt, syscall.SIGTERM)

	o.preUpload()

	buildLogs := logReadersFuncs(entries)
	metadata := combineMetadata(entries)
	return failures, o.doUpload(context.Background(), spec, passed, aborted, metadata, buildLogs, logFile, &once)
}

const errorKey = "sidecar-errors"

func logReadersFuncs(entries []wrapper.Options) map[string]gcs.ReaderFunc {
	readerFuncs := make(map[string]gcs.ReaderFunc)
	for _, opt := range entries {
		opt := opt
		f := func() (io.ReadCloser, error) {
			log, err := os.Open(opt.ProcessLog)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to open %s", opt.ProcessLog)
				r := strings.NewReader(fmt.Sprintf("Failed to open %s: %v\n", opt.ProcessLog, err))
				return io.NopCloser(r), nil
			} else {
				return log, nil
			}
		}
		buildLog := "build-log.txt"
		if len(entries) > 1 {
			buildLog = fmt.Sprintf("%s-build-log.txt", opt.ContainerName)
		}
		readerFuncs[buildLog] = f
	}
	return readerFuncs
}

func combineMetadata(entries []wrapper.Options) map[string]interface{} {
	errors := map[string]error{}
	metadata := map[string]interface{}{}
	for i, opt := range entries {
		ent := nameEntry(i, opt)
		metadataFile := opt.MetadataFile
		if _, err := os.Stat(metadataFile); err != nil {
			if !os.IsNotExist(err) {
				logrus.WithError(err).Errorf("Failed to stat %s", metadataFile)
				errors[ent] = err
			}
			continue
		}
		metadataRaw, err := os.ReadFile(metadataFile)
		if err != nil {
			logrus.WithError(err).Errorf("cannot read %s", metadataFile)
			errors[ent] = err
			continue
		}

		piece := map[string]interface{}{}
		if err := json.Unmarshal(metadataRaw, &piece); err != nil {
			logrus.WithError(err).Errorf("Failed to unmarshal %s", metadataFile)
			errors[ent] = err
			continue
		}

		for k, v := range piece {
			metadata[k] = v // TODO(fejta): consider deeper merge
		}
	}
	if len(errors) > 0 {
		metadata[errorKey] = errors
	}
	return metadata
}

// preUpload peforms steps required before actual upload
func (o Options) preUpload() {
	if o.DeprecatedWrapperOptions != nil {
		// This only fires if the prowjob controller and sidecar are at different commits
		logrus.Warn("Using deprecated wrapper_options instead of entries. Please update prow/pod-utils/decorate before June 2019")
	}

	if o.CensoringOptions != nil {
		if err := o.censor(); err != nil {
			logrus.WithError(err).Warn("Failed to censor data")
		}
	}
}

func (o Options) doUpload(ctx context.Context, spec *downwardapi.JobSpec, passed, aborted bool, metadata map[string]interface{}, logReadersFuncs map[string]gcs.ReaderFunc, logFile *os.File, once *sync.Once) error {
	startTime := time.Now()
	logrus.Info("Starting to upload")
	uploadTargets := make(map[string]gcs.UploadFunc)

	defer func() {
		logrus.WithField("duration", time.Since(startTime).String()).Info("Finished uploading")
	}()

	for logName, readerFunc := range logReadersFuncs {
		uploadTargets[logName] = gcs.DataUpload(readerFunc)
	}

	logFileName := logFile.Name()

	once.Do(func() {
		logrus.SetOutput(os.Stderr)
		logFile.Sync()
		logFile.Close()
	})

	newLogReader := func() (io.ReadCloser, error) {
		f, err := os.Open(logFileName)
		if err != nil {
			logrus.WithError(err).Error("Could not open log file")
			return nil, err
		}
		r := bufio.NewReader(f)
		return struct {
			io.Reader
			io.Closer
		}{r, f}, nil
	}
	uploadTargets[LogFileName] = gcs.DataUpload(newLogReader)

	var result string
	switch {
	case passed:
		result = "SUCCESS"
	case aborted:
		result = "ABORTED"
	default:
		result = "FAILURE"
	}

	now := time.Now().Unix()
	finished := testgridmetadata.Finished{
		Timestamp: &now,
		Passed:    &passed,
		Result:    result,
		Metadata:  metadata,
		// TODO(fejta): JobVersion,
	}

	// TODO(fejta): move to initupload and Started.Repos, RepoVersion
	finished.DeprecatedRevision = downwardapi.GetRevisionFromSpec(spec)

	finishedData, err := json.Marshal(&finished)
	if err != nil {
		logrus.WithError(err).Warn("Could not marshal finishing data")
	} else {
		newReader := func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(finishedData)), nil
		}
		uploadTargets[prowv1.FinishedStatusFile] = gcs.DataUpload(newReader)
	}

	if err := o.GcsOptions.Run(ctx, spec, uploadTargets); err != nil {
		return fmt.Errorf("failed to upload to GCS: %w", err)
	}

	return nil
}
