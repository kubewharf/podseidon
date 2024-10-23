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

package o11y

import (
	"context"
	"flag"
	"fmt"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/util"
)

// Provides a new observer implementation for `ObsT`.
//
// Note that `ObsT` is a concrete struct type that lists the observe functions required,
// while the fields on `ObsT` are function pointers that implements the observer.
//
// The implementation may request dependency on other components through registering in `depsFn`.
func Provide[ObsT Observer[ObsT], Deps any](
	depsFn func(*component.DepRequests) Deps,
	impl func(Deps) ObsT,
) component.Declared[ObsT] {
	decl := component.Declare(
		func(compArgs[ObsT]) string {
			return fmt.Sprintf("observer-%s", util.Zero[ObsT]().ComponentName())
		},
		func(compArgs[ObsT], *flag.FlagSet) util.Empty { return util.Empty{} },
		func(args compArgs[ObsT], requests *component.DepRequests) compDeps[ObsT] {
			obsCtor := (func() ObsT)(nil)
			if makeRequests := args.requestsToObsCtor; impl != nil {
				obsCtor = makeRequests(requests)
			}

			return compDeps[ObsT]{obsCtor: obsCtor}
		},
		func(_ context.Context, _ compArgs[ObsT], _ util.Empty, deps compDeps[ObsT]) (*compState[ObsT], error) {
			if deps.obsCtor == nil {
				return &compState[ObsT]{
					obs: util.Zero[ObsT]().Join(util.Zero[ObsT]()),
				}, nil
			}

			obs := ReflectPopulate(
				deps.obsCtor(),
			) // ensure all functions are non-nil if there is only one observer with unset fields

			return &compState[ObsT]{
				obs: obs,
			}, nil
		},
		component.Lifecycle[compArgs[ObsT], util.Empty, compDeps[ObsT], compState[ObsT]]{
			Start:        nil,
			Join:         nil,
			HealthChecks: nil,
		},
		func(d *component.Data[compArgs[ObsT], util.Empty, compDeps[ObsT], compState[ObsT]]) ObsT {
			return d.State.obs
		},
	).WithMergeFn(func(_ *compArgs[ObsT], deps *compDeps[ObsT], requests *component.DepRequests) {
		if impl == nil {
			return
		}

		newDeps := depsFn(requests)

		if prevCtor := deps.obsCtor; prevCtor != nil {
			deps.obsCtor = func() ObsT {
				return prevCtor().Join(impl(newDeps))
			}
		} else {
			deps.obsCtor = func() ObsT { return impl(newDeps) }
		}
	})

	return decl(compArgs[ObsT]{
		requestsToObsCtor: func(requests *component.DepRequests) func() ObsT {
			deps := depsFn(requests)
			return func() ObsT { return impl(deps) }
		},
	})
}

// Requests an observer of type `ObsT`.
//
// After initialization completes, the returned `Dep` returns all provided implementations in `.Get()`.
func Request[ObsT Observer[ObsT]](requests *component.DepRequests) component.Dep[ObsT] {
	return component.DepPtr(
		requests,
		Provide(
			func(*component.DepRequests) util.Empty { return util.Empty{} },
			(func(util.Empty) ObsT)(nil),
		),
	)
}

type compArgs[ObsT Observer[ObsT]] struct {
	requestsToObsCtor func(*component.DepRequests) func() ObsT
}

type compDeps[ObsT Observer[ObsT]] struct {
	obsCtor func() ObsT
}

type compState[ObsT Observer[ObsT]] struct {
	obs ObsT
}
