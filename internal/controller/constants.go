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

package controller

// Shared string constants used across the controller package. Extracted to
// avoid repeated string literals (goconst) and keep label/key naming consistent.
const (
	// Kubernetes recommended label keys.
	labelAppName      = "app.kubernetes.io/name"
	labelAppComponent = "app.kubernetes.io/component"
	labelApp          = "app"
	labelHostname     = "kubernetes.io/hostname"

	// HSM operator label keys.
	labelHSMDevice = "hsm.j5t.io/device"

	// Common label/identifier values.
	appNameHSMOperator = "hsm-secrets-operator"
	componentDiscovery = "discovery"
	componentAgent     = "agent"
	componentPool      = "pool"

	// Volume names used by the discovery DaemonSet pod spec.
	volumeNameSys             = "sys"
	volumeNameRunUdev         = "run-udev"
	volumeNameDevicePlugins   = "device-plugins"
	volumeNamePluginsRegistry = "plugins-registry"

	// volumeNameHSMDevice is the volume name for the HSM device hostPath mount.
	volumeNameHSMDevice = "hsm-device"

	// phaseError is the "Error" condition reason/phase value.
	phaseError = "Error"
)

// capabilityAll is the "drop everything" entry for container capability lists.
const capabilityAll = "ALL"
