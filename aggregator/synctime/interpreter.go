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

package synctime

import (
	"context"
	"flag"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/clock"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/aggregator/constants"
)

const PodInterpreterMuxName = "aggregator-informer-synctime-algorithm"

var RequestPodInterpreter = component.ProvideMux[PodInterpreter](
	PodInterpreterMuxName,
	"the algorithm to infer reflector update time from pod events",
)

var DefaultImpls = component.RequireDeps(
	ProvideClock(clock.RealClock{}),
	ProvideStatus(util.Empty{}),
)

// PodInterpreter determines the last timestamp a pod was updated from the object.
// Users may provide other implementations using side channels to determine this.
type PodInterpreter interface {
	Interpret(pod *corev1.Pod) (time.Time, error)
}

var ProvideClock = component.DeclareMuxImpl(
	PodInterpreterMuxName,
	func(clock.Clock) string { return "clock" },
	func(clock.Clock, *flag.FlagSet) util.Empty { return util.Empty{} },
	func(clock.Clock, *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, clock.Clock, util.Empty, util.Empty) (*util.Empty, error) {
		return &util.Empty{}, nil
	},
	component.Lifecycle[clock.Clock, util.Empty, util.Empty, util.Empty]{Start: nil, Join: nil, HealthChecks: nil},
	func(d *component.Data[clock.Clock, util.Empty, util.Empty, util.Empty]) PodInterpreter {
		return &ClockPodInterpreter{Clock: d.Args}
	},
)

// Always takes the informer receive time as the pod update time.
type ClockPodInterpreter struct {
	Clock clock.Clock
}

func (interp *ClockPodInterpreter) Interpret(*corev1.Pod) (time.Time, error) {
	return interp.Clock.Now(), nil
}

var ProvideStatus = component.DeclareMuxImpl(
	PodInterpreterMuxName,
	func(util.Empty) string { return "status" },
	func(util.Empty, *flag.FlagSet) util.Empty { return util.Empty{} },
	func(util.Empty, *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, util.Empty, util.Empty, util.Empty) (*util.Empty, error) {
		return &util.Empty{}, nil
	},
	component.Lifecycle[util.Empty, util.Empty, util.Empty, util.Empty]{Start: nil, Join: nil, HealthChecks: nil},
	func(*component.Data[util.Empty, util.Empty, util.Empty, util.Empty]) PodInterpreter {
		return StatusPodInterpreter{}
	},
)

type StatusPodInterpreter struct{}

func (StatusPodInterpreter) Interpret(pod *corev1.Pod) (time.Time, error) {
	maxTime := pod.CreationTimestamp.Time

	if !pod.DeletionTimestamp.Time.IsZero() {
		maxTime = pod.DeletionTimestamp.Time
	}

	if timeStr, isUpdateTrigger := pod.Annotations[constants.AnnotUpdateTriggerTime]; isUpdateTrigger {
		updateTime, err := time.Parse(time.RFC3339Nano, timeStr)
		if err == nil && updateTime.After(maxTime) {
			maxTime = updateTime
		}
	}

	for _, condition := range pod.Status.Conditions {
		if condition.LastProbeTime.Time.After(maxTime) {
			maxTime = condition.LastProbeTime.Time
		}
	}

	return maxTime, nil
}
