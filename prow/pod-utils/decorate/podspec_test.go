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

package decorate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/clonerefs"
	"k8s.io/test-infra/prow/entrypoint"
	"k8s.io/test-infra/prow/gcsupload"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/initupload"
	"k8s.io/test-infra/prow/pod-utils/wrapper"
	"k8s.io/test-infra/prow/sidecar"
	"k8s.io/test-infra/prow/testutil"
)

func pStr(str string) *string {
	return &str
}
func pInt64(i int64) *int64 {
	return &i
}

func cookieVolumeOnly(secret string) coreapi.Volume {
	v, _, _ := cookiefileVolume(secret)
	return v
}

func cookieMountOnly(secret string) coreapi.VolumeMount {
	_, vm, _ := cookiefileVolume(secret)
	return vm
}
func cookiePathOnly(secret string) string {
	_, _, vp := cookiefileVolume(secret)
	return vp
}

func TestCloneRefs(t *testing.T) {
	truth := true
	logMount := coreapi.VolumeMount{
		Name:      "log",
		MountPath: "/log-mount",
	}
	codeMount := coreapi.VolumeMount{
		Name:      "code",
		MountPath: "/code-mount",
	}
	tmpMount := coreapi.VolumeMount{
		Name:      "clonerefs-tmp",
		MountPath: "/tmp",
	}
	tmpVolume := coreapi.Volume{
		Name: "clonerefs-tmp",
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	}
	envOrDie := func(opt clonerefs.Options) []coreapi.EnvVar {
		e, err := cloneEnv(opt)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	sshVolumeOnly := func(secret string) coreapi.Volume {
		v, _ := sshVolume(secret)
		return v
	}

	sshMountOnly := func(secret string) coreapi.VolumeMount {
		_, vm := sshVolume(secret)
		return vm
	}

	cases := []struct {
		name              string
		pj                prowapi.ProwJob
		codeMountOverride *coreapi.VolumeMount
		logMountOverride  *coreapi.VolumeMount
		expected          *coreapi.Container
		volumes           []coreapi.Volume
		err               bool
	}{
		{
			name: "empty returns nil",
		},
		{
			name: "nil refs and extrarefs returns nil",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{},
				},
			},
		},
		{
			name: "nil DecorationConfig returns nil",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					Refs: &prowapi.Refs{},
				},
			},
		},
		{
			name: "SkipCloning returns nil",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					Refs: &prowapi.Refs{},
					DecorationConfig: &prowapi.DecorationConfig{
						SkipCloning: &truth,
					},
				},
			},
		},
		{
			name: "reject empty code mount name",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{},
					Refs:             &prowapi.Refs{},
				},
			},
			codeMountOverride: &coreapi.VolumeMount{
				MountPath: "/whatever",
			},
			err: true,
		},
		{
			name: "reject empty code mountpath",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{},
					Refs:             &prowapi.Refs{},
				},
			},
			codeMountOverride: &coreapi.VolumeMount{
				Name: "wee",
			},
			err: true,
		},
		{
			name: "reject empty log mount name",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{},
					Refs:             &prowapi.Refs{},
				},
			},
			logMountOverride: &coreapi.VolumeMount{
				MountPath: "/whatever",
			},
			err: true,
		},
		{
			name: "reject empty log mountpath",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{},
					Refs:             &prowapi.Refs{},
				},
			},
			logMountOverride: &coreapi.VolumeMount{
				Name: "wee",
			},
			err: true,
		},
		{
			name: "create clonerefs container when refs are set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					Refs: &prowapi.Refs{},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:            []prowapi.Refs{{}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					SrcRoot:            codeMount.MountPath,
					Log:                CloneLogPath(logMount),
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{logMount, codeMount, tmpMount},
			},
			volumes: []coreapi.Volume{tmpVolume},
		},
		{
			name: "create clonerefs containers when extrarefs are set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					ExtraRefs: []prowapi.Refs{{}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:            []prowapi.Refs{{}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					SrcRoot:            codeMount.MountPath,
					Log:                CloneLogPath(logMount),
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{logMount, codeMount, tmpMount},
			},
			volumes: []coreapi.Volume{tmpVolume},
		},
		{
			name: "append extrarefs after refs",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					Refs:      &prowapi.Refs{Org: "first"},
					ExtraRefs: []prowapi.Refs{{Org: "second"}, {Org: "third"}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:            []prowapi.Refs{{Org: "first"}, {Org: "second"}, {Org: "third"}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					SrcRoot:            codeMount.MountPath,
					Log:                CloneLogPath(logMount),
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{logMount, codeMount, tmpMount},
			},
			volumes: []coreapi.Volume{tmpVolume},
		},
		{
			name: "append ssh secrets when set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					Refs: &prowapi.Refs{},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
						SSHKeySecrets: []string{"super", "secret"},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:            []prowapi.Refs{{}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					KeyFiles:           []string{sshMountOnly("super").MountPath, sshMountOnly("secret").MountPath},
					SrcRoot:            codeMount.MountPath,
					Log:                CloneLogPath(logMount),
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{
					logMount,
					codeMount,
					sshMountOnly("super"),
					sshMountOnly("secret"),
					tmpMount,
				},
			},
			volumes: []coreapi.Volume{sshVolumeOnly("super"), sshVolumeOnly("secret"), tmpVolume},
		},
		{
			name: "include ssh host fingerprints when set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					ExtraRefs: []prowapi.Refs{{}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages:       &prowapi.UtilityImages{},
						SSHHostFingerprints: []string{"thumb", "pinky"},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:            []prowapi.Refs{{}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					SrcRoot:            codeMount.MountPath,
					HostFingerprints:   []string{"thumb", "pinky"},
					Log:                CloneLogPath(logMount),
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{logMount, codeMount, tmpMount},
			},
			volumes: []coreapi.Volume{tmpVolume},
		},
		{
			name: "include cookiefile secrets when set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					ExtraRefs: []prowapi.Refs{{}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages:    &prowapi.UtilityImages{},
						CookiefileSecret: pStr("oatmeal"),
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Args: []string{"--cookiefile=" + cookiePathOnly("oatmeal")},
				Env: envOrDie(clonerefs.Options{
					CookiePath:         cookiePathOnly("oatmeal"),
					GitRefs:            []prowapi.Refs{{}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					SrcRoot:            codeMount.MountPath,
					Log:                CloneLogPath(logMount),
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{logMount, codeMount, tmpMount, cookieMountOnly("oatmeal")},
			},
			volumes: []coreapi.Volume{tmpVolume, cookieVolumeOnly("oatmeal")},
		},
		{
			name: "intentional empty string cookiefile secrets is valid",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					ExtraRefs: []prowapi.Refs{{}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages:    &prowapi.UtilityImages{},
						CookiefileSecret: pStr(""),
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:            []prowapi.Refs{{}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					SrcRoot:            codeMount.MountPath,
					Log:                CloneLogPath(logMount),
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{logMount, codeMount, tmpMount},
			},
			volumes: []coreapi.Volume{tmpVolume},
		},
		{
			name: "include oauth token secret when set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					ExtraRefs: []prowapi.Refs{{}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
						OauthTokenSecret: &prowapi.OauthTokenSecret{
							Name: "oauth-secret",
							Key:  "oauth-file",
						},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:            []prowapi.Refs{{}},
					GitUserEmail:       clonerefs.DefaultGitUserEmail,
					GitUserName:        clonerefs.DefaultGitUserName,
					SrcRoot:            codeMount.MountPath,
					Log:                CloneLogPath(logMount),
					OauthTokenFile:     "/secrets/oauth/oauth-file",
					GitHubAPIEndpoints: []string{github.DefaultAPIEndpoint},
				}),
				VolumeMounts: []coreapi.VolumeMount{
					logMount, codeMount,
					{Name: "oauth-secret", ReadOnly: true, MountPath: "/secrets/oauth"},
					tmpMount,
				},
			},
			volumes: []coreapi.Volume{
				{
					Name: "oauth-secret",
					VolumeSource: coreapi.VolumeSource{
						Secret: &coreapi.SecretVolumeSource{
							SecretName: "oauth-secret",
							Items: []coreapi.KeyToPath{{
								Key:  "oauth-file",
								Path: "./oauth-file",
							}},
						},
					},
				},
				tmpVolume,
			},
		},
		{
			name: "include GitHub App ID and private key when set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					ExtraRefs: []prowapi.Refs{{}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
						GitHubAppID:   "123456",
						GitHubAppPrivateKeySecret: &prowapi.GitHubAppPrivateKeySecret{
							Name: "github-app-secret",
							Key:  "private-key",
						},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:                 []prowapi.Refs{{}},
					GitUserEmail:            clonerefs.DefaultGitUserEmail,
					GitUserName:             clonerefs.DefaultGitUserName,
					SrcRoot:                 codeMount.MountPath,
					Log:                     CloneLogPath(logMount),
					GitHubAPIEndpoints:      []string{github.DefaultAPIEndpoint},
					GitHubAppID:             "123456",
					GitHubAppPrivateKeyFile: "/secrets/github-app/private-key",
				}),
				VolumeMounts: []coreapi.VolumeMount{
					logMount, codeMount,
					{
						Name:      "github-app-secret",
						ReadOnly:  true,
						MountPath: "/secrets/github-app",
					},
					tmpMount,
				},
			},
			volumes: []coreapi.Volume{
				{
					Name: "github-app-secret",
					VolumeSource: coreapi.VolumeSource{
						Secret: &coreapi.SecretVolumeSource{
							SecretName: "github-app-secret",
							Items: []coreapi.KeyToPath{{
								Key:  "private-key",
								Path: "./private-key",
							}},
						},
					},
				},
				tmpVolume,
			},
		},
		{
			name: "include custom GitHub API endpoints when set",
			pj: prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					ExtraRefs: []prowapi.Refs{{}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages:      &prowapi.UtilityImages{},
						GitHubAPIEndpoints: []string{"http://example.com"},
						GitHubAppID:        "123456",
						GitHubAppPrivateKeySecret: &prowapi.GitHubAppPrivateKeySecret{
							Name: "github-app-secret",
							Key:  "private-key",
						},
					},
				},
			},
			expected: &coreapi.Container{
				Name: cloneRefsName,
				Env: envOrDie(clonerefs.Options{
					GitRefs:                 []prowapi.Refs{{}},
					GitUserEmail:            clonerefs.DefaultGitUserEmail,
					GitUserName:             clonerefs.DefaultGitUserName,
					SrcRoot:                 codeMount.MountPath,
					Log:                     CloneLogPath(logMount),
					GitHubAPIEndpoints:      []string{"http://example.com"},
					GitHubAppID:             "123456",
					GitHubAppPrivateKeyFile: "/secrets/github-app/private-key",
				}),
				VolumeMounts: []coreapi.VolumeMount{
					logMount, codeMount,
					{
						Name:      "github-app-secret",
						ReadOnly:  true,
						MountPath: "/secrets/github-app",
					},
					tmpMount,
				},
			},
			volumes: []coreapi.Volume{
				{
					Name: "github-app-secret",
					VolumeSource: coreapi.VolumeSource{
						Secret: &coreapi.SecretVolumeSource{
							SecretName: "github-app-secret",
							Items: []coreapi.KeyToPath{{
								Key:  "private-key",
								Path: "./private-key",
							}},
						},
					},
				},
				tmpVolume,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lm := logMount
			if tc.logMountOverride != nil {
				lm = *tc.logMountOverride
			}
			cm := codeMount
			if tc.codeMountOverride != nil {
				cm = *tc.codeMountOverride
			}
			actual, refs, volumes, err := CloneRefs(tc.pj, cm, lm)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Error("failed to receive expected exception")
			case !equality.Semantic.DeepEqual(tc.expected, actual):
				t.Errorf("unexpected container:\n%s", diff.ObjectReflectDiff(tc.expected, actual))
			case !equality.Semantic.DeepEqual(tc.volumes, volumes):
				t.Errorf("unexpected volume:\n%s", diff.ObjectReflectDiff(tc.volumes, volumes))
			case actual != nil:
				var er []prowapi.Refs
				if tc.pj.Spec.Refs != nil {
					er = append(er, *tc.pj.Spec.Refs)
				}
				er = append(er, tc.pj.Spec.ExtraRefs...)
				if !equality.Semantic.DeepEqual(refs, er) {
					t.Errorf("unexpected refs:\n%s", diff.ObjectReflectDiff(er, refs))
				}
			}
		})
	}
}

