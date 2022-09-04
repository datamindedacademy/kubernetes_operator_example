/*
Copyright 2022 Niels Claeys.

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
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	datamindedbev1 "dataminded.be/presentation/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PresentationReconciler reconciles a Presentation object
type PresentationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=dataminded.be,resources=presentations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=dataminded.be,resources=presentations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=dataminded.be,resources=presentations/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Presentation object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *PresentationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("presentation", req.NamespacedName)

	// Fetch the Presentation instance
	instance := &datamindedbev1.Presentation{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	configMapChanged, err := r.ensureLatestConfigMap(instance)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.ensureLatestPod(instance, configMapChanged)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *PresentationReconciler) ensureLatestConfigMap(instance *datamindedbev1.Presentation) (bool, error) {
	configMap := newConfigMap(instance)

	// Set Presentation instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, configMap, r.Scheme); err != nil {
		return false, err
	}

	// Check if this ConfigMap already exists
	foundMap := &v1.ConfigMap{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, foundMap)
	if err != nil && errors.IsNotFound(err) {
		err = r.Client.Create(context.TODO(), configMap)
		if err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}

	if foundMap.Data["slides.md"] != configMap.Data["slides.md"] {
		err = r.Client.Update(context.TODO(), configMap)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (r *PresentationReconciler) ensureLatestPod(instance *datamindedbev1.Presentation, configMapChanged bool) error {
	// Define a new Pod object
	pod := newPodForCR(instance)

	// Set Presentation instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, pod, r.Scheme); err != nil {
		return err
	}
	// Check if this Pod already exists
	found := &v1.Pod{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		err = r.Client.Create(context.TODO(), pod)
		if err != nil {
			return err
		}

		// Pod created successfully - don't requeue
		return nil
	} else if err != nil {

		return err
	}

	if configMapChanged {
		err = r.Client.Delete(context.TODO(), found)
		if err != nil {
			return err
		}
		err = r.Client.Create(context.TODO(), pod)
		if err != nil {
			return err
		}
	}
	return nil
}

func newConfigMap(cr *datamindedbev1.Presentation) *v1.ConfigMap {
	labels := map[string]string{
		"app": cr.Name,
	}
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-config",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			"slides.md": cr.Spec.Markdown,
		},
	}
}

// newPodForCR returns a busybox pod with the same name/namespace as the cr
func newPodForCR(cr *datamindedbev1.Presentation) *v1.Pod {
	labels := map[string]string{
		"app": cr.Name,
	}
	volumeName := cr.Name + "-config"
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pod",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "slides",
					Image: "nilli9990/dataminded-presentation:1.0",
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      volumeName,
							MountPath: "/config",
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: volumeName,
					VolumeSource: v1.VolumeSource{
						ConfigMap: &v1.ConfigMapVolumeSource{
							LocalObjectReference: v1.LocalObjectReference{
								Name: cr.Name + "-config",
							},
						},
					},
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PresentationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&datamindedbev1.Presentation{}).
		Complete(r)
}
