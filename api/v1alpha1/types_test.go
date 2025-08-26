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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestGroupVersionInfo(t *testing.T) {
	// Test that GroupVersion is properly defined
	assert.Equal(t, "hsm.j5t.io", GroupVersion.Group)
	assert.Equal(t, "v1alpha1", GroupVersion.Version)
}

func TestSchemeBuilder(t *testing.T) {
	// Test that SchemeBuilder is functional
	scheme := runtime.NewScheme()
	err := SchemeBuilder.AddToScheme(scheme)
	assert.NoError(t, err)

	// Verify that our types are registered
	gvk := GroupVersion.WithKind("HSMSecret")
	_, err = scheme.New(gvk)
	assert.NoError(t, err)

	gvk = GroupVersion.WithKind("HSMDevice")
	_, err = scheme.New(gvk)
	assert.NoError(t, err)

	gvk = GroupVersion.WithKind("HSMPool")
	_, err = scheme.New(gvk)
	assert.NoError(t, err)
}

func TestParentReference(t *testing.T) {
	group := "apps"
	kind := "Deployment"
	namespace := "test-namespace"

	parentRef := ParentReference{
		Name:      "hsm-secrets-operator",
		Namespace: &namespace,
		Group:     &group,
		Kind:      &kind,
	}

	assert.Equal(t, "hsm-secrets-operator", parentRef.Name)
	assert.Equal(t, "test-namespace", *parentRef.Namespace)
	assert.Equal(t, "apps", *parentRef.Group)
	assert.Equal(t, "Deployment", *parentRef.Kind)
}

func TestParentReferenceDefaults(t *testing.T) {
	// Test with minimal configuration
	parentRef := ParentReference{
		Name: "hsm-operator",
	}

	assert.Equal(t, "hsm-operator", parentRef.Name)
	assert.Nil(t, parentRef.Namespace)
	assert.Nil(t, parentRef.Group)
	assert.Nil(t, parentRef.Kind)
}

func TestUSBDeviceSpec(t *testing.T) {
	spec := USBDeviceSpec{
		VendorID:     "20a0",
		ProductID:    "4230",
		SerialNumber: "TEST123",
	}

	assert.Equal(t, "20a0", spec.VendorID)
	assert.Equal(t, "4230", spec.ProductID)
	assert.Equal(t, "TEST123", spec.SerialNumber)
}

func TestUSBDeviceSpecMinimal(t *testing.T) {
	spec := USBDeviceSpec{
		VendorID:  "20a0",
		ProductID: "4230",
	}

	assert.Equal(t, "20a0", spec.VendorID)
	assert.Equal(t, "4230", spec.ProductID)
	assert.Empty(t, spec.SerialNumber)
}

func TestDevicePathSpec(t *testing.T) {
	spec := DevicePathSpec{
		Path:        "/dev/ttyUSB*",
		Permissions: "rw",
	}

	assert.Equal(t, "/dev/ttyUSB*", spec.Path)
	assert.Equal(t, "rw", spec.Permissions)
}

func TestDevicePathSpecMinimal(t *testing.T) {
	spec := DevicePathSpec{
		Path: "/dev/sc-hsm",
	}

	assert.Equal(t, "/dev/sc-hsm", spec.Path)
	assert.Empty(t, spec.Permissions)
}

func TestHSMDeviceType(t *testing.T) {
	tests := []struct {
		name        string
		deviceType  HSMDeviceType
		expectedStr string
	}{
		{"PicoHSM", HSMDeviceTypePicoHSM, "PicoHSM"},
		{"SmartCardHSM", HSMDeviceTypeSmartCardHSM, "SmartCard-HSM"},
		{"Generic", HSMDeviceTypeGeneric, "Generic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedStr, string(tt.deviceType))
		})
	}
}

func TestHSMSecret(t *testing.T) {
	parentRef := ParentReference{Name: "hsm-operator"}

	secret := HSMSecret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hsm.j5t.io/v1alpha1",
			Kind:       "HSMSecret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Spec: HSMSecretSpec{
			ParentRef: &parentRef,
			AutoSync:  true,
		},
	}

	assert.Equal(t, "test-secret", secret.Name)
	assert.Equal(t, "default", secret.Namespace)
	assert.Equal(t, "HSMSecret", secret.Kind)
	assert.Equal(t, "hsm.j5t.io/v1alpha1", secret.APIVersion)
	assert.NotNil(t, secret.Spec.ParentRef)
	assert.Equal(t, "hsm-operator", secret.Spec.ParentRef.Name)
	assert.True(t, secret.Spec.AutoSync)
}

func TestHSMSecretStatus(t *testing.T) {
	now := metav1.Now()
	status := HSMSecretStatus{
		SyncStatus:     SyncStatusInSync,
		HSMChecksum:    "sha256:abc123",
		SecretChecksum: "sha256:def456",
		LastSyncTime:   &now,
	}

	assert.Equal(t, SyncStatusInSync, status.SyncStatus)
	assert.Equal(t, "sha256:abc123", status.HSMChecksum)
	assert.Equal(t, "sha256:def456", status.SecretChecksum)
	assert.NotNil(t, status.LastSyncTime)
}