func TestProwJobToPod(t *testing.T) {
	truth := true
	tests := []struct {
		podName  string
		buildID  string
		labels   map[string]string
		pjSpec   prowapi.ProwJobSpec
		pjStatus prowapi.ProwJobStatus
	}{
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				Agent:   prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "pull-branch-name",
						Title:   "pull-title",
					}},
				},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image: "tester",
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
			pjStatus: prowapi.ProwJobStatus{
				BuildID: "blabla",
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
						MediaTypes:   map[string]string{"log": "text/plain"},
					},
					GCSCredentialsSecret: pStr("secret-name"),
					CookiefileSecret:     pStr("yummy/.gitcookies"),
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "my-big-change",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
					},
					GCSCredentialsSecret: pStr("secret-name"),
					CookiefileSecret:     pStr("yummy"),
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "fix-typos-99",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
					},
					GCSCredentialsSecret: pStr("secret-name"),
					SSHKeySecrets:        []string{"ssh-1", "ssh-2"},
					SSHHostFingerprints:  []string{"hello", "world"},
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "fixes-fixes-fixes",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
							TerminationMessagePolicy: coreapi.TerminationMessageReadFile,
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
					},
					GCSCredentialsSecret: pStr("secret-name"),
					SSHKeySecrets:        []string{"ssh-1", "ssh-2"},
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "fixes-9",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PeriodicJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
					},
					GCSCredentialsSecret: pStr("secret-name"),
					SSHKeySecrets:        []string{"ssh-1", "ssh-2"},
				},
				Agent: prowapi.KubernetesAgent,
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
					},
					GCSCredentialsSecret: pStr("secret-name"),
					SSHKeySecrets:        []string{"ssh-1", "ssh-2"},
					SkipCloning:          &truth,
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "best-branch-name",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{
					{
						Org:  "extra-org",
						Repo: "extra-repo",
					},
				},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
					},
					GCSCredentialsSecret: pStr("secret-name"),
					SSHKeySecrets:        []string{"ssh-1", "ssh-2"},
					CookiefileSecret:     pStr("yummy"),
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "pr-head-ref-11",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{
					{
						Org:  "extra-org",
						Repo: "extra-repo",
					},
				},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Name:    "test-0",
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
						{
							Name:    "test-1",
							Image:   "othertester",
							Command: []string{"/bin/otherthing"},
							Args:    []string{"other", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "stones"},
							},
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
						MediaTypes:   map[string]string{"log": "text/plain"},
					},
					// Specify K8s SA rather than cloud storage secret key.
					DefaultServiceAccountName: pStr("default-SA"),
					CookiefileSecret:          pStr("yummy/.gitcookies"),
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "orig-branch-name",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
		},
		{
			podName: "pod",
			buildID: "blabla",
			labels:  map[string]string{"needstobe": "inherited"},
			pjSpec: prowapi.ProwJobSpec{
				Type:    prowapi.PresubmitJob,
				Job:     "job-name",
				Context: "job-context",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: 120 * time.Minute},
					GracePeriod: &prowapi.Duration{Duration: 10 * time.Second},
					UtilityImages: &prowapi.UtilityImages{
						CloneRefs:  "clonerefs:tag",
						InitUpload: "initupload:tag",
						Entrypoint: "entrypoint:tag",
						Sidecar:    "sidecar:tag",
					},
					GCSConfiguration: &prowapi.GCSConfiguration{
						Bucket:       "my-bucket",
						PathStrategy: "legacy",
						DefaultOrg:   "kubernetes",
						DefaultRepo:  "kubernetes",
						MediaTypes:   map[string]string{"log": "text/plain"},
					},
					// Specify K8s SA rather than cloud storage secret key.
					DefaultServiceAccountName: pStr("default-SA"),
					CookiefileSecret:          pStr("yummy/.gitcookies"),
					RunAsGroup:                pInt64(1000),
					RunAsUser:                 pInt64(1000),
					FsGroup:                   pInt64(2000),
				},
				Agent: prowapi.KubernetesAgent,
				Refs: &prowapi.Refs{
					Org:     "org-name",
					Repo:    "repo-name",
					BaseRef: "base-ref",
					BaseSHA: "base-sha",
					Pulls: []prowapi.Pull{{
						Number:  1,
						Author:  "author-name",
						SHA:     "pull-sha",
						HeadRef: "orig-branch-name",
						Title:   "pull-title",
					}},
					PathAlias: "somewhere/else",
				},
				ExtraRefs: []prowapi.Refs{},
				PodSpec: &coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Image:   "tester",
							Command: []string{"/bin/thing"},
							Args:    []string{"some", "args"},
							Env: []coreapi.EnvVar{
								{Name: "MY_ENV", Value: "rocks"},
							},
						},
					},
				},
			},
		},
	}

	findContainer := func(name string, pod coreapi.Pod) *coreapi.Container {
		for _, c := range pod.Spec.Containers {
			if c.Name == name {
				return &c
			}
		}
		return nil
	}
	findEnv := func(key string, container coreapi.Container) *string {
		for _, env := range container.Env {
			if env.Name == key {
				v := env.Value
				return &v
			}

		}
		return nil
	}

	type checker interface {
		ConfigVar() string
		LoadConfig(string) error
		Validate() error
	}

	checkEnv := func(pod coreapi.Pod, name string, opt checker) error {
		c := findContainer(name, pod)
		if c == nil {
			return nil
		}
		env := opt.ConfigVar()
		val := findEnv(env, *c)
		if val == nil {
			return fmt.Errorf("missing %s env var", env)
		}
		if err := opt.LoadConfig(*val); err != nil {
			return fmt.Errorf("load: %w", err)
		}
		if err := opt.Validate(); err != nil {
			return fmt.Errorf("validate: %w", err)
		}
		return nil
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			pj := prowapi.ProwJob{ObjectMeta: metav1.ObjectMeta{Name: test.podName, Labels: test.labels}, Spec: test.pjSpec, Status: test.pjStatus}
			pj.Status.BuildID = test.buildID
			got, err := ProwJobToPod(pj)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			fixtureName := filepath.Join("testdata", fmt.Sprintf("%s.yaml", strings.ReplaceAll(t.Name(), "/", "_")))
			if os.Getenv("UPDATE") != "" {
				marshalled, err := yaml.Marshal(got)
				if err != nil {
					t.Fatalf("failed to marhsal pod: %v", err)
				}
				if err := os.WriteFile(fixtureName, marshalled, 0644); err != nil {
					t.Errorf("failed to update fixture: %v", err)
				}
			}
			expectedRaw, err := os.ReadFile(fixtureName)
			if err != nil {
				t.Fatalf("failed to read fixture: %v", err)
			}
			expected := &coreapi.Pod{}
			if err := yaml.Unmarshal(expectedRaw, expected); err != nil {
				t.Fatalf("failed to unmarshal fixture: %v", err)
			}
			if !equality.Semantic.DeepEqual(got, expected) {
				t.Errorf("unexpected pod diff:\n%s. You can update the fixtures by running this test with UPDATE=true if this is expected.", diff.ObjectReflectDiff(expected, got))
			}
			if err := checkEnv(*got, "sidecar", sidecar.NewOptions()); err != nil {
				t.Errorf("bad sidecar env: %v", err)
			}
			if err := checkEnv(*got, "initupload", initupload.NewOptions()); err != nil {
				t.Errorf("bad clonerefs env: %v", err)
			}
			if err := checkEnv(*got, "clonerefs", &clonerefs.Options{}); err != nil {
				t.Errorf("bad clonerefs env: %v", err)
			}
			if test.pjSpec.DecorationConfig != nil { // all jobs get a test container
				// But only decorated jobs need valid entrypoint options
				if err := checkEnv(*got, "test", entrypoint.NewOptions()); err != nil {
					t.Errorf("bad test entrypoint: %v", err)
				}
			}
		})
	}
}

