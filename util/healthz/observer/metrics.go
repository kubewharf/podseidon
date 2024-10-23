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

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
)

func ProvideMetrics() component.Declared[Observer] {
	return o11y.Provide(
		metrics.MakeObserverDeps,
		func(deps metrics.ObserverDeps) Observer {
			type healthzFailureTags struct {
				Check string
				Error string
			}

			failureHandle := metrics.Register(
				deps.Registry(),
				"healthck_failure",
				"Health check endpoint returned an error.",
				metrics.IntCounter(),
				metrics.NewReflectTags[healthzFailureTags](),
			)

			return Observer{
				CheckFailed: func(_ context.Context, arg CheckFailed) {
					failureHandle.Emit(1, healthzFailureTags{
						Check: arg.CheckName,
						Error: errors.SerializeTags(arg.Err),
					})
				},
			}
		},
	)
}
