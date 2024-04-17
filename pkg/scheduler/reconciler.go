/*
Copyright 2024 The Kubernetes Authors.

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

package scheduler

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/scheduler/strategy"
)

const ControllerName = "scheduler"

func Add(mgr controllerruntime.Manager, cfg config.Getter, numWorkers int) error {
	predicates := predicate.NewPredicateFuncs(func(object client.Object) bool {
		pj, isPJ := object.(*prowv1.ProwJob)
		return isPJ && pj.Status.State == prowv1.SchedulingState
	})

	reconciler := NewReconciler(mgr.GetClient(), cfg, strategy.Get)
	if err := controllerruntime.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&prowv1.ProwJob{}).
		WithEventFilter(predicates).
		WithOptions(controller.Options{MaxConcurrentReconciles: numWorkers}).
		Complete(reconciler); err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	return nil
}

type StrategyGetter func(cfg *config.Config) strategy.Interface

type Reconciler struct {
	pjClient    client.Client
	passthrough strategy.Interface
	log         *logrus.Entry
	cfg         config.Getter
	strategy    StrategyGetter
}

func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", request)

	pj := &prowv1.ProwJob{}
	if err := r.pjClient.Get(ctx, request.NamespacedName, pj); err != nil {
		if !kerrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("get prowjob %s: %w", request.Name, err)
		}
		return reconcile.Result{}, nil
	}

	log = log.WithField("job", pj.Spec.Job)

	var result strategy.Result
	var err error
	// So far only k8s and tekton use the cluster field in a meaninful way. Hence
	// if we're reconciling a job having a different agent (or no agent at all) applying
	// the passthrough strategy may be the safest approach.
	if pj.Spec.Agent == prowv1.KubernetesAgent || pj.Spec.Agent == prowv1.TektonAgent {
		result, err = r.strategy(r.cfg()).Schedule(ctx, pj)
	} else {
		result, err = r.passthrough.Schedule(ctx, pj)
	}

	if err != nil {
		return reconcile.Result{}, fmt.Errorf("schedule prowjob %s: %w", request.Name, err)
	}
	log.WithField("cluster", result.Cluster).Info("Cluster assigned")

	// Don't mess the cache up
	scheduled := pj.DeepCopy()
	scheduled.Spec.Cluster = result.Cluster
	scheduled.Status.State = prowv1.TriggeredState

	if err := r.pjClient.Patch(ctx, scheduled, client.MergeFrom(pj.DeepCopy())); err != nil {
		return reconcile.Result{}, fmt.Errorf("patch prowjob: %w", err)
	}

	return reconcile.Result{}, nil
}

func NewReconciler(pjClient client.Client, cfg config.Getter, strtgy StrategyGetter) *Reconciler {
	return &Reconciler{
		pjClient:    pjClient,
		passthrough: &strategy.Passthrough{},
		log:         logrus.NewEntry(logrus.StandardLogger()).WithField("controller", ControllerName),
		cfg:         cfg,
		strategy:    strtgy,
	}
}
