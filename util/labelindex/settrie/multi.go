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

package settrie

import (
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/optional"
)

// A multimap that stores multiple items in each SetTrie node, analogous to map[Word]map[Subkey]Data.
type Multi[Alpha Ordered[Alpha], Subkey comparable, Data any] struct {
	trie *SetTrie[Alpha, map[Subkey]Data]
}

func NewMulti[Alpha Ordered[Alpha], Subkey comparable, Data any]() *Multi[Alpha, Subkey, Data] {
	return &Multi[Alpha, Subkey, Data]{
		trie: New[Alpha, map[Subkey]Data](),
	}
}

type ExistedBefore bool

func (multi *Multi[Alphabet, Subkey, Data]) Insert(
	word Word[Alphabet],
	subkey Subkey,
	data Data,
) ExistedBefore {
	var output ExistedBefore

	multi.trie.CreateOrUpdate(word, func() map[Subkey]Data {
		output = ExistedBefore(false)
		return map[Subkey]Data{subkey: data}
	}, func(map_ *map[Subkey]Data) {
		_, existed := (*map_)[subkey]
		output = ExistedBefore(existed)

		(*map_)[subkey] = data
	})

	return output
}

func (multi *Multi[Alphabet, Subkey, Data]) Remove(
	word Word[Alphabet],
	subkey Subkey,
) ExistedBefore {
	var output ExistedBefore

	multi.trie.RetainOrDrop(word, func(map_ map[Subkey]Data) optional.Optional[map[Subkey]Data] {
		_, existed := map_[subkey]
		output = ExistedBefore(existed)

		delete(map_, subkey)

		if len(map_) == 0 {
			return optional.None[map[Subkey]Data]()
		}

		return optional.Some(map_)
	})

	return output
}

func (multi *Multi[Alphabet, Subkey, Data]) Subsets(
	word Word[Alphabet],
) iter.Iter[iter.Pair[Subkey, Data]] {
	return iter.FlatMap(
		multi.trie.Subsets(word),
		func(pair iter.Pair[Word[Alphabet], map[Subkey]Data]) iter.Iter[iter.Pair[Subkey, Data]] {
			return iter.MapKvs(pair.Right)
		},
	)
}
