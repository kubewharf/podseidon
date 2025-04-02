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
	"fmt"
	"time"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	"github.com/kubewharf/podseidon/util/o11y/metrics/unique"
	"github.com/kubewharf/podseidon/util/util"
)

//nolint:mnd
var UniqueRejectRateWindows = map[string]time.Duration{
	"30s": time.Second * 30,
	"5m":  time.Minute * 5,
	"15m": time.Minute * 15,
	"1h":  time.Hour,
	"1d":  time.Hour * 24,
}

func ProvideMetrics() component.Declared[Observer] {
	return o11y.Provide(
		metrics.MakeObserverDeps,
		func(deps metrics.ObserverDeps) Observer {
			type requestTags struct {
				Cell   string
				Status string
			}

			type httpErrorTags struct {
				Cell  string
				Error string
			}

			type (
				requestCtxKey   struct{}
				requestCtxValue struct {
					cellPath  string
					cellId    string
					startTime time.Time
				}
			)

			type (
				podInPprCtxKey   struct{}
				podInPprCtxValue struct {
					startTime time.Time
					namespace string
					pprName   string
					podName   string
				}
			)

			type podInPprTags struct {
				Cell     string
				Rejected bool
			}

			requestHandle := metrics.Register(
				deps.Registry(),
				"webhook_request",
				"Response latency of webhook server.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[requestTags](),
			)

			httpErrorHandle := metrics.Register(
				deps.Registry(),
				"webhook_http_error",
				"Number of error events during webhook processing.",
				metrics.IntCounter(),
				metrics.NewReflectTags[httpErrorTags](),
			)

			podInPprHandle := metrics.Register(
				deps.Registry(),
				"webhook_handle_pod_in_ppr",
				"Duration of handling each matched PodProtector for a pod admission event.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[podInPprTags](),
			)

			handleUniquePprHandles := make([]metrics.Handle[podInPprTags, string], 0, len(UniqueRejectRateWindows))
			handleUniquePodHandles := make([]metrics.Handle[podInPprTags, string], 0, len(UniqueRejectRateWindows))

			for freqStr, freq := range UniqueRejectRateWindows {
				handleUniquePprHandles = append(handleUniquePprHandles, metrics.RegisterFlushable(
					deps.Registry(),
					fmt.Sprintf("webhook_handle_unique_ppr_%s", freqStr),
					fmt.Sprintf(
						"Number of unique PodProtectors handled every %s. Value is always 0 in the first %s after process restart.",
						freqStr,
						freqStr,
					),
					unique.NewCounterVec(),
					metrics.NewReflectTags[podInPprTags](),
					freq,
				))

				handleUniquePodHandles = append(handleUniquePodHandles, metrics.RegisterFlushable(
					deps.Registry(),
					fmt.Sprintf("webhook_handle_unique_pod_%s", freqStr),
					fmt.Sprintf(
						"Number of unique pods handled every %s. Value is always 0 in the first %s after process restart.",
						freqStr,
						freqStr,
					),
					unique.NewCounterVec(),
					metrics.NewReflectTags[podInPprTags](),
					freq,
				))
			}

			return Observer{
				HttpRequest: func(ctx context.Context, arg Request) (context.Context, context.CancelFunc) {
					startTime := time.Now()

					return context.WithValue(
						ctx,
						requestCtxKey{},
						requestCtxValue{cellPath: arg.CellPath, cellId: "<unknown>", startTime: startTime},
					), util.NoOp
				},
				HttpRequestComplete: func(ctx context.Context, arg RequestComplete) {
					ctxValue := ctx.Value(requestCtxKey{}).(requestCtxValue)
					requestHandle.Emit(
						time.Since(ctxValue.startTime),
						requestTags{Cell: ctxValue.cellPath, Status: string(arg.Status)},
					)
				},
				HttpError: func(ctx context.Context, arg HttpError) {
					value := ctx.Value(requestCtxKey{}).(requestCtxValue)

					httpErrorHandle.Emit(1, httpErrorTags{
						Cell:  value.cellPath,
						Error: errors.SerializeTags(arg.Err),
					})
				},
				RequestFromCell: func(ctx context.Context, arg RequestFromCell) (context.Context, context.CancelFunc) {
					value := ctx.Value(requestCtxKey{}).(requestCtxValue)
					value.cellId = arg.CellId
					return context.WithValue(ctx, requestCtxKey{}, value), util.NoOp
				},
				StartHandlePodInPpr: func(ctx context.Context, arg StartHandlePodInPpr) (context.Context, context.CancelFunc) {
					return context.WithValue(
						ctx,
						podInPprCtxKey{},
						podInPprCtxValue{
							startTime: time.Now(),
							namespace: arg.Namespace,
							pprName:   arg.PprName,
							podName:   arg.PodName,
						},
					), util.NoOp
				},
				EndHandlePodInPpr: func(ctx context.Context, arg EndHandlePodInPpr) {
					reqCtxValue := ctx.Value(requestCtxKey{}).(requestCtxValue)
					pipCtxValue := ctx.Value(podInPprCtxKey{}).(podInPprCtxValue)

					tags := podInPprTags{
						Cell:     reqCtxValue.cellId,
						Rejected: arg.Rejected,
					}

					podInPprHandle.Emit(time.Since(pipCtxValue.startTime), tags)

					for _, handle := range handleUniquePprHandles {
						handle.Emit(fmt.Sprintf("%s/%s", pipCtxValue.namespace, pipCtxValue.pprName), tags)
					}

					for _, handle := range handleUniquePodHandles {
						handle.Emit(fmt.Sprintf("%s/%s", pipCtxValue.namespace, pipCtxValue.podName), tags)
					}
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
