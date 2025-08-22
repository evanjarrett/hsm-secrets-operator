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

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

func TestAgentNeedsUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	tests := []struct {
		name           string
		deployment     *appsv1.Deployment
		hsmDevice      *hsmv1alpha1.HSMDevice
		hsmPool        *hsmv1alpha1.HSMPool
		expectedUpdate bool
		expectError    bool
	}{
		{
			name: "no update needed - same device path",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-agent",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "agent",
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "hsm-device",
											MountPath: "/dev/hsm",
										},
									},
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "hsm-device",
									VolumeSource: corev1.VolumeSource{
										HostPath: &corev1.HostPathVolumeSource{
											Path: "/dev/bus/usb/001/015",
										},
									},
								},
							},
						},
					},
				},
			},
			hsmDevice: &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device",
					Namespace: "default",
				},
			},
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-pool",
					Namespace: "default",
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/015",
							Available:  true,
						},
					},
				},
			},
			expectedUpdate: false,
			expectError:    false,
		},
		{
			name: "update needed - device path changed",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-agent",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "agent",
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "hsm-device",
											MountPath: "/dev/hsm",
										},
									},
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "hsm-device",
									VolumeSource: corev1.VolumeSource{
										HostPath: &corev1.HostPathVolumeSource{
											Path: "/dev/bus/usb/001/015", // Old path
										},
									},
								},
							},
						},
					},
				},
			},
			hsmDevice: &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device",
					Namespace: "default",
				},
			},
			hsmPool: &hsmv1alpha1.HSMPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device-pool",
					Namespace: "default",
				},
				Status: hsmv1alpha1.HSMPoolStatus{
					AggregatedDevices: []hsmv1alpha1.DiscoveredDevice{
						{
							DevicePath: "/dev/bus/usb/001/016", // New path
							Available:  true,
						},
					},
				},
			},
			expectedUpdate: true,
			expectError:    false,
		},
		{
			name: "no update needed - pool not found",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-agent",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "agent",
								},
							},
						},
					},
				},
			},
			hsmDevice: &hsmv1alpha1.HSMDevice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-device",
					Namespace: "default",
				},
			},
			// No HSMPool object created
			expectedUpdate: false,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create fake client with objects
			objs := []runtime.Object{tt.hsmDevice}
			if tt.hsmPool != nil {
				objs = append(objs, tt.hsmPool)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			manager := &Manager{
				Client:     fakeClient,
				AgentImage: "test-image",
			}

			needsUpdate, err := manager.agentNeedsUpdate(ctx, tt.deployment, tt.hsmDevice)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedUpdate, needsUpdate)
			}
		})
	}
}
