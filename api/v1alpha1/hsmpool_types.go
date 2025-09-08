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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HSMPoolSpec defines the desired state of HSMPool
type HSMPoolSpec struct {
	// GracePeriod defines how long to wait before considering a pod's report stale
	// +kubebuilder:default="5m"
	// +optional
	GracePeriod *metav1.Duration `json:"gracePeriod,omitempty"`

	// Mirroring defines device mirroring configuration for this pool
	// +optional
	Mirroring *MirroringSpec `json:"mirroring,omitempty"`
}

// HSMPoolStatus defines the observed state of HSMPool
type HSMPoolStatus struct {
	// AggregatedDevices lists all devices found across all reporting pods
	// +optional
	AggregatedDevices []DiscoveredDevice `json:"aggregatedDevices,omitempty"`

	// TotalDevices is the total number of unique devices found
	TotalDevices int32 `json:"totalDevices"`

	// AvailableDevices is the number of currently available devices
	AvailableDevices int32 `json:"availableDevices"`

	// ReportingPods tracks which pods have provided discovery reports
	// +optional
	ReportingPods []PodReport `json:"reportingPods,omitempty"`

	// ExpectedPods is the number of pods expected to report (from DaemonSet)
	ExpectedPods int32 `json:"expectedPods"`

	// LastAggregationTime is when the pool was last updated from pod reports
	// +optional
	LastAggregationTime *metav1.Time `json:"lastAggregationTime,omitempty"`

	// Conditions represent the latest available observations of the pool state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase represents the current phase of device aggregation
	// +optional
	Phase HSMPoolPhase `json:"phase,omitempty"`

	// Mirroring represents the status of device mirroring for this pool
	// +optional
	Mirroring *MirroringStatus `json:"mirroring,omitempty"`
}

// PodReport represents a discovery report from a specific pod
type PodReport struct {
	// PodName is the name of the reporting pod
	PodName string `json:"podName"`

	// NodeName is the node where the pod is running
	NodeName string `json:"nodeName"`

	// DevicesFound is the number of devices found by this pod
	DevicesFound int32 `json:"devicesFound"`

	// LastReportTime is when this pod last reported via annotations
	LastReportTime metav1.Time `json:"lastReportTime"`

	// DiscoveryStatus is the status of discovery from this pod
	DiscoveryStatus string `json:"discoveryStatus"` // "discovering", "completed", "error"

	// Error message if discovery failed
	// +optional
	Error string `json:"error,omitempty"`

	// Fresh indicates if this report is within the grace period
	Fresh bool `json:"fresh"`
}

// HSMPoolPhase represents the current phase of device pool aggregation
type HSMPoolPhase string

const (
	// HSMPoolPhasePending indicates pool is waiting for pod reports
	HSMPoolPhasePending HSMPoolPhase = "Pending"
	// HSMPoolPhaseAggregating indicates pool is collecting reports from pods
	HSMPoolPhaseAggregating HSMPoolPhase = "Aggregating"
	// HSMPoolPhaseReady indicates pool has complete device information
	HSMPoolPhaseReady HSMPoolPhase = "Ready"
	// HSMPoolPhasePartial indicates some pods are not reporting (grace period active)
	HSMPoolPhasePartial HSMPoolPhase = "Partial"
	// HSMPoolPhaseError indicates an error occurred during aggregation
	HSMPoolPhaseError HSMPoolPhase = "Error"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hsmpool
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalDevices`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.availableDevices`
// +kubebuilder:printcolumn:name="Reporting",type=string,JSONPath=`.status.reportingPods[*].podName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Last Aggregation",type=date,JSONPath=`.status.lastAggregationTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HSMPool is the Schema for the hsmpools API
type HSMPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HSMPoolSpec   `json:"spec,omitempty"`
	Status HSMPoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HSMPoolList contains a list of HSMPool
type HSMPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HSMPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HSMPool{}, &HSMPoolList{})
}
