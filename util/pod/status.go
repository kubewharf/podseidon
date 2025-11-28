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

package podutil

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/clock"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

type PodStatus struct {
	IsScheduled bool
	IsRunning   bool
	IsReady     bool
	IsAvailable bool
}

func GetPodStatus(clk clock.Clock, pod *corev1.Pod, minReadySeconds int32, requeue *optional.Optional[time.Duration]) PodStatus {
	return PodStatus{
		IsScheduled: isPodConditionTrue(pod, corev1.PodScheduled),
		IsRunning:   pod.Status.Phase == corev1.PodRunning,
		IsReady:     isPodConditionTrue(pod, corev1.PodReady),
		IsAvailable: IsPodAvailable(clk, pod, minReadySeconds, requeue),
	}
}

// Tests if a pod is available.
// Sets requeue to a shorter period if the availability state is going to change.
func IsPodAvailable(clk clock.Clock, pod *corev1.Pod, minReadySeconds int32, requeue *optional.Optional[time.Duration]) bool {
	if !pod.DeletionTimestamp.IsZero() {
		return false // terminating
	}

	readyConditionIndex := util.FindInSliceWith(
		pod.Status.Conditions,
		func(condition corev1.PodCondition) bool { return condition.Type == corev1.PodReady },
	)
	if readyConditionIndex == -1 {
		return false // readiness unknown
	}

	readyCondition := pod.Status.Conditions[readyConditionIndex]
	if readyCondition.Status != corev1.ConditionTrue {
		return false // not ready
	}

	readyTime := clk.Since(readyCondition.LastTransitionTime.Time)
	minReadyDuration := time.Duration(minReadySeconds) * time.Second

	if readyTime < minReadyDuration {
		readyIn := minReadyDuration - readyTime

		if requeue != nil {
			requeue.SetOrFn(
				readyIn,
				func(base, increment time.Duration) time.Duration { return min(base, increment) },
			)
		}

		return false
	}

	return true
}

func isPodConditionTrue(
	pod *corev1.Pod,
	conditionType corev1.PodConditionType,
) bool {
	return iter.Any(iter.Map(
		iter.FromSlice(pod.Status.Conditions),
		func(condition corev1.PodCondition) bool {
			return condition.Type == conditionType && condition.Status == corev1.ConditionTrue
		},
	))
}
