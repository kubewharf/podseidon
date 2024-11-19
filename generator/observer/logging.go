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

	"k8s.io/klog/v2"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
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
			return Observer{
				InterpretProtectors: func(ctx context.Context, arg InterpretProtectors) {
					klog.FromContext(ctx).WithValues(
						"group", arg.Group,
						"version", arg.Version,
						"resource", arg.Resource,
						"namespace", arg.Namespace,
						"name", arg.Name,
						"decisions", arg.Decisions,
						"requiredProtectors", len(arg.RequiredProtectors),
					).V(4).Info("interpret protectors")
				},
				StartReconcile: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
					logger := klog.FromContext(ctx)
					logger = logger.WithValues(
						"group", arg.Group,
						"version", arg.Version,
						"resource", arg.Resource,
						"namespace", arg.Namespace,
						"name", arg.Name,
					)
					logger.V(2).WithCallDepth(1).Info("reconcile start")
					return klog.NewContext(ctx, logger), util.NoOp
				},
				EndReconcile: func(ctx context.Context, arg EndReconcile) {
					logger := klog.FromContext(ctx)
					logger.WithValues(
						"action", arg.Action,
						"pprName", arg.PprName,
					).V(4).WithCallDepth(1).Info("finished")
				}, // no additional info, worker logs are sufficient
				DanglingProtector: func(ctx context.Context, ppr *podseidonv1a1.PodProtector) {
					if ppr.DeletionTimestamp.IsZero() {
						klog.FromContext(ctx).
							WithCallDepth(1).
							Info("Identified dangling PodProtector object", "namespace", ppr.Namespace, "name", ppr.Name)
					} else {
						klog.FromContext(ctx).
							WithCallDepth(1).
							V(4).
							Info("PodProtector object is still finalizing", "namespace", ppr.Namespace, "name", ppr.Name)
					}
				},
				CreateProtector: func(ctx context.Context, _ util.Empty) (context.Context, context.CancelFunc) {
					klog.FromContext(ctx).
						V(3).
						WithCallDepth(1).
						Info("Creating corresponding PodProtector object")
					return ctx, util.NoOp
				},
				SyncProtector: func(ctx context.Context, _ *podseidonv1a1.PodProtector) (context.Context, context.CancelFunc) {
					klog.FromContext(ctx).
						V(3).
						WithCallDepth(1).
						Info("Sync template to PodProtector object")
					return ctx, util.NoOp
				},
				DeleteProtector: func(ctx context.Context, _ *podseidonv1a1.PodProtector) (context.Context, context.CancelFunc) {
					klog.FromContext(ctx).
						V(3).
						WithCallDepth(1).
						Info("Cascade deleting PodProtector object")
					return ctx, util.NoOp
				},
				CleanSourceFinalizer: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
					klog.FromContext(ctx).
						V(3).
						WithCallDepth(1).
						WithValues(
							"group", arg.Group,
							"version", arg.Version,
							"resource", arg.Resource,
							"namespace", arg.Namespace,
							"name", arg.Name,
						).
						Info("No PodProtectors present, ensuring finalizer removal from source object")
					return ctx, util.NoOp
				},
				MonitorWorkloads: func(context.Context, util.Empty, func() MonitorWorkloads) {},
			}
		},
	)
}
