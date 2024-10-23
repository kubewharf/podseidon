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

package matchable

import (
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex/canonical"
	"github.com/kubewharf/podseidon/util/labelindex/settrie"
	"github.com/kubewharf/podseidon/util/util"
)

// A Matchable indicates either the existence of a key or a kv pair, used as the SetTrie alphabet.
type Matchable struct {
	key    string
	exists bool
	value  string
}

func (lhs Matchable) Less(rhs Matchable) bool {
	if lhs.key != rhs.key {
		return lhs.key < rhs.key
	}

	if lhs.exists != rhs.exists {
		return !lhs.exists
	}

	return lhs.value != rhs.value
}

func ForSet(set canonical.Set) iter.Iter[Matchable] {
	return iter.FlatMap(iter.FromSlice(set), func(pair canonical.KvPair) iter.Iter[Matchable] {
		return iter.FromSlice([]Matchable{
			{key: pair.Key, exists: true, value: util.Zero[string]()},
			{key: pair.Key, exists: false, value: pair.Value},
		})
	})
}

func WordsForSelector(selector canonical.Selector) iter.Iter[settrie.Word[Matchable]] {
	return func(yield iter.Yield[settrie.Word[Matchable]]) iter.Flow {
		return recurseMatchableWordsForSelector(
			selector,
			make([]Matchable, 0, len(selector.KeyExist)+len(selector.KeyIn)),
			yield,
		)
	}
}

func recurseMatchableWordsForSelector(
	selector canonical.Selector,
	baseWord []Matchable,
	yield iter.Yield[settrie.Word[Matchable]],
) iter.Flow {
	if len(selector.KeyExist) > 0 {
		popped := selector.KeyExist[0]
		selector.KeyExist = selector.KeyExist[1:]

		return recurseMatchableWordsForSelector(
			selector,
			append(baseWord, Matchable{
				key:    popped.Key,
				exists: true,
				value:  util.Zero[string](),
			}),
			yield,
		)
	}

	if len(selector.KeyIn) > 0 {
		popped := selector.KeyIn[0]
		selector.KeyIn = selector.KeyIn[1:]

		for _, value := range popped.Values {
			if recurseMatchableWordsForSelector(
				selector,
				append(baseWord, Matchable{
					key:    popped.Key,
					exists: false,
					value:  value,
				}),
				yield,
			).Break() {
				return iter.Break
			}
		}

		return iter.Continue
	}

	return yield(settrie.NewWord(baseWord))
}
