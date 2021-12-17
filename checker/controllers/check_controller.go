/*
Copyright 2021.

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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	checkerv1 "github.com/YutaroHayakawa/learning-kubebuilder/api/v1"

	"github.com/YutaroHayakawa/learning-kubebuilder/checker"
)

// CheckReconciler reconciles a Check object
type CheckReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	sourceCh chan event.GenericEvent
}

func (r *CheckReconciler) createOrUpdateCheck(id string, v1check *checkerv1.Check) (*checker.Check, error) {
	return checker.GlobalChecker.CreateOrUpdateCheck(
		id,
		v1check.GetName(),
		v1check.GetNamespace(),
		&checker.Check{
			Url:      v1check.Spec.Url,
			Interval: time.Millisecond * time.Duration(v1check.Spec.IntervalMilliseconds),
		},
	)
}

//+kubebuilder:rbac:groups=checker.checker.io,resources=checks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=checker.checker.io,resources=checks/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=checker.checker.io,resources=checks/finalizers,verbs=update
func (r *CheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var v1check checkerv1.Check
	if err := r.Get(ctx, req.NamespacedName, &v1check); err != nil {
		logger.Error(err, "unable to fetch Check", "name", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	check, err := r.createOrUpdateCheck(v1check.Status.Id, &v1check)
	if err != nil {
		r.Recorder.Event(&v1check, corev1.EventTypeWarning, "RegisterCheckFailed", err.Error())
		return ctrl.Result{}, err
	}

	v1check.Status.Id = check.Id()
	v1check.Status.Reason = check.Reason()

	err = r.Status().Update(ctx, &v1check)
	if err != nil {
		logger.Error(err, "failed to update status", "name", req.NamespacedName)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

var updateLog = ctrl.Log.WithName("update")

type WrappedCheckerEvent struct {
	ctrl.ObjectMeta
	runtime.Unknown
}

func (r *CheckReconciler) OnUpdate(ev checker.CheckerEvent) {
	updateLog.Info("checker state changed",
		"id", ev.Id,
		"k8sName", ev.K8sName,
		"k8sNamespace", ev.K8sNamespace,
	)

	wce := WrappedCheckerEvent{}
	wce.SetName(ev.K8sName)
	wce.SetNamespace(ev.K8sNamespace)

	r.sourceCh <- event.GenericEvent{
		Object: &wce,
	}
}

func genericEventHandler(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	updateLog.Info("event handler invoked",
		"name", evt.Object.GetName(),
		"namespace", evt.Object.GetNamespace(),
	)

	req := ctrl.Request{}
	req.Name = evt.Object.GetName()
	req.Namespace = evt.Object.GetNamespace()

	q.AddRateLimited(req)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.sourceCh = make(chan event.GenericEvent)

	source := &source.Channel{
		Source: r.sourceCh,
	}

	eventHandler := handler.Funcs{
		GenericFunc: genericEventHandler,
	}

	if err := checker.GlobalChecker.Subscribe(r); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&checkerv1.Check{}).
		Watches(source, eventHandler).
		Complete(r)
}
