/*
Copyright 2017 The Kubernetes Authors.

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

package pjutil

import (
	"context"
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowv1 "sigs.k8s.io/prow/pkg/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
)

func resultForJob(pjclient prowv1.ProwJobInterface, selector string) (*pjapi.ProwJobStatus, bool, error) {
	w, err := pjclient.Watch(context.Background(), metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil, false, fmt.Errorf("failed to create watch for ProwJobs: %w", err)
	}
	for event := range w.ResultChan() {
		prowJob, ok := event.Object.(*pjapi.ProwJob)
		if !ok {
			return nil, false, fmt.Errorf("received an unexpected object from Watch: object-type %s", fmt.Sprintf("%T", event.Object))
		}

		switch prowJob.Status.State {
		case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState, pjapi.SuccessState:
			return &prowJob.Status, false, nil
		}
	}
	return nil, true, nil
}

// TriggerAndWatchProwJob would trigger the job provided by the prowjob parameter
func TriggerAndWatchProwJob(o prowflagutil.KubernetesOptions, prowjob *pjapi.ProwJob, config *prowconfig.Config, envVars map[string]string, dryRun bool) (succeeded bool, err error) {
	return TriggerAndWatchProwJobState(o, prowjob, config, envVars, pjapi.SuccessState, dryRun)
}

// TriggerAndWatchProwJobState triggers the job provided by the prowjob parameter
// and waits for the provided state
func TriggerAndWatchProwJobState(o prowflagutil.KubernetesOptions, prowjob *pjapi.ProwJob, config *prowconfig.Config, envVars map[string]string, state pjapi.ProwJobState, dryRun bool) (stateReached bool, err error) {
	logrus.Info("getting cluster config")
	pjclient, err := o.ProwJobClient(config.ProwJobNamespace, dryRun)
	if err != nil {
		return false, fmt.Errorf("failed getting prowjob client: %w", err)
	}

	logrus.WithFields(ProwJobFields(prowjob)).Info("submitting a new prowjob")
	created, err := pjclient.Create(context.Background(), prowjob, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to submit the prowjob: %w", err)
	}

	logger := logrus.WithFields(ProwJobFields(created))
	logger.Info("submitted the prowjob, waiting for its result")

	selector := fields.SelectorFromSet(map[string]string{"metadata.name": created.Name})

	var result *pjapi.ProwJobStatus
	var shouldContinue bool
	for {
		result, shouldContinue, err = resultForJob(pjclient, selector.String())
		if err != nil {
			return false, fmt.Errorf("failed to watch job: %w", err)
		}
		if !shouldContinue {
			break
		}
	}

	if result.State != state {
		logrus.Error("job failed")
	} else {
		stateReached = true
	}

	b, err := yaml.Marshal(result)
	if err != nil {
		logrus.WithError(err).Error("failed to marshal prow job result")
	}

	fmt.Println(string(b))
	return stateReached, nil
}
