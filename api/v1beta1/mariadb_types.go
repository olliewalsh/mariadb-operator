/*
Copyright 2022.

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

package v1beta1

import (
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TLSSpec defines the TLS options
type TLSSpec struct {
	// Secret in the same namespace containing the server private key (tls.key) and public cert (tls.crt) for TLS
	SecretName string `json:"secretName,omitempty"`
	// Secret in the same namespace containing the CA cert (ca.crt) for client certificate validation
	CaSecretName string `json:"caSecretName,omitempty"`
}

// MariaDBSpec defines the desired state of MariaDB
type MariaDBSpec struct {
	// Secret containing a RootPassword
	Secret string `json:"secret,omitempty"`

	StorageClass string `json:"storageClass,omitempty"`

	StorageRequest string `json:"storageRequest,omitempty"`

	// ContainerImage - Container Image URL (will be set to environmental default if empty)
	ContainerImage string `json:"containerImage"`

	AdoptionRedirect AdoptionRedirectSpec `json:"adoptionRedirect,omitempty"`

	TLS TLSSpec `json:"tls,omitempty"`
}

// AdoptionRedirectSpec defines redirection to a different DB instance during Adoption
type AdoptionRedirectSpec struct {
	// MariaDB host to redirect to (IP or name)
	Host string `json:"host,omitempty"`
}

// MariaDBStatus defines the observed state of MariaDB
type MariaDBStatus struct {
	// db init completed
	DbInitHash string `json:"dbInitHash"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].status",description="Status"
//+kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// MariaDB is the Schema for the mariadbs API
type MariaDB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MariaDBSpec   `json:"spec,omitempty"`
	Status MariaDBStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MariaDBList contains a list of MariaDB
type MariaDBList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MariaDB `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MariaDB{}, &MariaDBList{})
}

// IsReady - returns true if service is ready to serve requests
func (instance MariaDB) IsReady() bool {
	return instance.Status.Conditions.IsTrue(condition.DeploymentReadyCondition)
}

// RbacConditionsSet - set the conditions for the rbac object
func (instance MariaDB) RbacConditionsSet(c *condition.Condition) {
	instance.Status.Conditions.Set(c)
}

// RbacNamespace - return the namespace
func (instance MariaDB) RbacNamespace() string {
	return instance.Namespace
}

// RbacResourceName - return the name to be used for rbac objects (serviceaccount, role, rolebinding)
func (instance MariaDB) RbacResourceName() string {
	return "mariadb-" + instance.Name
}
