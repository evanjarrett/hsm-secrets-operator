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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// USBDeviceSpec defines USB device identification criteria
type USBDeviceSpec struct {
	// VendorID is the USB vendor ID (e.g., "20a0" for Pico HSM)
	VendorID string `json:"vendorId"`

	// ProductID is the USB product ID (e.g., "4230" for Pico HSM)
	ProductID string `json:"productId"`

	// SerialNumber optionally matches a specific device serial number
	// +optional
	SerialNumber string `json:"serialNumber,omitempty"`
}

// DevicePathSpec defines device path-based identification
type DevicePathSpec struct {
	// Path is the device path pattern (e.g., "/dev/ttyUSB*", "/dev/sc-hsm*")
	Path string `json:"path"`

	// Permissions are the required permissions for device access
	// +optional
	Permissions string `json:"permissions,omitempty"`
}

// HSMDeviceType represents the type of HSM device
type HSMDeviceType string

const (
	// HSMDeviceTypePicoHSM represents a Pico HSM device
	HSMDeviceTypePicoHSM HSMDeviceType = "PicoHSM"
	// HSMDeviceTypeSmartCardHSM represents a SmartCard-HSM
	HSMDeviceTypeSmartCardHSM HSMDeviceType = "SmartCard-HSM"
	// HSMDeviceTypeGeneric represents a generic PKCS#11 device
	HSMDeviceTypeGeneric HSMDeviceType = "Generic"
)

// MirroringPolicy defines how devices should be mirrored across nodes
type MirroringPolicy string

const (
	// MirroringPolicyNone disables device mirroring
	MirroringPolicyNone MirroringPolicy = "None"
	// MirroringPolicyReadOnly enables readonly mirroring across nodes
	MirroringPolicyReadOnly MirroringPolicy = "ReadOnly"
	// MirroringPolicyActive enables active-active mirroring (future)
	MirroringPolicyActive MirroringPolicy = "Active"
)

// MirroringSpec defines device mirroring configuration
type MirroringSpec struct {
	// Policy specifies the mirroring strategy
	// +kubebuilder:default="None"
	// +optional
	Policy MirroringPolicy `json:"policy,omitempty"`

	// SyncInterval defines how often to sync device data across nodes (in seconds)
	// +kubebuilder:default=60
	// +optional
	SyncInterval int32 `json:"syncInterval,omitempty"`

	// TargetNodes specifies nodes that should have mirrored access
	// If empty, mirrors to all nodes with the device
	// +optional
	TargetNodes []string `json:"targetNodes,omitempty"`

	// PrimaryNode specifies the preferred primary node for write operations
	// +optional
	PrimaryNode string `json:"primaryNode,omitempty"`

	// AutoFailover enables automatic failover to healthy nodes
	// +kubebuilder:default=true
	// +optional
	AutoFailover bool `json:"autoFailover,omitempty"`
}

// PKCS11Config defines PKCS#11 connection configuration for an HSM device
type PKCS11Config struct {
	// LibraryPath is the path to the PKCS#11 library for this device
	// +optional
	LibraryPath string `json:"libraryPath,omitempty"`

	// SlotId is the PKCS#11 slot ID to use for this device
	// +kubebuilder:default=0
	// +optional
	SlotId int32 `json:"slotId,omitempty"`

	// PinSecret references a Kubernetes Secret containing the HSM PIN
	// +optional
	PinSecret *SecretKeySelector `json:"pinSecret,omitempty"`

	// TokenLabel optionally specifies the token label to match
	// +optional
	TokenLabel string `json:"tokenLabel,omitempty"`
}

