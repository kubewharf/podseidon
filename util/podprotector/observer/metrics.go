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
			type webhookInformerEventCtxKey struct{}

			type webhookInformerEventCtxValue struct {
				start time.Time
			}

			type webhookInformerEventTags struct{}

			type webhookInformerEventErrorTags struct {
				Error string
			}

			informerEventHandle := metrics.Register(
				deps.Registry(),
				"ppr_enqueue",
				"Informer event handler for PodProtector, including SetTrie maintenance.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[webhookInformerEventTags](),
			)

			indexErrorHandle := metrics.Register(
				deps.Registry(),
				"ppr_index_error",
				"Error events during PodProtector informer event handling.",
				metrics.IntCounter(),
				metrics.NewReflectTags[webhookInformerEventErrorTags](),
			)

			return IndexedInformerObserver{
				StartHandleEvent: func(ctx context.Context, _ types.NamespacedName) (context.Context, context.CancelFunc) {
					ctx = context.WithValue(
						ctx,
						webhookInformerEventCtxKey{},
						webhookInformerEventCtxValue{
							start: time.Now(),
						},
					)

					return ctx, util.NoOp
				},
				EndHandleEvent: func(ctx context.Context, _ util.Empty) {
					ctxValue := ctx.Value(webhookInformerEventCtxKey{}).(webhookInformerEventCtxValue)
					duration := time.Since(ctxValue.start)

					informerEventHandle.Emit(duration, webhookInformerEventTags{})
				},
				HandleEventError: func(_ context.Context, arg HandleEventError) {
					indexErrorHandle.Emit(1, webhookInformerEventErrorTags{
						Error: errors.SerializeTags(arg.Err),
					})
				},
			}
		},
	)
}
