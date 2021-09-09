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
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"context"

	execv1 "github.com/pkoska-corona/exec-operator/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// ExecutorReconciler reconciles a Executor object
type ExecutorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=exec.example.com,resources=executors,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=exec.example.com,resources=executors/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=exec.example.com,resources=executors/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Executor object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *ExecutorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the Executor instance
	Executor := &execv1.Executor{}
	err := r.Get(ctx, req.NamespacedName, Executor)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("Executor resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Executor")
		return ctrl.Result{}, err
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: Executor.Name, Namespace: Executor.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentForExecutor(Executor)
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	size := Executor.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		err = r.Update(ctx, found)
		if err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}
		// Ask to requeue after 1 minute in order to give enough time for the
		// pods be created on the cluster side and the operand be able
		// to do the next update step accurately.
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Update the Executor status with the pod names
	// List the pods for this Executor's deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(Executor.Namespace),
		client.MatchingLabels(labelsForExecutor(Executor.Name)),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		log.Error(err, "Failed to list pods", "Executor.Namespace", Executor.Namespace, "Executor.Name", Executor.Name)
		return ctrl.Result{}, err
	}
	podNames := getPodNames(podList.Items)

	// Update status.Nodes if needed
	if !reflect.DeepEqual(podNames, Executor.Status.Nodes) {
		Executor.Status.Nodes = podNames
		err := r.Status().Update(ctx, Executor)
		if err != nil {
			log.Error(err, "Failed to update Executor status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// deploymentForExecutor returns a Executor Deployment object
func (r *ExecutorReconciler) deploymentForExecutor(m *execv1.Executor) *appsv1.Deployment {
	ls := labelsForExecutor(m.Name)
	replicas := m.Spec.Size

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:   "Executor:1.4.36-alpine",
						Name:    "Executor",
						Command: []string{"Executor", "-m=64", "-o", "modern", "-v"},
						Ports: []corev1.ContainerPort{{
							ContainerPort: 11211,
							Name:          "Executor",
						}},
					}},
				},
			},
		},
	}
	// Set Executor instance as the owner and controller
	ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

// labelsForExecutor returns the labels for selecting the resources
// belonging to the given Executor CR name.
func labelsForExecutor(name string) map[string]string {
	return map[string]string{"app": "Executor", "Executor_cr": name}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

func runCMDinInstances() {}

// SetupWithManager sets up the controller with the Manager.
func (r *ExecutorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&execv1.Executor{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}
