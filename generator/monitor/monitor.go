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

// Reports global metrics about all PodProtector objects.
package monitor

import (
	"context"
	"flag"
	"sync"

	"k8s.io/apimachinery/pkg/types"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
	podseidoninformers "github.com/kubewharf/podseidon/client/informers/externalversions"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/generator/constants"
	"github.com/kubewharf/podseidon/generator/observer"
)

const ProportionPpmUnits = 1000000

var New = component.Declare[Args, Options, Deps, State, util.Empty](
	func(Args) string { return "generator-monitor" },
	func(_ Args, fs *flag.FlagSet) Options {
		return Options{
			Enable: fs.Bool("enable", true, "Enable global PodProtector monitor"),
		}
	},
	func(_ Args, requests *component.DepRequests) Deps {
		return Deps{
			observer: o11y.Request[observer.Observer](requests),
			podseidonInformers: component.DepPtr(requests, kube.NewInformers(kube.PodseidonInformers(
				constants.CoreClusterName,
				constants.LeaderPhase,
				optional.Some(constants.GeneratorElectorArgs),
			))),
		}
	},
	func(_ context.Context, _ Args, options Options, deps Deps) (*State, error) {
		state := &State{
			statusMu:        sync.Mutex{},
			status:          util.Zero[observer.MonitorWorkloads](),
			addedDeltaCache: map[types.NamespacedName]observer.MonitorWorkloads{},
		}

		if *options.Enable {
			pprInformer := deps.podseidonInformers.Get().Podseidon().V1alpha1().PodProtectors()
			_, err := pprInformer.Informer().AddEventHandler(kube.GenericEventHandlerWithStaleState(
				func(ppr *podseidonv1a1.PodProtector, stillPresent bool) {
					nsName := types.NamespacedName{Namespace: ppr.Namespace, Name: ppr.Name}

					if stillPresent {
						status := pprToStatus(ppr)
						state.add(nsName, status)
					} else {
						state.remove(nsName)
					}
				},
			))
			if err != nil {
				return nil, errors.TagWrapf("AddEventHandler", err, "add event handler to ppr informer")
			}
		}

		return state, nil
	},
	component.Lifecycle[Args, Options, Deps, State]{
		Start: func(ctx context.Context, _ *Args, options *Options, deps *Deps, state *State) error {
			if *options.Enable {
				go func() {
					deps.podseidonInformers.Get().WaitForCacheSync(ctx.Done())

					if ctx.Err() == nil {
						deps.observer.Get().MonitorWorkloads(ctx, util.Empty{}, func() observer.MonitorWorkloads {
							state.statusMu.Lock()
							defer state.statusMu.Unlock()

							return state.status
						})
					}
				}()
			}

			return nil
		},
		Join:         nil,
		HealthChecks: nil,
	},
	func(_ *component.Data[Args, Options, Deps, State]) util.Empty { return util.Empty{} },
)

type Args struct{}

type Options struct {
	Enable *bool
}

type Deps struct {
	observer           component.Dep[observer.Observer]
	podseidonInformers component.Dep[podseidoninformers.SharedInformerFactory]
}

type State struct {
	statusMu sync.Mutex
	status   observer.MonitorWorkloads

	addedDeltaCache map[types.NamespacedName]observer.MonitorWorkloads
}

func pprToStatus(ppr *podseidonv1a1.PodProtector) observer.MonitorWorkloads {
	availableProportionPpm := int64(0)
	if ppr.Spec.MinAvailable > 0 {
		availableProportionPpm = min(ProportionPpmUnits, util.RoundedIntDiv(
			int64(ppr.Status.Summary.AggregatedAvailable)*ProportionPpmUnits,
			int64(ppr.Spec.MinAvailable),
		))
	}

	isNonZero := 0
	if ppr.Spec.MinAvailable > 0 {
		isNonZero = 1
	}

	isMinCreated := 0
	if ppr.Status.Summary.Total >= ppr.Spec.MinAvailable {
		isMinCreated = 1
	}

	isFullyAvailable := 0
	if ppr.Status.Summary.AggregatedAvailable >= ppr.Spec.MinAvailable {
		isFullyAvailable = 1
	}

	return observer.MonitorWorkloads{
		NumWorkloads:                1,
		NumNonZeroWorkloads:         isNonZero,
		NumMinCreatedWorkloads:      isMinCreated,
		NumAvailableWorkloads:       isFullyAvailable,
		MinAvailable:                int64(ppr.Spec.MinAvailable),
		TotalReplicas:               int64(ppr.Status.Summary.Total),
		AggregatedAvailableReplicas: int64(ppr.Status.Summary.AggregatedAvailable),
		EstimatedAvailableReplicas:  int64(ppr.Status.Summary.EstimatedAvailable),
		SumAvailableProportionPpm:   availableProportionPpm,
		SumLatencyMillis:            ppr.Status.Summary.MaxLatencyMillis,
	}
}

func (state *State) add(nsName types.NamespacedName, newDelta observer.MonitorWorkloads) {
	old := state.addedDeltaCache[nsName] // use zero value if absent
	state.addedDeltaCache[nsName] = newDelta

	state.statusMu.Lock()
	defer state.statusMu.Unlock()

	state.status.Subtract(old)
	state.status.Add(newDelta)
}

func (state *State) remove(nsName types.NamespacedName) {
	old := state.addedDeltaCache[nsName]
	delete(state.addedDeltaCache, nsName)

	state.statusMu.Lock()
	defer state.statusMu.Unlock()

	state.status.Subtract(old)
}
