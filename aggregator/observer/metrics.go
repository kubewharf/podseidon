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

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	"github.com/kubewharf/podseidon/util/util"
)

func ProvideMetrics() component.Declared[Observer] {
	return o11y.Provide(
		metrics.MakeObserverDeps,
		func(deps metrics.ObserverDeps) Observer {
			type reconcileTags struct {
				Namespace string

				Error string
			}

			type enqueueTags struct {
				Namespace string
				Kind      string
			}

			type enqueueCtxKey struct{}

			type enqueueCtxValue struct {
				kind  string
				start time.Time
			}

			type reconcileStartTime struct{}

			type updateTriggerTags struct {
				Event string
				Error string
			}

			type namespaceCtxKey struct{}

			reconcileHandle := metrics.Register(
				deps.Registry(),
				"aggregator_reconcile",
				"Duration of an aggregator reconcile run for a PodProtector.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[reconcileTags](),
			)

			enqueueHandle := metrics.Register(
				deps.Registry(),
				"aggregator_enqueue",
				"Duration of an aggregator pod informer event handler run.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[enqueueTags](),
			)

			indexErrorHandle := metrics.Register(
				deps.Registry(),
				"aggregator_index_error",
				"Number of error events during pod informer event handling.",
				metrics.IntCounter(),
				metrics.NewReflectTags[reconcileTags](),
			)

			nextEventPoolCurrentSizeHandle := metrics.Register(
				deps.Registry(),
				"aggregator_next_event_pool_current_size",
				"Current size of the aggregator NextEventPool (during sample).",
				metrics.IntGauge(),
				metrics.NewReflectTags[util.Empty](),
			).With(util.Empty{})
			nextEventPoolCurrentLatencyHandle := metrics.Register(
				deps.Registry(),
				"aggregator_next_event_pool_current_latency",
				"Time since the oldest current object in the NextEventPool (during sample).",
				metrics.DurationGauge(),
				metrics.NewReflectTags[util.Empty](),
			).With(util.Empty{})
			nextEventPoolDrainSize := metrics.Register(
				deps.Registry(),
				"aggregator_next_event_pool_drain_count",
				"Number of items in NextEventPool each time it is drained.",
				metrics.ExponentialIntHistogram(16),
				metrics.NewReflectTags[util.Empty](),
			)
			nextEventPoolDrainPeriod := metrics.Register(
				deps.Registry(),
				"aggregator_next_event_pool_drain_period",
				"Period between NextEventPool drains.",
				metrics.AsyncLatencyDurationHistogram(),
				metrics.NewReflectTags[util.Empty](),
			)
			nextEventPoolDrainObjectLatency := metrics.Register(
				deps.Registry(),
				"aggregator_next_event_pool_drain_object_latency",
				"Time since the oldest object in NextEventPool during each drain.",
				metrics.AsyncLatencyDurationHistogram(),
				metrics.NewReflectTags[util.Empty](),
			)

			updateTriggerHandle := metrics.Register(
				deps.Registry(),
				"aggregator_update_trigger_spin",
				"A spin of aggregator update-trigger controller",
				metrics.IntCounter(),
				metrics.NewReflectTags[updateTriggerTags](),
			)

			return Observer{
				StartReconcile: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
					ctx = context.WithValue(ctx, reconcileStartTime{}, time.Now())
					ctx = context.WithValue(ctx, namespaceCtxKey{}, arg.Namespace)
					return ctx, util.NoOp
				},
				EndReconcile: func(ctx context.Context, arg EndReconcile) {
					duration := time.Since(ctx.Value(reconcileStartTime{}).(time.Time))

					reconcileHandle.Emit(duration, reconcileTags{
						Namespace: ctx.Value(namespaceCtxKey{}).(string),
						Error:     errors.SerializeTags(arg.Err),
					})
				},
				StartEnqueue: func(ctx context.Context, arg StartEnqueue) (context.Context, context.CancelFunc) {
					ctx = context.WithValue(
						ctx,
						enqueueCtxKey{},
						enqueueCtxValue{
							kind:  arg.Kind,
							start: time.Now(),
						},
					)
					ctx = context.WithValue(ctx, namespaceCtxKey{}, arg.Namespace)

					return ctx, util.NoOp
				},
				EndEnqueue: func(ctx context.Context, _ EndEnqueue) {
					ctxValue := ctx.Value(enqueueCtxKey{}).(enqueueCtxValue)
					duration := time.Since(ctxValue.start)

					enqueueHandle.Emit(duration, enqueueTags{
						Namespace: ctx.Value(namespaceCtxKey{}).(string),
						Kind:      ctxValue.kind,
					})
				},
				EnqueueError: func(ctx context.Context, arg EnqueueError) {
					indexErrorHandle.Emit(1, reconcileTags{
						Namespace: ctx.Value(namespaceCtxKey{}).(string),
						Error:     errors.SerializeTags(arg.Err),
					})
				},
				Aggregated: func(_ context.Context, _ Aggregated) {},
				NextEventPoolCurrentSize: func(ctx context.Context, _ util.Empty, getter func() int) {
					metrics.Repeating(ctx, deps, nextEventPoolCurrentSizeHandle, getter)
				},
				NextEventPoolCurrentLatency: func(ctx context.Context, _ util.Empty, getter func() time.Duration) {
					metrics.Repeating(
						ctx,
						deps,
						nextEventPoolCurrentLatencyHandle,
						getter,
					)
				},
				NextEventPoolSingleDrain: func(_ context.Context, arg NextEventPoolSingleDrain) {
					nextEventPoolDrainSize.Emit(arg.Size, util.Empty{})
					nextEventPoolDrainObjectLatency.Emit(arg.ObjectLatency, util.Empty{})
					if timeSinceLastDrain, present := arg.TimeSinceLastDrain.Get(); present {
						nextEventPoolDrainPeriod.Emit(timeSinceLastDrain, util.Empty{})
					}
				},
				TriggerPodCreate: func(_ context.Context, arg TriggerPodCreate) {
					updateTriggerHandle.Emit(1, updateTriggerTags{
						Event: "create",
						Error: errors.SerializeTags(arg.Err),
					})
				},
				TriggerPodUpdate: func(_ context.Context, arg TriggerPodUpdate) {
					updateTriggerHandle.Emit(1, updateTriggerTags{
						Event: "update",
						Error: errors.SerializeTags(arg.Err),
					})
				},
			}
		},
	)
}
