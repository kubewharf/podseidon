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

package metrics

import (
	"context"
	"flag"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/util"
)

func newRegistry() component.Declared[*registryState] {
	return component.Declare(
		func(RegistryArgs) string { return "metrics" },
		func(_ RegistryArgs, fs *flag.FlagSet) registryOptions {
			return registryOptions{
				sampleFrequency: fs.Duration(
					"sample-frequency",
					time.Second*30,
					"frequency of sending heartbeat and monitoring metrics",
				),
			}
		},
		func(RegistryArgs, *component.DepRequests) util.Empty { return util.Empty{} },
		func(_ context.Context, _ RegistryArgs, options registryOptions, _ util.Empty) (*registryState, error) {
			registry := prometheus.NewRegistry()

			heartbeatHandle := Register(
				registry,
				"heartbeat",
				"Number of seconds since process startup",
				FloatGauge(),
				NewReflectTags[util.Empty](),
			)

			return &registryState{
				Registry:        registry,
				heartbeatHandle: heartbeatHandle,
				startupTime:     time.Now(),
				sampleFrequency: *options.sampleFrequency,
			}, nil
		},
		component.Lifecycle[RegistryArgs, registryOptions, util.Empty, registryState]{
			Start: func(ctx context.Context, _ *RegistryArgs, options *registryOptions, _ *struct{}, state *registryState) error {
				go wait.UntilWithContext(ctx, func(_ context.Context) {
					state.heartbeatHandle.Emit(
						time.Since(state.startupTime).Seconds(),
						util.Empty{},
					)
				}, *options.sampleFrequency)

				return nil
			},
			Join:         nil,
			HealthChecks: nil,
		},
		func(d *component.Data[RegistryArgs, registryOptions, util.Empty, registryState]) *registryState {
			return d.State
		},
	)(
		RegistryArgs{},
	)
}

type RegistryArgs struct{}

type registryOptions struct {
	sampleFrequency *time.Duration
}

type registryState struct {
	Registry        *prometheus.Registry
	heartbeatHandle Handle[util.Empty, float64]

	startupTime     time.Time
	sampleFrequency time.Duration
}

type ObserverDeps struct {
	reg component.Dep[*registryState]
}

func (deps ObserverDeps) Registry() *prometheus.Registry {
	return deps.reg.Get().Registry
}

func Repeating[V any](
	ctx context.Context,
	deps ObserverDeps,
	handle TaggedHandle[V],
	getter func() V,
) {
	wait.UntilWithContext(ctx, func(_ context.Context) {
		handle.Emit(getter())
	}, deps.reg.Get().sampleFrequency)
}

func MakeObserverDeps(requests *component.DepRequests) ObserverDeps {
	return ObserverDeps{
		reg: component.DepPtr(requests, newRegistry()),
	}
}
