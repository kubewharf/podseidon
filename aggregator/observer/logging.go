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

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
	o11yklog "github.com/kubewharf/podseidon/util/o11y/klog"
	"github.com/kubewharf/podseidon/util/util"
)

func NewLoggingObserver() Observer {
	return Observer{
		StartReconcile: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
			logger := klog.FromContext(ctx)
			logger = logger.WithValues(
				"namespace", arg.Namespace,
				"name", arg.Name,
			)
			logger.V(2).WithCallDepth(1).Info("reconcile start")
			return klog.NewContext(ctx, logger), util.NoOp
		},
		EndReconcile: func(ctx context.Context, arg EndReconcile) {
			logger := klog.FromContext(ctx)
			logger = logger.V(4).WithCallDepth(1).WithValues(
				"hasChange", arg.HasChange.String(),
				"action", arg.Action,
			)
			if arg.Err != nil {
				logger.Error(arg.Err, "reconcile error")
			} else {
				logger.Info("reconcile complete")
			}
		},
		StartEnqueue: func(ctx context.Context, _ StartEnqueue) (context.Context, context.CancelFunc) {
			return ctx, util.NoOp
		},
		EndEnqueue: func(context.Context, EndEnqueue) {},
		EnqueueError: func(ctx context.Context, arg EnqueueError) {
			klog.FromContext(ctx).
				WithCallDepth(1).
				Error(arg.Err, "handle reflector event", "namespace", arg.Namespace, "name", arg.Name)
		},
		Aggregated: func(ctx context.Context, arg Aggregated) {
			klog.FromContext(ctx).V(4).WithCallDepth(1).Info(
				"Updated PodProtector aggregation status",
				"numPods", arg.NumPods,
				"readyReplicas", arg.ReadyReplicas,
				"scheduledReplicas", arg.ScheduledReplicas,
				"runningReplicas", arg.RunningReplicas,
				"availableReplicas", arg.AvailableReplicas,
			)
		},
		NextEventPoolCurrentSize:    nil,
		NextEventPoolCurrentLatency: nil,
		NextEventPoolSingleDrain:    nil,
		TriggerPodCreate: func(ctx context.Context, arg TriggerPodCreate) {
			if arg.Err != nil {
				klog.FromContext(ctx).Error(arg.Err, "error creating update-trigger pod", o11yklog.ErrTagKvs(arg.Err)...)
			}
		},
		TriggerPodUpdate: func(ctx context.Context, arg TriggerPodUpdate) {
			if arg.Err != nil {
				klog.FromContext(ctx).Error(arg.Err, "error updating update-trigger pod", o11yklog.ErrTagKvs(arg.Err)...)
			}
		},
	}
}

func ProvideLogging() component.Declared[Observer] {
	return o11y.Provide(
		func(requests *component.DepRequests) util.Empty {
			o11yklog.RequestKlogArgs(requests)
			return util.Empty{}
		},
		func(util.Empty) Observer {
			return NewLoggingObserver()
		},
	)
}
