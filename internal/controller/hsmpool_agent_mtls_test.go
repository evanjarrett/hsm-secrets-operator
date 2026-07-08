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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hsmv1alpha1 "tangled.org/evan.jarrett.net/hsm-secrets-operator/api/v1alpha1"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/config"
)

const (
	mtlsServerSecret = "hsm-agent-server-tls"
	mtlsDeviceName   = "test-mtls-device"
	mtlsAgentName    = "hsm-agent-test-mtls-device-0"
)

func mtlsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, hsmv1alpha1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	return s
}

func mtlsPoolAndDevice() (*hsmv1alpha1.HSMPool, *hsmv1alpha1.HSMDevice, *hsmv1alpha1.DiscoveredDevice) {
	pool := &hsmv1alpha1.HSMPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mtlsDeviceName + "-pool",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "hsm.j5t.io/v1alpha1", Kind: "HSMDevice", Name: mtlsDeviceName, UID: "uid-mtls"},
			},
		},
	}
	device := &hsmv1alpha1.HSMDevice{
		ObjectMeta: metav1.ObjectMeta{Name: mtlsDeviceName, Namespace: "default"},
		Spec:       hsmv1alpha1.HSMDeviceSpec{DeviceType: "PicoHSM"},
	}
	discovered := &hsmv1alpha1.DiscoveredDevice{
		NodeName:     "worker-1",
		SerialNumber: "SER123",
		DevicePath:   "/dev/bus/usb/001/015",
		Available:    true,
	}
	return pool, device, discovered
}

func getAgentDeployment(t *testing.T, r *HSMPoolAgentReconciler) *appsv1.Deployment {
	t.Helper()
	var dep appsv1.Deployment
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: mtlsAgentName, Namespace: "default"}, &dep))
	return &dep
}

func TestCreateAgentDeployment_TLSEnabled_MountsCertAndArgs(t *testing.T) {
	scheme := mtlsScheme(t)
	pool, device, discovered := mtlsPoolAndDevice()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pool, device).Build()

	r := &HSMPoolAgentReconciler{
		Client:             fakeClient,
		Scheme:             scheme,
		AgentImage:         "test-image",
		AgentTLSEnabled:    true,
		AgentTLSSecretName: mtlsServerSecret,
	}
	require.NoError(t, r.createAgentDeployment(context.Background(), pool, discovered, mtlsAgentName))

	dep := getAgentDeployment(t, r)
	podSpec := dep.Spec.Template.Spec

	// Volume: Secret source, correct name, 0400 default mode.
	var vol *corev1.Volume
	for i := range podSpec.Volumes {
		if podSpec.Volumes[i].Name == agentTLSVolumeName {
			vol = &podSpec.Volumes[i]
		}
	}
	require.NotNil(t, vol, "expected %q volume on the agent pod", agentTLSVolumeName)
	require.NotNil(t, vol.Secret, "TLS volume must be sourced from a Secret")
	assert.Equal(t, mtlsServerSecret, vol.Secret.SecretName)
	require.NotNil(t, vol.Secret.DefaultMode)
	assert.Equal(t, int32(0o400), *vol.Secret.DefaultMode)

	// Mount: read-only at /etc/hsm/tls on the agent container.
	require.NotEmpty(t, podSpec.Containers)
	var mount *corev1.VolumeMount
	for i := range podSpec.Containers[0].VolumeMounts {
		if podSpec.Containers[0].VolumeMounts[i].Name == agentTLSVolumeName {
			mount = &podSpec.Containers[0].VolumeMounts[i]
		}
	}
	require.NotNil(t, mount, "expected %q volume mount on the agent container", agentTLSVolumeName)
	assert.Equal(t, agentTLSMountPath, mount.MountPath)
	assert.True(t, mount.ReadOnly)

	// Args: the three --tls-* flags pointing into the mount path.
	args := podSpec.Containers[0].Args
	assert.Contains(t, args, "--tls-cert-file="+agentTLSMountPath+"/tls.crt")
	assert.Contains(t, args, "--tls-key-file="+agentTLSMountPath+"/tls.key")
	assert.Contains(t, args, "--tls-client-ca-file="+agentTLSMountPath+"/ca.crt")
}

