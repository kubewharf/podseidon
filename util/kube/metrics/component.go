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

package kubemetrics

import (
	"context"
	"flag"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	"github.com/kubewharf/podseidon/util/util"
)

var NewComp = component.Declare(
	func(_ CompArgs) string { return "kube-metrics" },
	func(_ CompArgs, _ *flag.FlagSet) compOptions { return compOptions{} },
	func(_ CompArgs, requests *component.DepRequests) compDeps {
		return compDeps{
			prom: metrics.MakeObserverDeps(requests),
		}
	},
	func(_ context.Context, _ CompArgs, _ compOptions, deps compDeps) (*compState, error) {
		RegisterForPrometheus(deps.prom.Registry().Prometheus)
		return &compState{}, nil
	},
	component.Lifecycle[CompArgs, compOptions, compDeps, compState]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(_ *component.Data[CompArgs, compOptions, compDeps, compState]) util.Empty { return util.Empty{} },
)

type CompArgs struct{}

type compOptions struct{}

type compDeps struct {
	prom metrics.ObserverDeps
}

type compState struct{}
