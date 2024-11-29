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
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
	o11yklog "github.com/kubewharf/podseidon/util/o11y/klog"
	"github.com/kubewharf/podseidon/util/util"
)

var nextReconcileId atomic.Uint64

type workerStartTimeKey struct{}

func ProvideLogging() component.Declared[Observer] {
	return o11y.Provide(
		func(requests *component.DepRequests) util.Empty {
			o11yklog.RequestKlogArgs(requests)
			return util.Empty{}
		},
		func(util.Empty) Observer {
			return Observer{
				StartReconcile: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
					ctx = context.WithValue(ctx, workerStartTimeKey{}, time.Now())

					logger := klog.FromContext(ctx)
					logger = logger.WithValues(
						"worker", arg.WorkerName,
						"reconcile", nextReconcileId.Add(1),
					)
					return klog.NewContext(ctx, logger), util.NoOp
				},
				EndReconcile: func(ctx context.Context, args EndReconcile) {
					startTime := ctx.Value(workerStartTimeKey{}).(time.Time)

					logger := klog.FromContext(ctx)
					logger = logger.WithValues("duration", time.Since(startTime))

					if args.Err != nil {
						logger.WithCallDepth(1).
							Error(args.Err, "reconcile error", o11yklog.ErrTagKvs(args.Err)...)
					} else {
						logger.V(3).WithCallDepth(1).Info("reconcile complete")
					}
				},
				QueueLength: func(_ context.Context, _ QueueLength, _ func() int) {},
			}
		},
	)
}
