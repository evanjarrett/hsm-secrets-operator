//go:build !cgo
// +build !cgo

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
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

// USBDevice represents a discovered USB device
type USBDevice struct {
	VendorID     string
	ProductID    string
	SerialNumber string
	DevicePath   string
	Manufacturer string
	Product      string
	DeviceInfo   map[string]string
}

// USBEvent represents a USB device event
type USBEvent struct {
	Action        string    // "add" or "remove"
	Device        USBDevice // The device that changed
	Timestamp     time.Time
	HSMDeviceName string // Which HSMDevice spec this event relates to
}

// USBDiscoverer handles USB device discovery and monitoring (no-CGO stub)
type USBDiscoverer struct {
	logger       logr.Logger
	eventChannel chan USBEvent
	activeSpecs  map[string]*hsmv1alpha1.USBDeviceSpec
}

// NewUSBDiscoverer creates a new USB device discoverer (no-CGO stub)
func NewUSBDiscoverer() *USBDiscoverer {
	logger := ctrl.Log.WithName("usb-discoverer-nocgo")

	return &USBDiscoverer{
		logger:       logger,
		eventChannel: make(chan USBEvent, 100),
		activeSpecs:  make(map[string]*hsmv1alpha1.USBDeviceSpec),
	}
}

// NewUSBDiscovererWithMethod creates a new USB device discoverer (method parameter is ignored, kept for compatibility)
func NewUSBDiscovererWithMethod(method string) *USBDiscoverer {
	return NewUSBDiscoverer()
}

// DiscoverDevices finds USB devices matching the given specification (no-CGO stub - returns empty)
func (u *USBDiscoverer) DiscoverDevices(ctx context.Context, spec *hsmv1alpha1.USBDeviceSpec) ([]USBDevice, error) {
	u.logger.Info("USB device discovery not available (CGO disabled)",
		"vendorId", spec.VendorID,
		"productId", spec.ProductID)

	// Return empty slice - no devices found without udev
	return []USBDevice{}, nil
}

// StartEventMonitoring starts monitoring for USB device events (no-CGO stub - does nothing)
func (u *USBDiscoverer) StartEventMonitoring(ctx context.Context) error {
	u.logger.Info("USB event monitoring not available (CGO disabled)")
	return nil
}

// AddSpecForMonitoring adds a device spec to monitor for events (no-CGO stub)
func (u *USBDiscoverer) AddSpecForMonitoring(hsmDeviceName string, spec *hsmv1alpha1.USBDeviceSpec) {
	u.logger.V(1).Info("USB monitoring not available (CGO disabled)", "device", hsmDeviceName)
	u.activeSpecs[hsmDeviceName] = spec
}

// RemoveSpecFromMonitoring removes a device spec from event monitoring (no-CGO stub)
func (u *USBDiscoverer) RemoveSpecFromMonitoring(hsmDeviceName string) {
	u.logger.V(1).Info("USB monitoring not available (CGO disabled)", "device", hsmDeviceName)
	delete(u.activeSpecs, hsmDeviceName)
}

// GetEventChannel returns the channel for USB events (no-CGO stub)
func (u *USBDiscoverer) GetEventChannel() <-chan USBEvent {
	return u.eventChannel
}

// StopEventMonitoring stops USB device event monitoring (no-CGO stub)
func (u *USBDiscoverer) StopEventMonitoring() {
	u.logger.Info("USB event monitoring not available (CGO disabled)")
}

// IsEventMonitoringActive returns whether event monitoring is active (no-CGO stub - always false)
func (u *USBDiscoverer) IsEventMonitoringActive() bool {
	return false
}