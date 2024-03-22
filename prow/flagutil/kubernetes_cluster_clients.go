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

package flagutil

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	k8sauthorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	authorizationv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	prow "k8s.io/test-infra/prow/client/clientset/versioned"
	prowv1 "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	"k8s.io/test-infra/prow/kube"
)

func init() {
	prometheus.MustRegister(clientCreationFailures)
}

// KubernetesOptions holds options for interacting with Kubernetes.
// These options are both useful for clients interacting with ProwJobs
// and other resources on the infrastructure cluster, as well as Pods
// on build clusters.
type KubernetesOptions struct {
	kubeconfig               string
	kubeconfigDir            string
	kubeconfigSuffix         string
	projectedTokenFile       string
	noInClusterConfig        bool
	NOInClusterConfigDefault bool

	// from the setter SetDisabledClusters
	disabledClusters sets.Set[string]

	// from resolution
	resolved                    bool
	dryRun                      bool
	prowJobClientset            prow.Interface
	clusterConfigs              map[string]rest.Config
	kubernetesClientsByContext  map[string]kubernetes.Interface
	infrastructureClusterConfig *rest.Config
	kubeconfigWach              *sync.Once
	kubeconfigWatchEvents       <-chan fsnotify.Event
}

var MissingPermissions = errors.New("missing permissions")

// AddKubeconfigChangeCallback adds a callback that gets called whenever the kubeconfig changes.
// The main usecase for this is to exit components that can not reload a kubeconfig at runtime
// so the kubelet restarts them
func (o *KubernetesOptions) AddKubeconfigChangeCallback(callback func()) error {
	if err := o.resolve(o.dryRun); err != nil {
		return fmt.Errorf("resolving failed: %w", err)
	}

	var err error
	o.kubeconfigWach.Do(func() {
		var watcher *fsnotify.Watcher
		watcher, err = fsnotify.NewWatcher()
		if err != nil {
			err = fmt.Errorf("failed to create watcher: %w", err)
			return
		}
		if o.kubeconfig != "" {
			err = watcher.Add(o.kubeconfig)
			if err != nil {
				err = fmt.Errorf("failed to watch %s: %w", o.kubeconfig, err)
				return
			}
		}
		if o.kubeconfigDir != "" {
			err = watcher.Add(o.kubeconfigDir)
			if err != nil {
				err = fmt.Errorf("failed to watch %s: %w", o.kubeconfigDir, err)
				return
			}
		}
		if o.kubeconfig == "" && o.kubeconfigDir == "" {
			if envVal := os.Getenv(clientcmd.RecommendedConfigPathEnvVar); envVal != "" {
				for _, element := range sets.List(sets.New[string](filepath.SplitList(envVal)...)) {
					err = watcher.Add(element)
					if err != nil {
						err = fmt.Errorf("failed to watch %s: %w", element, err)
						return
					}
				}
			}
		}
		o.kubeconfigWatchEvents = watcher.Events

		go func() {
			for watchErr := range watcher.Errors {
				logrus.WithError(watchErr).Error("Kubeconfig watcher errored")
			}
			if err := watcher.Close(); err != nil {
				logrus.WithError(err).Error("Failed to close watcher")
			}
		}()
	})
	if err != nil {
		return fmt.Errorf("failed to set up watches: %w", err)
	}

	go func() {
		for e := range o.kubeconfigWatchEvents {
			if e.Op == fsnotify.Chmod {
				// For some reason we get frequent chmod events
				continue
			}
			logrus.WithField("event", e.String()).Info("Kubeconfig changed")
			callback()
		}
	}()

	return nil
}

// LoadClusterConfigs returns the resolved rest.Configs and each callback function will be executed if
// the underlying kubeconfig files are modified. This function is for the case where the rest.Configs are
// needed without interests of the clients.
func (o *KubernetesOptions) LoadClusterConfigs(callBacks ...func()) (map[string]rest.Config, error) {
	var errs []error
	if !o.resolved {
		if err := o.resolve(o.dryRun); err != nil {
			errs = append(errs, fmt.Errorf("failed to resolve the kubeneates options: %w", err))
		}
	}

	if o.kubeconfig == "" && o.kubeconfigDir == "" {
		if envVal := os.Getenv(clientcmd.RecommendedConfigPathEnvVar); envVal != "" {
			if kubeconfigsFromEnv := strings.Split(envVal, ":"); len(kubeconfigsFromEnv) > 0 &&
				len(kubeconfigsFromEnv) > len(o.clusterConfigs) {
				errs = append(errs, fmt.Errorf("%s env var with value %s had %d elements but only got %d kubeconfigs",
					clientcmd.RecommendedConfigPathEnvVar, envVal, len(kubeconfigsFromEnv), len(o.clusterConfigs)))
			}
		}
	}

	for i, callBack := range callBacks {
		if callBack != nil {
			if err := o.AddKubeconfigChangeCallback(callBack); err != nil {
				errs = append(errs, fmt.Errorf("failed to add the %d-th kubeconfig change call back: %w", i, err))
			}
		}
	}
	return o.clusterConfigs, utilerrors.NewAggregate(errs)
}

