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

package labelindex

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex/canonical"
	"github.com/kubewharf/podseidon/util/labelindex/mm"
	"github.com/kubewharf/podseidon/util/util"
)

// Maintains an index of known label sets.
// Efficiently queries the names of sets matching a selector.
type Sets[Name comparable] struct {
	knownSets *canonical.SetStore[Name]

	kvIndex  mm.MultiMap[canonical.KvPair, Name]
	keyIndex mm.MultiMap[string, Name]
}

func _[Name comparable](
	index *Sets[Name],
) Index[Name, map[string]string, metav1.LabelSelector, util.Empty, error] {
	return index
}

func NewSets[Name comparable]() *Sets[Name] {
	return &Sets[Name]{
		knownSets: canonical.NewSetStore[Name](),
		kvIndex:   mm.MultiMap[canonical.KvPair, Name]{},
		keyIndex:  mm.MultiMap[string, Name]{},
	}
}

func (indexer *Sets[Name]) Track(name Name, set map[string]string) util.Empty {
	indexer.doUntrack(name)
	indexer.doTrack(name, set)

	return util.Empty{}
}

func (indexer *Sets[Name]) Untrack(name Name) ExistedBefore {
	return indexer.doUntrack(name)
}

func (indexer *Sets[Name]) doTrack(
	name Name,
	rawSet map[string]string,
) {
	set, existed := indexer.knownSets.Insert(name, rawSet)

	if existed {
		panic("doTrack must not be called before untracking")
	}

	for _, pair := range set {
		indexer.kvIndex.Insert(pair, name)

		indexer.keyIndex.Insert(pair.Key, name)
	}
}

func (indexer *Sets[Name]) doUntrack(name Name) ExistedBefore {
	set, existed := indexer.knownSets.Remove(name)
	if !existed {
		return ExistedBefore(false)
	}

	for _, pair := range set {
		kvExisted := indexer.kvIndex.Remove(pair, name)
		if !kvExisted {
			panic(
				fmt.Sprintf(
					"label index is inconsistent, (%#v, %#v, %#v) missing from kvIndex",
					pair.Key, pair.Value, name,
				),
			)
		}

		keyExisted := indexer.keyIndex.Remove(pair.Key, name)
		if !keyExisted {
			panic(
				fmt.Sprintf(
					"label index is inconsistent, (%#v, %#v) missing from keyIndex",
					pair.Key, name,
				),
			)
		}
	}

	return ExistedBefore(true)
}

func (indexer *Sets[Name]) Query(rawSelector metav1.LabelSelector) (iter.Iter[Name], error) {
	// zero values for untracked label selector fields are fine because we do not compare two strings from the input selector
	selector, err := canonical.CreateSelector(rawSelector)
	if err != nil {
		return nil, errors.TagWrapf("CanonicalizeSelector", err, "canonicalize label selector")
	}

	bestIndexer := queryIndex[*Sets[Name], Name](queryIndexAll[Name]{})

	for _, reqmt := range selector.KeyExist {
		replaceBetterIndex(indexer, &bestIndexer, queryIndexKeyExist[Name]{reqmt: reqmt})
	}

	for _, reqmt := range selector.KeyIn {
		replaceBetterIndex(indexer, &bestIndexer, queryIndexKeyValues[Name]{reqmt: reqmt})
	}

	return iter.Deduplicate(bestIndexer.Iter(indexer).Filter(func(name Name) bool {
		return selector.Matches(
			indexer.knownSets.Get(name).MustGet("indexed name does not exist in set store"),
		)
	})), nil
}

type queryIndex[Subj any, Name comparable] interface {
	Cardinality(subj Subj) int
	Iter(subj Subj) iter.Iter[Name]
}

type queryIndexAll[Name comparable] struct{}

func (queryIndexAll[Name]) Cardinality(indexer *Sets[Name]) int { return indexer.knownSets.Len() }

func (queryIndexAll[Name]) Iter(indexer *Sets[Name]) iter.Iter[Name] {
	return iter.Map(indexer.knownSets.Iter(), func(item iter.Pair[Name, canonical.Set]) Name {
		return item.Left
	})
}

type queryIndexKeyExist[Name comparable] struct {
	reqmt canonical.KeyExistReqmt
}

func (index queryIndexKeyExist[Name]) Cardinality(indexer *Sets[Name]) int {
	return len(indexer.keyIndex[index.reqmt.Key])
}

func (index queryIndexKeyExist[Name]) Iter(indexer *Sets[Name]) iter.Iter[Name] {
	return iter.MapKeys(indexer.keyIndex[index.reqmt.Key])
}

type queryIndexKeyValues[Name comparable] struct{ reqmt canonical.KeyInReqmt }

func (index queryIndexKeyValues[Name]) indices(indexer *Sets[Name]) iter.Iter[sets.Set[Name]] {
	return iter.Map(
		iter.FromSlice(index.reqmt.Values),
		func(value string) sets.Set[Name] {
			return indexer.kvIndex[canonical.KvPair{Key: index.reqmt.Key, Value: value}]
		},
	)
}

func (index queryIndexKeyValues[Name]) Cardinality(indexer *Sets[Name]) int {
	return iter.Sum(iter.Map(index.indices(indexer), sets.Set[Name].Len))
}

func (index queryIndexKeyValues[Name]) Iter(indexer *Sets[Name]) iter.Iter[Name] {
	return iter.FlatMap(index.indices(indexer), iter.FromSet[Name])
}

func replaceBetterIndex[Subj any, Name comparable](
	subj Subj,
	best *queryIndex[Subj, Name],
	next queryIndex[Subj, Name],
) {
	if (*best).Cardinality(subj) > next.Cardinality(subj) {
		*best = next
	}
}
