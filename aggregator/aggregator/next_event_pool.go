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

package aggregator

import (
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/kubewharf/podseidon/util/optional"
)

// Pool of items that need to reconcile upon receiving a new informer event.
//
// If there are admission history entries in a PodProtector cell later than the informer sync time,
// aggregator inserts them to NextEventPool,
// which is a pool of reconciliation keys to be executed when an informer is updated.
// This makes the informer sync time global &mdash;
// as long as we receive informer events for any pod,
// we can conclude that the informer stream is updated up to that point,
// which allows clearing the admission history entries older than it.
type nextEventPool struct {
	// nil value indicates empty item set.
	// non-nil value is the time since the first item push after the last drain.
	undrainedTime atomic.Pointer[time.Time]
	// nil value indicates the pool has never been drained before.
	// non-nil value is the time Drain() was last called.
	lastDrain atomic.Pointer[time.Time]

	itemsMu sync.Mutex
	items   sets.Set[types.NamespacedName]
}

func newNextEventPool() *nextEventPool {
	return &nextEventPool{
		undrainedTime: atomic.Pointer[time.Time]{},
		lastDrain:     atomic.Pointer[time.Time]{},
		itemsMu:       sync.Mutex{},
		items:         sets.New[types.NamespacedName](),
	}
}

func (pool *nextEventPool) Push(item types.NamespacedName) {
	pool.itemsMu.Lock()
	defer pool.itemsMu.Unlock()

	if pool.items.Len() == 0 {
		// actually we only want to measure the time since the last push
		pool.undrainedTime.Store(ptr.To(time.Now()))
	}

	pool.items.Insert(item)
}

func (pool *nextEventPool) Drain() (
	_objects sets.Set[types.NamespacedName],
	_objectLatency time.Duration,
	_sinceLastDrain optional.Optional[time.Duration],
) {
	pool.itemsMu.Lock()
	defer pool.itemsMu.Unlock()

	old := pool.items
	pool.items = sets.New[types.NamespacedName]()

	undrainedTime := pool.undrainedTime.Swap(nil)
	lastDrain := pool.lastDrain.Swap(ptr.To(time.Now()))

	elapsed := time.Duration(0)
	if undrainedTime != nil {
		elapsed = time.Since(*undrainedTime)
	}

	return old, elapsed, optional.Map(optional.FromPtr(lastDrain), time.Since)
}

func (pool *nextEventPool) TimeSinceLastDrain() time.Duration {
	undrainedTime := pool.undrainedTime.Load()

	if undrainedTime == nil {
		return 0
	}

	return time.Since(*undrainedTime)
}

func (pool *nextEventPool) ItemCount() int {
	// Querying item count is a cold path. Does not justify a RWMutex.
	pool.itemsMu.Lock()
	defer pool.itemsMu.Unlock()

	return pool.items.Len()
}
