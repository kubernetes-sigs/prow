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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/cron"
	pkgflagutil "sigs.k8s.io/prow/pkg/flagutil"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pjutil/pprof"
)

const (
	defaultTickInterval = time.Minute
	maxRetries          = 10
)

type options struct {
	config configflagutil.ConfigOptions

	kubernetes             prowflagutil.KubernetesOptions
	instrumentationOptions prowflagutil.InstrumentationOptions
	controllerManager      prowflagutil.ControllerManagerOptions
	dryRun                 bool
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether or not to make mutating API calls to Kubernetes.")
	o.config.AddFlags(fs)
	o.kubernetes.AddFlags(fs)
	o.instrumentationOptions.AddFlags(fs)
	o.controllerManager.TimeoutListingProwJobsDefault = 60 * time.Second
	o.controllerManager.AddFlags(fs)

	fs.Parse(args)
	return o
}

func (o *options) Validate() error {
	for _, group := range []pkgflagutil.OptionGroup{&o.kubernetes, &o.config, &o.controllerManager} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	defer interrupts.WaitForGracefulShutdown()

	pprof.Instrument(o.instrumentationOptions)

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	cfg, err := o.kubernetes.InfrastructureClusterConfig(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get prowjob kubeconfig")
	}
	cluster, err := cluster.New(cfg, func(o *cluster.Options) {
		o.Cache.DefaultNamespaces = map[string]cache.Config{
			configAgent.Config().ProwJobNamespace: {},
		}
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct prowjob client")
	}
	// Trigger cache creation for ProwJobs so the following cacheSync actually does something. If we don't
	// do this here, the first List request for ProwJobs will transiently trigger cache creation and sync,
	// which doesn't allow us to fail the binary if it doesn't work.
	if _, err := cluster.GetCache().GetInformer(interrupts.Context(), &prowapi.ProwJob{}); err != nil {
		logrus.WithError(err).Fatal("Failed to get a prowjob informer")
	}
	interrupts.Run(func(ctx context.Context) {
		if err := cluster.Start(ctx); err != nil {
			logrus.WithError(err).Fatal("Controller failed to start")
		}
		logrus.Info("Controller finished gracefully.")
	})
	mgrSyncCtx, mgrSyncCtxCancel := context.WithTimeout(context.Background(), o.controllerManager.TimeoutListingProwJobs)
	defer mgrSyncCtxCancel()
	if synced := cluster.GetCache().WaitForCacheSync(mgrSyncCtx); !synced {
		logrus.Fatal("Timed out waiting for cache sync")
	}

	// start a cron
	cr := cron.New()
	cr.Start()

	metrics.ExposeMetrics("horologium", configAgent.Config().PushGateway, o.instrumentationOptions.MetricsPort)

	tickInterval := defaultTickInterval
	if configAgent.Config().Horologium.TickInterval != nil {
		tickInterval = configAgent.Config().Horologium.TickInterval.Duration
	}
	interrupts.TickLiteral(func() {
		start := time.Now()
		if err := sync(cluster.GetClient(), configAgent.Config(), cr, start); err != nil {
			logrus.WithError(err).Error("Error syncing periodic jobs.")
		}
		logrus.WithField("duration", time.Since(start)).Info("Synced periodic jobs")
	}, tickInterval)
}

type cronClient interface {
	SyncConfig(cfg *config.Config) error
	QueuedJobs() []string
}

func sync(prowJobClient ctrlruntimeclient.Client, cfg *config.Config, cr cronClient, now time.Time) error {
	jobs := &prowapi.ProwJobList{}
	if err := prowJobClient.List(context.TODO(), jobs, ctrlruntimeclient.InNamespace(cfg.ProwJobNamespace)); err != nil {
		return fmt.Errorf("error listing prow jobs: %w", err)
	}
	latestJobs := pjutil.GetLatestProwJobs(jobs.Items, prowapi.PeriodicJob)

	if err := cr.SyncConfig(cfg); err != nil {
		logrus.WithError(err).Error("Error syncing cron jobs.")
	}

	cronTriggers := sets.New[string]()
	for _, job := range cr.QueuedJobs() {
		cronTriggers.Insert(job)
	}

	var errs []error
	for _, p := range cfg.Periodics {
		j, previousFound := latestJobs[p.Name]
		logger := logrus.WithFields(logrus.Fields{
			"job":            p.Name,
			"previous-found": previousFound,
		})

		var shouldTrigger = false
		switch {
		case p.Cron == "": // no cron expression is set, we use interval to trigger
			if j.Complete() {
				intervalRef := j.Status.StartTime.Time
				intervalDuration := p.GetInterval()
				if p.MinimumInterval != "" {
					intervalRef = j.Status.CompletionTime.Time
					intervalDuration = p.GetMinimumInterval()
				}
				shouldTrigger = now.Sub(intervalRef) > intervalDuration
			}
		case cronTriggers.Has(p.Name):
			shouldTrigger = j.Complete()
		default:
			if !cronTriggers.Has(p.Name) {
				logger.WithFields(logrus.Fields{
					"previous-found": previousFound,
					"should-trigger": shouldTrigger,
					"name":           p.Name,
					"job":            p.JobBase.Name,
				}).Info("Skipping cron periodic")
			}
			continue
		}
		if !shouldTrigger {
			logger.WithFields(logrus.Fields{
				"previous-found": previousFound,
				"name":           p.Name,
				"job":            p.JobBase.Name,
			}).Debug("Trigger time has not yet been reached.")
		}

		var labels map[string]string
		if p.Labels != nil {
			labels = make(map[string]string)
			for k, v := range p.Labels {
				labels[k] = v
			}
		}

		if !previousFound || shouldTrigger || shouldTriggerFailedRun(j, p, now, logger, &labels) {
			prowJob := pjutil.NewProwJob(pjutil.PeriodicSpec(p), labels, p.Annotations,
				pjutil.RequireScheduling(cfg.Scheduler.Enabled))
			prowJob.Namespace = cfg.ProwJobNamespace
			logger.WithFields(logrus.Fields{
				"should-trigger": shouldTrigger,
				"previous-found": previousFound,
			}).WithFields(
				pjutil.ProwJobFields(&prowJob),
			).Info("Triggering new run.")
			if err := prowJobClient.Create(context.TODO(), &prowJob); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to create %d prowjobs: %v", len(errs), errs)
	}
	return nil
}

func shouldTriggerFailedRun(j v1.ProwJob, p config.Periodic, now time.Time, logger *logrus.Entry, labels *map[string]string) bool {
	if p.Retry == nil {
		return false
	}
	if !j.Complete() && !p.Retry.RunAll {
		return false
	}
	runCount := 1

	countLabel, exists := j.Labels[kube.ReRunLabel]
	if exists {
		count, err := strconv.Atoi(countLabel)
		if err != nil {
			logger.WithFields(logrus.Fields{}).WithFields(
				pjutil.ProwJobFields(&j),
			).Warn("Failed to convert label value.")
			return false
		}
		runCount = count + 1
	}

	maxForJob := p.Retry.Attempts
	if maxForJob > maxRetries {
		maxForJob = maxRetries
	}
	if runCount > maxForJob {
		return false
	}

	lastRunTime := j.Status.StartTime.Time
	if now.Sub(lastRunTime) <= p.Retry.GetInterval() {
		return false
	}

	if !p.Retry.RunAll && j.Status.State == v1.SuccessState {
		return false
	}

	if *labels == nil {
		*labels = make(map[string]string)
	}
	(*labels)[kube.ReRunLabel] = strconv.Itoa(runCount)

	logger.WithFields(logrus.Fields{
		"attempt": strconv.Itoa(runCount),
		"run_all": p.Retry.RunAll,
	}).Debug("Job marked to be retried")

	return true
}
