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

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex"
)

func BenchmarkSelectorsInsert(b *testing.B) {
	index := labelindex.NewSelectors[string]()
	rng := rand.New(rand.NewChaCha8([32]byte{}))

	items := make([]iter.Pair[string, metav1.LabelSelector], b.N/2*2)
	appId := 0
	subAppId := 0

	for itemId := range b.N / 2 {
		if rng.IntN(2) == 0 {
			appId++
			subAppId = 0
		} else {
			subAppId++
		}

		items[itemId] = iter.NewPair(fmt.Sprintf("%d-%d", appId, subAppId), metav1.LabelSelector{MatchLabels: map[string]string{
			"aaa":    "aaa",
			"appId":  fmt.Sprint(appId),
			"subApp": fmt.Sprint(subAppId),
			"zzz":    "zzz",
		}})
		items[itemId+b.N/2] = iter.NewPair(fmt.Sprintf("%d-%d", appId, subAppId), metav1.LabelSelector{MatchLabels: map[string]string{
			"aaa":    "aaa",
			"appId":  fmt.Sprint(-appId),
			"subApp": fmt.Sprint(subAppId),
			"zzz":    "zzz",
		}})
	}

	b.ResetTimer()

	for _, item := range items {
		err := index.Track(item.Left, item.Right)
		if err != nil {
			b.Log(err)
			b.FailNow()
		}
	}
}

func BenchmarkSelectorsQueryBroad(b *testing.B) {
	// Generate data
	index := labelindex.NewSelectors[string]()

	rng := rand.New(rand.NewChaCha8([32]byte{}))

	appId := 0
	subAppId := 0

	for appId < b.N {
		err := index.Track(fmt.Sprintf("%d-%d", appId, subAppId), metav1.LabelSelector{MatchLabels: map[string]string{
			"aaa":    "aaa",
			"appId":  fmt.Sprint(appId),
			"subApp": fmt.Sprint(subAppId),
			"zzz":    "zzz",
		}})
		if err != nil {
			b.Log(err)
			b.FailNow()
		}

		if rng.IntN(2) == 0 {
			appId++
			subAppId = 0
		} else {
			subAppId++
		}
	}

	queries := iter.Map(iter.Range(0, b.N), func(appId int) map[string]string {
		return map[string]string{
			"aaa":    "aaa",
			"appId":  fmt.Sprint(appId),
			"subApp": "0",
			"zzz":    "zzz",
		}
	}).CollectSlice()

	b.ResetTimer()

	for _, query := range queries {
		resultIter, _ := index.Query(query)
		result := resultIter.Count()
		assert.Equal(b, uint64(1), result)
	}
}
