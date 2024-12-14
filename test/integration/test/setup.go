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

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/flowcontrol"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultNamespace = "default"
	testpodNamespace = "test-pods"
)

var (
	clusterContext = flag.String("cluster", "kind-kind-prow-integration", "The context of cluster to use for test")

	jobConfigMux      sync.Mutex
	prowComponentsMux sync.Mutex
)

func getClusterContext() string {
	return *clusterContext
}

func NewClients(configPath, clusterName string) (ctrlruntimeclient.Client, error) {
	cfg, err := NewRestConfig(configPath, clusterName)
	if err != nil {
		return nil, err
	}
	cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	return ctrlruntimeclient.New(cfg, ctrlruntimeclient.Options{})
}

func NewRestConfig(configPath, clusterName string) (*rest.Config, error) {
	var loader clientcmd.ClientConfigLoader
	if configPath != "" {
		loader = &clientcmd.ClientConfigLoadingRules{ExplicitPath: configPath}
	} else {
		loader = clientcmd.NewDefaultClientConfigLoadingRules()
	}

	overrides := clientcmd.ConfigOverrides{}
	// Override the cluster name if provided.
	if clusterName != "" {
		overrides.Context.Cluster = clusterName
		overrides.CurrentContext = clusterName
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loader, &overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed create rest config: %w", err)
	}

	return cfg, nil
}

func getPodLogs(clientset *kubernetes.Clientset, namespace, podName string, opts *coreapi.PodLogOptions) (string, error) {
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, opts)
	podLogs, err := req.Stream(context.Background())
	if err != nil {
		return "", fmt.Errorf("error in opening stream")
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", fmt.Errorf("error in copy information from podLogs to buf")
	}
	str := buf.String()

	return str, nil
}

func rolloutDeployment(t *testing.T, ctx context.Context, client ctrlruntimeclient.Client, name string) error {
	prowComponentsMux.Lock()
	defer prowComponentsMux.Unlock()

	var depl appsv1.Deployment
	if err := client.Get(ctx, types.NamespacedName{Name: name, Namespace: defaultNamespace}, &depl); err != nil {
		return fmt.Errorf("failed to get Deployment: %w", err)
	}

	if replicas := depl.Spec.Replicas; replicas == nil || *replicas < 1 {
		return errors.New("cannot restart a Deployment with zero replicas.")
	}

	labels := depl.Spec.Template.Labels
	if labels == nil {
		// This should never happen.
		labels = map[string]string{}
	}
	labels["restart"] = RandomString(t)

	t.Logf("Restarting %s...", name)
	if err := client.Update(ctx, &depl); err != nil {
		return fmt.Errorf("failed to update Deployment: %w", err)
	}

	timeout := 30 * time.Second
	var lastErr string
	if err := wait.PollUntilContextTimeout(ctx, time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		var current appsv1.Deployment
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(&depl), &current); err != nil {
			return false, fmt.Errorf("failed to get current Deployment: %w", err)
		}

		replicas := current.Spec.Replicas
		if replicas == nil || *replicas < 1 {
			// This should never happen.
			return false, errors.New("Deployment has no replicas defined")
		}

		var errMsg string
		if remaining := *replicas - current.Status.UpdatedReplicas; remaining != 0 {
			errMsg = fmt.Sprintf("not all replicas updated (%d remaining)", remaining)
		} else if remaining := *replicas - current.Status.AvailableReplicas; remaining != 0 {
			errMsg = fmt.Sprintf("not all replicas available (%d remaining)", remaining)
		} else if remaining := *replicas - current.Status.ReadyReplicas; remaining != 0 {
			errMsg = fmt.Sprintf("not all replicas ready (%d remaining)", remaining)
		} else if current.Status.UnavailableReplicas != 0 {
			errMsg = fmt.Sprintf("%d unavailable replicas remaining", current.Status.UnavailableReplicas)
		}

		if errMsg != "" {
			if errMsg != lastErr {
				t.Logf("Still waiting: %s.", errMsg)
			}
		}

		lastErr = errMsg

		return errMsg == "", nil
	}); err != nil {
		return fmt.Errorf("Deployment did not fully roll out after %v: %w", timeout, err)
	}

	return nil
}

// RandomString generates random string of 32 characters in length, and fail if it failed
func RandomString(t *testing.T) string {
	b := make([]byte, 512)
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("failed to generate random: %v", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b[:]))[:32]
}

func updateJobConfig(ctx context.Context, kubeClient ctrlruntimeclient.Client, filename string, rawConfig []byte) error {
	jobConfigMux.Lock()
	defer jobConfigMux.Unlock()

	var existingMap coreapi.ConfigMap
	if err := kubeClient.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: defaultNamespace,
		Name:      "job-config",
	}, &existingMap); err != nil {
		return err
	}

	if existingMap.BinaryData == nil {
		existingMap.BinaryData = make(map[string][]byte)
	}
	existingMap.BinaryData[filename] = rawConfig
	return kubeClient.Update(ctx, &existingMap)
}

// execRemoteCommand is the Golang-equivalent of "kubectl exec". The command
// string should be something like {"/bin/sh", "-c", "..."} if you want to run a
// shell script.
//
// Adapted from https://discuss.kubernetes.io/t/go-client-exec-ing-a-shel-command-in-pod/5354/5.
func execRemoteCommand(restCfg *rest.Config, clientset *kubernetes.Clientset, pod *coreapi.Pod, command []string) (string, string, error) {
	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	request := clientset.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&coreapi.PodExecOptions{
			Command: command,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(restCfg, "POST", request.URL())
	if err != nil {
		return "", "", err
	}

	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdout: buf,
		Stderr: errBuf,
	})
	if err != nil {
		return "", "", fmt.Errorf("%w Failed executing command %s on %v/%v", err, command, pod.Namespace, pod.Name)
	}

	// Return stdout, stderr.
	return buf.String(), errBuf.String(), nil
}
