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

package optional

import (
	"fmt"

	"github.com/kubewharf/podseidon/util/util"
)

// A value that may not be present.
//
// Wrapping optional values with this struct is preferred over pointers
// for better type safety, avoid accidental mutation and self-explanatory explicitness.
//
// An Optional value is always either `Some(value)` or `None`.
// When it is `None`, the internal `value` field is always the zero value.
type Optional[T any] struct {
	value  T
	isSome bool
}

// Returns an `Optional` representing a present value.
func Some[T any](value T) Optional[T] {
	return Optional[T]{value: value, isSome: true}
}

// Returns an `Optional` representing an absent value.
func None[T any]() Optional[T] {
	return Optional[T]{value: util.Zero[T](), isSome: false}
}

// Checks whether the optional is a `Some` instead of a `None`.
func (v Optional[T]) IsSome() bool { return v.isSome }

// Checks whether the optional is a `None` instead of a `Some`.
func (v Optional[T]) IsNone() bool { return !v.isSome }

func (v Optional[T]) GoString() string {
	if v.isSome {
		return fmt.Sprintf("Some(%#v)", v.value)
	}

	return fmt.Sprintf("None[%s]()", util.TypeName[T]())
}

// Converts from `Optional` form to the (value, isPresent) form,
// which is consistent with Go syntax with map access and type cast.
func (v Optional[T]) Get() (T, bool) {
	return v.value, v.isSome
}

// Gets the value or falls back to a default value.
func (v Optional[T]) GetOr(value T) T {
	return v.GetOrFn(func() T { return value })
}

// Gets the value or falls back to the zero value.
func (v Optional[T]) GetOrZero() T {
	return v.GetOrFn(util.Zero[T])
}

// Gets the value or falls back to a lazily computed value.
func (v Optional[T]) GetOrFn(fn func() T) T {
	if v.isSome {
		return v.value
	}

	return fn()
}

// Attempt to replace the value with another lazily computed optional.
func (v Optional[T]) OrFn(fn func() Optional[T]) Optional[T] {
	if v.isSome {
		return v
	}

	return fn()
}

// Asserts that the receiver is `Some`, panics with the given justification otherwise.
//
// The message should be a justification stating why the value must exist,
// e.g. "1 + 1 should be 2".
func (v Optional[T]) MustGet(msg string) T {
	if !v.isSome {
		panic(fmt.Sprintf("value must exist: %s", msg))
	}

	return v.value
}

// Returns true only if the value is present and matches the predicate.
func (v Optional[T]) IsSomeAnd(fn func(T) bool) bool {
	return v.isSome && fn(v.value)
}

// Returns true if the value is absent or if the present value matches the predicate.
func (v Optional[T]) IsNoneOr(fn func(T) bool) bool {
	return !v.isSome || fn(v.value)
}

// Sets the receiver to `Some(value)` if it is None or the new value wins `preferRight`.
func (v *Optional[T]) SetOrChoose(value T, preferRight func(T, T) bool) {
	v.SetOrFn(value, func(base, increment T) T {
		if preferRight(base, increment) {
			return increment
		}

		return base
	})
}

// Initializes the receiver with `value` or updates it with the reduction function `fn`.
func (v *Optional[T]) SetOrFn(value T, fn func(base, increment T) T) {
	if v.isSome {
		v.value = fn(v.value, value)
	} else {
		v.value = value
		v.isSome = true
	}
}

// Returns a pointer to the value field,
// which may represent an uninitialized object.
// Never returns nil, not even when present.
func (v *Optional[T]) GetValueRef() *T {
	return &v.value
}

// Returns a pointer to the value field, ONLY IF the value is present.
func (v Optional[T]) GetValueRefOrNil() *T {
	if v.isSome {
		return &v.value
	}

	return nil
}

func Map[T any, U any](v Optional[T], fn func(T) U) Optional[U] {
	if v.isSome {
		return Some(fn(v.value))
	}

	return None[U]()
}

// Dereferences a pointer if it is non nil.
// Converts from the K8s pointer-optional convention.
func FromPtr[T any](ptr *T) Optional[T] {
	if ptr == nil {
		return None[T]()
	}

	return Some(*ptr)
}

// Returns `Some(slice[index])` if it exists, None otherwise.
func GetSlice[T any](slice []T, index int) Optional[T] {
	if index < len(slice) {
		return Some(slice[index])
	}

	return None[T]()
}

// Returns `Some(map_[key])` if `key` exists in `map_`, None otherwise.
func GetMap[K comparable, V any](map_ map[K]V, key K) Optional[V] {
	if value, exists := map_[key]; exists {
		return Some(value)
	}

	return None[V]()
}
