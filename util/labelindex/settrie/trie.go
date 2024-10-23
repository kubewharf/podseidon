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

// Implements a modified version of SetTrie that acts like a Map[Word, Data].
// Reference: Savnik, Iztok. "Efficient subset and superset queries." DB&Local Proceedings. 2012.
package settrie

import (
	"sort"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/optional"
)

type Ordered[Self any] interface {
	comparable
	Less(other Self) bool
}

type Word[Alpha any] struct {
	sorted []Alpha
}

// Creates a new word from an unsorted slice. The input slice is not mutated nor leaked.
func NewWord[Alpha Ordered[Alpha]](unsorted []Alpha) Word[Alpha] {
	sorted := make([]Alpha, len(unsorted))
	copy(sorted, unsorted)

	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Less(sorted[j]) })

	return Word[Alpha]{sorted: sorted}
}

func EqualsWord[Alpha comparable](left, right Word[Alpha]) bool {
	return iter.All(
		iter.Map(
			iter.Zip(left.sorted, right.sorted),
			func(pair iter.Pair[optional.Optional[Alpha], optional.Optional[Alpha]]) bool {
				return pair.Left == pair.Right
			},
		),
	)
}

func (word Word[Alpha]) Sorted() []Alpha {
	return word.sorted
}

// Non-mutating function that might mutate underlying shared capacity beyond length, similar to append().
func (word Word[Alpha]) Append(alpha Alpha) Word[Alpha] {
	return Word[Alpha]{
		sorted: append(word.sorted, alpha),
	}
}

func (word *Word[Alpha]) PopFirst() (Alpha, bool) {
	if len(word.sorted) == 0 {
		return [1]Alpha{}[0], false
	}

	ret := word.sorted[0]
	word.sorted = word.sorted[1:]

	return ret, true
}

type trieNode[Alpha comparable, Data any] struct {
	children map[Alpha]*trieNode[Alpha, Data]

	// If the current node is the tail of a word, this field contains the data associated with that word.
	data optional.Optional[Data]
}

func newNode[Alpha comparable, Data any]() *trieNode[Alpha, Data] {
	return &trieNode[Alpha, Data]{
		children: map[Alpha]*trieNode[Alpha, Data]{},
		data:     optional.None[Data](),
	}
}

// Returns true if there are no words at or under this node.
func (node *trieNode[Alpha, Data]) isGarbageNode() bool {
	return len(node.children) == 0 && !node.data.IsSome()
}

type SetTrie[Alpha Ordered[Alpha], Data any] struct {
	root *trieNode[Alpha, Data]

	// Only for capacity estimate in DFS to optimize allocation.
	// Does not reduce upon removal.
	maxDepth int
}

func New[Alpha Ordered[Alpha], Data any]() *SetTrie[Alpha, Data] {
	return &SetTrie[Alpha, Data]{
		root:     newNode[Alpha, Data](),
		maxDepth: 0,
	}
}

func (trie *SetTrie[Alpha, Data]) RetainOrDrop(
	word Word[Alpha],
	process func(Data) optional.Optional[Data],
) {
	cursorUnderNode(trie.root, word, true, func(data *optional.Optional[Data]) {
		if ptr := data.GetValueRefOrNil(); ptr != nil {
			newOptional := process(*ptr)
			*data = newOptional
		}
	})
}

func (trie *SetTrie[Alpha, Data]) CreateOrUpdate(
	word Word[Alpha],
	initNode func() Data,
	updateNode func(*Data),
) {
	cursorUnderNode(trie.root, word, true, func(data *optional.Optional[Data]) {
		if ptr := data.GetValueRefOrNil(); ptr != nil {
			updateNode(ptr)
		} else {
			*data = optional.Some(initNode())
		}
	})
}

func cursorUnderNode[Alpha comparable, Data any](
	node *trieNode[Alpha, Data],
	word Word[Alpha],
	createMissingNodes bool,
	editNode func(*optional.Optional[Data]),
) {
	if alpha, hasAlpha := word.PopFirst(); hasAlpha {
		child, hasChild := node.children[alpha]
		if !hasChild {
			if !createMissingNodes {
				return
			}

			child = newNode[Alpha, Data]()
			node.children[alpha] = child
		}

		cursorUnderNode(child, word, createMissingNodes, editNode)

		if child.isGarbageNode() {
			delete(node.children, alpha)
		}

		return
	}

	editNode(&node.data)
}

