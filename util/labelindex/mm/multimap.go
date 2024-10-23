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

// MultiMap types.
package mm

import "k8s.io/apimachinery/pkg/util/sets"

// Like a map[K]V, but allows mapping a single key to multiple distinct values.
type MultiMap[K comparable, V comparable] map[K]sets.Set[V]

type ExistedBefore bool

func (mm MultiMap[K, V]) Insert(key K, value V) ExistedBefore {
	set, exists := mm[key]
	if !exists {
		set = sets.New[V]()
		mm[key] = set
	}

	hadValue := set.Has(value)
	set.Insert(value)

	return ExistedBefore(hadValue)
}

func (mm MultiMap[K, V]) Remove(key K, value V) ExistedBefore {
	set, exists := mm[key]
	if !exists {
		return ExistedBefore(false)
	}

	hadValue := set.Has(value)
	if !hadValue {
		return ExistedBefore(false)
	}

	set.Delete(value)

	if set.Len() == 0 {
		delete(mm, key)
	}

	return ExistedBefore(true)
}
