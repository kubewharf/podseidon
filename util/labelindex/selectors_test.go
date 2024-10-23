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
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	"github.com/kubewharf/podseidon/util/labelindex"
	"github.com/kubewharf/podseidon/util/util"
)

type SelectorName string

const (
	selK1V1      = SelectorName("1{1}")
	selK1V12K2V3 = SelectorName("1{1,2}+2{3}")
	selK1V2K2V3  = SelectorName("1{2}+2{3}")
	selK1All     = SelectorName("1{*}")
	selK1None    = SelectorName("1{/}")
	selK2V1      = SelectorName("2{1}")
	selK2All     = SelectorName("2{*}")
	selK2None    = SelectorName("2{/}")
)

var StandardSelectors = map[SelectorName]metav1.LabelSelector{
	selK1V1: {
		MatchLabels: map[string]string{
			"k1": "v1",
		},
		MatchExpressions: nil,
	},

	selK1V12K2V3: {
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Operator: metav1.LabelSelectorOpIn,
				Key:      "k1",
				Values:   []string{"v1", "v2"},
			},
			{
				Operator: metav1.LabelSelectorOpIn,
				Key:      "k2",
				Values:   []string{"v3"},
			},
		},
	},
	selK1V2K2V3: {
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Operator: metav1.LabelSelectorOpIn,
				Key:      "k1",
				Values:   []string{"v2"},
			},
			{
				Operator: metav1.LabelSelectorOpIn,
				Key:      "k2",
				Values:   []string{"v3"},
			},
		},
	},
	selK1All: {
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Operator: metav1.LabelSelectorOpExists,
				Key:      "k1",
				Values:   nil,
			},
		},
	},
	selK1None: {
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Operator: metav1.LabelSelectorOpDoesNotExist,
				Key:      "k1",
				Values:   nil,
			},
		},
	},
	selK2V1: {
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Operator: metav1.LabelSelectorOpIn,
				Key:      "k2",
				Values:   []string{"v1"},
			},
		},
	},
	selK2All: {
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Operator: metav1.LabelSelectorOpExists,
				Key:      "k2",
				Values:   nil,
			},
		},
	},
	selK2None: {
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Operator: metav1.LabelSelectorOpDoesNotExist,
				Key:      "k2",
				Values:   nil,
			},
		},
	},
}

func TestSelectorsQuery(t *testing.T) {
	t.Parallel()

	index := labelindex.NewSelectors[SelectorName]()

	// Init with a random mutant to test that updating works correctly
	for name, selector := range StandardSelectors {
		err := index.Track(name, metav1.LabelSelector{
			MatchLabels: selector.MatchLabels,
			MatchExpressions: util.AppendSliceCopy(
				selector.MatchExpressions,
				metav1.LabelSelectorRequirement{
					Key:      fmt.Sprint(rand.Int()),
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{fmt.Sprint(rand.Int())},
				},
			),
		})
		require.NoError(t, err)
	}

	// Update selectors to the expected values
	for name, selector := range StandardSelectors {
		err := index.Track(name, selector)
		require.NoError(t, err)
	}

	generatePairs(
		map[string]string{},
		[]string{"k1", "k2"},
		[]string{"", "v1", "v2", "v3"},
		func(labelSet map[string]string) {
			t.Run(fmt.Sprintf("LabelSet:%v", labelSet), func(t *testing.T) {
				t.Parallel()

				expected := []SelectorName{}

				for name, selector := range StandardSelectors {
					sel, err := metav1.LabelSelectorAsSelector(ptr.To(selector))

					require.NoError(t, err)

					if sel.Matches(labels.Set(labelSet)) {
						expected = append(expected, name)
					}
				}

				assertQuery(t, index, labelSet, expected...)
			})
		},
	)
}

func generatePairs(
	base map[string]string,
	keys []string,
	values []string,
	fn func(map[string]string),
) {
	if len(keys) == 0 {
		fn(base)
		return
	}

	newKey, restKeys := keys[0], keys[1:]

	for _, newValue := range values {
		newBase := make(map[string]string, len(base)+1)
		if newValue != "" {
			newBase[newKey] = newValue
		}

		for prevKey, prevValue := range base {
			newBase[prevKey] = prevValue
		}

		generatePairs(newBase, restKeys, values, fn)
	}
}

func assertQuery(
	t *testing.T,
	index *labelindex.Selectors[SelectorName],
	labelSet map[string]string,
	expected ...SelectorName,
) {
	t.Helper()

	actualIter, _ := index.Query(labelSet)
	actual := actualIter.CollectSlice()

	sort.SliceStable(expected, func(i, j int) bool { return expected[i] < expected[j] })
	sort.SliceStable(actual, func(i, j int) bool { return actual[i] < actual[j] })

	assert.Equal(t, expected, actual)
}
