/*
Copyright 2023.

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

package controllers

import (
	"context"
	"fmt"
	evacuationplanner_v1alpha1 "github.com/Technion-SpotOS/EvacuationPlanner/pkg/api/v1alpha1"
	spotworkload_v1alpha1 "github.com/Technion-SpotOS/SpotWorkload/pkg/api/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const loggerName = "rearrangement-executor-controller"

var executorBusy = fmt.Errorf("received a plan while controller is already executing one")

// RearrangementExecutorReconciler reconciles an EvacuationPlan object.
type RearrangementExecutorReconciler struct {
	log    logr.Logger
	client client.Client
	scheme *runtime.Scheme

	activePlan *evacuationplanner_v1alpha1.EvacuationPlan

	workloadChan         chan workloadToJuggle
	completionSignalChan chan interface{}
}

func initRearrangementExecutorReconciler(mgr ctrl.Manager) *RearrangementExecutorReconciler {
	return &RearrangementExecutorReconciler{
		log:    ctrl.Log.WithName(loggerName),
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),

		activePlan: nil,

		workloadChan:         make(chan workloadToJuggle, 1),
		completionSignalChan: make(chan interface{}, 1),
	}
}

// executePlan executes the active plan sequentially until completion.
func (r *RearrangementExecutorReconciler) executePlan() {
	// Sequentially go over the map and migrate each workload in order. Block until completion
	// of each migration. If a workload already fulfills the scheduling, skip.
	if r.activePlan == nil {
		return
	}

	for workloadNamespacedName, instanceName := range r.activePlan.Spec.SequentialJuggleMap {
		go r.executeJuggle() // blocks until writing to channel

		r.workloadChan <- workloadToJuggle{
			namespacedName: evacuationplanner_v1alpha1.StringToNamespacedName(workloadNamespacedName),
			instanceName:   instanceName,
		}

		<-r.completionSignalChan
	}
}

func (r *RearrangementExecutorReconciler) executeJuggle() {
	// Get the workloadName resource, update tolerations to accommodate the juggling request,
	// wait until the responsible controller migrates the workloadName to the new instanceName.
	defer func() { r.completionSignalChan <- nil }()

	workloadNamespacedName := <-r.workloadChan

	spotWorkload := &spotworkload_v1alpha1.SpotWorkload{} //TODO: exponential backoff retry
	if err := r.client.Get(context.Background(), workloadNamespacedName.namespacedName, spotWorkload); err != nil {
		r.log.Error(err, "failed to get SpotWorkload resource")
		return
	}

	// The SpotWorkload controller is responsible for the migration. We only need to update the scheduling mapping,
	// and set status phase to "scheduled" for the first to pickup on the actual deploying.
	componentStatuses := map[string]spotworkload_v1alpha1.ComponentStatus{}
	for name := range spotWorkload.Spec.Components {
		componentStatuses[name] = spotworkload_v1alpha1.ComponentStatus{
			Stage:        "scheduled",
			InstanceName: workloadNamespacedName.instanceName,
		}
	}

	if err := r.client.Status().Update(context.Background(), spotWorkload); err != nil {
		r.log.Error(err, "failed to update SpotWorkload status")
		return
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RearrangementExecutorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.log.WithValues("evacuationplan", req.NamespacedName)

	arrangementPlan := &evacuationplanner_v1alpha1.EvacuationPlan{}

	// assuming one plan can be active at a time
	// TODO: support multiple plans
	if r.activePlan != nil && evacuationPlansMatch(r.activePlan, arrangementPlan) {
		return ctrl.Result{}, executorBusy
	}

	if err := r.client.Get(ctx, req.NamespacedName, arrangementPlan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// if the plan's phase is not pending, ignore it
	if arrangementPlan.Status.Phase != evacuationplanner_v1alpha1.EvacuationPlanPhasePending {
		return ctrl.Result{}, nil
	}

	r.activePlan = arrangementPlan
	go r.executePlan()

	return ctrl.Result{}, nil
}

// setupRearrangementExecutorController sets up the controller with the Manager.
func setupRearrangementExecutorController(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&evacuationplanner_v1alpha1.EvacuationPlan{}).
		Complete(initRearrangementExecutorReconciler(mgr)); err != nil {
		return fmt.Errorf("failed to add spot-instance controller to the manager: %w", err)
	}

	return nil
}
