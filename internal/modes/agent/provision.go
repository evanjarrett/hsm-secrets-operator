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
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hsmv1alpha1 "tangled.org/evan.jarrett.net/hsm-secrets-operator/api/v1alpha1"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/agent"
	"tangled.org/evan.jarrett.net/hsm-secrets-operator/internal/hsm"
)

// attemptAutoProvision runs the guarded auto-provisioning flow after a PKCS#11
// Initialize failure. It only ever initializes a token that is opted-in
// (HSMDevice.Spec.PKCS11.AutoProvision) AND positively detected as blank. A SO-PIN is
// generated in-process and discarded; the user PIN is taken from the existing pinSecret.
//
// Returns:
//   - provisioned:   the token was blank and has now been initialized (caller should retry Initialize).
//   - requested:     AutoProvision was enabled for this device. When true and provisioned is
//     false, the caller MUST fail loudly rather than fall back to the mock client — a real
//     hardware device that isn't a provisionable blank should never silently serve mock data.
//   - err:           a fatal error occurred while provisioning an eligible blank device.
func attemptAutoProvision(
	ctx context.Context,
	logger logr.Logger,
	ctrlClient client.Client,
	typedClient kubernetes.Interface,
	deviceName, namespace string,
	pinProvider hsm.PINProvider,
	hsmConfig hsm.Config,
) (provisioned bool, requested bool, err error) {
	// Load the HSMDevice to read the opt-in flag (and to anchor events).
	hsmDevice := &hsmv1alpha1.HSMDevice{}
	if getErr := ctrlClient.Get(ctx, client.ObjectKey{Name: deviceName, Namespace: namespace}, hsmDevice); getErr != nil {
		// Can't determine intent — treat as not requested and let the caller decide.
		logger.Error(getErr, "Could not load HSMDevice to check AutoProvision", "device", deviceName)
		return false, false, nil
	}

	if hsmDevice.Spec.PKCS11 == nil || !hsmDevice.Spec.PKCS11.AutoProvision {
		return false, false, nil
	}
	// From here on this device opted in, so every return reports requested=true.

	// Positive blank-token detection. Fails closed: any error or "not blank" means we do
	// NOT provision, which prevents re-initializing (wiping) an existing device.
	blank, blankErr := hsm.IsTokenBlank(ctx, hsmConfig)
	if blankErr != nil {
		emitDeviceEvent(ctx, logger, typedClient, hsmDevice, corev1.EventTypeWarning, "ProvisionSkipped",
			fmt.Sprintf("Auto-provision enabled but blank-token detection failed: %v", blankErr))
		return false, true, nil
	}
	if !blank {
		emitDeviceEvent(ctx, logger, typedClient, hsmDevice, corev1.EventTypeWarning, "ProvisionSkipped",
			"Auto-provision enabled but token is not a blank, uninitialized device; not initializing")
		return false, true, nil
	}

	// Use the same user PIN that normal operation will log in with.
	userPIN, pinErr := pinProvider.GetPIN(ctx)
	if pinErr != nil {
		emitDeviceEvent(ctx, logger, typedClient, hsmDevice, corev1.EventTypeWarning, "ProvisionFailed",
			fmt.Sprintf("Could not read user PIN from pinSecret: %v", pinErr))
		return false, true, fmt.Errorf("failed to read user PIN for provisioning: %w", pinErr)
	}

	emitDeviceEvent(ctx, logger, typedClient, hsmDevice, corev1.EventTypeNormal, "Provisioning",
		"Blank SC-HSM detected; initializing with a random (discarded) SO-PIN and the configured user PIN")

	provisioner := agent.NewProvisioner(logger)
	if provErr := provisioner.Provision(ctx, userPIN, deviceName); provErr != nil {
		emitDeviceEvent(ctx, logger, typedClient, hsmDevice, corev1.EventTypeWarning, "ProvisionFailed",
			fmt.Sprintf("sc-hsm-tool initialization failed: %v", provErr))
		return false, true, fmt.Errorf("failed to initialize blank device %q: %w", deviceName, provErr)
	}

	emitDeviceEvent(ctx, logger, typedClient, hsmDevice, corev1.EventTypeNormal, "Provisioned",
		"SC-HSM token initialized; connecting")
	return true, true, nil
}

// emitDeviceEvent records a Kubernetes Event against the HSMDevice. It is best-effort:
// failures (e.g. missing RBAC) are logged at low verbosity and never abort provisioning.
func emitDeviceEvent(
	ctx context.Context,
	logger logr.Logger,
	typedClient kubernetes.Interface,
	device *hsmv1alpha1.HSMDevice,
	eventType, reason, message string,
) {
	if typedClient == nil {
		return
	}
	now := metav1.Now()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: device.Name + ".",
			Namespace:    device.Namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "HSMDevice",
			Namespace:  device.Namespace,
			Name:       device.Name,
			UID:        device.UID,
			APIVersion: hsmv1alpha1.GroupVersion.String(),
		},
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		Source:         corev1.EventSource{Component: "hsm-agent"},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
	}
	if _, err := typedClient.CoreV1().Events(device.Namespace).Create(ctx, event, metav1.CreateOptions{}); err != nil {
		logger.V(1).Info("Failed to emit provisioning event", "reason", reason, "error", err)
	}
}
