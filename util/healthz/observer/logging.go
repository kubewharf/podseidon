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

func ProvideLogging() component.Declared[Observer] {
	return o11y.Provide(
		func(requests *component.DepRequests) util.Empty {
			o11yklog.RequestKlogArgs(requests)
			return util.Empty{}
		},
		func(util.Empty) Observer {
			return Observer{
				CheckFailed: func(ctx context.Context, arg CheckFailed) {
					logger := klog.FromContext(ctx)
					logger.WithCallDepth(1).
						Error(arg.Err, "health check failed", "check", arg.CheckName)
				},
			}
		},
	)
}
