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
			type workerDataKey struct{}

			type workerDataValue struct {
				startTime  time.Time
				workerName string
			}

			type reconcileTags struct {
				Worker string
				Error  string
			}

			type queueLengthTags struct {
				Worker string
			}

			reconcileHandle := metrics.Register(
				deps.Registry(),
				"worker_reconcile",
				"Duration of a worker reconcile run.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[reconcileTags](),
			)
			queueLengthHandle := metrics.Register(
				deps.Registry(),
				"worker_queue_length",
				"Queue length of a worker.",
				metrics.IntGauge(),
				metrics.NewReflectTags[queueLengthTags](),
			)

			return Observer{
				StartReconcile: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
					return context.WithValue(ctx, workerDataKey{}, workerDataValue{
						startTime:  time.Now(),
						workerName: arg.WorkerName,
					}), util.NoOp
				},
				EndReconcile: func(ctx context.Context, arg EndReconcile) {
					data := ctx.Value(workerDataKey{}).(workerDataValue)

					duration := time.Since(data.startTime)

					reconcileHandle.Emit(duration, reconcileTags{
						Worker: data.workerName,
						Error:  errors.SerializeTags(arg.Err),
					})
				},
				QueueLength: func(ctx context.Context, arg QueueLength, getter func() int) {
					metrics.Repeating(ctx, deps, queueLengthHandle.With(queueLengthTags{Worker: arg.WorkerName}), getter)
				},
			}
		},
	)
}
