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

// Utils to complement the standard library.
package util

func MapSlice[T any, U any](input []T, fn func(T) U) []U {
	output := make([]U, len(input))

	for i, item := range input {
		output[i] = fn(item)
	}

	return output
}

func CountSlice[T any](input []T, predicate func(T) bool) int {
	count := 0

	for _, item := range input {
		if predicate(item) {
			count++
		}
	}

	return count
}

func SliceToMap[K comparable, V any](input []V, keyFn func(V) K) map[K]V {
	output := make(map[K]V, len(input))

	for _, item := range input {
		output[keyFn(item)] = item
	}

	return output
}

// Returns the first occurrence of needle in input, or -1 if not found.
func FindInSlice[T comparable](input []T, needle T) int {
	return FindInSliceWith(input, func(other T) bool { return needle == other })
}

func SliceContains[T comparable](input []T, needle T) bool {
	return FindInSlice(input, needle) != -1
}

// Returns the first occurrence in input where predicate returns true, or -1 if not found.
func FindInSliceWith[T any](input []T, predicate func(T) bool) int {
	for i, item := range input {
		if predicate(item) {
			return i
		}
	}

	return -1
}

// Removes the item at index i from input, moving the last item to its original position.
func SwapRemove[T any](input *[]T, i int) {
	(*input)[i] = (*input)[len(*input)-1]
	*input = (*input)[:len(*input)-1]
}

// Removes items in the slice that do not match the predicate, preserving order of other items.
func DrainSliceOrdered[T any](slice *[]T, predicate func(T) bool) {
	writer, reader := 0, 0
	for reader < len(*slice) {
		if predicate((*slice)[reader]) {
			(*slice)[writer] = (*slice)[reader]
			writer++
		}

		reader++
	}

	for i := 0; i < len(*slice); {
		if predicate((*slice)[i]) {
			i++
		} else {
			SwapRemove(slice, i)
		}
	}

	*slice = (*slice)[:writer]
}

// Swap-removes items in the slice that do not match the predicate.
func DrainSliceUnordered[T any](slice *[]T, predicate func(T) bool) {
	for i := 0; i < len(*slice); {
		if predicate((*slice)[i]) {
			i++
		} else {
			SwapRemove(slice, i)
		}
	}
}

// Similar to append(), but always reallocates exactly one new slice,
// and never mutates the buffer behind `immutable` even if there is remaining capacity.
func AppendSliceCopy[T any](immutable []T, suffix ...T) []T {
	mutable := make([]T, len(immutable)+len(suffix))

	copy(mutable, immutable)
	copy(mutable[len(immutable):], suffix)

	return mutable
}

// Returns a pointer to a slice entry satisfying the condition,
// appending a new zero entry if none satisfies.
func GetOrAppend[T any](slice *[]T, predicate func(*T) bool) *T {
	return GetOrAppendSliceWith(slice, predicate, Zero[T])
}

// Similar to GetOrAppend, but appends an entry from the ctor function.
func GetOrAppendSliceWith[T any](slice *[]T, predicate func(*T) bool, ctor func() T) *T {
	for i := range *slice {
		item := &(*slice)[i]
		if predicate(item) {
			return item
		}
	}

	*slice = append(*slice, ctor())

	return &(*slice)[len(*slice)-1]
}

// Clears the slice without deallocating its capacity, freeing any possible underlying pointers for GC.
func ClearSlice[T any](slice *[]T) {
	for i := range *slice {
		(*slice)[i] = Zero[T]()
	}

	*slice = (*slice)[:0]
}

// Execute `eachFn` on each item of `slice` by reference.
func ForEachRef[T any](slice []T, eachFn func(*T)) {
	for i := range slice {
		eachFn(&slice[i])
	}
}
