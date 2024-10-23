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
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/labelindex/mm"
	"github.com/kubewharf/podseidon/util/util"
)

type ExistedBefore = mm.ExistedBefore

type Index[Name any, Tracked any, Query any, TrackErr any, QueryErr any] interface {
	// Starts tracking an item.
	// The state of the index is the same as before calling the method if an error is returned.
	Track(name Name, item Tracked) TrackErr

	// Stops tracking an item.
	// Returns true if the item was not tracked before.
	Untrack(name Name) ExistedBefore

	// Returns all tracked items consistent with the query.
	// Read-only operation.
	Query(query Query) (iter.Iter[Name], QueryErr)
}

type ErrAdapter[Err any] interface {
	IsErr(err Err) bool

	Nil() Err
}

type EmptyErrAdapter struct{}

func (EmptyErrAdapter) IsErr(_ util.Empty) bool { return false }

func (EmptyErrAdapter) Nil() util.Empty { return util.Empty{} }

type ErrorErrAdapter struct{}

func (ErrorErrAdapter) IsErr(err error) bool { return err != nil }

func (ErrorErrAdapter) Nil() error { return nil }