func TestProwJobToPod_setsTerminationGracePeriodSeconds(t *testing.T) {
	testCases := []struct {
		name                                  string
		prowjob                               *prowapi.ProwJob
		expectedTerminationGracePeriodSeconds int64
	}{
		{
			name: "GracePeriodSeconds from decoration config",
			prowjob: &prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					PodSpec: &coreapi.PodSpec{Containers: []coreapi.Container{{}}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
						GracePeriod:   &prowapi.Duration{Duration: 10 * time.Second},
					},
				},
			},
			expectedTerminationGracePeriodSeconds: 12,
		},
		{
			name: "Existing GracePeriodSeconds is not overwritten",
			prowjob: &prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					PodSpec: &coreapi.PodSpec{TerminationGracePeriodSeconds: utilpointer.Int64(60), Containers: []coreapi.Container{{}}},
					DecorationConfig: &prowapi.DecorationConfig{
						UtilityImages: &prowapi.UtilityImages{},
						Timeout:       &prowapi.Duration{Duration: 10 * time.Second},
					},
				},
			},
			expectedTerminationGracePeriodSeconds: 60,
		},
	}

	for idx := range testCases {
		tc := testCases[idx]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := decorate(tc.prowjob.Spec.PodSpec, tc.prowjob, map[string]string{}, ""); err != nil {
				t.Fatalf("decoration failed: %v", err)
			}
			if tc.prowjob.Spec.PodSpec.TerminationGracePeriodSeconds == nil || *tc.prowjob.Spec.PodSpec.TerminationGracePeriodSeconds != tc.expectedTerminationGracePeriodSeconds {
				t.Errorf("expected pods TerminationGracePeriodSeconds to be %d was %v", tc.expectedTerminationGracePeriodSeconds, tc.prowjob.Spec.PodSpec.TerminationGracePeriodSeconds)
			}
		})
	}
}

