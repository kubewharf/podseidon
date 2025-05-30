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
	"github.com/prometheus/client_golang/prometheus/collectors"
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
				tagFilter: &TagFilter{
					Namespace: fs.Bool("by-namespace", false, "group metrics by namespace when available"),
					User: fs.Bool(
						"by-user",
						false,
						"group metrics by username (node name is truncated for kubelet users) when available",
					),
				},
			}
		},
		func(RegistryArgs, *component.DepRequests) util.Empty { return util.Empty{} },
		func(_ context.Context, _ RegistryArgs, options registryOptions, _ util.Empty) (*registryState, error) {
			tagFilter := options.tagFilter
			registry := prometheus.NewRegistry()

			registry.MustRegister(
				collectors.NewBuildInfoCollector(),
				collectors.NewGoCollector(),
				//nolint:exhaustruct // optional fields
				collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
			)

			heartbeatHandle := registerWith(
				registry,
				tagFilter,
				"heartbeat",
				"Number of seconds since process startup",
				FloatGauge(),
				NewReflectTags[util.Empty](),
			)

			return &registryState{
				PrometheusRegistry: registry,
				tagFilter:          tagFilter,
				heartbeatHandle:    heartbeatHandle,
				flushables:         nil,
				started:            false,
				startupTime:        time.Now(),
				sampleFrequency:    *options.sampleFrequency,
			}, nil
		},
		component.Lifecycle[RegistryArgs, registryOptions, util.Empty, registryState]{
			Start: func(ctx context.Context, _ *RegistryArgs, options *registryOptions, _ *util.Empty, state *registryState) error {
				for _, flushable := range state.flushables {
					flushable(ctx)
				}

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

	tagFilter *TagFilter
}

type registryState struct {
	PrometheusRegistry *prometheus.Registry
	tagFilter          *TagFilter
	heartbeatHandle    Handle[util.Empty, float64]

	flushables []func(context.Context)

	started         bool
	startupTime     time.Time
	sampleFrequency time.Duration
}

type ObserverDeps struct {
	reg component.Dep[*registryState]
}

func (deps ObserverDeps) Registry() Registry {
	state := deps.reg.Get()

	return Registry{
		Prometheus: state.PrometheusRegistry,
		TagFilter:  state.tagFilter,
		flushables: &state.flushables,
		started:    state.started,
	}
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

type Registry struct {
	Prometheus *prometheus.Registry
	TagFilter  *TagFilter
	flushables *[]func(context.Context)
	started    bool
}

type TagFilter struct {
	Namespace *bool
	User      *bool
}

func (filter *TagFilter) Test(tagName string) bool {
	switch tagName {
	case "namespace":
		return *filter.Namespace
	case "user":
		return *filter.User
	default:
		return true
	}
}
