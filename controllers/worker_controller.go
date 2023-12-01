/*
Copyright 2023 KubeAGI.

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
	"reflect"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	arcadiav1alpha1 "github.com/kubeagi/arcadia/api/base/v1alpha1"
	"github.com/kubeagi/arcadia/pkg/config"
	arcadiaworker "github.com/kubeagi/arcadia/pkg/worker"
)

// WorkerReconciler reconciles a Worker object
type WorkerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=arcadia.kubeagi.k8s.com.cn,resources=workers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=arcadia.kubeagi.k8s.com.cn,resources=workers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=arcadia.kubeagi.k8s.com.cn,resources=workers/finalizers,verbs=update
//+kubebuilder:rbac:groups=arcadia.kubeagi.k8s.com.cn,resources=datasources,verbs=get;list;watch
//+kubebuilder:rbac:groups=arcadia.kubeagi.k8s.com.cn,resources=datasources/status,verbs=get
//+kubebuilder:rbac:groups=arcadia.kubeagi.k8s.com.cn,resources=embedders;llms,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=arcadia.kubeagi.k8s.com.cn,resources=embedders/status;llms/status,verbs=get;update;patch

//+kubebuilder:rbac:groups="",resources=configmaps;secrets,verbs=get;list
//+kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=deployments/status,verbs=get;watch
//+kubebuilder:rbac:groups="",resources=services;pods;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status;services/status;persistentvolumeclaims/status,verbs=get;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Worker object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *WorkerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(5).Info("Start Worker Reconcile")
	worker := &arcadiav1alpha1.Worker{}
	if err := r.Get(ctx, req.NamespacedName, worker); err != nil {
		// There's no need to requeue if the resource no longer exists.
		// Otherwise, we'll be requeued implicitly because we return an error.
		log.V(1).Info("Failed to get Worker")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.V(5).Info("Get Worker instance")

	// Add a finalizer.Then, we can define some operations which should
	// occur before the Worker to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if newAdded := controllerutil.AddFinalizer(worker, arcadiav1alpha1.Finalizer); newAdded {
		log.Info("Try to add Finalizer for Worker")
		if err = r.Update(ctx, worker); err != nil {
			log.Error(err, "Failed to update Worker to add finalizer, will try again later")
			return ctrl.Result{Requeue: true}, err
		}
		log.Info("Adding Finalizer for Worker done")
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if the Worker instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	if worker.GetDeletionTimestamp() != nil && controllerutil.ContainsFinalizer(worker, arcadiav1alpha1.Finalizer) {
		log.Info("Performing Finalizer Operations for Worker before delete CR")
		// TODO perform the finalizer operations here, for example: remove vectorstore data?
		log.Info("Removing Finalizer for Worker after successfully performing the operations")
		controllerutil.RemoveFinalizer(worker, arcadiav1alpha1.Finalizer)
		if err = r.Update(ctx, worker); err != nil {
			log.Error(err, "Failed to remove finalizer for Worker")
			return ctrl.Result{}, err
		}
		log.Info("Remove Worker done")
		return ctrl.Result{}, nil
	}

	// initialize labels
	requeue, err := r.Initialize(ctx, log, worker)
	if err != nil {
		log.Error(err, "unable to update labels after generation update")
		return ctrl.Result{Requeue: true}, err
	}
	if requeue {
		return ctrl.Result{Requeue: true}, nil
	}

	// core reconcile logic
	if err := r.reconcile(ctx, log, worker); err != nil {
		r.setCondition(worker, worker.ErrorCondition(err.Error()))
	}

	if updateStatusErr := r.patchStatus(ctx, worker); updateStatusErr != nil {
		log.Error(updateStatusErr, "unable to update status after generation update")
		return ctrl.Result{Requeue: true}, updateStatusErr
	}

	return ctrl.Result{}, nil
}

func (r *WorkerReconciler) Initialize(ctx context.Context, logger logr.Logger, instance *arcadiav1alpha1.Worker) (bool, error) {
	instanceDeepCopy := instance.DeepCopy()

	var update bool

	// Initialize Labels
	if instanceDeepCopy.Labels == nil {
		instanceDeepCopy.Labels = make(map[string]string)
	}

	// For worker type
	currentType := string(instanceDeepCopy.Type())
	if v := instanceDeepCopy.Labels[arcadiav1alpha1.LabelWorkerType]; v != currentType {
		instanceDeepCopy.Labels[arcadiav1alpha1.LabelWorkerType] = currentType
		update = true
	}

	if update {
		return true, r.Client.Update(ctx, instanceDeepCopy)
	}

	return false, nil
}

func (r *WorkerReconciler) reconcile(ctx context.Context, logger logr.Logger, worker *arcadiav1alpha1.Worker) error {
	// reconcile worker instance
	system, err := config.GetSystemDatasource(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get system datasource with %w", err)
	}
	w, err := arcadiaworker.NewPodWorker(ctx, r.Client, r.Scheme, worker, system)
	if err != nil {
		return fmt.Errorf("failed to new a pod worker with %w", err)
	}

	if err := w.BeforeStart(ctx); err != nil {
		return fmt.Errorf("failed to do BeforeStart: %w", err)
	}

	if err := w.Start(ctx); err != nil {
		return fmt.Errorf("failed to do Start: %w", err)
	}

	status, err := w.State(ctx)
	if err != nil {
		return fmt.Errorf("failed to do State: %w", err)
	}

	// check & patch state
	podStatus := status.(*corev1.PodStatus)
	switch podStatus.Phase {
	case corev1.PodRunning, corev1.PodSucceeded:
		r.setCondition(worker, worker.ReadyCondition())
	case corev1.PodPending, corev1.PodUnknown:
		r.setCondition(worker, worker.PendingCondition())
	case corev1.PodFailed:
		r.setCondition(worker, worker.ErrorCondition("Pod failed"))
	}

	worker.Status.PodStatus = *podStatus

	// further reconcile when worker is ready
	if worker.Status.IsReady() {
		if err := r.reconcileWhenWorkerReady(ctx, logger, worker, w.Model()); err != nil {
			return fmt.Errorf("failed to reconcileWhenWorkerReady: %w", err)
		}
	}

	return nil
}

func (r *WorkerReconciler) reconcileWhenWorkerReady(ctx context.Context, logger logr.Logger, worker *arcadiav1alpha1.Worker, model *arcadiav1alpha1.Model) error {
	// reconcile worker's Embedder when its model is a embedding model
	if model.IsEmbeddingModel() {
		embedder := &arcadiav1alpha1.Embedder{}
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: worker.Namespace, Name: worker.Name + "-worker"}, embedder)
		switch arcadiaworker.ActionOnError(err) {
		case arcadiaworker.Create:
			// Create when not found
			embedder = worker.BuildEmbedder()
			if err = controllerutil.SetControllerReference(worker, embedder, r.Scheme); err != nil {
				return err
			}
			if err = r.Client.Create(ctx, embedder); err != nil {
				// Ignore error when already exists
				if !k8serrors.IsAlreadyExists(err) {
					return err
				}
			}
		case arcadiaworker.Update:
			// Skip update when found
		case arcadiaworker.Panic:
			return err
		}
	}

	// reconcile worker's LLM when its model is a LLM model
	if model.IsLLMModel() {
		llm := &arcadiav1alpha1.LLM{}
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: worker.Namespace, Name: worker.Name + "-worker"}, llm)
		switch arcadiaworker.ActionOnError(err) {
		case arcadiaworker.Create:
			// Create when not found
			llm = worker.BuildLLM()
			if err = controllerutil.SetControllerReference(worker, llm, r.Scheme); err != nil {
				return err
			}
			if err = r.Client.Create(ctx, llm); err != nil {
				// Ignore error when already exists
				if !k8serrors.IsAlreadyExists(err) {
					return err
				}
			}
		case arcadiaworker.Update:
			// Skip update when found
		case arcadiaworker.Panic:
			return err
		}
	}

	return nil
}

func (r *WorkerReconciler) setCondition(worker *arcadiav1alpha1.Worker, condition ...arcadiav1alpha1.Condition) *arcadiav1alpha1.Worker {
	worker.Status.SetConditions(condition...)
	return worker
}

func (r *WorkerReconciler) patchStatus(ctx context.Context, worker *arcadiav1alpha1.Worker) error {
	latest := &arcadiav1alpha1.Worker{}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(worker), latest); err != nil {
		return err
	}
	patch := client.MergeFrom(latest.DeepCopy())
	latest.Status = worker.Status
	return r.Client.Status().Patch(ctx, latest, patch, client.FieldOwner("worker-controller"))
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&arcadiav1alpha1.Worker{}, builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(ue event.UpdateEvent) bool {
				oldWorker := ue.ObjectOld.(*arcadiav1alpha1.Worker)
				newWorker := ue.ObjectNew.(*arcadiav1alpha1.Worker)
				return !reflect.DeepEqual(oldWorker.Spec, newWorker.Spec) || newWorker.DeletionTimestamp != nil
			},
		})).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &arcadiav1alpha1.Worker{},
		}).
		Complete(r)
}
