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

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
	o11yklog "github.com/kubewharf/podseidon/util/o11y/klog"
	"github.com/kubewharf/podseidon/util/util"
)

func ProvideInformerLogging() component.Declared[IndexedInformerObserver] {
	return o11y.Provide(
		func(requests *component.DepRequests) util.Empty {
			o11yklog.RequestKlogArgs(requests)
			return util.Empty{}
		},
		func(util.Empty) IndexedInformerObserver {
			return IndexedInformerObserver{
				StartHandleEvent: func(ctx context.Context, _ types.NamespacedName) (context.Context, context.CancelFunc) {
					return ctx, util.NoOp
				},
				EndHandleEvent: func(context.Context, util.Empty) {},
				HandleEventError: func(ctx context.Context, arg HandleEventError) {
					klog.FromContext(ctx).
						WithCallDepth(1).
						Error(arg.Err, "handle reflector event", append(o11yklog.ErrTagKvs(arg.Err), "namespace", arg.Namespace, "name", arg.Name)...)
				},
				UpdateSourceList: func(ctx context.Context, arg UpdateSourceList) {
					if arg.Additions != 0 || arg.Removals != 0 {
						klog.FromContext(ctx).WithValues(
							"additions", arg.Additions,
							"removals", arg.Removals,
							"newLength", arg.NewLength,
						).WithCallDepth(1).Info("PodProtector source list updated")
					}
				},
				UpdateSourceListError: func(ctx context.Context, arg UpdateSourceListError) {
					klog.FromContext(ctx).WithCallDepth(1).Error(arg.Err, "updating source list", o11yklog.ErrTagKvs(arg.Err)...)
				},
			}
		},
	)
}
