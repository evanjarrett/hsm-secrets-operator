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

import "strings"

// maxAgentNameLength bounds the generated agent name. It must stay within the
// 63-character limit for the app.kubernetes.io/instance label (Deployment names
// themselves allow 253, but the label is the tighter constraint).
const maxAgentNameLength = 63

// AgentInstanceName builds the deployment/pod name for the agent that serves a
// single physical HSM, keyed on the device's serial number. The serial is the
// stable identity for a device, so it is used directly rather than a positional
// index (which is unstable across discovery reorderings).
//
// It returns (name, true) on success. If the serial is empty or sanitizes to
// empty, it returns ("", false): a device with no stable serial has no stable
// identity to route to, so the caller must skip deploying an agent for it.
func AgentInstanceName(deviceName, serial string) (string, bool) {
	safeSerial := sanitizeNameSegment(serial)
	if safeSerial == "" {
		return "", false
	}

	name := AgentNamePrefix + "-" + deviceName + "-" + safeSerial
	if len(name) > maxAgentNameLength {
		name = strings.TrimRight(name[:maxAgentNameLength], "-")
	}
	return name, true
}

// sanitizeNameSegment lowercases the input and maps any character outside
// [a-z0-9-] to '-', then trims leading/trailing '-'. The result is safe for use
// as part of a DNS-1123 resource name. This is stricter than sanitizeLabelValue,
// which permits '_', '.', and uppercase — all illegal in resource names.
func sanitizeNameSegment(value string) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r == '-':
			return r
		default:
			return '-'
		}
	}, value)

	return strings.Trim(sanitized, "-")
}
