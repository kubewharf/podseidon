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

package utilhealthz

import (
	"context"
	"flag"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/healthz/observer"
	utilhttp "github.com/kubewharf/podseidon/util/http"
	"github.com/kubewharf/podseidon/util/o11y"
)

const (
	defaultHttpPort  uint16 = 8081
	defaultHttpsPort uint16 = 8444
)

var NewServer = utilhttp.DeclareServer(
	func(_ Args) string { return "healthz" },
	defaultHttpPort, defaultHttpsPort,
	func(_ Args, _ *flag.FlagSet) Options {
		return Options{}
	},
	func(_ Args, requests *component.DepRequests) Deps {
		return Deps{
			Observer: o11y.Request[observer.Observer](requests),
		}
	},
	func(args Args, _ Options, _ Deps, mux *http.ServeMux) (*State, error) {
		mux.Handle("/readyz", http.StripPrefix("/readyz", args.Handler))
		mux.Handle("/readyz/", http.StripPrefix("/readyz/", args.Handler))

		return &State{}, nil
	},
	component.Lifecycle[Args, Options, Deps, State]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(_ Args, _ Options, deps Deps, _ *State) *Api {
		return &Api{
			observer: deps.Observer.Get(),
		}
	},
)

type Args struct {
	Handler *healthz.Handler
}

type Options struct{}

type Deps struct {
	Observer component.Dep[observer.Observer]
}

type State struct{}

type Api struct {
	observer observer.Observer
}

func (api *Api) OnHealthCheckFailed(ctx context.Context, name string, err error) {
	api.observer.CheckFailed(ctx, observer.CheckFailed{CheckName: name, Err: err})
}
