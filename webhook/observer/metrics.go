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
			type webhookRequestTags struct {
				Cell   string
				Status string
			}

			type webhookHttpErrorTags struct {
				Cell  string
				Error string
			}

			type webhookCtxKey struct{}

			type webhookCtxValue struct {
				cell      string
				startTime time.Time
			}

			type (
				webhookPodInPprCtxKey   struct{}
				webhookPodInPprCtxValue struct {
					startTime time.Time
				}
			)

			type webhookPodInPprTags struct {
				Rejected bool
			}

			requestHandle := metrics.Register(
				deps.Registry(),
				"webhook_request",
				"Response latency of webhook server.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[webhookRequestTags](),
			)

			httpErrorHandle := metrics.Register(
				deps.Registry(),
				"webhook_http_error",
				"Number of error events during webhook processing.",
				metrics.IntCounter(),
				metrics.NewReflectTags[webhookHttpErrorTags](),
			)

			podInPprHandle := metrics.Register(
				deps.Registry(),
				"webhook_handle_pod_in_ppr",
				"Duration of handling each matched PodProtector for a pod admission event.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[webhookPodInPprTags](),
			)

			return Observer{
				HttpRequest: func(ctx context.Context, arg Request) (context.Context, context.CancelFunc) {
					startTime := time.Now()

					return context.WithValue(
						ctx,
						webhookCtxKey{},
						webhookCtxValue{cell: arg.Cell, startTime: startTime},
					), util.NoOp
				},
				HttpRequestComplete: func(ctx context.Context, arg RequestComplete) {
					ctxValue := ctx.Value(webhookCtxKey{}).(webhookCtxValue)
					requestHandle.Emit(
						time.Since(ctxValue.startTime),
						webhookRequestTags{Cell: ctxValue.cell, Status: string(arg.Status)},
					)
				},
				HttpError: func(ctx context.Context, arg HttpError) {
					value := ctx.Value(webhookCtxKey{}).(webhookCtxValue)

					httpErrorHandle.Emit(1, webhookHttpErrorTags{
						Cell:  value.cell,
						Error: errors.SerializeTags(arg.Err),
					})
				},
				StartHandlePodInPpr: func(ctx context.Context, _ StartHandlePodInPpr) (context.Context, context.CancelFunc) {
					return context.WithValue(
						ctx,
						webhookPodInPprCtxKey{},
						webhookPodInPprCtxValue{
							startTime: time.Now(),
						},
					), util.NoOp
				},
				EndHandlePodInPpr: func(ctx context.Context, arg EndHandlePodInPpr) {
					ctxValue := ctx.Value(webhookPodInPprCtxKey{}).(webhookPodInPprCtxValue)

					podInPprHandle.Emit(time.Since(ctxValue.startTime), webhookPodInPprTags{
						Rejected: arg.Rejected,
					})
				},
				StartExecuteRetry: func(ctx context.Context, _ StartExecuteRetry) (context.Context, context.CancelFunc) {
					return ctx, util.NoOp
				},
				EndExecuteRetrySuccess: func(context.Context, EndExecuteRetrySuccess) {},
				EndExecuteRetryRetry:   func(context.Context, EndExecuteRetryRetry) {},
				EndExecuteRetryErr:     func(context.Context, EndExecuteRetryErr) {},
				ExecuteRetryQuota:      func(context.Context, ExecuteRetryQuota) {},
			}
		},
	)
}
