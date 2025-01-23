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
// the same instance of the component is used (see the Merging section).
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
// The `depsFn` function declares the dependencies of a component
// and obtains such handles from the framework from a `DepRequests`.
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
//
// A component is typically declared by calling [Declare],
// which accepts functions specifying how to construct `Options`, `Deps`, `State` and `Api`
// as well as injecting different lifecycles hooks for the component.
// The result of [Declare] is used to lookup the dependency by name during `depsFn`
// and also tells the framework how to construct the component if it does not already exist.
//
// # Merging
//
// If the same component name is provided (through `depsFn` or the main `cmd.Run`) repeatedly,
// only the first instance is used.
// The additional setup (to be represented in `Args` and `Deps`) in latter provisions
// may be merged into the first instance by calling `WithMergeFn`.
//
// # Runtime disabling
//
// Components required from the main `cmd.Run`
// may be enabled/disabled by the user at runtime through CLI options.
// Such components may call [DeclaredCtor.WithRequestedFromMainFn]
// to translate options into this enabled/disabled state.
// Note that the components would still remain enabled if they are directly requested from other components.
//
// Disabled components do not get `init`ed, and their lifecycle hooks are never called.
// Components only (transitively) requested by disabled components are also disabled.
//
// # Multiplexing
//
// The Mux API allows components to request an interface dependency without concerning about its implementations.
// The actual implementations are exposed to the main `cmd.Run` by calling [DeclareMuxImpl],
// and selected by the user at runtime through CLI options.
// Non-selected mux implementations and their transitive dependencies are automatically disabled.
package component

import (
	"context"
	"flag"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
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
	// Returns a string that identifies this component for dependency resolution.
	Name() string
	// Returns the list of requested dependencies.
	dependencies() []*depRequest

	// Updates the other component upon name duplication.
	// Returns a list of incremental dependency requests, which could duplicate with the existing ones.
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
	// Registers health check handlers.
	RegisterHealthChecks(handler *healthz.Handler, onFail func(name string, err error))
	// Waits for the component to shut down gracefully.
	// Should return when ctx expires.
	Join(ctx context.Context) error

	// Returns whether the component should still be enabled if it is only requested from main.
	//
	// This method is only called if it was requested directly from [ResolveList].
	isRequestedFromMain() bool
	// Returns a list of dependency names to unrequest.
	//
	// The same name may be passed multiple times if it was requested multiple times.
	unrequestDeps() []string
}

// Dummy requester name for components directly requested from the [ResolveList] call.
const mainRequester string = "_main"

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
					"Components of types %T and %T declared the same name %q with incompatible APIs %T and %v",
					comp, base, comp.Name(), util.Type[Api]().Out(0), reflect.TypeOf(api).Out(0),
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
		_, comp, api := resolveRequest(componentMap, request, mainRequester)
		request.set(comp, api)
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
			NamedComponent{
				Name:       compName,
				Component:  entry.comp,
				apiGetter:  entry.apiGetter,
				requesters: entry.requesters,
				deps:       entry.deps,
			},
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

	requesters map[string]int
	deps       map[string]int
}

type componentMapEntry struct {
	comp      Component
	apiGetter any

	requesters map[string]int // requester name -> number of times requested
	deps       map[string]int // dependency name -> number of times requested
}

// Returns an object equivalent to `request` that exists in `componentMap`.
func resolveRequest(
	componentMap map[string]*componentMapEntry,
	request *depRequest,
	requester string,
) (string, Component, any) {
	requestComp, requestApi := request.getNew()
	requestName := requestComp.Name()

	// already exists, return previous value
	if prev, hasPrev := componentMap[requestName]; hasPrev {
		if prev == nil {
			panic(fmt.Sprintf("cyclic dependency detected: %q", requestName))
		}

		deps := requestComp.mergeInto(prev.comp)

		// resolve incremental dependencies
		for _, dep := range deps {
			depName, depComp, depApi := resolveRequest(componentMap, dep, requestName)
			dep.set(depComp, depApi)

			prev.deps[depName]++
		}

		prev.requesters[requester]++

		return requestName, prev.comp, prev.apiGetter
	}

	// if dependency resolution recurses to the same request, ensure `hasPrev` above is true to detect cycles
	componentMap[requestName] = nil

	requestDeps := sets.New[string]()
	requestTimes := map[string]int{}

	// new component; resolve dependencies, init and return the instance we got
	for _, dep := range requestComp.dependencies() {
		depName, depComp, depApi := resolveRequest(componentMap, dep, requestName)
		dep.set(depComp, depApi)

		requestDeps.Insert(depName)

		requestTimes[depName]++
	}

	componentMap[requestName] = &componentMapEntry{
		comp:       requestComp,
		apiGetter:  requestApi,
		requesters: map[string]int{requester: 1},
		deps:       requestTimes,
	}

	return requestName, requestComp, requestApi
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

func TrimNonRequested(components []NamedComponent) []NamedComponent {
	componentMap := util.SliceToMap(components, func(comp NamedComponent) string { return comp.Name })

	removeDirectDeps(components, componentMap)

	for {
		hasChanged := shakeTreeOnce(componentMap)
		if !hasChanged {
			break
		}
	}

	output := make([]NamedComponent, 0, len(componentMap))

	for _, comp := range components {
		if _, exists := componentMap[comp.Name]; exists {
			klog.InfoS("component enabled", "comp", comp.Name, "requesters", iter.MapKeys(comp.requesters).CollectSlice())
			output = append(output, comp)
		}
	}

	return output
}

func removeDirectDeps(components []NamedComponent, componentMap map[string]NamedComponent) {
	for _, comp := range components {
		if _, hasMain := comp.requesters[mainRequester]; hasMain {
			if !comp.Component.isRequestedFromMain() {
				delete(comp.requesters, mainRequester)
			}
		}

		for _, dep := range comp.Component.unrequestDeps() {
			depRequests := comp.deps[dep]

			if depRequests != componentMap[dep].requesters[comp.Name] {
				panic("out-edge and in-edge value are out of sync")
			}

			if depRequests <= 0 {
				panic("cannot unrequest dependency that was not requested")
			}

			newCount := depRequests - 1
			if newCount == 0 {
				delete(comp.deps, dep)
				delete(componentMap[dep].requesters, comp.Name)
			} else {
				comp.deps[dep] = newCount
				componentMap[dep].requesters[comp.Name] = newCount
			}
		}
	}
}

func shakeTreeOnce(componentMap map[string]NamedComponent) bool {
	hasChanged := false

	for compName, comp := range componentMap {
		if iter.Sum(iter.MapValues(comp.requesters)) == 0 {
			hasChanged = true

			for dep := range comp.deps {
				delete(componentMap[dep].requesters, compName)
			}

			klog.InfoS("component disabled", "comp", compName)
			delete(componentMap, compName)
		}
	}

	return hasChanged
}