// AddFlags injects Kubernetes options into the given FlagSet.
func (o *KubernetesOptions) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.kubeconfig, "kubeconfig", "", "Path to .kube/config file. If neither of --kubeconfig and --kubeconfig-dir is provided, use the in-cluster config. All contexts other than the default are used as build clusters.")
	fs.StringVar(&o.kubeconfigDir, "kubeconfig-dir", "", "Path to the directory containing kubeconfig files. If neither of --kubeconfig and --kubeconfig-dir is provided, use the in-cluster config. All contexts other than the default are used as build clusters.")
	fs.StringVar(&o.kubeconfigSuffix, "kubeconfig-suffix", "", "The files without the suffix will be ignored when loading kubeconfig files from --kubeconfig-dir. It must be used together with --kubeconfig-dir.")
	fs.StringVar(&o.projectedTokenFile, "projected-token-file", "", "A projected serviceaccount token file. If set, this will be configured as token file in the in-cluster config.")
	fs.BoolVar(&o.noInClusterConfig, "no-in-cluster-config", o.NOInClusterConfigDefault, "Not resolving InCluster Config if set.")
}

// Validate validates Kubernetes options.
func (o *KubernetesOptions) Validate(_ bool) error {
	if o.kubeconfig != "" {
		if _, err := os.Stat(o.kubeconfig); err != nil {
			return fmt.Errorf("error accessing --kubeconfig: %w", err)
		}
	}

	if o.kubeconfigDir != "" {
		if fileInfo, err := os.Stat(o.kubeconfigDir); err != nil {
			return fmt.Errorf("error accessing --kubeconfig-dir: %w", err)
		} else if !fileInfo.IsDir() {
			return fmt.Errorf("--kubeconfig-dir must be a directory")
		}
	}

	if o.kubeconfigSuffix != "" && o.kubeconfigDir == "" {
		return fmt.Errorf("--kubeconfig-dir must be set if --kubeconfig-suffix is set")
	}

	return nil
}

// resolve loads all of the clients we need and caches them for future calls.
func (o *KubernetesOptions) resolve(dryRun bool) error {
	if o.resolved {
		return nil
	}

	o.kubeconfigWach = &sync.Once{}

	clusterConfigs, err := kube.LoadClusterConfigs(kube.NewConfig(kube.ConfigFile(o.kubeconfig),
		kube.ConfigDir(o.kubeconfigDir), kube.ConfigProjectedTokenFile(o.projectedTokenFile),
		kube.NoInClusterConfig(o.noInClusterConfig), kube.ConfigSuffix(o.kubeconfigSuffix),
		kube.DisabledClusters(o.disabledClusters)))
	if err != nil {
		return fmt.Errorf("load --kubeconfig=%q configs: %w", o.kubeconfig, err)
	}
	o.clusterConfigs = clusterConfigs

	clients := map[string]kubernetes.Interface{}
	for context, config := range clusterConfigs {
		client, err := kubernetes.NewForConfig(&config)
		if err != nil {
			return fmt.Errorf("create %s kubernetes client: %w", context, err)
		}
		clients[context] = client
	}

	localCfg := clusterConfigs[kube.InClusterContext]
	o.infrastructureClusterConfig = &localCfg
	pjClient, err := prow.NewForConfig(&localCfg)
	if err != nil {
		return err
	}

	o.dryRun = dryRun
	if dryRun {
		return nil
	}

	o.prowJobClientset = pjClient
	o.kubernetesClientsByContext = clients
	o.resolved = true

	return nil
}

// ProwJobClientset returns a ProwJob clientset for use in informer factories.
func (o *KubernetesOptions) ProwJobClientset(dryRun bool) (prowJobClientset prow.Interface, err error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	if o.dryRun {
		return nil, errors.New("no dry-run prowjob clientset is supported in dry-run mode")
	}

	return o.prowJobClientset, nil
}

// ProwJobClient returns a ProwJob client.
func (o *KubernetesOptions) ProwJobClient(namespace string, dryRun bool) (prowJobClient prowv1.ProwJobInterface, err error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	if o.dryRun {
		return nil, errors.New("no dry-run prowjob client is supported in dry-run mode")
	}
	return o.prowJobClientset.ProwV1().ProwJobs(namespace), nil
}

// InfrastructureClusterConfig returns the *rest.Config for the infrastructure cluster
func (o *KubernetesOptions) InfrastructureClusterConfig(dryRun bool) (*rest.Config, error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	return o.infrastructureClusterConfig, nil
}