func TestSidecar(t *testing.T) {
	var testCases = []struct {
		name                                    string
		config                                  *prowapi.DecorationConfig
		gcsOptions                              gcsupload.Options
		blobStorageMounts                       []coreapi.VolumeMount
		logMount                                coreapi.VolumeMount
		outputMount                             *coreapi.VolumeMount
		encodedJobSpec                          string
		requirePassingEntries, ignoreInterrupts bool
		secretVolumeMounts                      []coreapi.VolumeMount
		wrappers                                []wrapper.Options
	}{
		{
			name: "basic case",
			config: &prowapi.DecorationConfig{
				UtilityImages: &prowapi.UtilityImages{Sidecar: "sidecar-image"},
			},
			gcsOptions: gcsupload.Options{
				Items:            []string{"first", "second"},
				GCSConfiguration: &prowapi.GCSConfiguration{Bucket: "bucket"},
			},
			blobStorageMounts:     []coreapi.VolumeMount{{Name: "blob", MountPath: "/blob"}},
			logMount:              coreapi.VolumeMount{Name: "logs", MountPath: "/logs"},
			outputMount:           &coreapi.VolumeMount{Name: "outputs", MountPath: "/outputs"},
			encodedJobSpec:        "spec",
			requirePassingEntries: true,
			ignoreInterrupts:      true,
			wrappers:              []wrapper.Options{{Args: []string{"yes"}}},
		},
		{
			name: "with secrets",
			config: &prowapi.DecorationConfig{
				UtilityImages: &prowapi.UtilityImages{Sidecar: "sidecar-image"},
			},
			gcsOptions: gcsupload.Options{
				Items:            []string{"first", "second"},
				GCSConfiguration: &prowapi.GCSConfiguration{Bucket: "bucket"},
			},
			blobStorageMounts:     []coreapi.VolumeMount{{Name: "blob", MountPath: "/blob"}},
			logMount:              coreapi.VolumeMount{Name: "logs", MountPath: "/logs"},
			outputMount:           &coreapi.VolumeMount{Name: "outputs", MountPath: "/outputs"},
			encodedJobSpec:        "spec",
			requirePassingEntries: true,
			ignoreInterrupts:      true,
			secretVolumeMounts: []coreapi.VolumeMount{
				{Name: "very", MountPath: "/very"},
				{Name: "secret", MountPath: "/secret"},
				{Name: "stuff", MountPath: "/stuff"},
			},
			wrappers: []wrapper.Options{{Args: []string{"yes"}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			container, err := Sidecar(
				testCase.config, testCase.gcsOptions,
				testCase.blobStorageMounts, testCase.logMount, testCase.outputMount,
				testCase.encodedJobSpec,
				testCase.requirePassingEntries, testCase.ignoreInterrupts,
				testCase.secretVolumeMounts, testCase.wrappers...,
			)
			if err != nil {
				t.Fatalf("%s: got an error from Sidecar(): %v", testCase.name, err)
			}
			testutil.CompareWithSerializedFixture(t, container)
		})
	}
}

func TestDecorate(t *testing.T) {
	gCSCredentialsSecret := "gcs-secret"
	defaultServiceAccountName := "default-sa"
	censor := true
	ignoreInterrupts := true
	resourcePtr := func(s string) *resource.Quantity {
		q := resource.MustParse(s)
		return &q
	}
	var testCases = []struct {
		name      string
		spec      *coreapi.PodSpec
		pj        *prowapi.ProwJob
		rawEnv    map[string]string
		outputDir string
	}{
		{
			name: "basic happy case",
			spec: &coreapi.PodSpec{
				Volumes: []coreapi.Volume{
					{Name: "secret", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "secretname"}}},
				},
				Containers: []coreapi.Container{
					{Name: "test", Command: []string{"/bin/ls"}, Args: []string{"-l", "-a"}, VolumeMounts: []coreapi.VolumeMount{{Name: "secret", MountPath: "/secret"}}},
				},
				ServiceAccountName: "tester",
			},
			pj: &prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{
						Timeout:     &prowapi.Duration{Duration: time.Minute},
						GracePeriod: &prowapi.Duration{Duration: time.Hour},
						UtilityImages: &prowapi.UtilityImages{
							CloneRefs:  "cloneimage",
							InitUpload: "initimage",
							Entrypoint: "entrypointimage",
							Sidecar:    "sidecarimage",
						},
						Resources: &prowapi.Resources{
							CloneRefs:       &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							InitUpload:      &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							PlaceEntrypoint: &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							Sidecar:         &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
						},
						GCSConfiguration: &prowapi.GCSConfiguration{
							Bucket:       "bucket",
							PathStrategy: "single",
							DefaultOrg:   "org",
							DefaultRepo:  "repo",
						},
						GCSCredentialsSecret:      &gCSCredentialsSecret,
						DefaultServiceAccountName: &defaultServiceAccountName,
					},
					Refs: &prowapi.Refs{
						Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "abcd1234",
						Pulls: []prowapi.Pull{{Number: 1, SHA: "aksdjhfkds"}},
					},
					ExtraRefs: []prowapi.Refs{{Org: "other", Repo: "something", BaseRef: "release", BaseSHA: "sldijfsd"}},
				},
			},
			rawEnv: map[string]string{"custom": "env"},
		},
		{
			name: "enforcing memory limit",
			spec: &coreapi.PodSpec{
				Containers: []coreapi.Container{
					{
						Name:    "test",
						Command: []string{"/bin/ls"},
						Args:    []string{"-l", "-a"},
						Resources: coreapi.ResourceRequirements{
							Requests: coreapi.ResourceList{
								"memory": resource.MustParse("8Gi"),
							},
							Limits: coreapi.ResourceList{
								"memory": resource.MustParse("100Gi"),
							},
						},
					},
				},
				ServiceAccountName: "tester",
			},
			pj: &prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{
						Timeout:     &prowapi.Duration{Duration: time.Minute},
						GracePeriod: &prowapi.Duration{Duration: time.Hour},
						UtilityImages: &prowapi.UtilityImages{
							CloneRefs:  "cloneimage",
							InitUpload: "initimage",
							Entrypoint: "entrypointimage",
							Sidecar:    "sidecarimage",
						},
						Resources: &prowapi.Resources{
							CloneRefs:       &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							InitUpload:      &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							PlaceEntrypoint: &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							Sidecar:         &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
						},
						GCSConfiguration: &prowapi.GCSConfiguration{
							Bucket:       "bucket",
							PathStrategy: "single",
							DefaultOrg:   "org",
							DefaultRepo:  "repo",
						},
						GCSCredentialsSecret:        &gCSCredentialsSecret,
						DefaultServiceAccountName:   &defaultServiceAccountName,
						SetLimitEqualsMemoryRequest: utilpointer.Bool(true),
					},
					Refs: &prowapi.Refs{
						Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "abcd1234",
						Pulls: []prowapi.Pull{{Number: 1, SHA: "aksdjhfkds"}},
					},
					ExtraRefs: []prowapi.Refs{{Org: "other", Repo: "something", BaseRef: "release", BaseSHA: "sldijfsd"}},
				},
			},
			rawEnv: map[string]string{"custom": "env"},
		},
		{
			name: "default memory request",
			spec: &coreapi.PodSpec{
				Containers: []coreapi.Container{
					{
						Name:    "test",
						Command: []string{"/bin/ls"},
						Args:    []string{"-l", "-a"},
						Resources: coreapi.ResourceRequirements{
							Requests: coreapi.ResourceList{
								"memory": resource.MustParse("8Gi"),
							},
							Limits: coreapi.ResourceList{
								"memory": resource.MustParse("100Gi"),
							},
						},
					},
					{
						Name:    "test2",
						Command: []string{"/bin/ls"},
						Args:    []string{"-l", "-a"},
					},
				},
				ServiceAccountName: "tester",
			},
			pj: &prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{
						Timeout:     &prowapi.Duration{Duration: time.Minute},
						GracePeriod: &prowapi.Duration{Duration: time.Hour},
						UtilityImages: &prowapi.UtilityImages{
							CloneRefs:  "cloneimage",
							InitUpload: "initimage",
							Entrypoint: "entrypointimage",
							Sidecar:    "sidecarimage",
						},
						Resources: &prowapi.Resources{
							CloneRefs:       &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							InitUpload:      &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							PlaceEntrypoint: &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							Sidecar:         &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
						},
						GCSConfiguration: &prowapi.GCSConfiguration{
							Bucket:       "bucket",
							PathStrategy: "single",
							DefaultOrg:   "org",
							DefaultRepo:  "repo",
						},
						GCSCredentialsSecret:        &gCSCredentialsSecret,
						DefaultServiceAccountName:   &defaultServiceAccountName,
						SetLimitEqualsMemoryRequest: utilpointer.Bool(true),
						DefaultMemoryRequest:        resourcePtr("4Gi"),
					},
					Refs: &prowapi.Refs{
						Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "abcd1234",
						Pulls: []prowapi.Pull{{Number: 1, SHA: "aksdjhfkds"}},
					},
					ExtraRefs: []prowapi.Refs{{Org: "other", Repo: "something", BaseRef: "release", BaseSHA: "sldijfsd"}},
				},
			},
			rawEnv: map[string]string{"custom": "env"},
		},
		{
			name: "censor secrets in sidecar",
			spec: &coreapi.PodSpec{
				Volumes: []coreapi.Volume{
					{Name: "secret", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "secretname"}}},
				},
				Containers: []coreapi.Container{
					{Name: "test", Command: []string{"/bin/ls"}, Args: []string{"-l", "-a"}, VolumeMounts: []coreapi.VolumeMount{{Name: "secret", MountPath: "/secret"}}},
				},
				ServiceAccountName: "tester",
			},
			pj: &prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{
						Timeout:     &prowapi.Duration{Duration: time.Minute},
						GracePeriod: &prowapi.Duration{Duration: time.Hour},
						UtilityImages: &prowapi.UtilityImages{
							CloneRefs:  "cloneimage",
							InitUpload: "initimage",
							Entrypoint: "entrypointimage",
							Sidecar:    "sidecarimage",
						},
						Resources: &prowapi.Resources{
							CloneRefs:       &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							InitUpload:      &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							PlaceEntrypoint: &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							Sidecar:         &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
						},
						GCSConfiguration: &prowapi.GCSConfiguration{
							Bucket:       "bucket",
							PathStrategy: "single",
							DefaultOrg:   "org",
							DefaultRepo:  "repo",
						},
						GCSCredentialsSecret:      &gCSCredentialsSecret,
						DefaultServiceAccountName: &defaultServiceAccountName,
						CensorSecrets:             &censor,
					},
					Refs: &prowapi.Refs{
						Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "abcd1234",
						Pulls: []prowapi.Pull{{Number: 1, SHA: "aksdjhfkds"}},
					},
					ExtraRefs: []prowapi.Refs{{Org: "other", Repo: "something", BaseRef: "release", BaseSHA: "sldijfsd"}},
				},
			},
			rawEnv: map[string]string{"custom": "env"},
		},
		{
			name: "ignore interrupts in sidecar",
			spec: &coreapi.PodSpec{
				Volumes: []coreapi.Volume{
					{Name: "secret", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "secretname"}}},
				},
				Containers: []coreapi.Container{
					{Name: "test", Command: []string{"/bin/ls"}, Args: []string{"-l", "-a"}, VolumeMounts: []coreapi.VolumeMount{{Name: "secret", MountPath: "/secret"}}},
				},
				ServiceAccountName: "tester",
			},
			pj: &prowapi.ProwJob{
				Spec: prowapi.ProwJobSpec{
					DecorationConfig: &prowapi.DecorationConfig{
						Timeout:                 &prowapi.Duration{Duration: time.Minute},
						GracePeriod:             &prowapi.Duration{Duration: time.Hour},
						UploadIgnoresInterrupts: &ignoreInterrupts,
						UtilityImages: &prowapi.UtilityImages{
							CloneRefs:  "cloneimage",
							InitUpload: "initimage",
							Entrypoint: "entrypointimage",
							Sidecar:    "sidecarimage",
						},
						Resources: &prowapi.Resources{
							CloneRefs:       &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							InitUpload:      &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							PlaceEntrypoint: &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
							Sidecar:         &coreapi.ResourceRequirements{Limits: coreapi.ResourceList{"cpu": resource.Quantity{}}, Requests: coreapi.ResourceList{"memory": resource.Quantity{}}},
						},
						GCSConfiguration: &prowapi.GCSConfiguration{
							Bucket:       "bucket",
							PathStrategy: "single",
							DefaultOrg:   "org",
							DefaultRepo:  "repo",
						},
						GCSCredentialsSecret:      &gCSCredentialsSecret,
						DefaultServiceAccountName: &defaultServiceAccountName,
					},
					Refs: &prowapi.Refs{
						Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "abcd1234",
						Pulls: []prowapi.Pull{{Number: 1, SHA: "aksdjhfkds"}},
					},
					ExtraRefs: []prowapi.Refs{{Org: "other", Repo: "something", BaseRef: "release", BaseSHA: "sldijfsd"}},
				},
			},
			rawEnv: map[string]string{"custom": "env"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := decorate(testCase.spec, testCase.pj, testCase.rawEnv, testCase.outputDir); err != nil {
				t.Fatalf("got an error from decorate(): %v", err)
			}
			testutil.CompareWithSerializedFixture(t, testCase.spec)
		})
	}
}
