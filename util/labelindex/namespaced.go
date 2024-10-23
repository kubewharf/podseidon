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
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubewharf/podseidon/util/iter"
)

// Isolates each namespace with a separate Index.
type Namespaced[
	Tracked any, Query any, TrackErr any, QueryErr any,
	IndexT Index[string, Tracked, Query, TrackErr, QueryErr],
] struct {
	new        func() IndexT
	namespaces map[string]IndexT

	queryErrAdapter ErrAdapter[QueryErr]
}

func NewNamespaced[
	Tracked any, Query any, TrackErr any, QueryErr any,
	IndexT Index[string, Tracked, Query, TrackErr, QueryErr],
](
	newIndex func() IndexT,
	queryErrAdapter ErrAdapter[QueryErr],
) *Namespaced[Tracked, Query, TrackErr, QueryErr, IndexT] {
	return &Namespaced[Tracked, Query, TrackErr, QueryErr, IndexT]{
		new:             newIndex,
		namespaces:      map[string]IndexT{},
		queryErrAdapter: queryErrAdapter,
	}
}

func (index *Namespaced[Tracked, Query, TrackErr, QueryErr, IndexT]) _() Index[
	types.NamespacedName, Tracked, NamespacedQuery[Query], TrackErr, QueryErr,
] {
	return index
}

func (index *Namespaced[Tracked, Query, TrackErr, QueryErr, IndexT]) Track(
	name types.NamespacedName,
	item Tracked,
) TrackErr {
	namespaced, exists := index.namespaces[name.Namespace]
	if !exists {
		namespaced = index.new()
		index.namespaces[name.Namespace] = namespaced
	}

	return namespaced.Track(name.Name, item)
}

func (index *Namespaced[Tracked, Query, TrackErr, QueryErr, IndexT]) Untrack(
	name types.NamespacedName,
) ExistedBefore {
	namespaced, exists := index.namespaces[name.Namespace]
	if !exists {
		return ExistedBefore(false)
	}

	return namespaced.Untrack(name.Name)
}

type NamespacedQuery[T any] struct {
	Namespace string
	Query     T
}

func (index *Namespaced[Tracked, Query, TrackErr, QueryErr, IndexT]) Query(
	query NamespacedQuery[Query],
) (iter.Iter[types.NamespacedName], QueryErr) {
	namespaced, exists := index.namespaces[query.Namespace]
	if !exists {
		return iter.Empty[types.NamespacedName](), index.queryErrAdapter.Nil()
	}

	names, err := namespaced.Query(query.Query)
	if index.queryErrAdapter.IsErr(err) {
		return nil, err
	}

	return iter.Map(
		names,
		func(name string) types.NamespacedName {
			return types.NamespacedName{
				Namespace: query.Namespace,
				Name:      name,
			}
		},
	), index.queryErrAdapter.Nil()
}
