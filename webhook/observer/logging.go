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
	"strings"

	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/o11y"
	o11yklog "github.com/kubewharf/podseidon/util/o11y/klog"
	"github.com/kubewharf/podseidon/util/util"
)

func ProvideLogging() component.Declared[Observer] {
	return o11y.Provide(
		func(requests *component.DepRequests) util.Empty {
			o11yklog.RequestKlogArgs(requests)
			return util.Empty{}
		},
		func(util.Empty) Observer {
			type executeRetryArgsCtxKey struct{}

			type executeRetryArgsCtxValue struct{ batchSize int }

			return Observer{
				HttpRequest: func(ctx context.Context, arg Request) (context.Context, context.CancelFunc) {
					logger := klog.FromContext(ctx)
					logger = logger.WithValues("peerAddr", arg.RemoteAddr, "cell", arg.Cell)
					return klog.NewContext(ctx, logger), util.NoOp
				},
				HttpRequestComplete: func(ctx context.Context, arg RequestComplete) {
					logger := klog.FromContext(ctx)
					if request := arg.Request; request != nil {
						logger = logger.WithValues(
							"status", arg.Status,
							"objNamespace", arg.Request.Namespace,
							"objName", arg.Request.Name,
							"requestUid", arg.Request.UID,
							"dryRun", ptr.Deref(arg.Request.DryRun, false),
						)
					}
					logger.V(4).WithCallDepth(1).Info("webhook request completed")
				},
				HttpError: func(ctx context.Context, arg HttpError) {
					klog.FromContext(ctx).WithCallDepth(1).Error(arg.Err, "HTTP error", o11yklog.ErrTagKvs(arg.Err)...)
				},
				StartHandlePodInPpr: func(ctx context.Context, arg StartHandlePodInPpr) (context.Context, context.CancelFunc) {
					logger := klog.FromContext(ctx)
					logger = logger.WithValues(
						"namespace", arg.Namespace,
						"pprName", arg.PprName,
						"podName", arg.PodName,
						"requestType", arg.RequestType,
						"userName", arg.DeleteUserName,
						"userGroups", strings.Join(arg.DeleteUserGroups, ","),
					)
					logger.V(2).WithCallDepth(1).Info("handle pod deletion for ppr start")
					return klog.NewContext(ctx, logger), util.NoOp
				},
				EndHandlePodInPpr: func(ctx context.Context, arg EndHandlePodInPpr) {
					logger := klog.FromContext(ctx)
					logger.V(2).
						WithValues("rejected", arg.Rejected).
						WithCallDepth(1).Info("handle pod deletion for ppr end")
				},
				StartExecuteRetry: func(ctx context.Context, arg StartExecuteRetry) (context.Context, context.CancelFunc) {
					logger := klog.FromContext(ctx)
					logger = logger.WithValues(
						"namespace", arg.Key.Namespace,
						"name", arg.Key.Name,
					)

					cellCounts := iter.Histogram(
						iter.Map(
							iter.FromSlice(arg.Args),
							func(arg BatchItem) string { return arg.CellId },
						),
					)

					for cellId, count := range cellCounts {
						logger = logger.WithValues(fmt.Sprintf("cell.%s", cellId), count)
					}

					logger.V(4).WithCallDepth(1).Info("start executing ppr update batch")

					ctx = context.WithValue(
						ctx,
						executeRetryArgsCtxKey{},
						executeRetryArgsCtxValue{batchSize: len(arg.Args)},
					)

					return klog.NewContext(ctx, logger), util.NoOp
				},
				EndExecuteRetrySuccess: func(ctx context.Context, arg EndExecuteRetrySuccess) {
					logger := klog.FromContext(ctx)

					batchSize := ctx.Value(executeRetryArgsCtxKey{}).(executeRetryArgsCtxValue).batchSize

					results := iter.Histogram(iter.Map(iter.Range(0, batchSize), arg.Results))

					for result, count := range results {
						logger = logger.WithValues(fmt.Sprintf("result.%v", result), count)
					}

					logger.V(3).WithCallDepth(1).Info("ppr update batch successful")
				},
				EndExecuteRetryRetry: func(ctx context.Context, arg EndExecuteRetryRetry) {
					logger := klog.FromContext(ctx)
					logger.WithValues("delay", arg.Delay).
						V(4).
						WithCallDepth(1).
						Info("ppr update batch needs retry")
				},
				EndExecuteRetryErr: func(ctx context.Context, arg EndExecuteRetryErr) {
					logger := klog.FromContext(ctx)
					logger.WithCallDepth(1).Error(arg.Err, "ppr update batch error", o11yklog.ErrTagKvs(arg.Err)...)
				},
				AdmitBatchState: func(ctx context.Context, arg AdmitBatchState) {
					logger := klog.FromContext(ctx)
					var when string
					if arg.After {
						when = "after"
					} else {
						when = "before"
					}
					logger.WithCallDepth(1).V(4).Info(
						"admit batch quota",
						"when", when,
						"ppr.source", arg.PprKey.SourceName,
						"ppr.namespace", arg.PprKey.Namespace,
						"ppr.name", arg.PprKey.Name,
						"batchSize", arg.BatchSize,
						"state.scheduled", arg.State.Scheduled,
						"state.running", arg.State.Running,
						"state.ready", arg.State.Ready,
						"state.available", arg.State.Available,
						"state.threshold.reject", arg.State.Threshold.Reject,
						"state.threshold.retry", arg.State.Threshold.Retry,
						"state.threshold.remaining", arg.State.Threshold.Remaining.String(),
					)
				},
				ExecuteBatchItem: func(ctx context.Context, arg ExecuteBatchItem) {
					logger := klog.FromContext(ctx)
					logger.WithCallDepth(1).V(4).Info(
						"execute batch item",
						"cell", arg.CellId,
						"pod", arg.PodName,
						"criterion", arg.HealthCriterion.String(),
						"result", arg.Result,
					)
				},
			}
		},
	)
}
