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

package synctime

import (
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/aggregator/constants"
)

// PodInterpreter determines the last timestamp a pod was updated from the object.
// Users may provide other implementations using side channels to determine this.
type PodInterpreter interface {
	Interpret(pod *corev1.Pod) (time.Time, error)
}

// Always takes the informer receive time as the pod update time.
type ClockPodInterpreter struct {
	Clock clock.Clock
}

func (interp *ClockPodInterpreter) Interpret(*corev1.Pod) (time.Time, error) {
	return interp.Clock.Now(), nil
}

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

func New(interpreter PodInterpreter) (InitialMarker, Notifier, Reader) {
	lastInformerSync := &atomic.Pointer[time.Time]{}

	// Since we only use this on pod informers,
	// pods always receive multiple update events (due to deletion status updates),
	// so we don't need to worry about not sending events on deleted objects.

	initialMarker := func() {
		// Empty list, initialize pointer with current timestamp if it is the initial event.
		_ = lastInformerSync.CompareAndSwap(nil, ptr.To(time.Now()))
	}

	notifier := func(pod *corev1.Pod) error {
		newTime, err := interpreter.Interpret(pod)
		if err != nil {
			return errors.TagWrapf("InterpretPod", err, "interpreting sync timestamp from pod")
		}

		util.AtomicExtrema(lastInformerSync, ptr.To(newTime), func(left, right *time.Time) bool {
			if left == nil {
				return true
			}

			if right == nil {
				return false
			}

			return right.After(*left)
		})

		return nil
	}

	reader := func() optional.Optional[time.Time] {
		return optional.FromPtr(lastInformerSync.Load())
	}

	return initialMarker, notifier, reader
}

type InitialMarker func()

type Notifier func(*corev1.Pod) error

type Reader func() optional.Optional[time.Time]