func (trie *SetTrie[Alpha, Data]) Get(word Word[Alpha]) optional.Optional[Data] {
	return getWordInNode(trie.root, word)
}

func getWordInNode[Alpha comparable, Data any](
	node *trieNode[Alpha, Data],
	word Word[Alpha],
) optional.Optional[Data] {
	if alpha, hasAlpha := word.PopFirst(); hasAlpha {
		child, hasChild := node.children[alpha]
		if !hasChild {
			return optional.None[Data]()
		}

		return getWordInNode(child, word)
	}

	return node.data
}

// Supersets is currently unused as it is very inefficient for tries with many children under the same node.
func (trie *SetTrie[Alpha, Data]) Supersets(
	word Word[Alpha],
) iter.Iter[iter.Pair[Word[Alpha], Data]] {
	return func(yield iter.Yield[iter.Pair[Word[Alpha], Data]]) iter.Flow {
		return supersetsInNode(
			trie.root,
			word,
			yield,
			Word[Alpha]{sorted: make([]Alpha, 0, trie.maxDepth)},
		)
	}
}

// word must be a subset of prefix.
// Since prefix is sorted, a new alphabet in prefix must be <= the next alphabet in word.
func supersetsInNode[Alpha Ordered[Alpha], Data any](
	node *trieNode[Alpha, Data],
	word Word[Alpha],
	yield iter.Yield[iter.Pair[Word[Alpha], Data]],
	prefix Word[Alpha],
) iter.Flow {
	if len(word.sorted) == 0 {
		// yield is only allowed if word is exhausted,
		// i.e. prefix is a superset of word
		if data, hasData := node.data.Get(); hasData {
			if yield(iter.NewPair(prefix, data)).Break() {
				return iter.Break
			}
		}

		// exhaustive dfs of all child nodes, since they are all supersets with a tail after word.last
		for _, child := range node.children {
			if supersetsInNode(child, word, yield, prefix).Break() {
				return iter.Break
			}
		}

		return iter.Continue
	}

	nextWordAlpha := word.sorted[0]

	for childAlpha, child := range node.children {
		if nextWordAlpha.Less(childAlpha) {
			// the prefix will not contain nextWordAlpha, so it is not a superset of word
			continue
		}

		wordForChild := word

		if childAlpha == nextWordAlpha {
			// The child does not need to check a matched alphabet
			wordForChild.sorted = wordForChild.sorted[1:]
		}

		if supersetsInNode(child, wordForChild, yield, prefix.Append(childAlpha)).Break() {
			return iter.Break
		}
	}

	return iter.Continue
}

func (trie *SetTrie[Alpha, Data]) Subsets(
	word Word[Alpha],
) iter.Iter[iter.Pair[Word[Alpha], Data]] {
	return func(yield iter.Yield[iter.Pair[Word[Alpha], Data]]) iter.Flow {
		return subsetsInNode(
			trie.root,
			word,
			yield,
			Word[Alpha]{sorted: make([]Alpha, 0, trie.maxDepth)},
		)
	}
}

// prefix must be a subset of word,
// i.e. all new alphabets in prefix must be in word too.
func subsetsInNode[Alpha Ordered[Alpha], Data any](
	node *trieNode[Alpha, Data],
	word Word[Alpha],
	yield iter.Yield[iter.Pair[Word[Alpha], Data]],
	prefix Word[Alpha],
) iter.Flow {
	// A node is visited iff prefix does not contain alphabets not in node.
	// This includes the root node, if a word exists there.
	if data, hasData := node.data.Get(); hasData {
		if yield(iter.NewPair(prefix, data)).Break() {
			return iter.Break
		}
	}

	if len(word.sorted) == 0 {
		// All child nodes have a tail behind word, thus are not subsets of word.
		return iter.Continue
	}

	// If prefix is a subset of word, the next item in prefix must be a value in word,
	// i.e. we only need to check node.children[alpha] where alpha is in word.
	for subsliceRange, childAlpha := range word.sorted {
		child, hasChild := node.children[childAlpha]
		if !hasChild {
			continue
		}

		wordForChild := Word[Alpha]{sorted: word.sorted[subsliceRange+1:]}

		if subsetsInNode(child, wordForChild, yield, prefix.Append(childAlpha)).Break() {
			return iter.Break
		}
	}

	return iter.Continue
}
