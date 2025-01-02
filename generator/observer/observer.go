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
	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/generator/resource"
)

var Provide = component.RequireDeps(
	component.RequireDep(ProvideLogging()),
	component.RequireDep(ProvideMetrics()),
)

type Observer struct {
	InterpretProtectors o11y.ObserveFunc[InterpretProtectors]

	StartReconcile o11y.ObserveScopeFunc[StartReconcile]
	EndReconcile   o11y.ObserveFunc[EndReconcile]

	DanglingProtector    o11y.ObserveFunc[*podseidonv1a1.PodProtector]
	CreateProtector      o11y.ObserveScopeFunc[util.Empty]
	SyncProtector        o11y.ObserveScopeFunc[*podseidonv1a1.PodProtector]
	DeleteProtector      o11y.ObserveScopeFunc[*podseidonv1a1.PodProtector]
	CleanSourceFinalizer o11y.ObserveScopeFunc[StartReconcile]

	MonitorWorkloads o11y.MonitorFunc[util.Empty, MonitorWorkloads]
}

func (Observer) ComponentName() string { return "generator" }

func (observer Observer) Join(other Observer) Observer { return o11y.ReflectJoin(observer, other) }

type InterpretProtectors struct {
	Group     string
	Version   string
	Resource  string
	Kind      string
	Namespace string
	Name      string

	RequiredProtectors []resource.RequiredProtector

	// Short strings describing the justification for the selection of protectors
	Decisions []string
}

type StartReconcile struct {
	Group     string
	Version   string
	Resource  string
	Kind      string
	Namespace string
	Name      string
}

type EndReconcile struct {
	PprName string
	Action  Action
	Err     error
}

type Action string

const (
	ActionNeitherObjectExists Action = "NeitherObjectExists"
	ActionCreatingProtector   Action = "CreatingProtector"
	ActionNoPprNeeded         Action = "NoPprNeeded"
	ActionDanglingProtector   Action = "DanglingProtector"
	ActionSyncProtector       Action = "SyncProtector"
	ActionDeleteProtector     Action = "DeleteProtector"
	ActionError               Action = "Error"
)

type MonitorWorkloads struct {
	// Number of workload objects managed by this generator.
	NumWorkloads int

	// Number of workload objects managed by this generator with non-zero minAvailable.
	NumNonZeroWorkloads int

	// Number of workload objects managed by this generator with
	// the number of aggregated replicas not less than the minAvailable requirement.
	// This is useful for detecting cases if aggregator is not properly deployed
	// or as a less sensitive alternative of `NumAvailableWorkloads`
	// to detect pods not getting created at all.
	NumMinCreatedWorkloads int

	// Number of workload objects managed by this generator with minAvailable satisfied.
	NumAvailableWorkloads int

	// Sum of minAvailable over workloads managed by this generator.
	MinAvailable int64

	// Sum of total aggregated replicas over workloads managed by this generator.
	TotalReplicas int64

	// Sum of available aggregated replicas over workloads managed by this generator.
	AggregatedAvailableReplicas int64

	// Sum of available estimated replicas over workloads managed by this generator.
	EstimatedAvailableReplicas int64

	// Sum of the proportion of aggregated available replicas, saturated at 1, rounded to nearest ppm (parts-per-million).
	// Workloads with zero minAvailable do not contribute to this sum.
	// When divided by NumNonZeroWorkloads, this is the unweighted average of service availability for every PodProtector.
	SumAvailableProportionPpm int64

	// Sum of the .status.summary.maxLatencyMillis field over workloads managed by this generator.
	SumLatencyMillis int64
}

func (dest *MonitorWorkloads) Add(delta MonitorWorkloads) {
	dest.NumWorkloads += delta.NumWorkloads
	dest.NumNonZeroWorkloads += delta.NumNonZeroWorkloads
	dest.NumMinCreatedWorkloads += delta.NumMinCreatedWorkloads
	dest.NumAvailableWorkloads += delta.NumAvailableWorkloads
	dest.MinAvailable += delta.MinAvailable
	dest.TotalReplicas += delta.TotalReplicas
	dest.AggregatedAvailableReplicas += delta.AggregatedAvailableReplicas
	dest.EstimatedAvailableReplicas += delta.EstimatedAvailableReplicas
	dest.SumAvailableProportionPpm += delta.SumAvailableProportionPpm
	dest.SumLatencyMillis += delta.SumLatencyMillis
}

func (dest *MonitorWorkloads) Subtract(delta MonitorWorkloads) {
	dest.NumWorkloads -= delta.NumWorkloads
	dest.NumNonZeroWorkloads -= delta.NumNonZeroWorkloads
	dest.NumMinCreatedWorkloads -= delta.NumMinCreatedWorkloads
	dest.NumAvailableWorkloads -= delta.NumAvailableWorkloads
	dest.MinAvailable -= delta.MinAvailable
	dest.TotalReplicas -= delta.TotalReplicas
	dest.AggregatedAvailableReplicas -= delta.AggregatedAvailableReplicas
	dest.EstimatedAvailableReplicas -= delta.EstimatedAvailableReplicas
	dest.SumAvailableProportionPpm -= delta.SumAvailableProportionPpm
	dest.SumLatencyMillis -= delta.SumLatencyMillis
}
