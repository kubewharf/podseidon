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

package utilsort

import (
	"cmp"
	"sort"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/optional"
)

func ByKey[T any, K cmp.Ordered](slice []T, keyFn func(T) K) {
	sort.SliceStable(slice, func(i, j int) bool {
		return keyFn(slice[i]) < keyFn(slice[j])
	})
}

type LeftZipItem[Left any, Right any] struct {
	Left  Left
	Right optional.Optional[Right]
}

// Zip-iter two sorted slices, yielding each element of leftSlice exactly once,
// together with a rightSlice element with the same key if available.
// If the same key appears in rightSlice multiple times, only the first occurrence may get yielded.
func LeftZip[Key cmp.Ordered, Left any, Right any](
	leftSlice []Left,
	leftKeyFn func(Left) Key,
	rightSlice []Right,
	rightKeyFn func(Right) Key,
) iter.Iter[LeftZipItem[Left, Right]] {
	return func(yield iter.Yield[LeftZipItem[Left, Right]]) iter.Flow {
		for _, leftItem := range leftSlice {
			leftKey := leftKeyFn(leftItem)

			rightItem := optional.None[Right]()

			for len(rightSlice) != 0 {
				rightKey := rightKeyFn(rightSlice[0])

				if rightKey < leftKey {
					// right slice head is too small, can be shifted
					rightSlice = rightSlice[1:]
					continue
				}

				if rightKey == leftKey {
					rightItem = optional.Some(rightSlice[0])
				}

				// further items in right cannot match anyway
				break
			}

			if yield(LeftZipItem[Left, Right]{Left: leftItem, Right: rightItem}).Break() {
				return iter.Break
			}
		}

		return iter.Continue
	}
}