// InfrastructureClusterClient returns a Kubernetes client for the infrastructure cluster.
func (o *KubernetesOptions) InfrastructureClusterClient(dryRun bool) (kubernetesClient kubernetes.Interface, err error) {
	return o.ClusterClientForContext(kube.InClusterContext, dryRun)
}

// ClusterClientForContext returns a Kubernetes client for the given context name.
func (o *KubernetesOptions) ClusterClientForContext(context string, dryRun bool) (kubernetesClient kubernetes.Interface, err error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	if o.dryRun {
		return nil, errors.New("no dry-run kubernetes client is supported in dry-run mode")
	}

	client, exists := o.kubernetesClientsByContext[context]
	if !exists {
		return nil, fmt.Errorf("context %q does not exist in the provided config", context)
	}
	return client, nil
}

// BuildClusterClients returns Pod clients for build clusters.
func (o *KubernetesOptions) BuildClusterClients(namespace string, dryRun bool) (buildClusterClients map[string]corev1.PodInterface, err error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	if o.dryRun {
		return nil, errors.New("no dry-run pod client is supported for build clusters in dry-run mode")
	}

	buildClients := map[string]corev1.PodInterface{}
	for context, client := range o.kubernetesClientsByContext {
		buildClients[context] = client.CoreV1().Pods(namespace)
	}
	return buildClients, nil
}

// BuildClusterCoreV1Clients returns core v1 clients for build clusters.
func (o *KubernetesOptions) BuildClusterCoreV1Clients(dryRun bool) (v1Clients map[string]corev1.CoreV1Interface, err error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	if o.dryRun {
		return nil, errors.New("no dry-run pod client is supported for build clusters in dry-run mode")
	}

	clients := map[string]corev1.CoreV1Interface{}
	for context, client := range o.kubernetesClientsByContext {
		clients[context] = client.CoreV1()
	}
	return clients, nil
}

var clientCreationFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "kubernetes_failed_client_creations",
	Help: "The number of clusters for which we failed to create a client.",
}, []string{"cluster"})

// BuildClusterManagers returns a manager per buildCluster.
// Per default, LeaderElection and the metrics listener are disabled, as we assume
// that there is another manager for ProwJobs that handles that.
func (o *KubernetesOptions) BuildClusterManagers(dryRun bool, requiredTestPodVerbs []string, callBack func(), opts ...func(*manager.Options)) (map[string]manager.Manager, error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	options := manager.Options{
		LeaderElection:     false,
		MetricsBindAddress: "0",
		DryRunClient:       o.dryRun,
	}
	for _, opt := range opts {
		opt(&options)
	}

	res := map[string]manager.Manager{}
	var errs []error
	var lock sync.Mutex
	var threads sync.WaitGroup
	threads.Add(len(o.clusterConfigs))
	for buildClusterName, buildClusterConfig := range o.clusterConfigs {
		go func(name string, config rest.Config) {
			defer threads.Done()
			// This fails if we are unable to connect to the cluster --- either
			// due to missing or expired kubeconfig secrets, or if some other
			// auth-related executable (e.g., gke-gcloud-auth-plugin) is missing
			// from the base image.
			mgr, err := manager.New(&config, options)
			if err != nil {
				clientCreationFailures.WithLabelValues(name).Add(1)
				lock.Lock()
				errs = append(errs, fmt.Errorf("failed to construct manager for cluster %s: %w", name, err))
				lock.Unlock()
				return
			}

			// Check to see if we are able to perform actions against pods in
			// the build cluster. The actions are given in requiredTestPodVerbs.
			authzClient, err := authorizationv1.NewForConfig(&config)
			if err != nil {
				lock.Lock()
				errs = append(errs, fmt.Errorf("failed to construct authz client: %s", err))
				lock.Unlock()
				return
			}
			if err := CheckAuthorizations(authzClient.SelfSubjectAccessReviews(), options.Namespace, requiredTestPodVerbs); err != nil {
				lock.Lock()
				errs = append(errs, fmt.Errorf("failed pod resource authorization check: %w", err))
				lock.Unlock()
				return
			}

			lock.Lock()
			res[name] = mgr
			lock.Unlock()
		}(buildClusterName, buildClusterConfig)
	}
	threads.Wait()

	aggregatedErr := utilerrors.NewAggregate(errs)

	if aggregatedErr != nil {
		// Retry the build clusters that failed to be connected initially. If
		// suddenly we can connect to them successfully, execute the callback
		// function (e.g., to terminate this pod to force a restart). This is
		// useful where a build cluster is not reachable transiently, such as
		// when an API server upgrade causes connection problems.
		go func() {
			for {
				for buildClusterName, buildClusterConfig := range o.clusterConfigs {
					// Do not check already-successfully-checked build clusters.
					if _, ok := res[buildClusterName]; ok {
						continue
					}

					// If there are any errors with this (still troublesome)
					// build cluster, keep checking.
					if _, err := manager.New(&buildClusterConfig, options); err != nil {
						logrus.WithField("build-cluster", buildClusterName).Tracef("failed to construct build cluster manager: %s", err)
						continue
					}

					authzClient, err := authorizationv1.NewForConfig(&buildClusterConfig)
					if err != nil {
						logrus.WithField("build-cluster", buildClusterName).Tracef("failed to construct authz client: %s", err)
						continue
					}
					if err := CheckAuthorizations(authzClient.SelfSubjectAccessReviews(), options.Namespace, requiredTestPodVerbs); err != nil {
						logrus.WithField("build-cluster", buildClusterName).Tracef("failed to construct build cluster manager: %s", err)
						continue
					}

					logrus.WithField("build-cluster", buildClusterName).Info("Build cluster that failed to connect initially now worked.")
					callBack()
				}
				// Sleep arbitrarily amount of time
				time.Sleep(5 * time.Second)
			}
		}()
	} else {
		logrus.Debug("No error constructing managers for build clusters, skip polling build clusters.")
	}
	return res, aggregatedErr
}

