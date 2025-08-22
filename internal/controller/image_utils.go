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

import (
	"context"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const defaultImage = "ghcr.io/evanjarrett/hsm-secrets-operator:latest"

// ImageResolver provides functionality to resolve container images
type ImageResolver struct {
	Client client.Client
}

// NewImageResolver creates a new ImageResolver
func NewImageResolver(k8sClient client.Client) *ImageResolver {
	return &ImageResolver{
		Client: k8sClient,
	}
}

// GetManagerImage attempts to detect the manager's running image by looking for manager deployments
func (r *ImageResolver) GetManagerImage(ctx context.Context) string {
	logger := log.FromContext(ctx)

	// Get manager deployment by looking for deployments with manager labels
	deployments := &appsv1.DeploymentList{}
	listOpts := []client.ListOption{
		client.MatchingLabels{
			"app.kubernetes.io/name":      "hsm-secrets-operator",
			"app.kubernetes.io/component": "manager",
		},
	}

	if err := r.Client.List(ctx, deployments, listOpts...); err != nil {
		logger.V(1).Info("Failed to list manager deployments for image detection", "error", err)
		return ""
	}

	// Find the manager container and extract its image
	for _, deployment := range deployments.Items {
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name == "manager" {
				logger.V(1).Info("Detected manager image", "image", container.Image)
				return container.Image
			}
		}
	}

	logger.V(1).Info("Could not detect manager image")
	return ""
}

func (r *ImageResolver) GetImage(ctx context.Context, env string) string {
	// Try environment variable first
	if discoveryImage := os.Getenv(env); discoveryImage != "" {
		return discoveryImage
	}

	// Try to detect the manager's running image as fallback
	if managerImage := r.GetManagerImage(ctx); managerImage != "" {
		return managerImage
	}

	// last resort
	return defaultImage
}
