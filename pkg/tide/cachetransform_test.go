/*
Copyright The Kubernetes Authors.

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

package tide

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pjutil"
)

// TestIndexFuncsToleratePrunedProwJobs verifies that the cache indexes tide
// registers still work on ProwJobs that went through pjutil.TrimCachedProwJob,
// which is set as the transform on tide's cache: the index funcs run on the
// trimmed objects, not on what the API server returned.
func TestIndexFuncsToleratePrunedProwJobs(t *testing.T) {
	pulls := []prowapi.Pull{{Number: 1, SHA: "pull-sha"}}
	full := func() *prowapi.ProwJob {
		pj := getProwJob(prowapi.BatchJob, "org", "repo", "master", "base-sha", prowapi.SuccessState, pulls)
		pj.Name = "some-job"
		pj.Spec.Job = "some-batch-job"
		pj.Spec.Context = "some-context"
		pj.Spec.PodSpec = &corev1.PodSpec{Containers: []corev1.Container{{Image: "img"}}}
		pj.Spec.DecorationConfig = &prowapi.DecorationConfig{
			GCSConfiguration: &prowapi.GCSConfiguration{Bucket: "bucket"},
		}
		pj.Spec.ExtraRefs = []prowapi.Refs{{Org: "other-org"}}
		pj.Spec.RerunCommand = "/test all"
		return pj
	}

	trimmed, err := pjutil.TrimCachedProwJob(full())
	if err != nil {
		t.Fatalf("pjutil.TrimCachedProwJob failed: %v", err)
	}
	pj, ok := trimmed.(*prowapi.ProwJob)
	if !ok {
		t.Fatalf("expected a *prowapi.ProwJob, got %T", trimmed)
	}

	// Nothing the sync loop reads may be gone.
	if pj.Spec.PodSpec != nil {
		t.Error("expected the podspec to be trimmed away")
	}
	if diff := cmp.Diff(full().Spec.Refs, pj.Spec.Refs); diff != "" {
		t.Errorf("refs were changed: %s", diff)
	}
	if pj.Spec.Job != full().Spec.Job || pj.Spec.Context != full().Spec.Context {
		t.Errorf("the job identity was changed: %+v", pj.Spec)
	}
	if pj.Status.State != full().Status.State {
		t.Errorf("state was changed: %v", pj.Status.State)
	}

	for _, tc := range []struct {
		name      string
		indexFunc func(ctrlruntimeclient.Object) []string
	}{
		{name: cacheIndexName, indexFunc: cacheIndexFunc},
		{name: nonFailedBatchByNameBaseAndPullsIndexName, indexFunc: nonFailedBatchByNameBaseAndPullsIndexFunc},
		{name: indexNamePassingJobs, indexFunc: indexFuncPassingJobs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := tc.indexFunc(full())
			if len(expected) == 0 {
				t.Fatal("the untrimmed prowjob yields no index keys, the fixture is not exercising this index")
			}
			if diff := cmp.Diff(expected, tc.indexFunc(pj)); diff != "" {
				t.Errorf("trimming the prowjob changed the index keys: %s", diff)
			}
		})
	}
}
