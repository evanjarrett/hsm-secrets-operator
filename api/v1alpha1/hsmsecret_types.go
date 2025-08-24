/*
Copyright 2025.

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// HSMSecretSpec defines the desired state of HSMSecret.
type HSMSecretSpec struct {
	// SecretName is the name of the Kubernetes Secret object to create/update
	// Defaults to the HSMSecret name if not specified
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// AutoSync enables bidirectional synchronization between HSM and Kubernetes Secret
	// +kubebuilder:default=true
	// +optional
	AutoSync bool `json:"autoSync,omitempty"`

	// SecretType specifies the type of Kubernetes Secret to create
	// +kubebuilder:default="Opaque"
	// +optional
	SecretType corev1.SecretType `json:"secretType,omitempty"`

	// SyncInterval defines how often to check for HSM changes (in seconds)
	// Only applies when AutoSync is true
	// +kubebuilder:default=30
	// +optional
	SyncInterval int32 `json:"syncInterval,omitempty"`
}

// SyncStatus represents the synchronization state
type SyncStatus string

const (
	// SyncStatusInSync indicates HSM and K8s Secret are synchronized
	SyncStatusInSync SyncStatus = "InSync"
	// SyncStatusOutOfSync indicates HSM and K8s Secret differ
	SyncStatusOutOfSync SyncStatus = "OutOfSync"
	// SyncStatusError indicates an error occurred during synchronization
	SyncStatusError SyncStatus = "Error"
	// SyncStatusPending indicates synchronization is in progress
	SyncStatusPending SyncStatus = "Pending"
)

// HSMDeviceSync tracks synchronization state for a specific HSM device
type HSMDeviceSync struct {
	// DeviceName is the name of the HSM device
	DeviceName string `json:"deviceName"`

	// LastSyncTime is the timestamp of the last successful sync with this device
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Checksum is the SHA256 checksum of the data on this device
	// +optional
	Checksum string `json:"checksum,omitempty"`

	// Status indicates the sync status for this specific device
	// +optional
	Status SyncStatus `json:"status,omitempty"`

	// LastError contains the last error when syncing with this device
	// +optional
	LastError string `json:"lastError,omitempty"`

	// Online indicates if this device is currently available
	// +optional
	Online bool `json:"online,omitempty"`

	// Version is a monotonically increasing counter for conflict resolution
	// Updated each time the secret changes on this device
	// +optional
	Version int64 `json:"version,omitempty"`
}

// HSMSecretStatus defines the observed state of HSMSecret.
type HSMSecretStatus struct {
	// LastSyncTime is the timestamp of the last successful synchronization
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// HSMChecksum is the SHA256 checksum of the HSM data (deprecated - use DeviceSyncStatus)
	// +optional
	HSMChecksum string `json:"hsmChecksum,omitempty"`

	// SecretChecksum is the SHA256 checksum of the Kubernetes Secret data
	// +optional
	SecretChecksum string `json:"secretChecksum,omitempty"`

	// SyncStatus indicates the current synchronization status
	// +optional
	SyncStatus SyncStatus `json:"syncStatus,omitempty"`

	// LastError contains the last error message if SyncStatus is Error
	// +optional
	LastError string `json:"lastError,omitempty"`

	// Conditions represent the latest available observations of the HSMSecret's current state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// SecretRef references the created Kubernetes Secret
	// +optional
	SecretRef *corev1.ObjectReference `json:"secretRef,omitempty"`

	// DeviceSyncStatus tracks sync status for each HSM device in mirrored setups
	// +optional
	DeviceSyncStatus []HSMDeviceSync `json:"deviceSyncStatus,omitempty"`

	// PrimaryDevice indicates which device is currently considered the primary source of truth
	// Used for conflict resolution in multi-device scenarios
	// +optional
	PrimaryDevice string `json:"primaryDevice,omitempty"`

	// SyncConflict indicates if there are conflicting versions across devices
	// +optional
	SyncConflict bool `json:"syncConflict,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hsmsec
// +kubebuilder:printcolumn:name="Secret Name",type=string,JSONPath=`.spec.secretName`
// +kubebuilder:printcolumn:name="Sync Status",type=string,JSONPath=`.status.syncStatus`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HSMSecret is the Schema for the hsmsecrets API.
type HSMSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HSMSecretSpec   `json:"spec,omitempty"`
	Status HSMSecretStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HSMSecretList contains a list of HSMSecret.
type HSMSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HSMSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HSMSecret{}, &HSMSecretList{})
}
