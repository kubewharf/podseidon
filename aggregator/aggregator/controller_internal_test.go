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

package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kubewharf/podseidon/util/haschange"

	"github.com/kubewharf/podseidon/aggregator/observer"
)

func TestAssignOptionalInt32DoesNotReportUnchangedValue(t *testing.T) {
	t.Parallel()

	var target *int32
	firstChange := haschange.New[observer.StatusChangeCause]()
	assignOptionalInt32(&firstChange, &target, 3, observer.StatusChangeCausePending)
	require.NotNil(t, target)
	assert.Equal(t, int32(3), *target)
	assert.True(t, firstChange.HasChanged())

	firstPointer := target
	secondChange := haschange.New[observer.StatusChangeCause]()
	assignOptionalInt32(&secondChange, &target, 3, observer.StatusChangeCausePending)
	assert.Same(t, firstPointer, target)
	assert.False(t, secondChange.HasChanged())

	thirdChange := haschange.New[observer.StatusChangeCause]()
	assignOptionalInt32(&thirdChange, &target, 4, observer.StatusChangeCausePending)
	require.NotNil(t, target)
	assert.Equal(t, int32(4), *target)
	assert.True(t, thirdChange.HasChanged())
}