// SecretKeySelector selects a key from a Secret
type SecretKeySelector struct {
	// Name is the name of the secret
	Name string `json:"name"`

	// Key is the key within the secret
	Key string `json:"key"`

	// Namespace is the namespace of the secret (optional, defaults to HSMDevice namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// DiscoverySpec defines how to discover HSM devices
type DiscoverySpec struct {
	// USB defines USB-based device discovery criteria
	// +optional
	USB *USBDeviceSpec `json:"usb,omitempty"`

	// DevicePath defines path-based device discovery criteria
	// +optional
	DevicePath *DevicePathSpec `json:"devicePath,omitempty"`

	// AutoDiscovery enables automatic discovery based on device type
	// +kubebuilder:default=false
	// +optional
	AutoDiscovery bool `json:"autoDiscovery,omitempty"`
}

// HSMDeviceSpec defines the desired state of HSMDevice.
type HSMDeviceSpec struct {
	// DeviceType specifies the type of HSM device
	DeviceType HSMDeviceType `json:"deviceType"`

	// Discovery defines how to discover this HSM device
	// +optional
	Discovery *DiscoverySpec `json:"discovery,omitempty"`

	// NodeSelector specifies which nodes should be scanned for this device
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// PKCS11 defines PKCS#11 connection configuration for this device
	// +optional
	PKCS11 *PKCS11Config `json:"pkcs11,omitempty"`

	// MaxDevices limits how many instances of this device can be discovered
	// +kubebuilder:default=10
	// +optional
	MaxDevices int32 `json:"maxDevices,omitempty"`

	// Mirroring configures cross-node device mirroring for high availability
	// +optional
	Mirroring *MirroringSpec `json:"mirroring,omitempty"`
}

// DeviceRole defines the role of a device in a mirrored setup
type DeviceRole string

const (
	// DeviceRolePrimary indicates the device is the primary (read-write)
	DeviceRolePrimary DeviceRole = "Primary"
	// DeviceRoleReadOnly indicates the device is a readonly mirror
	DeviceRoleReadOnly DeviceRole = "ReadOnly"
	// DeviceRoleStandby indicates the device is available for failover
	DeviceRoleStandby DeviceRole = "Standby"
)

// DiscoveredDevice represents a discovered HSM device instance
type DiscoveredDevice struct {
	// DevicePath is the system path to the discovered device
	DevicePath string `json:"devicePath"`

	// SerialNumber is the serial number of the device (if available)
	// +optional
	SerialNumber string `json:"serialNumber,omitempty"`

	// NodeName is the name of the node where the device was discovered
	NodeName string `json:"nodeName"`

	// LastSeen is the timestamp when the device was last detected
	LastSeen metav1.Time `json:"lastSeen"`

	// DeviceInfo contains additional device information
	// +optional
	DeviceInfo map[string]string `json:"deviceInfo,omitempty"`

	// Available indicates if the device is currently available for use
	Available bool `json:"available"`

	// ResourceName is the Kubernetes resource name for this device
	// +optional
	ResourceName string `json:"resourceName,omitempty"`

	// Role indicates the role of this device in a mirrored setup
	// +optional
	Role DeviceRole `json:"role,omitempty"`

	// MirroredFrom indicates the primary device this is mirrored from
	// +optional
	MirroredFrom string `json:"mirroredFrom,omitempty"`

	// LastSyncTime is when this device was last synchronized
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Health represents the health status of the device
	// +optional
	Health string `json:"health,omitempty"`
}

// MirroringStatus represents the status of device mirroring
type MirroringStatus struct {
	// Enabled indicates if mirroring is currently active
	Enabled bool `json:"enabled"`

	// PrimaryNode is the current primary node
	// +optional
	PrimaryNode string `json:"primaryNode,omitempty"`

	// MirroredNodes lists nodes with mirrored access
	// +optional
	MirroredNodes []string `json:"mirroredNodes,omitempty"`

	// LastSyncTime is when devices were last synchronized
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// FailoverCount tracks the number of failovers that have occurred
	FailoverCount int32 `json:"failoverCount"`

	// SyncErrors tracks synchronization errors
	// +optional
	SyncErrors []string `json:"syncErrors,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=hsmdev
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.deviceType`
// +kubebuilder:printcolumn:name="Discovery",type=string,JSONPath=`.spec.discovery.autoDiscovery`
// +kubebuilder:printcolumn:name="Max Devices",type=integer,JSONPath=`.spec.maxDevices`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HSMDevice is the Schema for the hsmdevices API.
type HSMDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec HSMDeviceSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// HSMDeviceList contains a list of HSMDevice.
type HSMDeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HSMDevice `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HSMDevice{}, &HSMDeviceList{})
}
