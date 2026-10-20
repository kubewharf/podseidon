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

package v1alpha1_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
)

func TestPodProtectorAggregationPendingReplicasOptionalSemantics(t *testing.T) {
	t.Parallel()

	unknown, err := json.Marshal(podseidonv1a1.PodProtectorAggregation{})
	require.NoError(t, err)
	assert.NotContains(t, string(unknown), "pendingReplicas")
	assert.NotContains(t, string(unknown), "unscheduledPendingReplicas")

	pendingReplicas := int32(0)
	unscheduledPendingReplicas := int32(0)
	reported := podseidonv1a1.PodProtectorAggregation{
		PendingReplicas:            &pendingReplicas,
		UnscheduledPendingReplicas: &unscheduledPendingReplicas,
	}
	knownZero, err := json.Marshal(reported)
	require.NoError(t, err)
	assert.Contains(t, string(knownZero), `"pendingReplicas":0`)
	assert.Contains(t, string(knownZero), `"unscheduledPendingReplicas":0`)

	copied := reported.DeepCopy()
	require.NotSame(t, reported.PendingReplicas, copied.PendingReplicas)
	require.NotSame(t, reported.UnscheduledPendingReplicas, copied.UnscheduledPendingReplicas)
}
