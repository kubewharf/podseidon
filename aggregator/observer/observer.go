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

package observer

import (
	"time"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/haschange"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

var Provide = component.RequireDeps(
	component.RequireDep(ProvideLogging()),
	component.RequireDep(ProvideMetrics()),
)

type Observer struct {
	StartReconcile o11y.ObserveScopeFunc[StartReconcile]
	EndReconcile   o11y.ObserveFunc[EndReconcile]

	StartEnqueue o11y.ObserveScopeFunc[StartEnqueue]
	EndEnqueue   o11y.ObserveFunc[EndEnqueue]

	EnqueueError o11y.ObserveFunc[EnqueueError]
	Aggregated   o11y.ObserveFunc[Aggregated]

	NextEventPoolCurrentSize    o11y.MonitorFunc[util.Empty, int]
	NextEventPoolCurrentLatency o11y.MonitorFunc[util.Empty, time.Duration]
	NextEventPoolSingleDrain    o11y.ObserveFunc[NextEventPoolSingleDrain]

	TriggerPodCreate o11y.ObserveFunc[TriggerPodCreate]
	TriggerPodUpdate o11y.ObserveFunc[TriggerPodUpdate]
}

func (Observer) ComponentName() string { return "aggregator" }

func (observer Observer) Join(other Observer) Observer { return o11y.ReflectJoin(observer, other) }

type StartReconcile struct {
	Namespace string
	Name      string
}

type EndReconcile struct {
	Err       error
	HasChange haschange.Changed
	Action    ReconcileAction
}

type ReconcileAction string

const (
	ReconcileActionNoPpr            = ReconcileAction("NoPpr")
	ReconcileActionSelectorMismatch = ReconcileAction("SelectorMismatch")
	ReconcileActionUpdated          = ReconcileAction("Updated")
)

type StartEnqueue struct {
	Namespace string
	Name      string
	Kind      string
}

type EndEnqueue struct{}

type EnqueueError struct {
	Namespace string
	Name      string
	Err       error
}

type Aggregated struct {
	NumPods           int
	ReadyReplicas     int32
	ScheduledReplicas int32
	RunningReplicas   int32
	AvailableReplicas int32
}

type NextEventPoolSingleDrain struct {
	// Number of items in a single drain.
	Size int
	// Time since the earliest item required reconciliation.
	ObjectLatency time.Duration
	// Time since the previous drain, None if this is the first run.
	TimeSinceLastDrain optional.Optional[time.Duration]
}

type TriggerPodCreate struct {
	Err error
}

type TriggerPodUpdate struct {
	Err error
}
