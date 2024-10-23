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
	"time"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	"github.com/kubewharf/podseidon/util/util"
)

var ProvideElector = component.RequireDeps(
	component.RequireDep(ProvideElectorLogs()),
	component.RequireDep(ProvideElectorMetrics()),
)

func ProvideElectorMetrics() component.Declared[kube.ElectorObserver] {
	type leaderEventTags struct {
		ClusterClass string
		ElectorName  string
		Identity     string
		Event        string
	}

	type leaderTags struct {
		ClusterClass string
		ElectorName  string
		Identity     string
	}

	return o11y.Provide(
		metrics.MakeObserverDeps,
		func(deps metrics.ObserverDeps) kube.ElectorObserver {
			eventHandle := metrics.Register(
				deps.Registry(),
				"leader_event",
				"",
				metrics.IntCounter(),
				metrics.NewReflectTags[leaderEventTags](),
			)

			heartbeatHandle := metrics.Register(
				deps.Registry(),
				"leader_heartbeat",
				"Time since leader lease acquisition, only emitted by the active leader.",
				metrics.FloatGauge(),
				metrics.NewReflectTags[leaderTags](),
			)

			type acquireTimeKey struct{}

			return kube.ElectorObserver{
				Acquired: func(ctx context.Context, arg kube.ElectorObserverArg) (context.Context, context.CancelFunc) {
					eventHandle.Emit(1, leaderEventTags{
						ClusterClass: arg.ClusterName,
						ElectorName:  arg.ElectorName,
						Identity:     arg.Identity,
						Event:        "acquired",
					})

					return context.WithValue(ctx, acquireTimeKey{}, time.Now()), util.NoOp
				},
				Lost: func(_ context.Context, arg kube.ElectorObserverArg) {
					eventHandle.Emit(1, leaderEventTags{
						ClusterClass: arg.ClusterName,
						ElectorName:  arg.ElectorName,
						Identity:     arg.Identity,
						Event:        "lost",
					})
				},
				Heartbeat: func(ctx context.Context, arg kube.ElectorObserverArg) {
					acquireTime := ctx.Value(acquireTimeKey{}).(time.Time)
					heartbeatHandle.Emit(time.Since(acquireTime).Seconds(), leaderTags{
						ClusterClass: arg.ClusterName,
						ElectorName:  arg.ElectorName,
						Identity:     arg.Identity,
					})
				},
			}
		},
	)
}
