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

package haschange

import "golang.org/x/exp/constraints"

// A bitmask of change causes.
//
// Each bit in the bitmask denotes different causes for changing.
type Cause interface {
	constraints.Integer

	// Returns a string describing the cause.
	//
	// This method must only be called when the receiver is a power of 2.
	BitToString() string
}

// Tracks whether an object has been changed during reconciliation.
type Changed[CauseT Cause] struct {
	mask CauseT
}

func New[CauseT Cause]() Changed[CauseT] {
	return Changed[CauseT]{mask: 0}
}

func (changed *Changed[CauseT]) HasChanged() bool {
	return changed.mask != 0
}

// Updates `changed` to include all causes from `addend`.
func (changed *Changed[CauseT]) Add(addend CauseT) {
	changed.mask |= addend
}

// Assigns a new value to dst, updating `changed` if a change is detected using shallow equality `T == T`.
func Assign[CauseT Cause, T comparable](changed *Changed[CauseT], dst *T, src T, cause CauseT) {
	if *dst != src {
		*dst = src
		changed.mask |= cause
	}
}

func (changed *Changed[CauseT]) String() string {
	if changed.mask == 0 {
		return "nil"
	}

	output := ""
	maskIter := changed.mask

	for maskIter > 0 {
		bit := maskIter & -maskIter
		maskIter &= ^bit

		if output != "" {
			output += "+"
		}

		output += bit.BitToString()
	}

	return output
}
