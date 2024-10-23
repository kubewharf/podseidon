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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex/canonical"
	"github.com/kubewharf/podseidon/util/labelindex/matchable"
	"github.com/kubewharf/podseidon/util/labelindex/settrie"
	"github.com/kubewharf/podseidon/util/util"
)

// A selector must not select a label set if the selector contains some positive requirement that does not intersect with the set.
// For example, if one of the constraints of a selector is (foo = bar) and the set does not contain such a KvPair,
// we can immediately reject this selector from our range of search candidates.
// Thus, any selector that contains at least one positive requirement may be indexed by SetTrie for subset testing,
// where we treat each positive requirement as an alphabet and
// only scan selectors that only contain keys (for KeyExist) and kv pairs (for KeyIn) in the set.
// Odd selectors (empty selectors and selectors with only negative requirements) would result in empty sets and thus are always tested.
// For KeyIn requirements with multiple values, we generate all possible permutations for simplicity
// (TODO limit the number of permutations this to avoid cardinality explosion attack).

// Stores a dictionary of label selectors.
type Selectors[Name comparable] struct {
	knownSelectors *canonical.SelectorStore[Name]

	trie *settrie.Multi[matchable.Matchable, Name, util.Empty]
}

func _[Name comparable](
	selectors *Selectors[Name],
) Index[Name, metav1.LabelSelector, map[string]string, error, util.Empty] {
	return selectors
}

func NewSelectors[Name comparable]() *Selectors[Name] {
	return &Selectors[Name]{
		knownSelectors: canonical.NewSelectorStore[Name](),
		trie:           settrie.NewMulti[matchable.Matchable, Name, util.Empty](),
	}
}

// Start tracking a selector or update an existing one with the same name.
// The selector object must be immutable.
func (indexer *Selectors[Name]) Track(name Name, rawSelector metav1.LabelSelector) error {
	indexer.doUntrackSelector(name)

	err := indexer.doTrackSelector(name, rawSelector)
	if err != nil {
		return err
	}

	return nil
}

// Stop tracking a selector if it exists.
func (indexer *Selectors[Name]) Untrack(name Name) ExistedBefore {
	return indexer.doUntrackSelector(name)
}

func (indexer *Selectors[Name]) doTrackSelector(
	name Name,
	rawSelector metav1.LabelSelector,
) error {
	result, err := indexer.knownSelectors.Insert(name, rawSelector)
	if err != nil {
		return errors.TagWrapf(
			"CanonicalizeSelector",
			err,
			"canonicalize label selector",
		)
	}

	if result.ExistedBefore {
		panic("doTrack must not be called before untracking")
	}

	matchable.WordsForSelector(result.Selector)(
		func(word settrie.Word[matchable.Matchable]) iter.Flow {
			existed := indexer.trie.Insert(word, name, util.Empty{})
			if existed {
				panic("inconsistent index contains untracked name")
			}

			return iter.Continue
		},
	)

	return nil
}

func (indexer *Selectors[Name]) doUntrackSelector(name Name) ExistedBefore {
	selector, hasSelector := indexer.knownSelectors.Remove(name).Get()
	if !hasSelector {
		return ExistedBefore(false)
	}

	matchable.WordsForSelector(selector)(
		func(word settrie.Word[matchable.Matchable]) iter.Flow {
			existed := indexer.trie.Remove(word, name)
			if !existed {
				panic(
					"inconsistent index does not contain tracked name corresponding to generated matchable word",
				)
			}

			return iter.Continue
		},
	)

	return ExistedBefore(true)
}

func (indexer *Selectors[Name]) Query(rawSet map[string]string) (iter.Iter[Name], util.Empty) {
	set := canonical.CreateSet(rawSet)

	return indexer.doQuerySelector(set), util.Empty{}
}

func (indexer *Selectors[Name]) doQuerySelector(labelSet canonical.Set) iter.Iter[Name] {
	return iter.Deduplicate(
		iter.Map(
			indexer.trie.Subsets(settrie.NewWord(matchable.ForSet(labelSet).CollectSlice())),
			func(pair iter.Pair[Name, util.Empty]) Name { return pair.Left },
		).Filter(
			func(name Name) bool {
				return indexer.knownSelectors.Get(name).
					MustGet("indexed name does not exist in selector store").
					Matches(labelSet)
			},
		),
	)
}
