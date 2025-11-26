// Copyright 2025 The Podseidon Authors.
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

package disruptionquota

import (
	"fmt"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/optional"
	podutil "github.com/kubewharf/podseidon/util/pod"

	"github.com/kubewharf/podseidon/webhook/handler/healthcriterion"
)

type PodStatus struct {
	HealthCriterion healthcriterion.Type

	Status podutil.PodStatus
}

type State struct {
	Scheduled int32
	Running   int32
	Ready     int32
	Available int32

	Threshold Threshold
}

func MakeState(ppr *podseidonv1a1.PodProtector, config defaultconfig.Computed) State {
	summary := &ppr.Status.Summary
	lag := summary.AggregatedAvailable - summary.EstimatedAvailable
	return State{
		Scheduled: summary.AggregatedScheduled,
		Running:   summary.AggregatedRunning,
		Ready:     summary.AggregatedReady,
		Available: summary.AggregatedAvailable,
		Threshold: Threshold{
			Reject: ppr.Spec.MinAvailable,
			Retry:  ppr.Spec.MinAvailable + lag,
			Remaining: optional.Map(
				optional.IfNonZero(config.MaxConcurrentLag),
				func(maxLag int32) int32 { return maxLag - lag },
			),
		},
	}
}

type Threshold struct {
	// If final state is below Reject, reject.
	Reject int32
	// If final state is below Retry but not below Reject, advise retry.
	Retry int32

	// How many remaining additional disruptions can be made
	Remaining optional.Optional[int32]
}

type Result uint8

const (
	DisruptionResultOk = Result(iota)
	DisruptionResultAlreadyUnhealthy
	DisruptionResultRetry
	DisruptionResultDenied
)

func (result Result) ShouldDisrupt() bool {
	return result == DisruptionResultOk || result == DisruptionResultAlreadyUnhealthy
}

func (result Result) String() string {
	switch result {
	case DisruptionResultOk:
		return "Ok"
	case DisruptionResultAlreadyUnhealthy:
		return "AlreadyUnhealthy"
	case DisruptionResultRetry:
		return "Retry"
	case DisruptionResultDenied:
		return "Denied"
	default:
		panic(fmt.Sprintf("unknown enum value %#v", result))
	}
}

// Calculates whether each of the batch items may proceed.
// Does NOT mutate PodProtector; this is the responsibility of the caller.
func AdmitBatch(
	ppr *podseidonv1a1.PodProtector,
	podStatuses []PodStatus,
	config defaultconfig.Computed,
) []Result {
	state := MakeState(ppr, config)

	resultArray := make([]Result, len(podStatuses))

	// Start from the latest pod request, since earlier ones have a higher chance of timeout
	// and would not need to be allowed anyway
	for id := len(podStatuses) - 1; id >= 0; id-- {
		result := state.AdmitBatchItem(podStatuses[id])
		resultArray[id] = result
	}

	return resultArray
}

func (state *State) AdmitBatchItem(podStatus PodStatus) Result {
	result := getResult(*state, podStatus)

	if result.ShouldDisrupt() {
		disruptState(state, podStatus)
	}

	return result
}

func getResult(state State, podStatus PodStatus) Result {
	if state.Threshold.Remaining == optional.Some[int32](0) {
		return DisruptionResultRetry
	}

	switch podStatus.HealthCriterion {
	case healthcriterion.Scheduled:
		return getResultForCriterion(state.Scheduled, podStatus.Status.IsScheduled, state.Threshold)
	case healthcriterion.Running:
		return getResultForCriterion(state.Running, podStatus.Status.IsRunning, state.Threshold)
	case healthcriterion.Ready:
		return getResultForCriterion(state.Ready, podStatus.Status.IsReady, state.Threshold)
	case healthcriterion.Available:
		fallthrough
	default:
		return getResultForCriterion(state.Available, podStatus.Status.IsAvailable, state.Threshold)
	}
}

func getResultForCriterion(current int32, isContributor bool, threshold Threshold) Result {
	if !isContributor {
		return DisruptionResultAlreadyUnhealthy
	}

	if current <= threshold.Reject {
		return DisruptionResultDenied
	}

	if current <= threshold.Retry {
		return DisruptionResultRetry
	}

	return DisruptionResultOk
}

func disruptState(state *State, podStatus PodStatus) {
	if podStatus.Status.IsScheduled {
		state.Scheduled--
	}
	if podStatus.Status.IsRunning {
		state.Running--
	}
	if podStatus.Status.IsReady {
		state.Ready--
	}
	if podStatus.Status.IsAvailable {
		state.Available--
	}

	if threshold := state.Threshold.Remaining.GetValueRefOrNil(); threshold != nil {
		(*threshold)--
	}
}
