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

package canonical

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex/mm"
	"github.com/kubewharf/podseidon/util/optional"
	utilsort "github.com/kubewharf/podseidon/util/sort"
	"github.com/kubewharf/podseidon/util/util"
)

type Selector struct {
	KeyIn       []KeyInReqmt
	KeyExist    []KeyExistReqmt
	KeyNotIn    []KeyNotInReqmt
	KeyNotExist []KeyNotExistReqmt
}

type KeyInReqmt struct {
	Key    string
	Values []string
}

type KeyExistReqmt struct {
	Key string
}

type KeyNotInReqmt struct {
	Key    string
	Values []string
}

type KeyNotExistReqmt struct {
	Key string
}

func (selector Selector) Matches(set Set) bool {
	return iter.All(iter.Chain(
		iter.Map(utilsort.LeftZip(
			selector.KeyIn, func(reqmt KeyInReqmt) string { return reqmt.Key },
			set, func(pair KvPair) string { return pair.Key },
		), func(item utilsort.LeftZipItem[KeyInReqmt, KvPair]) bool {
			return item.Right.IsSomeAnd(
				func(pair KvPair) bool { return util.SliceContains(item.Left.Values, pair.Value) },
			)
		}),
		iter.Map(utilsort.LeftZip(
			selector.KeyNotIn, func(reqmt KeyNotInReqmt) string { return reqmt.Key },
			set, func(pair KvPair) string { return pair.Key },
		), func(item utilsort.LeftZipItem[KeyNotInReqmt, KvPair]) bool {
			return item.Right.IsNoneOr(
				func(pair KvPair) bool { return !util.SliceContains(item.Left.Values, pair.Value) },
			)
		}),
		iter.Map(utilsort.LeftZip(
			selector.KeyExist, func(reqmt KeyExistReqmt) string { return reqmt.Key },
			set, func(pair KvPair) string { return pair.Key },
		), func(item utilsort.LeftZipItem[KeyExistReqmt, KvPair]) bool {
			return item.Right.IsSome()
		}),
		iter.Map(utilsort.LeftZip(
			selector.KeyNotExist, func(reqmt KeyNotExistReqmt) string { return reqmt.Key },
			set, func(pair KvPair) string { return pair.Key },
		), func(item utilsort.LeftZipItem[KeyNotExistReqmt, KvPair]) bool {
			return !item.Right.IsSome()
		}),
	))
}

func CreateSelector(
	stringSelector metav1.LabelSelector,
) (Selector, error) {
	selector := Selector{
		KeyIn:       nil,
		KeyExist:    nil,
		KeyNotIn:    nil,
		KeyNotExist: nil,
	}

	for key, value := range stringSelector.MatchLabels {
		selector.KeyIn = append(selector.KeyIn, KeyInReqmt{Key: key, Values: []string{value}})
	}

	for _, expr := range stringSelector.MatchExpressions {
		err := addSelectorExprToSelector(&selector, expr)
		if err != nil {
			return util.Zero[Selector](), err
		}
	}

	utilsort.ByKey(selector.KeyIn, func(reqmt KeyInReqmt) string { return reqmt.Key })
	utilsort.ByKey(selector.KeyNotIn, func(reqmt KeyNotInReqmt) string { return reqmt.Key })
	utilsort.ByKey(selector.KeyExist, func(reqmt KeyExistReqmt) string { return reqmt.Key })
	utilsort.ByKey(selector.KeyNotExist, func(reqmt KeyNotExistReqmt) string { return reqmt.Key })

	return selector, nil
}

type SelectorStore[Name comparable] struct {
	selectors map[Name]Selector
}

func NewSelectorStore[Name comparable]() *SelectorStore[Name] {
	return &SelectorStore[Name]{
		selectors: map[Name]Selector{},
	}
}

type InsertResult struct {
	Selector      Selector
	ExistedBefore mm.ExistedBefore
}

func (store *SelectorStore[Name]) Insert(
	name Name,
	stringSelector metav1.LabelSelector,
) (InsertResult, error) {
	var result InsertResult

	selector, err := CreateSelector(stringSelector)
	if err != nil {
		return result, err
	}

	_, hadSelector := store.selectors[name]
	store.selectors[name] = selector

	result.Selector = selector
	result.ExistedBefore = mm.ExistedBefore(hadSelector)

	return result, nil
}

func addSelectorExprToSelector(
	selector *Selector,
	expr metav1.LabelSelectorRequirement,
) error {
	switch expr.Operator {
	case metav1.LabelSelectorOpIn:
		reqmt := KeyInReqmt{Key: expr.Key, Values: expr.Values}

		selector.KeyIn = append(selector.KeyIn, reqmt)

	case metav1.LabelSelectorOpNotIn:
		selector.KeyNotIn = append(
			selector.KeyNotIn,
			KeyNotInReqmt{Key: expr.Key, Values: expr.Values},
		)

	case metav1.LabelSelectorOpExists:
		selector.KeyExist = append(selector.KeyExist, KeyExistReqmt{Key: expr.Key})

	case metav1.LabelSelectorOpDoesNotExist:
		selector.KeyNotExist = append(selector.KeyNotExist, KeyNotExistReqmt{Key: expr.Key})

	default:
		return errors.TagErrorf(
			"UnknownSelectorOperator",
			"unsupported selector operator %q",
			expr.Operator,
		)
	}

	return nil
}

func (store *SelectorStore[Name]) Remove(name Name) (_previous optional.Optional[Selector]) {
	selector, hasSelector := store.selectors[name]
	if !hasSelector {
		return optional.None[Selector]()
	}

	delete(store.selectors, name)

	return optional.Some(selector)
}

func (store *SelectorStore[Name]) Get(name Name) optional.Optional[Selector] {
	return optional.GetMap(store.selectors, name)
}

func (store *SelectorStore[Name]) Len() int {
	return len(store.selectors)
}

func (store *SelectorStore[Name]) Iter() iter.Iter[iter.Pair[Name, Selector]] {
	return iter.MapKvs(store.selectors)
}
