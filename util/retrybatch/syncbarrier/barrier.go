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

package syncbarrier

import (
	"time"

	"github.com/kubewharf/podseidon/util/util"
)

// An internal interface to insert synchronization barriers to the operations of a retrybatch.Pool.
//
// Should not be used externally.
type Interface[Key any] interface {
	util.BlockNotifier

	TimeAfter(duration time.Duration) <-chan time.Time

	StartCheckCanceled(key Key)
	FinishCheckCanceled(key Key)

	StartDispatchResult(key Key)
	FinishDispatchResult(key Key)

	PrepareFuseAfterIdle(key Key)
	AfterFuse(key Key, success bool)

	BeforeHandleAcquire(key Key)
	AfterHandleAcquire(key Key)
}

type Empty[Key any] struct{}

func (Empty[Key]) TimeAfter(duration time.Duration) <-chan time.Time { return time.After(duration) }

func (Empty[Key]) OnBlock()   {}
func (Empty[Key]) OnUnblock() {}

func (Empty[Key]) StartCheckCanceled(Key)  {}
func (Empty[Key]) FinishCheckCanceled(Key) {}

func (Empty[Key]) StartDispatchResult(Key)  {}
func (Empty[Key]) FinishDispatchResult(Key) {}

func (Empty[Key]) PrepareFuseAfterIdle(Key) {}
func (Empty[Key]) AfterFuse(Key, bool)      {}

func (Empty[Key]) BeforeHandleAcquire(Key) {}
func (Empty[Key]) AfterHandleAcquire(Key)  {}
