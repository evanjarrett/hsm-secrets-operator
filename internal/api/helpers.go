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

package api

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/hsm"
)

// generateHSMPath creates an HSM path from label and ID
func (s *Server) generateHSMPath(label string, id uint32) string {
	return fmt.Sprintf("secrets/api/%s", label)
}

// convertToHSMData converts API data to HSM format based on the specified format
func (s *Server) convertToHSMData(data map[string]interface{}, format SecretFormat) (hsm.SecretData, error) {
	hsmData := make(hsm.SecretData)

	switch format {
	case SecretFormatJSON:
		// Convert each key-value pair to JSON bytes
		for key, value := range data {
			jsonBytes, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal value for key %s: %w", key, err)
			}
			hsmData[key] = jsonBytes
		}
	case SecretFormatText:
		// Convert values to string bytes
		for key, value := range data {
			str := fmt.Sprintf("%v", value)
			hsmData[key] = []byte(str)
		}
	case SecretFormatBinary:
		// Expect values to be base64 encoded strings or byte arrays
		for key, value := range data {
			switch v := value.(type) {
			case string:
				hsmData[key] = []byte(v)
			case []byte:
				hsmData[key] = v
			default:
				return nil, fmt.Errorf("binary format requires string or byte array values for key %s", key)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	return hsmData, nil
}

// convertFromHSMData converts HSM data back to API format
func (s *Server) convertFromHSMData(hsmData hsm.SecretData) (map[string]interface{}, error) {
	data := make(map[string]interface{})

	for key, value := range hsmData {
		// Try to unmarshal as JSON first
		var jsonValue interface{}
		if err := json.Unmarshal(value, &jsonValue); err == nil {
			data[key] = jsonValue
		} else {
			// Fall back to string representation
			data[key] = string(value)
		}
	}

	return data, nil
}

// createHSMSecretResource creates a corresponding HSMSecret Kubernetes resource
func (s *Server) createHSMSecretResource(ctx context.Context, label, hsmPath, description string, tags map[string]string) error {
	hsmSecret := &hsmv1alpha1.HSMSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      label,
			Namespace: "default", // TODO: make configurable
			Labels: map[string]string{
				"managed-by": "hsm-api",
				"app":        "hsm-secrets-operator",
			},
		},
		Spec: hsmv1alpha1.HSMSecretSpec{
			HSMPath:      hsmPath,
			SecretName:   label,
			AutoSync:     true,
			SyncInterval: 300,
			SecretType:   corev1.SecretTypeOpaque,
		},
	}

	// Add tags as annotations
	if len(tags) > 0 {
		if hsmSecret.Annotations == nil {
			hsmSecret.Annotations = make(map[string]string)
		}
		for k, v := range tags {
			hsmSecret.Annotations[fmt.Sprintf("hsm.j5t.io/tag-%s", k)] = v
		}
	}

	// Add description as annotation
	if description != "" {
		if hsmSecret.Annotations == nil {
			hsmSecret.Annotations = make(map[string]string)
		}
		hsmSecret.Annotations["hsm.j5t.io/description"] = description
	}

	return s.client.Create(ctx, hsmSecret)
}

// findHSMSecretByLabel finds an HSMSecret resource by its label/name
func (s *Server) findHSMSecretByLabel(ctx context.Context, label string) (*hsmv1alpha1.HSMSecret, error) {
	// Try default namespace first
	hsmSecret := &hsmv1alpha1.HSMSecret{}
	err := s.client.Get(ctx, types.NamespacedName{
		Name:      label,
		Namespace: "default",
	}, hsmSecret)

	if err == nil {
		return hsmSecret, nil
	}

	// If not found in default, search across all namespaces
	var hsmSecretList hsmv1alpha1.HSMSecretList
	if err := s.client.List(ctx, &hsmSecretList); err != nil {
		return nil, fmt.Errorf("failed to list HSMSecret resources: %w", err)
	}

	for _, secret := range hsmSecretList.Items {
		if secret.Name == label {
			return &secret, nil
		}
	}

	return nil, fmt.Errorf("HSMSecret with label %s not found", label)
}

// findHSMDevice finds a suitable HSMDevice for readonly operations
func (s *Server) findHSMDevice(ctx context.Context) (*hsmv1alpha1.HSMDevice, error) {
	var hsmDeviceList hsmv1alpha1.HSMDeviceList
	if err := s.client.List(ctx, &hsmDeviceList); err != nil {
		return nil, fmt.Errorf("failed to list HSM devices: %w", err)
	}

	// Look for devices that have mirroring enabled and are in a ready state
	for _, device := range hsmDeviceList.Items {
		if device.Spec.Mirroring != nil &&
			device.Spec.Mirroring.Policy != hsmv1alpha1.MirroringPolicyNone &&
			device.Status.Phase == hsmv1alpha1.HSMDevicePhaseReady &&
			len(device.Status.DiscoveredDevices) > 0 {
			return &device, nil
		}
	}

	return nil, fmt.Errorf("no suitable HSM device found")
}

// importFromKubernetes imports secret data from a Kubernetes Secret
func (s *Server) importFromKubernetes(ctx context.Context, secretName, namespace string, keyMapping map[string]string) (map[string]interface{}, error) {
	if namespace == "" {
		namespace = "default"
	}

	// Get the Kubernetes Secret
	secret := &corev1.Secret{}
	err := s.client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes secret %s/%s: %w", namespace, secretName, err)
	}

	// Convert secret data to API format
	data := make(map[string]interface{})
	for key, value := range secret.Data {
		targetKey := key

		// Apply key mapping if provided
		if keyMapping != nil {
			if mappedKey, exists := keyMapping[key]; exists {
				targetKey = mappedKey
			}
		}

		// Try to unmarshal as JSON, otherwise use as string
		var jsonValue interface{}
		if err := json.Unmarshal(value, &jsonValue); err == nil {
			data[targetKey] = jsonValue
		} else {
			data[targetKey] = string(value)
		}
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("no data found in Kubernetes secret %s/%s", namespace, secretName)
	}

	return data, nil
}

// validateSecretAccess checks if the current user has access to the secret (placeholder for future authorization)
func (s *Server) validateSecretAccess(ctx context.Context, label string, operation string) error {
	// TODO: Implement proper authorization logic
	// This could integrate with Kubernetes RBAC, external auth systems, etc.

	s.logger.V(1).Info("Access validation", "label", label, "operation", operation)
	return nil
}
