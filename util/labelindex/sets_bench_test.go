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
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex"
)

func BenchmarkSetsInsert(b *testing.B) {
	index := labelindex.NewSets[string]()
	rng := rand.New(rand.NewChaCha8([32]byte{}))

	items := make([]iter.Pair[string, map[string]string], b.N/2*2)
	appId := 0
	subAppId := 0

	for itemId := range b.N / 2 {
		if rng.IntN(2) == 0 {
			appId++
			subAppId = 0
		} else {
			subAppId++
		}

		items[itemId] = iter.NewPair(fmt.Sprintf("%d-%d", appId, subAppId), map[string]string{
			"aaa":    "aaa",
			"appId":  fmt.Sprint(appId),
			"subApp": fmt.Sprint(subAppId),
			"zzz":    "zzz",
		})
		items[itemId+b.N/2] = iter.NewPair(fmt.Sprintf("%d-%d", appId, subAppId), map[string]string{
			"aaa":    "aaa",
			"appId":  fmt.Sprint(-appId),
			"subApp": fmt.Sprint(subAppId),
			"zzz":    "zzz",
		})
	}

	b.ResetTimer()

	for _, item := range items {
		index.Track(item.Left, item.Right)
	}
}

func BenchmarkSetsQueryBroad(b *testing.B) {
	// Generate data
	index := labelindex.NewSets[string]()
	rng := rand.New(rand.NewChaCha8([32]byte{}))

	appId := 0
	subAppId := 0

	for appId < b.N {
		if rng.IntN(2) == 0 {
			appId++
			subAppId = 0
		} else {
			subAppId++
		}

		index.Track(fmt.Sprintf("%d-%d", appId, subAppId), map[string]string{
			"aaa":    "aaa",
			"appId":  fmt.Sprint(appId),
			"subApp": fmt.Sprint(subAppId),
			"zzz":    "zzz",
		})
	}

	queries := iter.Map(iter.Range(0, b.N), func(appId int) metav1.LabelSelector {
		return metav1.LabelSelector{
			MatchLabels: map[string]string{
				"aaa":   "aaa",
				"appId": fmt.Sprint(appId),
				"zzz":   "zzz",
			},
		}
	}).CollectSlice()

	b.ResetTimer()

	for _, query := range queries {
		resultIter, err := index.Query(query)
		require.NoError(b, err)

		resultCount := resultIter.Count()

		if resultCount == 0 {
			b.Fail()
			b.Logf("query %v yields no results", query)
		}
	}
}
