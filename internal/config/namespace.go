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
	"fmt"
	"os"
	"strings"
)

// GetCurrentNamespace returns the namespace the operator is running in.
// It first tries to read the namespace from the service account mount,
// and returns an error if it cannot be determined.
func GetCurrentNamespace() (string, error) {
	// First try the envvar
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return strings.TrimSpace(ns), nil
	}

	// Try to read namespace from service account mount
	if ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(ns)), nil
	}

	// Return error instead of defaulting to "default" namespace
	return "", fmt.Errorf("unable to determine current namespace: service account namespace file not found")
}
