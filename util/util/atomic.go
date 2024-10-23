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

package util

import "golang.org/x/exp/constraints"

type Atomic[Base any] interface {
	CompareAndSwap(oldValue, newValue Base) (swapped bool)
	Load() Base
	Store(newValue Base)
	Swap(newValue Base) (oldValue Base)
}

func AtomicMax[Base constraints.Ordered](ptr Atomic[Base], other Base) (_stored bool) {
	return AtomicExtrema(ptr, other, func(left, right Base) bool { return left < right })
}

func AtomicMin[Base constraints.Ordered](ptr Atomic[Base], other Base) (_stored bool) {
	return AtomicExtrema(ptr, other, func(left, right Base) bool { return left > right })
}

func AtomicExtrema[Base any](
	ptr Atomic[Base],
	other Base,
	rightIsBetter func(Base, Base) bool,
) (_stored bool) {
	for {
		old := ptr.Load()
		if !rightIsBetter(old, other) {
			return false
		}

		success := ptr.CompareAndSwap(old, other)
		if success {
			return true
		}
	}
}
