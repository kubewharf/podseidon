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

package kubeobserver

import (
	"context"

	"k8s.io/klog/v2"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	o11yklog "github.com/kubewharf/podseidon/util/o11y/klog"
	"github.com/kubewharf/podseidon/util/util"
)

func ProvideElectorLogs() component.Declared[kube.ElectorObserver] {
	return o11y.Provide(
		func(requests *component.DepRequests) util.Empty {
			o11yklog.RequestKlogArgs(requests)
			return util.Empty{}
		},
		func(util.Empty) kube.ElectorObserver {
			return kube.ElectorObserver{
				Acquired: func(ctx context.Context, arg kube.ElectorObserverArg) (context.Context, context.CancelFunc) {
					klog.FromContext(ctx).WithCallDepth(1).Info(
						"Acquired leader lease",
						"electorName", arg.ElectorName,
						"clusterName", arg.ClusterName,
						"identity", arg.Identity,
					)
					return ctx, util.NoOp
				},
				Lost: func(ctx context.Context, arg kube.ElectorObserverArg) {
					klog.FromContext(ctx).WithCallDepth(1).Info(
						"Lost leader lease",
						"electorName", arg.ElectorName,
						"clusterName", arg.ClusterName,
						"identity", arg.Identity,
					)
				},
				Heartbeat: func(context.Context, kube.ElectorObserverArg) {},
			}
		},
	)
}
