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

// A framework for component lifecycle orchestration.
//
// Each "component" is a subroutine with its own lifecycle.
// Components may depend on other components, which are initialized in topological order.
// If multiple components depend on the same component (using the component name as the equivalence class),
// the same instance of the component is used.
//
// # Concepts
//
// `Args` is a runtime value used to customize a component for the caller's needs.
// It is mainly used for two purposes:
// (1) Distinguish between multiple instances of the same component type,
// e.g. two Kubernetes client sets connecting to two different clusters
// would be specified by a `ClusterName` field in the Args,
// which is included as part of its component name
// (thus the `name` function can accept `Args` as its argument).
// (2) Provide custom plugin implementations in the startup script,
// e.g. the `tracing.Observer` component is requested by
// specifying all observers implementations in Args in the main entrypoint,
// and just requested from other components without specifying the observer implementations;
// for this to work properly, the implementation-providing component request must precede
// all (direct and transitive) dependents of the component and still resolve to the same component name.
//
// `Options` is a type that stores the data for the flags requested by each component.
// The `optionsFn` function registers flags into a given FlagSet,
// which are added to the global FlagSet using the component name as the prefix.
//
// `Deps` is a type that stores the handles (`component.Dep[Api]`)
// for the dependency components it requested.
//
// `State` stores the (possibly mutable) runtime states for the component.
// Data only need to be stored in `State` if they are to be used for lifecycle or component interactions.
//
// `Api` is an external interface for dependents to interact with a dependency component.
// The actual value of `Api` should be a simple wrapper around the internal data (including the types above)
// that exposes abstract capabilities to downstream components.
//
// The `init` function registers inter-component connections to prepare fthem for initialization.
// Since it is the first stage in the lifecycle that executes business logic,
// it is also the phase that constructs the `State`.
// A component `Api` is only considered fully usable after init is called.
// `init` is called in topological order of dependency,
// so `Api` of dependencies is considered fully usable in the init function as well.
package component

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/util"
)

var ErrRecursiveDependencies = errors.TagErrorf(
	"RecursiveDependencies",
	"recursive dependency chain",
)

// Controls the lifecycle of a component.
//
// This interface is only useful for lifecycle orchestration and should not be implemented by other packages.
type Component interface {
	// Describes this component for dependency resolution.
	manifest() manifest

	// Updates the other component upon name duplication.
	mergeInto(other Component) []*depRequest

	// Registers flags for this component.
	AddFlags(fs *flag.FlagSet)
	// Registers inter-component connections.
	//
	// Should not interact with the environment or start any background tasks.
	// The context may be removed in the future; background tasks should use the context from Start instead.
	Init(ctx context.Context) error
	// Starts the actual work.
	Start(ctx context.Context) error
	// Waits for the component to shut down gracefully.
	// Should return when ctx expires.
	Join(ctx context.Context) error
	// Registers health check handlers.
	RegisterHealthChecks(handler *healthz.Handler, onFail func(name string, err error))
}

type manifest struct {
	Name         string
	Dependencies []*depRequest
}

// A registry of dependencies requested by components.
type DepRequests struct {
	requests []*depRequest
}

// Describes how to provide a requested dependency to a dependent component.
type depRequest struct {
	// Call this function to initialize a new instance of the dependency.
	//
	// The second return type should downcast to `func() Api`
	getNew func() (Component, any)
	// Call this function to provide an existing instance of the dependency to the requester.
	//
	// The second return type must be the correct `func() Api` for this dependency.
	set func(c Component, api any)
}

// A dependency handle.
type Dep[Api any] interface {
	// Returns the interface to work with a component.
	//
	// The return value may differ before startup completion.
	// Do not use the result of .Get() called during init in the main lifecycle.
	Get() Api
}

// Returns a closure that can be passed to `cmd.Run`.
func RequireDep[Api any](base Declared[Api]) func(*DepRequests) {
	return func(requests *DepRequests) {
		DepPtr(requests, base)
	}
}

// Merges multiple `RequireDep` results into one.
func RequireDeps(deps ...func(*DepRequests)) func(*DepRequests) {
	return func(requests *DepRequests) {
		for _, dep := range deps {
			dep(requests)
		}
	}
}

// Requests a dependency, returning a handle to interact with the dependency.
func DepPtr[Api any](requests *DepRequests, base Declared[Api]) Dep[Api] {
	request := &depRequest{
		getNew: func() (Component, any) {
			return base.GetNew()
		},
		set: func(comp Component, api any) {
			typedApi, ok := api.(func() Api)
			if !ok {
				panic(fmt.Sprintf(
					"Components of types %T and %T declared the same name %q",
					comp, base, comp.manifest().Name,
				))
			}

			base.set(comp, typedApi)
		},
	}
	requests.requests = append(requests.requests, request)

	return base.asRawDep()
}

// Returns a slice of components with all dependencies resolved and in initialization order.
func ResolveList(requestFns []func(*DepRequests)) []NamedComponent {
	requests := DepRequests{requests: []*depRequest{}}
	for _, fn := range requestFns {
		fn(&requests)
	}

	componentMap := map[string]*componentMapEntry{}

	for _, request := range requests.requests {
		resolveRequest(componentMap, request)
	}

	return toposortComponentList(componentMap)
}

