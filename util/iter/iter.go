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

// Utilities for iterator types.
//
// This package should be deprecated in favor of std or x/exp/iter
// when they are generally available without build flag constraints.
package iter

import (
	"sync/atomic"

	"golang.org/x/exp/constraints"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/podseidon/util/optional"
)

// `Continue` or `Break`.
type Flow bool

func (flow Flow) Continue() bool { return bool(flow) }

func (flow Flow) Break() bool { return !bool(flow) }

const (
	Continue = Flow(true)
	Break    = Flow(false)
)

func ContinueIf(condition bool) Flow {
	return Flow(condition)
}

type (
	// A pushing iterator that yields items of type `T`.
	Iter[T any] func(yield Yield[T]) Flow
	// Receives items of type `T` and determines whether the iterator should be interrupted.
	Yield[T any] func(T) Flow
)

func (iter Iter[T]) Filter(filterFn func(T) bool) Iter[T] {
	return func(yield Yield[T]) Flow {
		return iter(func(item T) Flow {
			if filterFn(item) {
				yield(item)
			}

			return Continue
		})
	}
}

func Deduplicate[T comparable](iter Iter[T]) Iter[T] {
	seen := sets.New[T]()

	return iter.Filter(func(value T) bool {
		if seen.Has(value) {
			return false
		}

		seen.Insert(value)

		return true
	})
}

func Map[T any, U any](iter Iter[T], mapFn func(T) U) Iter[U] {
	return func(yield Yield[U]) Flow {
		return iter(func(value T) Flow {
			return yield(mapFn(value))
		})
	}
}

func FlatMap[T any, U any](iter Iter[T], mapFn func(T) Iter[U]) Iter[U] {
	return func(yield Yield[U]) Flow {
		return iter(func(intoIter T) Flow {
			return mapFn(intoIter)(func(value U) Flow {
				return yield(value)
			})
		})
	}
}

// Folds the elements into an accumulator by repeatedly applying a fold function.
func Fold[T any, U any](iter Iter[T], initial U, foldFn func(U, T) U) U {
	state := initial

	iter(func(value T) Flow {
		state = foldFn(state, value)
		return Continue
	})

	return state
}

// Reduces the elements to a single one by repeatedly applying a pairwise reduce function.
//
// Returns `None` if and only if the iterator is empty.
func (iter Iter[T]) Reduce(reduceFn func(T, T) T) optional.Optional[T] {
	return Fold(iter, optional.None[T](), func(opt optional.Optional[T], next T) optional.Optional[T] {
		if state, isSome := opt.Get(); isSome {
			return optional.Some(reduceFn(state, next))
		}

		return optional.Some(next)
	})
}

func (iter Iter[T]) Extremum(preferRight func(T, T) bool) optional.Optional[T] {
	return Fold(iter, optional.None[T](), func(opt optional.Optional[T], next T) optional.Optional[T] {
		opt.SetOrChoose(next, preferRight)

		return opt
	})
}

func Any(iter Iter[bool]) bool {
	found := false

	iter(func(value bool) Flow {
		if value {
			found = true
			return Break
		}
		return Continue
	})

	return found
}

func All(iter Iter[bool]) bool {
	return !Any(Map(iter, func(b bool) bool { return !b }))
}

func Empty[T any]() Iter[T] {
	return func(Yield[T]) Flow {
		return Continue
	}
}

func Chain[T any](iters ...Iter[T]) Iter[T] {
	return func(yield Yield[T]) Flow {
		for _, iter := range iters {
			if iter(yield).Break() {
				return Break
			}
		}

		return Continue
	}
}

func Repeat[T any](value T, count uint64) Iter[T] {
	return func(yield Yield[T]) Flow {
		for range count {
			if yield(value).Break() {
				return Break
			}
		}

		return Continue
	}
}

func Enumerate[T any](iter Iter[T]) Iter[Pair[uint64, T]] {
	return func(yield Yield[Pair[uint64, T]]) Flow {
		counter := uint64(0)

		return iter(func(item T) Flow {
			flow := yield(NewPair(counter, item))

			counter++

			return flow
		})
	}
}

// Ensures that the iterator can only be used once.
// Attempt to reuse would result in panic.
func (iter Iter[T]) MustBeAffine(message string) Iter[T] {
	lock := new(atomic.Bool)

	return func(yield Yield[T]) Flow {
		if !lock.CompareAndSwap(false, true) {
			panic(message)
		}

		return iter(yield)
	}
}

func (iter Iter[T]) Defer(finalizer func()) Iter[T] {
	// call MustBeAffine *out of* the deferred block so that we do not defer the finalizer during the affine panic.
	return Iter[T](func(yield Yield[T]) Flow {
		defer finalizer()
		return iter(yield)
	}).MustBeAffine("Cannot invoke iterators with a finalizer multiple times")
}

func MapKeys[K comparable, V any](map_ map[K]V) Iter[K] {
	return func(yield Yield[K]) Flow {
		for key := range map_ {
			if yield(key).Break() {
				return Break
			}
		}

		return Continue
	}
}

