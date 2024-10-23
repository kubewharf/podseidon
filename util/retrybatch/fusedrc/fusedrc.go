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

package fusedrc

import (
	"math"
	"sync/atomic"
)

// A reference counter that can be fused (deny subsequent acquisitions) when there are no references.
type Counter struct {
	// When rc == 0, the object is unfused and unused.
	// When -2^30 <= rc < 0, the object is fused.
	// When rc > 0, the object is used and cannot be fused.
	// The range between -2^31 and -2^30 is always a result of integer overflow or double free, and should lead to panic.
	rc atomic.Int32
}

func New() *Counter {
	return &Counter{rc: atomic.Int32{}}
}

// Adds a reference to the counter.
//
// Returns true if the counter can be acquired.
// Returns false if the counter has been fused.
//
// Panics if an integer overflow condition is detected.
func (counter *Counter) Acquire() bool {
	newValue := counter.rc.Add(1)

	if newValue > 0 {
		// If counter is unfused, rc >= 0, so newValue > 0.
		return true
	}

	if newValue == 0 {
		// Either another thread performed double release (panicking),
		// or 2^30 acquisition attempts took place after fuse.
		panic("too many acquire attempts after fuse")
	}

	if newValue == math.MinInt32 {
		// 2^30 successive acquire calls. At least one thread will hit this exact value.
		panic("too many concurrent acquisitions")
	}

	// All other negative values are either fused or the poisoned leftover value from another panicked call.
	return false
}

// Removes a reference from the counter.
// Must be called exactly once after Acquire.
func (counter *Counter) Release() {
	newValue := counter.rc.Add(-1)

	// It is theoretically impossible to reach the case of newValue == MaxInt32:
	// When unfused, this is only possible on a poisoned counter after a panicking overflowing Acquire.
	// When fused, this is only possible after 2^30 times o frelease-after-fuse, which is practically impossible.
	// Other non-negative values are within the normal range of unfused release.

	if newValue < 0 {
		// If unfused, this means there are more Release calls than Acquire calls.
		// If fused, the numbers of Acquire and Release calls were balanced during fuse,
		// so it is a double release nonetheless.
		panic("double release detected")
	}
}

// We select -2^30 to ensure that both subsequent Acquire and Release calls will panic.
const fusedInitValue = -1 << 30 // -2^30

// Deny all future acquisitions on this object.
//
// Returns true if success, i.e. there are no active references to the counter.
// Returns false if failure, i.e. there are active references to the counter.
func (counter *Counter) Fuse() bool {
	return counter.rc.CompareAndSwap(0, fusedInitValue)
}
