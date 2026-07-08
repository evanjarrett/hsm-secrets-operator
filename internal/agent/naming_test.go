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
	"strings"
	"testing"
)

func TestAgentInstanceName(t *testing.T) {
	tests := []struct {
		name       string
		deviceName string
		serial     string
		wantName   string
		wantOK     bool
	}{
		{
			name:       "normal serial",
			deviceName: "pico-hsm",
			serial:     "DC6A33145E23A42A",
			wantName:   "hsm-agent-pico-hsm-dc6a33145e23a42a",
			wantOK:     true,
		},
		{
			name:       "serial with illegal characters is sanitized",
			deviceName: "pico-hsm",
			serial:     "AB_12:34.56",
			wantName:   "hsm-agent-pico-hsm-ab-12-34-56",
			wantOK:     true,
		},
		{
			name:       "empty serial returns not-ok",
			deviceName: "pico-hsm",
			serial:     "",
			wantOK:     false,
		},
		{
			name:       "serial that sanitizes to empty returns not-ok",
			deviceName: "pico-hsm",
			serial:     "___",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := AgentInstanceName(tt.deviceName, tt.serial)
			if ok != tt.wantOK {
				t.Fatalf("AgentInstanceName(%q, %q) ok = %v, want %v", tt.deviceName, tt.serial, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got != tt.wantName {
				t.Errorf("AgentInstanceName(%q, %q) = %q, want %q", tt.deviceName, tt.serial, got, tt.wantName)
			}
		})
	}
}

func TestAgentInstanceNameLengthCap(t *testing.T) {
	longSerial := strings.Repeat("a", 200)
	got, ok := AgentInstanceName("pico-hsm", longSerial)
	if !ok {
		t.Fatalf("expected ok for long serial")
	}
	if len(got) > maxAgentNameLength {
		t.Errorf("name length = %d, want <= %d", len(got), maxAgentNameLength)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("name %q should not end with '-'", got)
	}
}
