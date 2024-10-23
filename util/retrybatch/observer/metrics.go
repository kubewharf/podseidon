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
			type submitDataKey struct{}

			type submitDataValue struct {
				startTime time.Time
				poolName  string
			}

			type submitTags struct {
				Pool  string
				Error string
			}

			type batchDataKey struct{}

			type batchDataValue struct {
				startTime time.Time
				poolName  string
			}

			type batchTags struct {
				Pool string
			}

			type executeDataKey struct{}

			type executeDataValue struct {
				startTime time.Time
				poolName  string
				batchSize int
			}

			type executeTags struct {
				Pool   string
				Result string
				Error  string
			}

			type poolTags struct {
				Pool string
			}

			submitHandle := metrics.Register(
				deps.Registry(),
				"retrybatch_submit",
				"Duration of a retrybatch submission.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[submitTags](),
			)

			submitRetryCountHandle := metrics.Register(
				deps.Registry(),
				"retrybatch_submit_retry_count",
				"Number of times each retrybatch submission is retried.",
				metrics.ExponentialIntHistogram(5),
				metrics.NewReflectTags[submitTags](),
			)

			batchHandle := metrics.Register(
				deps.Registry(),
				"retrybatch_batch",
				"Duration of a retrybatch batch.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[batchTags](),
			)

			batchRetryCountHandle := metrics.Register(
				deps.Registry(),
				"retrybatch_batch_retry_count",
				"Number of times each retrybatch batch is retried.",
				metrics.ExponentialIntHistogram(5),
				metrics.NewReflectTags[batchTags](),
			)

			executeHandle := metrics.Register(
				deps.Registry(),
				"retrybatch_execute",
				"Duration of a retrybatch execution.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[executeTags](),
			)

			executeBatchSizeHandle := metrics.Register(
				deps.Registry(),
				"retrybatch_execute_batch_size",
				"Number of batched items in a retrybatch execution.",
				metrics.ExponentialIntHistogram(16),
				metrics.NewReflectTags[executeTags](),
			)

			inFlightKeysHandle := metrics.Register(
				deps.Registry(),
				"retrybatch_inflight_keys",
				"Number of keys currently waiting for retries.",
				metrics.IntGauge(),
				metrics.NewReflectTags[poolTags](),
			)

			return Observer{
				StartSubmit: func(ctx context.Context, arg StartSubmit) (context.Context, context.CancelFunc) {
					return context.WithValue(ctx, submitDataKey{}, submitDataValue{
						startTime: time.Now(),
						poolName:  arg.PoolName,
					}), util.NoOp
				},
				EndSubmit: func(ctx context.Context, arg EndSubmit) {
					data := ctx.Value(submitDataKey{}).(submitDataValue)
					duration := time.Since(data.startTime)
					tags := submitTags{
						Pool:  data.poolName,
						Error: errors.SerializeTags(arg.Err),
					}

					submitHandle.Emit(duration, tags)
					submitRetryCountHandle.Emit(arg.RetryCount, tags)
				},
				StartBatch: func(ctx context.Context, arg StartBatch) (context.Context, context.CancelFunc) {
					return context.WithValue(ctx, batchDataKey{}, batchDataValue{
						startTime: time.Now(),
						poolName:  arg.PoolName,
					}), util.NoOp
				},
				EndBatch: func(ctx context.Context, arg EndBatch) {
					data := ctx.Value(batchDataKey{}).(batchDataValue)
					duration := time.Since(data.startTime)
					tags := batchTags{
						Pool: data.poolName,
					}

					batchHandle.Emit(duration, tags)
					batchRetryCountHandle.Emit(arg.RetryCount, tags)
				},
				StartExecute: func(ctx context.Context, arg StartExecute) (context.Context, context.CancelFunc) {
					return context.WithValue(ctx, executeDataKey{}, executeDataValue{
						startTime: time.Now(),
						poolName:  arg.PoolName,
						batchSize: arg.BatchSize,
					}), util.NoOp
				},
				EndExecute: func(ctx context.Context, arg EndExecute) {
					data := ctx.Value(executeDataKey{}).(executeDataValue)
					duration := time.Since(data.startTime)

					tags := executeTags{
						Pool:   data.poolName,
						Result: arg.ExecuteResultVariant,
						Error:  errors.SerializeTags(arg.Err),
					}

					executeHandle.Emit(duration, tags)
					executeBatchSizeHandle.Emit(data.batchSize, tags)
				},
				InFlightKeys: func(ctx context.Context, arg InFlightKeys, getter func() int) {
					metrics.Repeating(ctx, deps, inFlightKeysHandle.With(poolTags{Pool: arg.PoolName}), getter)
				},
			}
		},
	)
}