func MapValues[K comparable, V any](map_ map[K]V) Iter[V] {
	return func(yield Yield[V]) Flow {
		for _, value := range map_ {
			if yield(value).Break() {
				return Break
			}
		}

		return Continue
	}
}

func MapKvs[K comparable, V any](map_ map[K]V) Iter[Pair[K, V]] {
	return func(yield Yield[Pair[K, V]]) Flow {
		for k, v := range map_ {
			if yield(NewPair(k, v)).Break() {
				return Break
			}
		}

		return Continue
	}
}

func Range[T constraints.Integer](start, end T) Iter[T] {
	return func(yield Yield[T]) Flow {
		for i := start; i < end; i++ {
			if yield(i).Break() {
				return Break
			}
		}

		return Continue
	}
}

func FromSlice[T any](slice []T) Iter[T] {
	return func(yield Yield[T]) Flow {
		for _, item := range slice {
			if yield(item).Break() {
				return Break
			}
		}

		return Continue
	}
}

func (iter Iter[T]) CollectSlice() []T {
	output := []T{}

	iter(func(value T) Flow {
		output = append(output, value)
		return Continue
	})

	return output
}

func (iter Iter[T]) Count() uint64 {
	output := uint64(0)

	iter(func(_ T) Flow {
		output++
		return Continue
	})

	return output
}

func FromMap[K comparable, V any](m map[K]V) Iter[Pair[K, V]] {
	return func(yield Yield[Pair[K, V]]) Flow {
		for k, v := range m {
			if yield(NewPair(k, v)).Break() {
				return Break
			}
		}

		return Continue
	}
}

// Iterate over the union of keys from two different maps.
func FromMap2[K comparable, V1 any, V2 any](
	m1 map[K]V1,
	m2 map[K]V2,
) Iter[Pair[K, Pair[optional.Optional[V1], optional.Optional[V2]]]] {
	return func(yield Yield[Pair[K, Pair[optional.Optional[V1], optional.Optional[V2]]]]) Flow {
		for k, v1 := range m1 {
			if yield(NewPair(k, NewPair(
				optional.Some(v1),
				optional.GetMap(m2, k),
			))).Break() {
				return Break
			}
		}

		for k, v2 := range m2 {
			if _, exists := m1[k]; !exists {
				if yield(NewPair(k, NewPair(
					optional.None[V1](),
					optional.Some(v2),
				))).Break() {
					return Break
				}
			}
		}

		return Continue
	}
}

func CollectMap[K comparable, V any](iter Iter[Pair[K, V]]) map[K]V {
	output := map[K]V{}

	iter(func(pair Pair[K, V]) Flow {
		output[pair.Left] = pair.Right
		return Continue
	})

	return output
}

func FromSet[T comparable](slice sets.Set[T]) Iter[T] {
	return func(yield Yield[T]) Flow {
		for item := range slice {
			if yield(item).Break() {
				return Break
			}
		}

		return Continue
	}
}

func CollectSet[T comparable](iter Iter[T]) sets.Set[T] {
	set := sets.New[T]()

	iter(func(value T) Flow {
		set.Insert(value)
		return Continue
	})

	return set
}

func (iter Iter[T]) TryForEach(eachFn func(T) error) error {
	err := error(nil)

	iter(func(value T) Flow {
		if err = eachFn(value); err != nil {
			return Break
		}

		return Continue
	})

	return err
}

func Sum[T constraints.Integer](iter Iter[T]) T {
	return Fold(iter, 0, func(sum, next T) T { return sum + next })
}

func Histogram[T comparable](iter Iter[T]) map[T]int {
	output := map[T]int{}

	iter(func(item T) Flow {
		output[item]++

		return Continue
	})

	return output
}

func FirstSome[T any](iter Iter[optional.Optional[T]]) optional.Optional[T] {
	ret := optional.None[T]()

	iter(func(opt optional.Optional[T]) Flow {
		ret = opt

		if opt.IsSome() {
			return Break
		}

		return Continue
	})

	return ret
}

// Iterate over two slices concurrently, yielding `None` on up to one side if a slice is shorter.
func Zip[Left, Right any](
	leftSlice []Left,
	rightSlice []Right,
) Iter[Pair[optional.Optional[Left], optional.Optional[Right]]] {
	return func(yield Yield[Pair[optional.Optional[Left], optional.Optional[Right]]]) Flow {
		for len(leftSlice) > 0 || len(rightSlice) > 0 {
			leftItem := optional.None[Left]()
			if len(leftSlice) > 0 {
				leftItem, leftSlice = optional.Some(leftSlice[0]), leftSlice[1:]
			}

			rightItem := optional.None[Right]()
			if len(rightSlice) > 0 {
				rightItem, rightSlice = optional.Some(rightSlice[0]), rightSlice[1:]
			}

			if yield(
				NewPair(leftItem, rightItem),
			).Break() {
				return Break
			}
		}

		return Continue
	}
}

type Pair[Left any, Right any] struct {
	Left  Left
	Right Right
}

func NewPair[Left any, Right any](left Left, right Right) Pair[Left, Right] {
	return Pair[Left, Right]{Left: left, Right: right}
}
