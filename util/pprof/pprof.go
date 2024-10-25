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

package pprof

import (
	"flag"
	"net/http"
	"net/http/pprof"
	"runtime"

	"github.com/kubewharf/podseidon/util/component"
	utilhttp "github.com/kubewharf/podseidon/util/http"
	"github.com/kubewharf/podseidon/util/util"
)

const (
	defaultHttpPort  uint16 = 6060
	defaultHttpsPort uint16 = 6061
)

var New = utilhttp.DeclareServer(
	func(util.Empty) string { return "pprof" },
	defaultHttpPort,
	defaultHttpsPort,
	func(_ util.Empty, fs *flag.FlagSet) Options {
		return Options{
			BlockProfileRate: fs.Int(
				"block-profile",
				0,
				"fraction reciprocal of goroutine blocking events reported in blocking profile",
			),
			MutexProfileFraction: fs.Int(
				"mutex-profile",
				0,
				"fraction reciprocal of mutex contention events reported in mutex profile",
			),
		}
	},
	func(util.Empty, *component.DepRequests) util.Empty { return util.Empty{} },
	func(_ util.Empty, options Options, _ util.Empty, mux *http.ServeMux) (*State, error) {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

		if *options.BlockProfileRate > 0 {
			runtime.SetBlockProfileRate(*options.BlockProfileRate)
		}

		if *options.MutexProfileFraction > 0 {
			runtime.SetMutexProfileFraction(*options.MutexProfileFraction)
		}

		return &State{}, nil
	},
	component.Lifecycle[util.Empty, Options, util.Empty, State]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(util.Empty, Options, util.Empty, *State) util.Empty { return util.Empty{} },
)

type Options struct {
	BlockProfileRate     *int
	MutexProfileFraction *int
}

type State struct{}
