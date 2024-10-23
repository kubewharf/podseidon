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

package settrie_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex/settrie"
	"github.com/kubewharf/podseidon/util/optional"
)

type Alpha uint32

func (lhs Alpha) Less(rhs Alpha) bool {
	return lhs < rhs
}

type Word = settrie.Word[Alpha]

func NewWord(ints ...Alpha) Word {
	return settrie.NewWord(ints)
}

// Test data from figure 1 in the cited article.
func baseTrie(t *testing.T) (*settrie.SetTrie[Alpha, string], []Word) {
	t.Helper()

	trie := settrie.New[Alpha, string]()
	containedWords := []Word{}

	for _, slice := range [][]Alpha{
		{1, 3},
		{1, 3, 5},
		{1, 4},
		{1, 2, 4},
		{2, 4},
		{2, 3, 5},
	} {
		wordString := ""

		for _, item := range slice {
			wordString += fmt.Sprint(item)
		}

		word := NewWord(slice...)

		assertCalled(t, func(called func()) {
			trie.CreateOrUpdate(word, func() string {
				called()
				return wordString
			}, func(*string) {
				assert.Fail(t, "updateNode should not be called for a new item")
			})
		})

		containedWords = append(containedWords, word)
	}

	return trie, containedWords
}

func assertEqual[T comparable](t *testing.T, expected, actual T) {
	t.Helper()
	assert.Equal(t, expected, actual)
}

func TestGet(t *testing.T) {
	t.Parallel()

	trie, _ := baseTrie(t)

	assertEqual(t, optional.Some("13"), trie.Get(NewWord(1, 3)))
	assertEqual(t, optional.Some("135"), trie.Get(NewWord(1, 3, 5)))
	assertEqual(t, optional.Some("24"), trie.Get(NewWord(2, 4)))

	assertEqual(t, optional.None[string](), trie.Get(NewWord(2, 3)))
	assertEqual(t, optional.None[string](), trie.Get(NewWord(2, 4, 5)))
	assertEqual(t, optional.None[string](), trie.Get(NewWord()))

	trie.RetainOrDrop(NewWord(2, 3), func(_ string) optional.Optional[string] {
		assert.Fail(t, "23 does not exist")
		return optional.None[string]()
	})

	assertCalled(t, func(called func()) {
		trie.RetainOrDrop(NewWord(2, 4), func(s string) optional.Optional[string] {
			called()
			assertEqual(t, "24", s)

			return optional.None[string]()
		})
	})

	assertEqual(t, optional.None[string](), trie.Get(NewWord(2, 4)))
	assertEqual(t, optional.Some("235"), trie.Get(NewWord(2, 3, 5)))

	assertCalled(t, func(called func()) {
		trie.RetainOrDrop(NewWord(1, 3), func(s string) optional.Optional[string] {
			called()
			assertEqual(t, "13", s)

			return optional.None[string]()
		})
	})

	assertEqual(t, optional.None[string](), trie.Get(NewWord(1, 3)))
	assertEqual(t, optional.Some("135"), trie.Get(NewWord(1, 3, 5)))

	trie.CreateOrUpdate(NewWord(1, 3), func() string { return "13" }, func(*string) {
		assert.Fail(t, "updateNode should not be called for a new item")
	})
	assertEqual(t, optional.Some("13"), trie.Get(NewWord(1, 3)))

	assertCalled(t, func(called func()) {
		trie.RetainOrDrop(NewWord(1, 3, 5), func(s string) optional.Optional[string] {
			called()
			assertEqual(t, "135", s)

			return optional.None[string]()
		})
	})

	assertEqual(t, optional.None[string](), trie.Get(NewWord(1, 3, 5)))
	assertEqual(t, optional.Some("13"), trie.Get(NewWord(1, 3)))
}

func TestSubset(t *testing.T) {
	t.Parallel()

	trie, containedWords := baseTrie(t)

	GenerateAllWords(1, 7)(func(word Word) {
		assertSubsets(t, word, trie, containedWords)
	})
}

func GenerateAllWords(minAlpha, maxAlpha Alpha) func(yield func(Word)) {
	return func(yield func(Word)) {
		for bits := range 1 << (maxAlpha - minAlpha) {
			wordSlice := []Alpha{}

			for offset := range maxAlpha - minAlpha {
				if (bits & (1 << offset)) != 0 {
					wordSlice = append(wordSlice, offset+minAlpha)
				}
			}

			yield(NewWord(wordSlice...))
		}
	}
}

func assertCalled(t *testing.T, run func(func())) {
	t.Helper()

	called := false

	run(func() {
		called = true
	})

	assert.True(t, called, "expect function to be called")
}

func assertSubsets(
	t *testing.T,
	word Word,
	trie *settrie.SetTrie[Alpha, string],
	containedWords []Word,
) {
	t.Helper()

	expectedWords := []Word{}

	for _, containedWord := range containedWords {
		if isSubset(containedWord, word) {
			expectedWords = append(expectedWords, containedWord)
		}
	}

	assertIterWords(t, trie.Subsets(word), expectedWords)
}

func isSubset(sub, super Word) bool {
	return sets.New(super.Sorted()...).HasAll(sub.Sorted()...)
}

func assertIterWords(t *testing.T, words iter.Iter[iter.Pair[Word, string]], expectedWords []Word) {
	t.Helper()

	foundWords := make([]bool, len(expectedWords))

	words(func(yielded iter.Pair[Word, string]) iter.Flow {
		hasExpected := false

		for expectedWordIndex, expectedWord := range expectedWords {
			if !settrie.EqualsWord(expectedWord, yielded.Left) {
				continue
			}

			if foundWords[expectedWordIndex] {
				t.Fail()
				t.Errorf("Yielded duplicate word %v", yielded.Right)
			}

			hasExpected = true
			foundWords[expectedWordIndex] = true

			break
		}

		if !hasExpected {
			t.Fail()
			t.Errorf("Unexpected iterator output %v", yielded.Right)
		}

		return iter.Continue
	})

	for i, foundWord := range foundWords {
		if !foundWord {
			t.Fail()
			t.Errorf("Expected word %v not yielded", expectedWords[i])
		}
	}
}
