//go:build !cgo

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

package discovery

import (
	"context"
	"fmt"
)

// UDevDevice is a stub type for non-CGO builds
type UDevDevice struct{}

// Monitor is a stub type for non-CGO builds
type Monitor struct{}

// Udev is a stub type for non-CGO builds
type Udev struct{}

// NewEnumerate creates a stub enumerate object for non-CGO builds
func (u *Udev) NewEnumerate() *Enumerate {
	return &Enumerate{}
}

// NewMonitorFromNetlink creates a stub monitor for non-CGO builds
func (u *Udev) NewMonitorFromNetlink(string) *Monitor {
	return &Monitor{}
}

// Enumerate is a stub type for non-CGO builds
type Enumerate struct{}

// AddMatchSubsystem does nothing in non-CGO builds
func (e *Enumerate) AddMatchSubsystem(string) error {
	return nil
}

// AddMatchProperty does nothing in non-CGO builds
func (e *Enumerate) AddMatchProperty(string, string) error {
	return nil
}

// Devices returns an empty slice for non-CGO builds
func (e *Enumerate) Devices() ([]*UDevDevice, error) {
	return []*UDevDevice{}, nil
}

// DeviceChan returns empty channels for non-CGO builds
func (m *Monitor) DeviceChan(ctx context.Context) (<-chan *UDevDevice, <-chan error, error) {
	deviceChan := make(chan *UDevDevice)
	errorChan := make(chan error)

	// Close channels immediately since no devices will be found
	close(deviceChan)
	close(errorChan)

	return deviceChan, errorChan, nil
}

// FilterAddMatchSubsystem does nothing in non-CGO builds
func (m *Monitor) FilterAddMatchSubsystem(string) error {
	return nil
}

// convertUdevDevice always returns nil for non-CGO builds
func (u *USBDiscoverer) convertUdevDevice(device *UDevDevice) *USBDevice {
	return nil
}

// handleDeviceEvent does nothing for non-CGO builds
func (u *USBDiscoverer) handleDeviceEvent(device *UDevDevice) {
	// No-op for non-CGO builds
}

// init logs a warning about using fallback mode
func init() {
	// Note: We can't use the logger here since it's not available during init
	fmt.Println("WARNING: USB discovery running in fallback mode (CGO disabled). No USB devices will be detected.")
}
