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
	"net/http"
	"sync/atomic"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/util"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// Declares a generic component.
// Returns a constructor for Declared that actually instantiates the component request.
//
// This function is typically used to assign a global variable to act like a function:
//
//	var New = component.Declare(...)
//
// Refer to package documentation for the description of the arguments.
func Declare[Args any, Options any, Deps any, State any, Api any](
	name func(args Args) string,
	newOptions func(args Args, fs *flag.FlagSet) Options,
	newDeps func(args Args, requests *DepRequests) Deps,
	init func(ctx context.Context, args Args, options Options, deps Deps) (*State, error),
	lifecycle Lifecycle[Args, Options, Deps, State],
	api func(d *Data[Args, Options, Deps, State]) Api,
) DeclaredCtor[Args, Deps, Api] {
	return func(args Args) Declared[Api] {
		impl := &componentImpl[Args, Options, Deps, State]{
			Data: Data[Args, Options, Deps, State]{
				Args:    args,
				Options: util.Zero[Options](),
				Deps:    util.Zero[Deps](),
				State:   nil,
			},
			name:      name(args),
			optionsFn: newOptions,
			depsFn:    newDeps,
			init:      init,
			lifecycle: lifecycle,
			onMerge:   nil,
			phase:     nil,
		}

		if start := impl.lifecycle.Start; start != nil {
			phase := new(atomic.Pointer[string])
			phase.Store(ptr.To("Init"))
			impl.phase = phase

			impl.lifecycle.Start = func(ctx context.Context, args *Args, options *Options, deps *Deps, state *State) error {
				phase.Store(ptr.To("Starting"))

				err := start(ctx, args, options, deps, state)
				if err != nil {
					phase.Store(ptr.To("StartError"))
				} else {
					phase.Store(ptr.To(phaseStarted))
				}

				return err
			}
		}

		return &declaredImpl[Args, Deps, Api]{
			comp: impl,
			api: func() Api {
				return api(&impl.Data)
			},
		}
	}
}

type DeclaredCtor[Args any, Deps any, Api any] func(Args) Declared[Api]

func (ctor DeclaredCtor[Args, Deps, Api]) WithMergeFn(
	onMerge func(*Args, *Deps, *DepRequests),
) DeclaredCtor[Args, Deps, Api] {
	return func(args Args) Declared[Api] {
		decl := ctor(args).(*declaredImpl[Args, Deps, Api])
		impl := decl.comp.(setOnMerge[Args, Deps])
		impl.setOnMerge(onMerge)

		return decl
	}
}

type setOnMerge[Args any, Deps any] interface {
	setOnMerge(onMerge func(*Args, *Deps, *DepRequests))
}

//
//nolint:unused // Implements unexported interface setOnMerge, false positive from unused lint
func (impl *componentImpl[Args, Options, Deps, State]) setOnMerge(
	onMerge func(*Args, *Deps, *DepRequests),
) {
	impl.onMerge = onMerge
}

type Declared[Api any] interface {
	asRawDep() Dep[Api]

	// Constructs a new instance of the raw component without dependency deduplication.
	GetNew() (Component, func() Api)

	set(comp Component, typedApi func() Api)
}

// A component declaration.
//
// Pass this through `DepPtr`/`DepRequest` to declare a dependency.
// This is mainly used for dependency resolution;
// do not reuse this object externally.
type declaredImpl[Args any, Deps any, Api any] struct {
	comp Component
	api  func() Api
}

func (decl *declaredImpl[Args, Deps, Api]) GetNew() (Component, func() Api) {
	return decl.comp, decl.api
}

//nolint:unused // Implements unexported method from interface Declared[Api], false positive from unused lint
func (decl *declaredImpl[Args, Deps, Api]) set(comp Component, typedApi func() Api) {
	decl.comp = comp
	decl.api = typedApi
}

//nolint:unused // Used from asRawDep, false positive from unused lint
type rawDep[Api any] struct {
	api *func() Api
}

//nolint:unused // Implements Dep[Api], false positive from unused lint
func (dep *rawDep[Api]) Get() Api {
	return (*dep.api)()
}

// Returns a Dep for this Declared without registration.
//
//nolint:unused // Implements unexported method from interface Declared[Api], false positive from unused lint
func (decl *declaredImpl[Args, Deps, Api]) asRawDep() Dep[Api] {
	return &rawDep[Api]{api: &decl.api}
}


