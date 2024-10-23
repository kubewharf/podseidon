// Copyright 2024 The Podseidon Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:validation:Required
// +kubebuilder:resource:path=podprotectors,shortName=ppr
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Required",type=string,JSONPath=".spec.minAvailable"
// +kubebuilder:printcolumn:name="Aggregated",type=integer,JSONPath=".status.summary.aggregatedAvailableReplicas"
// +kubebuilder:printcolumn:name="Estimated",type=integer,JSONPath=".status.summary.aggregatedAvailableReplicas"
// +kubebuilder:printcolumn:name="Bucket lag",type=integer,JSONPath=".status.summary.maxLatencyMillis",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

type PodProtector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PodProtectorSpec   `json:"spec"`
	Status            PodProtectorStatus `json:"status,omitempty"`
}

const (
	PodProtectorKind     = "PodProtector"
	PodProtectorResource = "podprotectors"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type PodProtectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodProtector `json:"items"`
}

type PodProtectorSpec struct {
	// Minimum number of available pods to ensure.
	// Available pods cannot be deleted if the total availability is less than or equal to this value.
	// +kubebuilder:validation:Minimum=0
	MinAvailable int32 `json:"minAvailable"`
	// Number of seconds for which a pod must maintain readiness before being considered available.
	MinReadySeconds int32 `json:"minReadySeconds"`
	// Selects pods to be protected by this object.
	// Only matching pods will be prevented from deletion.
	Selector metav1.LabelSelector `json:"selector"`
	// Selects pods to be counted for MinAvailable.
	// Equal to Selector if unspecified.
	// Must select a superset of Selector.
	AggregationSelector *metav1.LabelSelector `json:"aggregationSelector,omitempty"`
	// Tune parameters for Podseidon components for a specific object.
	AdmissionHistoryConfig AdmissionHistoryConfig `json:"admissionHistoryConfig,omitempty"`
}

type AdmissionHistoryConfig struct {
	// Maximum sum of AdmissionCount.Counter at any point.
	MaxConcurrentLag *int32 `json:"maxConcurrentLag,omitempty"`
	// Number of single-item buckets to retain before compacting to a single bucket.
	CompactThreshold *int32 `json:"compactThreshold,omitempty"`
	// Delay period between receiving pod event and aggregation.
	AggregationRateMillis *int32 `json:"aggregationRateMillis,omitempty"`
}

type PodProtectorStatus struct {
	// +listType=map
	// +listMapKey=cellID
	Cells []PodProtectorCellStatus `json:"cells"`
	// Summary written by status-updating clients for display only,
	// indicating the overall status.
	Summary PodProtectorStatusSummary `json:"summary"`
}

type PodProtectorCellStatus struct {
	// Identifies the group of pods managed by an aggregator instance.
	CellId string `json:"cellID"`
	// The last aggregation data observed by the aggregator for this cell.
	Aggregation PodProtectorAggregation `json:"aggregation,omitempty"`
	// Admission history in this cell handled by webhooks.
	History PodProtectorAdmissionHistory `json:"admissionHistory,omitempty"`
}

type PodProtectorAggregation struct {
	// Number of non-terminated pods as observed by aggregator.
	// +kubebuilder:validation:Minimum=0
	TotalReplicas int32 `json:"totalReplicas"`
	// Number of pods maintaining ready condition for more than MinReadySeconds when observed by aggregator.
	// +kubebuilder:validation:Minimum=0
	AvailableReplicas int32 `json:"availableReplicas"`
	// Timestamp of the last event observed by the pod reflector of the aggregator when this snapshot was written.
	LastEventTime metav1.MicroTime `json:"lastEventTime"`
}

type PodProtectorAdmissionHistory struct {
	Buckets []PodProtectorAdmissionBucket `json:"buckets,omitempty"`
}

// Each bucket represents either one pod or the compacted set of old pods.
type PodProtectorAdmissionBucket struct {
	// Start time of this bucket.
	StartTime metav1.MicroTime `json:"startTime"`

	// The UID of the pod, if there is only one pod in this bucket.
	PodUid *types.UID `json:"podUID,omitempty"`

	// End time of this bucket, if this is a compacted bucket.
	// +optional
	EndTime *metav1.MicroTime `json:"endTime,omitempty"`
	// Number of approved admission reviews within this bucket, if this is a compacted bucket.
	Counter *int32 `json:"counter,omitempty"`
}

type PodProtectorStatusSummary struct {
	// Total number of running pods based on aggregation.
	Total int32 `json:"totalReplicas"`
	// Total number of available pods based on aggregation.
	// Ready pods may not be shown as available if the aggregator is lagging behind.
	AggregatedAvailable int32 `json:"aggregatedAvailableReplicas"`
	// Number of milliseconds elapsed since last reflector event in the slowest cell with outstanding admission history.
	MaxLatencyMillis int64 `json:"maxLatencyMillis,omitempty"`
	// Estimated number of available pods after deducting admission history.
	EstimatedAvailable int32 `json:"estimatedAvailableReplicas"`
}