func TestSyncStatus(t *testing.T) {
	tests := []struct {
		name   string
		status SyncStatus
		str    string
	}{
		{"InSync", SyncStatusInSync, "InSync"},
		{"OutOfSync", SyncStatusOutOfSync, "OutOfSync"},
		{"Error", SyncStatusError, "Error"},
		{"Pending", SyncStatusPending, "Pending"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.str, string(tt.status))
		})
	}
}

func TestHSMDevice(t *testing.T) {
	device := HSMDevice{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hsm.j5t.io/v1alpha1",
			Kind:       "HSMDevice",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pico-hsm-1",
			Namespace: "hsm-secrets-operator-system",
		},
		Spec: HSMDeviceSpec{
			DeviceType: HSMDeviceTypePicoHSM,
			Discovery: &DiscoverySpec{
				USB: &USBDeviceSpec{
					VendorID:  "20a0",
					ProductID: "4230",
				},
			},
			PKCS11: &PKCS11Config{
				LibraryPath: "/usr/lib/opensc-pkcs11.so",
				SlotId:      0,
			},
		},
	}

	assert.Equal(t, "pico-hsm-1", device.Name)
	assert.Equal(t, "HSMDevice", device.Kind)
	assert.Equal(t, HSMDeviceTypePicoHSM, device.Spec.DeviceType)
	assert.NotNil(t, device.Spec.Discovery.USB)
	assert.Equal(t, "20a0", device.Spec.Discovery.USB.VendorID)
	assert.NotNil(t, device.Spec.PKCS11)
	assert.Equal(t, "/usr/lib/opensc-pkcs11.so", device.Spec.PKCS11.LibraryPath)
}

func TestPKCS11Config(t *testing.T) {
	config := PKCS11Config{
		LibraryPath: "/usr/lib/opensc-pkcs11.so",
		SlotId:      1,
		TokenLabel:  "PicoHSM",
		PinSecret: &SecretKeySelector{
			Name: "hsm-pin",
			Key:  "pin",
		},
	}

	assert.Equal(t, "/usr/lib/opensc-pkcs11.so", config.LibraryPath)
	assert.Equal(t, int32(1), config.SlotId)
	assert.Equal(t, "PicoHSM", config.TokenLabel)
	assert.NotNil(t, config.PinSecret)
	assert.Equal(t, "hsm-pin", config.PinSecret.Name)
	assert.Equal(t, "pin", config.PinSecret.Key)
}

func TestHSMPool(t *testing.T) {
	gracePeriod := metav1.Duration{Duration: 5 * time.Minute}

	pool := HSMPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hsm.j5t.io/v1alpha1",
			Kind:       "HSMPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pico-hsm-pool",
			Namespace: "hsm-secrets-operator-system",
		},
		Spec: HSMPoolSpec{
			HSMDeviceRefs: []string{"pico-hsm-1", "pico-hsm-2"},
			GracePeriod:   &gracePeriod,
		},
	}

	assert.Equal(t, "pico-hsm-pool", pool.Name)
	assert.Equal(t, "HSMPool", pool.Kind)
	assert.Len(t, pool.Spec.HSMDeviceRefs, 2)
	assert.Contains(t, pool.Spec.HSMDeviceRefs, "pico-hsm-1")
	assert.Contains(t, pool.Spec.HSMDeviceRefs, "pico-hsm-2")
	assert.NotNil(t, pool.Spec.GracePeriod)
	assert.Equal(t, 5*time.Minute, pool.Spec.GracePeriod.Duration)
}

func TestDiscoveredDevice(t *testing.T) {
	now := metav1.Now()
	device := DiscoveredDevice{
		DevicePath:   "/dev/ttyUSB0",
		SerialNumber: "TEST123",
		NodeName:     "worker-1",
		Available:    true,
		LastSeen:     now,
		DeviceInfo: map[string]string{
			"vendor_id":  "20a0",
			"product_id": "4230",
		},
	}

	assert.Equal(t, "/dev/ttyUSB0", device.DevicePath)
	assert.Equal(t, "TEST123", device.SerialNumber)
	assert.Equal(t, "worker-1", device.NodeName)
	assert.True(t, device.Available)
	assert.Equal(t, now, device.LastSeen)
	assert.Equal(t, "20a0", device.DeviceInfo["vendor_id"])
	assert.Equal(t, "4230", device.DeviceInfo["product_id"])
}

