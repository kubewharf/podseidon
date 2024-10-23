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
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex"
)

type SetName string

var StandardSets = map[SetName]map[string]string{
	"1{1}": {"k1": "v1"},
	"1{2}": {"k1": "v2", "k2": "v2"},
	"2{2}": {"k2": "v2"},
}

func testSetsQuery(t *testing.T, selector metav1.LabelSelector) {
	t.Helper()

	index := labelindex.NewSets[SetName]()

	parsed, err := metav1.LabelSelectorAsSelector(&selector)
	require.NoError(t, err)

	expectedSet := sets.New[SetName]()

	// Init with a random mutant to test that updating works correctly
	for name, set := range StandardSets {
		setCopy := iter.CollectMap(iter.MapKvs(set))

		setCopy[fmt.Sprint(rand.Int())] = fmt.Sprint(rand.Int())

		index.Track(name, setCopy)
	}

	for name, set := range StandardSets {
		index.Track(name, set)

		if parsed.Matches(labels.Set(set)) {
			expectedSet.Insert(name)
		}
	}

	expected := sets.List(expectedSet)

	actualIter, err := index.Query(selector)
	require.NoError(t, err)

	actual := actualIter.CollectSlice()

	sort.SliceStable(expected, func(i, j int) bool { return expected[i] < expected[j] })
	sort.SliceStable(actual, func(i, j int) bool { return actual[i] < actual[j] })

	assert.Equal(t, expected, actual)
}

func TestSetsQuerySingleReqSingleMatch(t *testing.T) {
	t.Parallel()

	testSetsQuery(t, metav1.LabelSelector{
		MatchLabels:      map[string]string{"k1": "v1"},
		MatchExpressions: nil,
	})
}

func TestSetsQuerySingleReqDoubleMatch(t *testing.T) {
	t.Parallel()

	testSetsQuery(t, metav1.LabelSelector{
		MatchLabels: map[string]string{"k2": "v2"},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Operator: metav1.LabelSelectorOpIn, Key: "k2", Values: []string{"v2"}},
		},
	})
}

func TestSetsQueryDoubleReqSingleMatch(t *testing.T) {
	t.Parallel()

	testSetsQuery(t, metav1.LabelSelector{
		MatchLabels:      map[string]string{"k2": "v2", "k1": "v2"},
		MatchExpressions: nil,
	})
}

func TestSetsQueryExists(t *testing.T) {
	t.Parallel()

	testSetsQuery(t, metav1.LabelSelector{
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Operator: metav1.LabelSelectorOpExists, Key: "k2", Values: nil},
		},
	})
}

func TestSetsQueryDoesNotExist(t *testing.T) {
	t.Parallel()

	testSetsQuery(t, metav1.LabelSelector{
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Operator: metav1.LabelSelectorOpDoesNotExist, Key: "k2", Values: nil},
		},
	})
}

func TestSetsQueryNotIn(t *testing.T) {
	t.Parallel()

	testSetsQuery(t, metav1.LabelSelector{
		MatchLabels: nil,
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Operator: metav1.LabelSelectorOpNotIn, Key: "k1", Values: []string{"v1"}},
		},
	})
}

func TestSetsQueryEmpty(t *testing.T) {
	t.Parallel()

	testSetsQuery(t, metav1.LabelSelector{
		MatchLabels:      nil,
		MatchExpressions: nil,
	})
}
