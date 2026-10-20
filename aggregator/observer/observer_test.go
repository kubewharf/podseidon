// Copyright 2026 The Podseidon Authors.
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

package observer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/podseidon/aggregator/observer"
)

func TestPendingStatusChangeCauseBitToString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Pending", observer.StatusChangeCausePending.BitToString())
	assert.Equal(t, "UnscheduledPending", observer.StatusChangeCauseUnscheduledPending.BitToString())
}

func TestLoggingObserverAggregatedPendingReplicas(t *testing.T) {
	t.Parallel()

	observer.NewLoggingObserver().Aggregated(context.Background(), observer.Aggregated{
		NumPods:                    5,
		ReadyReplicas:              0,
		ScheduledReplicas:          3,
		RunningReplicas:            0,
		AvailableReplicas:          0,
		PendingReplicas:            3,
		UnscheduledPendingReplicas: 2,
	})
}