func TestCreateAgentDeployment_TLSDisabled_NoCertWiring(t *testing.T) {
	scheme := mtlsScheme(t)
	pool, device, discovered := mtlsPoolAndDevice()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pool, device).Build()

	r := &HSMPoolAgentReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		AgentImage: "test-image",
		// AgentTLSEnabled defaults false
	}
	require.NoError(t, r.createAgentDeployment(context.Background(), pool, discovered, mtlsAgentName))

	dep := getAgentDeployment(t, r)
	podSpec := dep.Spec.Template.Spec

	for _, v := range podSpec.Volumes {
		assert.NotEqual(t, agentTLSVolumeName, v.Name, "TLS volume must not be present when disabled")
	}
	for _, m := range podSpec.Containers[0].VolumeMounts {
		assert.NotEqual(t, agentTLSVolumeName, m.Name, "TLS mount must not be present when disabled")
	}
	for _, a := range podSpec.Containers[0].Args {
		assert.NotContains(t, a, "--tls-cert-file=", "TLS args must not be present when disabled")
	}
}

func TestBuildAgentArgs_TLSFlagsGated(t *testing.T) {
	scheme := mtlsScheme(t)
	pool, device, _ := mtlsPoolAndDevice()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pool, device).Build()

	enabled := &HSMPoolAgentReconciler{
		Client: fakeClient, Scheme: scheme,
		AgentTLSEnabled: true, AgentTLSSecretName: mtlsServerSecret,
	}
	args := enabled.buildAgentArgs(context.Background(), pool, mtlsDeviceName)
	assert.Contains(t, args, "--tls-cert-file="+agentTLSMountPath+"/tls.crt")

	disabled := &HSMPoolAgentReconciler{Client: fakeClient, Scheme: scheme}
	args = disabled.buildAgentArgs(context.Background(), pool, mtlsDeviceName)
	for _, a := range args {
		assert.NotContains(t, a, "--tls-cert-file=")
	}
}

// deploymentWithTLSVolume builds a minimal agent deployment, optionally carrying
// the mTLS volume, with the given container image.
func deploymentWithTLSVolume(image string, withTLS bool) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: mtlsAgentName, Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					// hardened securityContext so only the TLS check varies
					Containers: []corev1.Container{{Name: "agent", Image: image, SecurityContext: hardenedAgentSecurityContext()}},
				},
			},
		},
	}
	if withTLS {
		dep.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name:         agentTLSVolumeName,
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: mtlsServerSecret}},
		}}
	}
	return dep
}

func TestAgentNeedsUpdate_TLSToggle(t *testing.T) {
	scheme := mtlsScheme(t)
	pool, device, _ := mtlsPoolAndDevice()

	tests := []struct {
		name         string
		tlsEnabled   bool
		deployHasTLS bool
		wantUpdate   bool
	}{
		{"enable when volume absent", true, false, true},
		{"disable when volume present", false, true, true},
		{"stable when enabled and mounted", true, true, false},
		{"stable when disabled and absent", false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pool, device).Build()
			r := &HSMPoolAgentReconciler{
				Client:        fakeClient,
				Scheme:        scheme,
				ImageResolver: &config.ImageResolver{},
				AgentImage:    "test-image", // matches deployment image so only the TLS check varies
			}
			if tt.tlsEnabled {
				r.AgentTLSEnabled = true
				r.AgentTLSSecretName = mtlsServerSecret
			}

			dep := deploymentWithTLSVolume("test-image", tt.deployHasTLS)
			needsUpdate, err := r.agentNeedsUpdate(context.Background(), dep, pool)
			require.NoError(t, err)
			assert.Equal(t, tt.wantUpdate, needsUpdate)
		})
	}
}
