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
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex/mm"
	"github.com/kubewharf/podseidon/util/optional"
	utilsort "github.com/kubewharf/podseidon/util/sort"
)

type Set []KvPair

type KvPair struct {
	Key   string
	Value string
}

type SetStore[Name comparable] struct {
	sets map[Name]Set
}

func NewSetStore[Name comparable]() *SetStore[Name] {
	return &SetStore[Name]{
		sets: map[Name]Set{},
	}
}

func CreateSet(rawSet map[string]string) Set {
	set := make(Set, 0, len(rawSet))

	for key, value := range rawSet {
		set = append(set, KvPair{Key: key, Value: value})
	}

	utilsort.ByKey(set, func(pair KvPair) string { return pair.Key })

	return set
}

func (store *SetStore[Name]) Insert(
	name Name,
	stringMap map[string]string,
) (Set, mm.ExistedBefore) {
	set := CreateSet(stringMap)

	_, hadSet := store.sets[name]
	store.sets[name] = set

	return set, mm.ExistedBefore(hadSet)
}

func (store *SetStore[Name]) Remove(name Name) (Set, mm.ExistedBefore) {
	set, hasSet := store.sets[name]
	if !hasSet {
		return nil, mm.ExistedBefore(false)
	}

	delete(store.sets, name)

	return set, mm.ExistedBefore(true)
}

func (store *SetStore[Name]) Get(name Name) optional.Optional[Set] {
	return optional.GetMap(store.sets, name)
}

func (store *SetStore[Name]) Len() int {
	return len(store.sets)
}

func (store *SetStore[Name]) Iter() iter.Iter[iter.Pair[Name, Set]] {
	return iter.MapKvs(store.sets)
}
