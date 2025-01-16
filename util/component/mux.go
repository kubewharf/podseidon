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

package component

import (
	"context"
	"flag"
	"fmt"

	utilflag "github.com/kubewharf/podseidon/util/flag"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

// Provides an implementation that implements `Interface`,
// declaring a component for the implementation and a component for the interface.
// If the interface component is declared multiple times,
// an implementation is selected based on the CLI options provided by the user.
//
// The same `implName` must not be declared twice for the same `muxName`.
// All calls to `DeclareMuxImpl` with the same `muxName` must pass the same `Interface`.
//
// All options are prefixed by `{muxName}-{implName}` instead of `{implName}`.
// Implementation components that did not get selected by the user
// and components only requested by such components
// do not get `init`ed, and hence their lifecycles are never invoked.
//
// The first implementation that got declared is always the default option.
func DeclareMuxImpl[Args any, Options any, Deps any, State any, Interface any](
	muxName string,
	implName string,
	optionsFn func(Args, *flag.FlagSet) Options,
	depsFn func(Args, *DepRequests) Deps,
	init func(context.Context, Args, Options, Deps) (*State, error),
	lifecycle Lifecycle[Args, Options, Deps, State],
	api func(*Data[Args, Options, Deps, State]) Interface,
) func(Args) func(*DepRequests) {
	return func(implArgs Args) func(*DepRequests) {
		implComp := Declare(
			func(Args) string { return fmt.Sprintf("%s-%s", muxName, implName) },
			optionsFn,
			depsFn,
			init,
			lifecycle,
			func(d *Data[Args, Options, Deps, State]) MuxImpl[Interface] {
				return MuxImpl[Interface]{
					getImpl: func() Interface { return api(d) },
				}
			},
		)(implArgs)

		apiComp := declareApiComp[Interface](
			muxName,
		).WithMergeFn(func(prevArgs *muxArgs[Interface], prevDeps *muxDeps[Interface], reqs *DepRequests) {
			prevArgs.impls[implName] = implComp
			prevDeps.impls[implName] = DepPtr(reqs, implComp)

			if prevArgs.defaultOption.IsNone() {
				prevArgs.defaultOption = optional.Some(implName)
			}
		})(
			muxArgs[Interface]{
				description:   optional.None[string](), // to be populated by DeclareMuxInterface
				impls:         map[string]Declared[MuxImpl[Interface]]{implName: implComp},
				defaultOption: optional.Some(implName),
			},
		)

		return RequireDep(apiComp)
	}
}

// Declares a mux interface to request a dependency to the selected mux implementation.
// See [DeclareMuxImpl] for details.
// The returned value can be stored in a global to serve as a function like [Declare].
func ProvideMux[Interface any](muxName string, description string) func() Declared[Interface] {
	return func() Declared[Interface] {
		return declareApiComp[Interface](
			muxName,
		).WithMergeFn(func(args *muxArgs[Interface], _ *muxDeps[Interface], _ *DepRequests) {
			args.description = optional.Some(description)
		})(
			muxArgs[Interface]{
				description:   optional.Some(description),
				impls:         map[string]Declared[MuxImpl[Interface]]{},
				defaultOption: optional.None[string](),
			},
		)
	}
}

func declareApiComp[Interface any](muxName string) DeclaredCtor[muxArgs[Interface], muxOptions, muxDeps[Interface], Interface] {
	return Declare(
		func(muxArgs[Interface]) string { return muxName },
		muxArgs[Interface].addFlags,
		muxArgs[Interface].collectDeps,
		func(context.Context, muxArgs[Interface], muxOptions, muxDeps[Interface]) (*util.Empty, error) {
			return &util.Empty{}, nil
		},
		Lifecycle[muxArgs[Interface], muxOptions, muxDeps[Interface], util.Empty]{
			Start:        nil,
			Join:         nil,
			HealthChecks: nil,
		},
		func(d *Data[muxArgs[Interface], muxOptions, muxDeps[Interface], util.Empty]) Interface {
			return d.Deps.impls[*d.Options.selection.MustGet("this interface was never requested")].Get().getImpl()
		},
	).WithRequestedFromMainFn(
		// mux components can only be a transitive dependency.
		func(muxArgs[Interface], muxOptions) bool { return false },
	).WithUnrequestDepsFn(func(args muxArgs[Interface], options muxOptions) []string {
		return optional.Map(options.selection, func(selection *string) []string {
			output := make([]string, 0, len(args.impls)-1)

			for implName, implDecl := range args.impls {
				if implName != *selection {
					implComp, _ := implDecl.GetNew()
					output = append(output, implComp.Name())
				}
			}

			return output
		}).GetOr(nil)
	})
}

type MuxImpl[Interface any] struct {
	getImpl func() Interface
}

type muxArgs[Interface any] struct {
	description   optional.Optional[string]
	impls         map[string]Declared[MuxImpl[Interface]]
	defaultOption optional.Optional[string]
}

func (args muxArgs[Interface]) addFlags(fs *flag.FlagSet) muxOptions {
	selection := optional.Map(args.description, func(description string) *string {
		implNames := iter.CollectMap(
			iter.Map(iter.MapKeys(args.impls), func(key string) iter.Pair[string, string] { return iter.NewPair(key, key) }),
		)

		return utilflag.EnumFromMap(implNames).
			TypeName("type").
			Default(args.defaultOption.MustGet(fmt.Sprintf("no implementations for %v provided", util.Type[Interface]()))).
			Flag(fs, utilflag.EmptyFlagName, description)
	})

	return muxOptions{
		selection: selection,
	}
}

func (args muxArgs[Interface]) collectDeps(reqs *DepRequests) muxDeps[Interface] {
	impls := make(map[string]Dep[MuxImpl[Interface]], len(args.impls))
	for implName, implDecl := range args.impls {
		impls[implName] = DepPtr(reqs, implDecl)
	}

	return muxDeps[Interface]{
		impls: impls,
	}
}

type muxOptions struct {
	selection optional.Optional[*string]
}

type muxDeps[Interface any] struct {
	impls map[string]Dep[MuxImpl[Interface]]
}
