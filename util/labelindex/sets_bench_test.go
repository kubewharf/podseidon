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

package labelindex_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/podseidon/util/labelindex"
)

const numSets = 1000000

func BenchmarkSetsInsert(b *testing.B) {
	index := labelindex.NewSets[string]()

	for appId := range b.N {
		index.Track(fmt.Sprint(appId), map[string]string{
			"aaa":   "aaa",
			"appId": fmt.Sprint(-appId),
			"zzz":   "zzz",
		})
	}

	for appId := range b.N {
		index.Track(fmt.Sprint(appId), map[string]string{
			"aaa":   "aaa",
			"appId": fmt.Sprint(appId),
			"zzz":   "zzz",
		})
	}
}

func BenchmarkSetsQueryBroadExact(b *testing.B) {
	// Generate data
	index := labelindex.NewSets[string]()

	for appId := range numSets {
		index.Track(fmt.Sprint(appId), map[string]string{
			"aaa":   "aaa",
			"appId": fmt.Sprint(appId),
			"zzz":   "zzz",
		})
	}

	b.ResetTimer()

	for appId := range b.N {
		appIdString := fmt.Sprint(appId % numSets)

		resultIter, err := index.Query(metav1.LabelSelector{
			MatchLabels: map[string]string{
				"aaa":   "aaa",
				"appId": appIdString,
				"zzz":   "zzz",
			},
		})
		require.NoError(b, err)

		result := resultIter.CollectSlice()

		assert.Len(b, result, 1)
		assert.Equal(b, []string{appIdString}, result)
	}
}