// CheckAuthorizations checks if we are able to perform the required actions
// against test pods for the provided pod verbs (requiredTestPodVerbs).
func CheckAuthorizations(client authorizationv1.SelfSubjectAccessReviewInterface, namespace string, requiredTestPodVerbs []string) error {

	var errs []error
	// Unfortunately we have to do multiple API requests because there is no way
	// to check for multiple verbs on a resource at once. The closest
	// alternative is the "*" wildcard verb, but that appears to be overbroad
	// and fails on the integration test cluster. The approach we take here is
	// essentially equivalent to the following kubectl command:
	//
	// 	 $ cat <<EOF | kubectl --context=kind-kind-prow-integration create -f - -v 8
	//   apiVersion: authorization.k8s.io/v1
	//   kind: SubjectAccessReview
	//   spec:
	//     resourceAttributes:
	//       resource: pods
	//       verb: list # also test for get, create, etc
	//       namespace: test-pods
	//     user: system:serviceaccount:default:prow-controller-manager
	//   EOF
	//
	//  The difference in our case is that (1) we are running the below check
	//  *inside* the main service cluster itself, (2) we are running the check
	//  against an entirely different build cluster, and (3) we are using
	//  SelfSubjectAccessReview so that we don't have to provide a `user` field
	//  (so that this code can work with whatever user is the default when we're
	//  connecting to the build cluster).
	//
	// See
	// https://kubernetes.io/docs/reference/access-authn-authz/authorization/#checking-api-access
	// for more information.
	for _, verb := range requiredTestPodVerbs {
		ssar := k8sauthorizationv1.SelfSubjectAccessReview{
			Spec: k8sauthorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &k8sauthorizationv1.ResourceAttributes{
					Namespace: namespace,
					Verb:      verb,
					Resource:  "pods",
				},
			},
		}
		ssarExpanded, err := client.Create(context.TODO(), &ssar, metav1.CreateOptions{})
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !ssarExpanded.Status.Allowed {
			errs = append(errs, fmt.Errorf("%w: unable to %q pods", MissingPermissions, verb))
		}
	}

	return utilerrors.NewAggregate(errs)
}

// BuildClusterUncachedRuntimeClients returns ctrlruntimeclients for the build cluster in a non-caching implementation.
func (o *KubernetesOptions) BuildClusterUncachedRuntimeClients(dryRun bool) (map[string]ctrlruntimeclient.Client, error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}

	var errs []error
	clients := map[string]ctrlruntimeclient.Client{}
	for name := range o.clusterConfigs {
		cfg := o.clusterConfigs[name]
		client, err := ctrlruntimeclient.New(&cfg, ctrlruntimeclient.Options{})
		if err != nil {
			clientCreationFailures.WithLabelValues(name).Add(1)
			errs = append(errs, fmt.Errorf("failed to construct client for cluster %q: %w", name, err))
			continue
		}
		if o.dryRun {
			client = ctrlruntimeclient.NewDryRunClient(client)
		}
		clients[name] = client
	}

	return clients, utilerrors.NewAggregate(errs)
}

func (o *KubernetesOptions) KnownClusters(dryRun bool) (map[string]rest.Config, error) {
	if err := o.resolve(dryRun); err != nil {
		return nil, err
	}
	return o.clusterConfigs, nil
}

// SetDisabledClusters sets disabledClusters
// It has no effects if the options have been resolved.
func (o *KubernetesOptions) SetDisabledClusters(disabledClusters sets.Set[string]) {
	if o.resolved {
		logrus.WithField("disabledClusters", o.disabledClusters).Warn("SetDisabledClusters has to be called before it is resolved")
		return
	}
	o.disabledClusters = disabledClusters
}
