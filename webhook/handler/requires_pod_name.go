// Copyright 2025 The Podseidon Authors.
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

package handler

import (
	"context"
	"flag"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubewharf/podseidon/util/component"
	utilflag "github.com/kubewharf/podseidon/util/flag"
	"github.com/kubewharf/podseidon/util/util"
)

const RequiresPodNameMuxName = "webhook-requires-pod-name"

var RequestRequiresPodName = component.ProvideMux[RequiresPodName](
	RequiresPodNameMuxName,
	"whether pod name should be written into PodProtector admission history",
)

// Determines whether a pod name should be written into PodProtector admission history.
type RequiresPodName interface {
	RequiresPodName(arg RequiresPodNameArg) bool
}

type RequiresPodNameArg struct {
	CellId  string
	PodName string
}

var DefaultRequiresPodNameImpls = component.RequireDeps(
	AlwaysRequiresPodName,
	NeverRequiresPodName,
	FilterByCellRequiresPodName,
)

type ConstantRequiresPodName bool

func (b ConstantRequiresPodName) RequiresPodName(_ RequiresPodNameArg) bool { return bool(b) }

var provideConstantRequiresPodName = component.DeclareMuxImpl(
	RequiresPodNameMuxName,
	func(arg bool) string {
		if arg {
			return "always"
		}

		return "never"
	},
	func(bool, *flag.FlagSet) util.Empty { return util.Empty{} },
	func(bool, *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, bool, util.Empty, util.Empty) (*util.Empty, error) { return &util.Empty{}, nil },
	component.Lifecycle[bool, util.Empty, util.Empty, util.Empty]{Start: nil, Join: nil, HealthChecks: nil},
	func(d *component.Data[bool, util.Empty, util.Empty, util.Empty]) RequiresPodName {
		return ConstantRequiresPodName(d.Args)
	},
)

var AlwaysRequiresPodName = provideConstantRequiresPodName(true, false)

var NeverRequiresPodName = provideConstantRequiresPodName(false, true)

var FilterByCellRequiresPodName = component.DeclareMuxImpl(
	RequiresPodNameMuxName,
	func(util.Empty) string { return "by-cell" },
	func(_ util.Empty, fs *flag.FlagSet) FilterByCellRequiresPodNameOptions {
		return FilterByCellRequiresPodNameOptions{
			Cells: utilflag.StringSet(fs, "filter", nil, "list of cells that require pod names in admission history"),
		}
	},
	func(util.Empty, *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, util.Empty, FilterByCellRequiresPodNameOptions, util.Empty) (*util.Empty, error) {
		return &util.Empty{}, nil
	},
	component.Lifecycle[util.Empty, FilterByCellRequiresPodNameOptions, util.Empty, util.Empty]{Start: nil, Join: nil, HealthChecks: nil},
	func(d *component.Data[util.Empty, FilterByCellRequiresPodNameOptions, util.Empty, util.Empty]) RequiresPodName {
		return filterByCellRequiresPodName(d.Options.Cells)
	},
)(util.Empty{}, false)

type filterByCellRequiresPodName sets.Set[string]

func (cells filterByCellRequiresPodName) RequiresPodName(arg RequiresPodNameArg) bool {
	return sets.Set[string](cells).Has(arg.CellId)
}

type FilterByCellRequiresPodNameOptions struct {
	Cells sets.Set[string]
}
