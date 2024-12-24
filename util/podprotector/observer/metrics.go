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

package observer

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	"github.com/kubewharf/podseidon/util/util"
)

func ProvideInformerMetrics() component.Declared[IndexedInformerObserver] {
	return o11y.Provide(
		metrics.MakeObserverDeps,
		func(deps metrics.ObserverDeps) IndexedInformerObserver {
			type informerEventCtxKey struct{}

			type informerEventCtxValue struct {
				start time.Time
			}

			type informerEventTags struct{}

			type informerEventErrorTags struct {
				Error string
			}

			type sourceListEventErrorTags struct {
				Error string
			}

			informerEventHandle := metrics.Register(
				deps.Registry(),
				"ppr_enqueue",
				"Informer event handler for PodProtector, including SetTrie maintenance.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[informerEventTags](),
			)

			indexErrorHandle := metrics.Register(
				deps.Registry(),
				"ppr_index_error",
				"Error events during PodProtector informer event handling.",
				metrics.IntCounter(),
				metrics.NewReflectTags[informerEventErrorTags](),
			)

			sourceListLengthHandle := metrics.Register(
				deps.Registry(),
				"ppr_source_list_length",
				"Number of PodProtector source clusters",
				metrics.IntGauge(),
				metrics.NewReflectTags[util.Empty](),
			)

			sourceListErrorHandle := metrics.Register(
				deps.Registry(),
				"ppr_source_list_error",
				"Error events from watching SourceProvider",
				metrics.IntCounter(),
				metrics.NewReflectTags[sourceListEventErrorTags](),
			)

			return IndexedInformerObserver{
				StartHandleEvent: func(ctx context.Context, _ types.NamespacedName) (context.Context, context.CancelFunc) {
					ctx = context.WithValue(
						ctx,
						informerEventCtxKey{},
						informerEventCtxValue{
							start: time.Now(),
						},
					)

					return ctx, util.NoOp
				},
				EndHandleEvent: func(ctx context.Context, _ util.Empty) {
					ctxValue := ctx.Value(informerEventCtxKey{}).(informerEventCtxValue)
					duration := time.Since(ctxValue.start)

					informerEventHandle.Emit(duration, informerEventTags{})
				},
				HandleEventError: func(_ context.Context, arg HandleEventError) {
					indexErrorHandle.Emit(1, informerEventErrorTags{
						Error: errors.SerializeTags(arg.Err),
					})
				},
				UpdateSourceList: func(_ context.Context, arg UpdateSourceList) {
					sourceListLengthHandle.Emit(arg.NewLength, util.Empty{})
				},
				UpdateSourceListError: func(_ context.Context, arg UpdateSourceListError) {
					sourceListErrorHandle.Emit(1, sourceListEventErrorTags{
						Error: errors.SerializeTags(arg.Err),
					})
				},
			}
		},
	)
}