// Stores various data related to a component.
type Data[Args any, Options any, Deps any, State any] struct {
	// Runtime arguments for this component,
	// typically used to differentiate between multiple instances of the same type.
	Args Args

	// Resolved flags for the component.
	//
	// Initialized after newOptions is called.
	Options Options

	// Dependency handles for the component.
	//
	// Initialized after newDeps is called.
	Deps Deps

	// Runtime states for the component.
	//
	// Initialized after init is called.
	State *State
}

// Implements Component.
type componentImpl[Args any, Options any, Deps any, State any] struct {
	Data[Args, Options, Deps, State]

	// Component name.
	name string

	// Constructor for the Options field.
	optionsFn func(Args, *flag.FlagSet) Options

	// Constructor for the Deps field.
	depsFn func(Args, *DepRequests) Deps

	// Constructor for the State field.
	init func(context.Context, Args, Options, Deps) (*State, error)

	// Lifecycle hooks for the component.
	lifecycle Lifecycle[Args, Options, Deps, State]

	// Merge arguments of this instantiation into the previous instantiation.
	//
	// Optionally requests extra dependencies.
	onMerge func(*Args, *Deps, *DepRequests)

	phase *atomic.Pointer[string]
}

func (impl *componentImpl[Args, Options, Deps, State]) Name() string {
	return impl.name
}

func (impl *componentImpl[Args, Options, Deps, State]) dependencies() []*depRequest {
	deps := DepRequests{requests: []*depRequest{}}
	impl.Deps = impl.depsFn(impl.Args, &deps)

	return deps.requests
}

func (impl *componentImpl[Args, Options, Deps, State]) mergeInto(other Component) []*depRequest {
	switch other := other.(type) {
	case *componentImpl[Args, Options, Deps, State]:
		deps := DepRequests{requests: nil}

		if impl.onMerge != nil {
			impl.onMerge(&other.Args, &other.Deps, &deps)
		}

		return deps.requests

	case emptyComponent:
		// do not merge into empty components since the implementation is exclusively determined by the test case
		return []*depRequest{}

	default:
		panic(fmt.Sprintf("cannot merge %q (%T) into incompatible Component type %T", impl.name, impl, other))
	}
}

func (impl *componentImpl[Args, Options, Deps, State]) AddFlags(fs *flag.FlagSet) {
	impl.Options = impl.optionsFn(impl.Args, fs)
}

func (impl *componentImpl[Args, Options, Deps, State]) Init(ctx context.Context) error {
	state, err := impl.init(ctx, impl.Args, impl.Options, impl.Deps)
	if err != nil {
		return err
	}

	impl.State = state

	return nil
}

func (impl *componentImpl[Args, Options, Deps, State]) Start(ctx context.Context) error {
	if impl.lifecycle.Start != nil {
		return impl.lifecycle.Start(ctx, &impl.Args, &impl.Options, &impl.Deps, impl.State)
	}

	return nil
}

func (impl *componentImpl[Args, Options, Deps, State]) RegisterHealthChecks(
	handler *healthz.Handler,
	onFail func(name string, err error),
) {
	if impl.phase != nil {
		handler.Checks[fmt.Sprintf("%s/phase", impl.name)] = func(_ *http.Request) error {
			if phase := *impl.phase.Load(); !isPhaseReady(phase) {
				return errors.TagErrorf("PhaseReady", "phase is %s", phase)
			}

			return nil
		}
	}

	if healthChecksFn := impl.lifecycle.HealthChecks; healthChecksFn != nil {
		for name, checker := range healthChecksFn(impl.State) {
			if checker != nil {
				checkName := fmt.Sprintf("%s/%s", impl.name, name)
				handler.Checks[checkName] = func(_ *http.Request) error {
					err := checker()
					if err != nil {
						onFail(checkName, err)
					}

					return err
				}
			}
		}
	}
}

func (impl *componentImpl[Args, Options, Deps, State]) Join(ctx context.Context) error {
	if impl.lifecycle.Join != nil {
		return impl.lifecycle.Join(ctx, &impl.Args, &impl.Options, &impl.Deps, impl.State)
	}

	return nil
}

const phaseStarted = "Started"

func isPhaseReady(phase string) bool {
	return phase == phaseStarted
}