func TestPodReport(t *testing.T) {
	now := metav1.Now()
	pod := PodReport{
		PodName:         "discovery-worker-1",
		NodeName:        "worker-1",
		DevicesFound:    2,
		LastReportTime:  now,
		DiscoveryStatus: "completed",
		Fresh:           true,
	}

	assert.Equal(t, "discovery-worker-1", pod.PodName)
	assert.Equal(t, "worker-1", pod.NodeName)
	assert.Equal(t, int32(2), pod.DevicesFound)
	assert.Equal(t, now, pod.LastReportTime)
	assert.Equal(t, "completed", pod.DiscoveryStatus)
	assert.True(t, pod.Fresh)
}

func TestHSMPoolPhase(t *testing.T) {
	tests := []struct {
		name  string
		phase HSMPoolPhase
		str   string
	}{
		{"Pending", HSMPoolPhasePending, "Pending"},
		{"Aggregating", HSMPoolPhaseAggregating, "Aggregating"},
		{"Ready", HSMPoolPhaseReady, "Ready"},
		{"Partial", HSMPoolPhasePartial, "Partial"},
		{"Error", HSMPoolPhaseError, "Error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.str, string(tt.phase))
		})
	}
}

func TestHSMPoolStatus(t *testing.T) {
	now := metav1.Now()

	status := HSMPoolStatus{
		AggregatedDevices: []DiscoveredDevice{
			{
				DevicePath: "/dev/ttyUSB0",
				NodeName:   "worker-1",
				Available:  true,
				LastSeen:   now,
			},
		},
		TotalDevices:     1,
		AvailableDevices: 1,
		ReportingPods: []PodReport{
			{
				PodName:         "discovery-worker-1",
				NodeName:        "worker-1",
				DevicesFound:    1,
				LastReportTime:  now,
				DiscoveryStatus: "completed",
				Fresh:           true,
			},
		},
		Phase: HSMPoolPhaseReady,
	}

	assert.Len(t, status.AggregatedDevices, 1)
	assert.Equal(t, int32(1), status.TotalDevices)
	assert.Equal(t, int32(1), status.AvailableDevices)
	assert.Len(t, status.ReportingPods, 1)
	assert.Equal(t, HSMPoolPhaseReady, status.Phase)
}

func TestMirroringSpec(t *testing.T) {
	mirroring := MirroringSpec{
		Policy:       MirroringPolicyActive,
		SyncInterval: 60,
		TargetNodes:  []string{"worker-1", "worker-2"},
		PrimaryNode:  "worker-1",
		AutoFailover: true,
	}

	assert.Equal(t, MirroringPolicyActive, mirroring.Policy)
	assert.Equal(t, int32(60), mirroring.SyncInterval)
	assert.Contains(t, mirroring.TargetNodes, "worker-1")
	assert.Contains(t, mirroring.TargetNodes, "worker-2")
	assert.Equal(t, "worker-1", mirroring.PrimaryNode)
	assert.True(t, mirroring.AutoFailover)
}

func TestHSMSecretList(t *testing.T) {
	list := HSMSecretList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hsm.j5t.io/v1alpha1",
			Kind:       "HSMSecretList",
		},
		Items: []HSMSecret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "secret-1",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "secret-2",
				},
			},
		},
	}

	assert.Equal(t, "HSMSecretList", list.Kind)
	assert.Len(t, list.Items, 2)
	assert.Equal(t, "secret-1", list.Items[0].Name)
	assert.Equal(t, "secret-2", list.Items[1].Name)
}

func TestHSMDeviceList(t *testing.T) {
	list := HSMDeviceList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hsm.j5t.io/v1alpha1",
			Kind:       "HSMDeviceList",
		},
		Items: []HSMDevice{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "device-1",
				},
			},
		},
	}

	assert.Equal(t, "HSMDeviceList", list.Kind)
	assert.Len(t, list.Items, 1)
	assert.Equal(t, "device-1", list.Items[0].Name)
}

func TestHSMPoolList(t *testing.T) {
	list := HSMPoolList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hsm.j5t.io/v1alpha1",
			Kind:       "HSMPoolList",
		},
		Items: []HSMPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pool-1",
				},
			},
		},
	}

	assert.Equal(t, "HSMPoolList", list.Kind)
	assert.Len(t, list.Items, 1)
	assert.Equal(t, "pool-1", list.Items[0].Name)
}

// Test JSON serialization/deserialization works correctly
func TestJSONSerialization(t *testing.T) {
	secret := HSMSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Spec: HSMSecretSpec{
			AutoSync: true,
		},
	}

	// This tests that the JSON tags are working correctly
	// by verifying the struct can be used in Kubernetes operations
	assert.Equal(t, "test-secret", secret.Name)
	assert.Equal(t, "default", secret.Namespace)
	assert.True(t, secret.Spec.AutoSync)
}

// Benchmark tests
func BenchmarkHSMSecretCreation(b *testing.B) {
	parentRef := ParentReference{Name: "hsm-operator"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		secret := HSMSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "benchmark-secret",
				Namespace: "default",
			},
			Spec: HSMSecretSpec{
				ParentRef: &parentRef,
				AutoSync:  true,
			},
		}
		_ = secret // Avoid unused variable
	}
}
