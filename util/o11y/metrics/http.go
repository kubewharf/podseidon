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
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kubewharf/podseidon/util/component"
	utilhttp "github.com/kubewharf/podseidon/util/http"
	"github.com/kubewharf/podseidon/util/util"
)

const (
	defaultHttpPort  uint16 = 9090
	defaultHttpsPort uint16 = 9443
)

var NewHttp = utilhttp.DeclareServer(
	func(HttpArgs) string { return "prometheus-http" },
	defaultHttpPort,
	defaultHttpsPort,
	func(HttpArgs, *flag.FlagSet) HttpOptions { return HttpOptions{} },
	func(_ HttpArgs, requests *component.DepRequests) httpDeps {
		return httpDeps{
			comp: component.DepPtr(requests, newRegistry()),
		}
	},
	func(_ context.Context, _ HttpArgs, _ HttpOptions, deps httpDeps, mux *http.ServeMux) (*httpState, error) {
		//nolint:exhaustruct
		mux.Handle("/", promhttp.HandlerFor(deps.comp.Get().Registry, promhttp.HandlerOpts{
			Registry: deps.comp.Get().Registry,
		}))

		return &httpState{}, nil
	},
	component.Lifecycle[HttpArgs, HttpOptions, httpDeps, httpState]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(HttpArgs, HttpOptions, httpDeps, *httpState) util.Empty { return util.Empty{} },
)

type HttpArgs struct{}

type HttpOptions struct{}

type httpDeps struct {
	comp component.Dep[*registryState]
}

type httpState struct{}
