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

package labelindex

import (
	"sync"

	"github.com/kubewharf/podseidon/util/iter"
)

// An Index wrapper that protects read/write operations with a RWMutex.
type Locked[
	Name any, Tracked any, Query any, TrackErr any, QueryErr any,
	IndexT Index[Name, Tracked, Query, TrackErr, QueryErr],
] struct {
	lock            sync.RWMutex
	index           IndexT
	queryErrAdapter ErrAdapter[QueryErr]
}

func NewLocked[
	Name any, Tracked any, Query any, TrackErr any, QueryErr any,
	IndexT Index[Name, Tracked, Query, TrackErr, QueryErr],
](index IndexT,
	queryErrAdapter ErrAdapter[QueryErr],
) *Locked[Name, Tracked, Query, TrackErr, QueryErr, IndexT] {
	return &Locked[Name, Tracked, Query, TrackErr, QueryErr, IndexT]{
		lock:            sync.RWMutex{},
		index:           index,
		queryErrAdapter: queryErrAdapter,
	}
}

func (index *Locked[Name, Tracked, Query, TrackErr, QueryErr, IndexT]) Track(
	name Name,
	item Tracked,
) TrackErr {
	index.lock.Lock()
	defer index.lock.Unlock()

	return index.index.Track(name, item)
}

func (index *Locked[Name, Tracked, Query, TrackErr, QueryErr, IndexT]) Untrack(
	name Name,
) ExistedBefore {
	index.lock.Lock()
	defer index.lock.Unlock()

	return index.index.Untrack(name)
}

// Note that the index is read-locked until the iterator is fully exhausted.
// If the loop is time-consuming, consider collecting the result to a slice first.
func (index *Locked[Name, Tracked, Query, TrackErr, QueryErr, IndexT]) Query(
	query Query,
) (iter.Iter[Name], QueryErr) {
	index.lock.RLock()
	// do not unlock index.lock if iter is returned, until the iter is exhausted

	names, err := index.index.Query(query)
	if index.queryErrAdapter.IsErr(err) {
		index.lock.RUnlock()
		return nil, err
	}

	return names.Defer(index.lock.RUnlock), index.queryErrAdapter.Nil()
}
