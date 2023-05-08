/*


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
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	k8s_labels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	configmap "github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	env "github.com/openstack-k8s-operators/lib-common/modules/common/env"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	job "github.com/openstack-k8s-operators/lib-common/modules/common/job"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	secret "github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg"
	"k8s.io/client-go/kubernetes"
)

const mariaDBReconcileLabel = "mariadb-ref"

// MariaDBReconciler reconciles a MariaDB object
type MariaDBReconciler struct {
	Client  client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbs/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=endpoints,verbs=get;list;watch;create;update;delete;
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete;

// Reconcile reconcile mariadb API requests
func (r *MariaDBReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("mariadb", req.NamespacedName)

	// Fetch the MariaDB instance
	instance := &databasev1beta1.MariaDB{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	//
	// initialize status
	//
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = condition.Conditions{}
		// initialize conditions used later as Status=Unknown
		cl := condition.CreateList(
			condition.UnknownCondition(condition.ExposeServiceReadyCondition, condition.InitReason, condition.ExposeServiceReadyInitMessage),
			condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBInitializedCondition, condition.InitReason, databasev1beta1.MariaDBInitializedInitMessage),
			condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
			condition.UnknownCondition(condition.ServiceAccountReadyCondition, condition.InitReason, condition.ServiceAccountReadyInitMessage),
			condition.UnknownCondition(condition.RoleReadyCondition, condition.InitReason, condition.RoleReadyInitMessage),
			condition.UnknownCondition(condition.RoleBindingReadyCondition, condition.InitReason, condition.RoleBindingReadyInitMessage),
		)

		instance.Status.Conditions.Init(&cl)

		// Register overall status immediately to have an early feedback e.g. in the cli
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	h, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		r.Log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always patch the instance status when exiting this function so we can persist any changes.
	defer func() {
		// update the overall status condition if service is ready
		if instance.IsReady() {
			instance.Status.Conditions.MarkTrue(condition.ReadyCondition, condition.ReadyMessage)
		}

		if err := h.SetAfter(instance); err != nil {
			util.LogErrorForObject(h, err, "Set after and calc patch/diff", instance)
		}

		if changed := h.GetChanges()["status"]; changed {
			patch := client.MergeFrom(h.GetBeforeObject())

			if err := r.Client.Status().Patch(ctx, instance, patch); err != nil && !k8s_errors.IsNotFound(err) {
				util.LogErrorForObject(h, err, "Update status", instance)
			}
		}
	}()

	rbacRules := []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"security.openshift.io"},
			ResourceNames: []string{"anyuid"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"create", "get", "list", "watch", "update", "patch", "delete"},
		},
	}
	rbacResult, err := common_rbac.ReconcileRbac(ctx, h, instance, rbacRules)
	if err != nil {
		return rbacResult, err
	} else if (rbacResult != ctrl.Result{}) {
		return rbacResult, nil
	}

	// PVC
	// TODO: Add PVC condition handling?  We don't currently in other operators that have PVC concerns, though
	pvc := mariadb.Pvc(instance)
	op, err := controllerutil.CreateOrPatch(ctx, r.Client, pvc, func() error {

		pvc.Spec.Resources.Requests = corev1.ResourceList{
			corev1.ResourceStorage: resource.MustParse(instance.Spec.StorageRequest),
		}

		pvc.Spec.StorageClassName = &instance.Spec.StorageClass
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}

		err := controllerutil.SetOwnerReference(instance, pvc, r.Client.Scheme())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("%s %s database PVC %s - operation: %s", instance.Kind, instance.Name, pvc.Name, string(op)))
		return ctrl.Result{RequeueAfter: time.Duration(5) * time.Second}, err
	}

	// Service
	service := mariadb.Service(instance)
	op, err = controllerutil.CreateOrPatch(ctx, r.Client, service, func() error {
		err := controllerutil.SetControllerReference(instance, service, r.Scheme)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ExposeServiceReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		util.LogForObject(
			h,
			fmt.Sprintf("Service %s successfully reconciled - operation: %s", service.Name, string(op)),
			instance,
		)
	}

	// Endpoints
	endpoints := mariadb.Endpoints(instance)
	if endpoints != nil {
		op, err = controllerutil.CreateOrPatch(ctx, r.Client, endpoints, func() error {
			err := controllerutil.SetControllerReference(instance, endpoints, r.Scheme)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.ExposeServiceReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.ExposeServiceReadyErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}
		if op != controllerutil.OperationResultNone {
			util.LogForObject(
				h,
				fmt.Sprintf("Endpoints %s successfully reconciled - operation: %s", endpoints.Name, string(op)),
				instance,
			)
		}
	}

	instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)

	// Generate the config maps for the various services
	configMapVars := make(map[string]env.Setter)
	err = r.generateServiceConfigMaps(ctx, h, instance, &configMapVars)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error calculating configmap hash: %w", err)
	}

	//
	// check for TLS secrets
	//
	if instance.Spec.TLS.SecretName != "" {
		tlsSecret, tlsHash, err := secret.GetSecret(ctx, h, instance.Spec.TLS.SecretName, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				return ctrl.Result{RequeueAfter: time.Second * 10}, fmt.Errorf("TLS secret %s not found", instance.Spec.TLS.SecretName)
			}
			return ctrl.Result{}, err
		}

		if value, ok := tlsSecret.Labels[mariaDBReconcileLabel]; !ok || value != instance.Name {
			tlsSecret.GetObjectMeta().SetLabels(
				k8s_labels.Merge(
					tlsSecret.GetObjectMeta().GetLabels(),
					map[string]string{
						mariaDBReconcileLabel: instance.Name,
					},
				),
			)
			err = r.Client.Update(ctx, tlsSecret)
			if err != nil {
				if k8s_errors.IsConflict(err) || k8s_errors.IsNotFound(err) {
					return ctrl.Result{Requeue: true}, err
				}
				return ctrl.Result{}, err
			}
		}

		configMapVars[tlsSecret.Name] = env.SetValue(tlsHash)
	}

	if instance.Spec.TLS.CaSecretName != "" {
		caSecret, caHash, err := secret.GetSecret(ctx, h, instance.Spec.TLS.CaSecretName, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				return ctrl.Result{RequeueAfter: time.Second * 10}, fmt.Errorf("TLS CA secret %s not found", instance.Spec.TLS.CaSecretName)
			}
			return ctrl.Result{}, err
		}

		if value, ok := caSecret.Labels[mariaDBReconcileLabel]; !ok || value != instance.Name {
			caSecret.GetObjectMeta().SetLabels(
				k8s_labels.Merge(
					caSecret.GetObjectMeta().GetLabels(),
					map[string]string{
						mariaDBReconcileLabel: instance.Name,
					},
				),
			)
			err = r.Client.Update(ctx, caSecret)
			if err != nil {
				if k8s_errors.IsConflict(err) || k8s_errors.IsNotFound(err) {
					return ctrl.Result{Requeue: true}, err
				}
				return ctrl.Result{}, err
			}
		}

		configMapVars[caSecret.Name] = env.SetValue(caHash)
	}

	mergedMapVars := env.MergeEnvs([]corev1.EnvVar{}, configMapVars)

	configHash := ""
	for _, hashEnv := range mergedMapVars {
		configHash = configHash + hashEnv.Value
	}

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	// Define a new Job object
	jobDef := mariadb.DbInitJob(instance)

	job := job.NewJob(
		jobDef,
		"dbinit",
		false,
		time.Duration(5)*time.Second,
		instance.Status.DbInitHash,
	)

	ctrlResult, err := job.DoJob(
		ctx,
		h,
	)
	if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBInitializedCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			databasev1beta1.MariaDBInitializedRunningMessage))
		return ctrlResult, nil
	}
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBInitializedCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			databasev1beta1.MariaDBInitializedErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if job.HasChanged() {
		instance.Status.DbInitHash = job.GetHash()
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.DbInitHash))
	}

	instance.Status.Conditions.MarkTrue(databasev1beta1.MariaDBInitializedCondition, databasev1beta1.MariaDBInitializedReadyMessage)

	// Pod
	pod := mariadb.Pod(instance, configHash)

	op, err = controllerutil.CreateOrPatch(ctx, r.Client, pod, func() error {
		pod.Spec.Containers[0].Image = instance.Spec.ContainerImage
		pod.Spec.Containers[0].Env = []corev1.EnvVar{
			{
				Name:  "KOLLA_CONFIG_STRATEGY",
				Value: "COPY_ALWAYS",
			},
			{
				Name:  "CONFIG_HASH",
				Value: configHash,
			},
		}
		err := controllerutil.SetControllerReference(instance, pod, r.Scheme)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DeploymentReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	if op != controllerutil.OperationResultNone {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DeploymentReadyRunningMessage))

		util.LogForObject(
			h,
			fmt.Sprintf("Pod %s successfully reconciled - operation: %s", pod.Name, string(op)),
			instance,
		)
	}

	if pod.Status.Phase == corev1.PodRunning {
		instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)
	}

	return ctrl.Result{}, nil
}

func (r *MariaDBReconciler) generateServiceConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *databasev1beta1.MariaDB,
	envVars *map[string]env.Setter,
) error {
	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(mariadb.ServiceName), map[string]string{})
	templateParameters := make(map[string]interface{})
	// TODO: We probably need to make this configurable.
	templateParameters["DbMaxTimeout"] = 60

	// ConfigMaps for mariadb
	cms := []util.Template{
		// ScriptsConfigMap
		{
			Name:               "mariadb-" + instance.Name,
			Namespace:          instance.Namespace,
			Type:               util.TemplateTypeScripts,
			InstanceType:       instance.Kind,
			AdditionalTemplate: map[string]string{},
			ConfigOptions:      templateParameters,
			Labels:             cmLabels,
		},
	}

	err := configmap.EnsureConfigMaps(ctx, h, instance, cms, envVars)

	if err != nil {
		// FIXME error conditions here
		return err
	}

	return nil
}

// SetupWithManager -
func (r *MariaDBReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1beta1.MariaDB{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Pod{}).
		Watches(&source.Kind{Type: &corev1.Secret{}}, handler.EnqueueRequestsFromMapFunc(
			func(o client.Object) []reconcile.Request {
				labels := o.GetLabels()

				reconcileCR, hasLabel := labels[mariaDBReconcileLabel]
				if !hasLabel {
					return []reconcile.Request{}
				}

				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      reconcileCR,
						Namespace: o.GetNamespace(),
					}},
				}
			},
		)).
		Complete(r)
}
