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

package config

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetCurrentServiceAccount returns the service account name the current pod is running under.
// It first gets the current namespace and pod name, then fetches the pod spec from the Kubernetes API
// to get the ServiceAccountName. Returns an error if it cannot be determined.
func GetCurrentServiceAccount(ctx context.Context, k8sClient client.Client) (string, error) {
	// Get current namespace
	namespace, err := GetCurrentNamespace()
	if err != nil {
		return "", fmt.Errorf("unable to get current namespace: %w", err)
	}

	// Get pod name from hostname
	podName, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("unable to get pod name from hostname: %w", err)
	}

	// Fetch the pod spec from Kubernetes API
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{
		Name:      podName,
		Namespace: namespace,
	}

	if err := k8sClient.Get(ctx, podKey, pod); err != nil {
		return "", fmt.Errorf("unable to get pod %s/%s: %w", namespace, podName, err)
	}

	// Get the service account name from pod spec
	if pod.Spec.ServiceAccountName == "" {
		return "", fmt.Errorf("pod %s/%s has no service account specified in its spec", namespace, podName)
	}

	return pod.Spec.ServiceAccountName, nil
}