func toposortComponentList(componentMap map[string]*componentMapEntry) []NamedComponent {
	pending := iter.CollectSet(iter.MapKeys(componentMap))
	visited := make(sets.Set[string], pending.Len())
	sorted := make([]NamedComponent, 0, pending.Len())

	var dfs func(string) error
	dfs = func(compName string) error {
		if !pending.Has(compName) {
			return nil
		}

		if visited.Has(compName) {
			return fmt.Errorf("%w %q", ErrRecursiveDependencies, compName)
		}

		visited.Insert(compName)

		entry := componentMap[compName]

		for dep := range entry.deps {
			if err := dfs(dep); err != nil {
				return fmt.Errorf("%w <- %q", err, compName)
			}
		}

		sorted = append(
			sorted,
			NamedComponent{Name: compName, Component: entry.comp, apiGetter: entry.apiGetter},
		)

		pending.Delete(compName)

		return nil
	}

	for {
		seed, _, hasMore := util.GetArbitraryMapEntry(pending)
		if !hasMore {
			return sorted
		}

		if err := dfs(seed); err != nil {
			panic(err)
		}
	}
}

// Exposes the lifecycle and interaction interface of a component, used for component orchestration.
type NamedComponent struct {
	Component Component
	Name      string
	apiGetter any
}

type componentMapEntry struct {
	comp      Component
	apiGetter any
	deps      sets.Set[string]
}

// Returns an object equivalent to `request` that exists in `componentMap`.
func resolveRequest(
	componentMap map[string]*componentMapEntry,
	request *depRequest,
) (string, Component, any) {
	requestComp, requestApi := request.getNew()
	manifest := requestComp.manifest()

	// already exists, return previous value
	if prev, hasPrev := componentMap[manifest.Name]; hasPrev {
		deps := requestComp.mergeInto(prev.comp)
		// resolve incremental dependencies

		for _, dep := range deps {
			depName, depComp, depApi := resolveRequest(componentMap, dep)
			dep.set(depComp, depApi)
			prev.deps.Insert(depName)
		}

		return manifest.Name, prev.comp, prev.apiGetter
	}

	requestDeps := sets.New[string]()

	// new component; resolve dependencies, init and return the instance we got
	for _, dep := range manifest.Dependencies {
		depName, depComp, depApi := resolveRequest(componentMap, dep)
		dep.set(depComp, depApi)
		requestDeps.Insert(depName)
	}

	componentMap[manifest.Name] = &componentMapEntry{
		comp:      requestComp,
		apiGetter: requestApi,
		deps:      requestDeps,
	}

	return manifest.Name, requestComp, requestApi
}

// Accessor to interact with components by name.
// Use ApiFromMap to get the actual interfaces.
type ApiMap map[string]any

// Converts a NamedComponent slice to an ApiMap.
func NamedComponentsToApiMap(components []NamedComponent) ApiMap {
	return ApiMap(
		iter.CollectMap(iter.Map(
			iter.FromSlice(components),
			func(comp NamedComponent) iter.Pair[string, any] {
				return iter.NewPair(comp.Name, comp.apiGetter)
			},
		)),
	)
}

// Retrieves the interface to interact with a component of a known name.
func ApiFromMap[Api any](apiMap ApiMap, name string) Api {
	return apiMap[name].(func() Api)()
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

const phaseStarted = "Started"

func isPhaseReady(phase string) bool {
	return phase == phaseStarted
}

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

// Lifecycle hooks for a component.
//
// The zero value (nil functions) is a valid default.
type Lifecycle[Args any, Options any, Deps any, State any] struct {
	// Starts the background tasks of a component.
	//
	// `ctx` is canceled when the process starts terminating.
	// All public fields in `state` are available for use.
	Start func(ctx context.Context, args *Args, options *Options, deps *Deps, state *State) error

	// Waits for the component to shut down gracefully.
	//
	// `ctx` is canceled after graceful shutdown times out.
	// All public fields in `state` are available for use.
	Join func(ctx context.Context, args *Args, options *Options, deps *Deps, state *State) error

	// Defines health checks for this component.
	HealthChecks func(*State) HealthChecks
}

// A map of health checks, where each non-nil function returns nil for ready status
// and returns a non-nil error for unready status.
type HealthChecks map[string]func() error

func (impl *componentImpl[Args, Options, Deps, State]) manifest() manifest {
	deps := DepRequests{requests: []*depRequest{}}
	impl.Deps = impl.depsFn(impl.Args, &deps)

	return manifest{
		Name:         impl.name,
		Dependencies: deps.requests,
	}
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

// A dummy component that does nothing, used for `ApiOnly`.
type emptyComponent struct {
	name string
}

func (comp emptyComponent) manifest() manifest {
	return manifest{
		Name:         comp.name,
		Dependencies: []*depRequest{},
	}
}

func (comp emptyComponent) mergeInto(other Component) []*depRequest {
	panic(fmt.Sprintf("component %q is already registered as %T", comp.name, other))
}

func (emptyComponent) AddFlags(*flag.FlagSet) {}

func (emptyComponent) Init(context.Context) error { return nil }

func (emptyComponent) Start(context.Context) error { return nil }

func (emptyComponent) Join(context.Context) error { return nil }

func (emptyComponent) RegisterHealthChecks(
	*healthz.Handler,
	func(name string, err error),
) {
}

// Provides a named component with a different implementation of its API.
// Used for mocking components in integration tests.
func ApiOnly[Api any](name string, api Api) func(*DepRequests) {
	return RequireDep(&declaredImpl[util.Empty, util.Empty, Api]{
		comp: emptyComponent{name: name},
		api: func() Api {
			return api
		},
	})
}
