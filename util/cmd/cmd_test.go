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

package cmd_test

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	"github.com/kubewharf/podseidon/util/cmd"
	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/util"
)

var TestingT = component.Declare(
	func(_ *testing.T) string { return "testing" },
	func(_ *testing.T, _ *flag.FlagSet) util.Empty { return util.Empty{} },
	func(_ *testing.T, _ *component.DepRequests) util.Empty { return util.Empty{} },
	func(_ context.Context, _ *testing.T, _ util.Empty, _ util.Empty) (*util.Empty, error) {
		return &util.Empty{}, nil
	},
	component.Lifecycle[*testing.T, util.Empty, util.Empty, util.Empty]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(d *component.Data[*testing.T, util.Empty, util.Empty, util.Empty]) *testing.T { return d.Args },
)

var Counter = component.Declare[CounterArgs, CounterOptions, CounterDeps, CounterState, CounterApi](
	func(args CounterArgs) string { return args.Name },
	func(_ CounterArgs, fs *flag.FlagSet) CounterOptions {
		return CounterOptions{
			Multiplier:         fs.Int("multiplier", 10, ""),
			ExpectMutableField: fs.String("expect", "", ""),
		}
	},
	func(_ CounterArgs, reqs *component.DepRequests) CounterDeps {
		return CounterDeps{Testing: component.DepPtr(reqs, TestingT(nil))}
	},
	func(_ context.Context, _ CounterArgs, _ CounterOptions, _ CounterDeps) (*CounterState, error) {
		return &CounterState{mutableField: []string{}}, nil
	},
	component.Lifecycle[CounterArgs, CounterOptions, CounterDeps, CounterState]{
		Start: func(_ context.Context, _ *CounterArgs, options *CounterOptions, deps *CounterDeps, state *CounterState) error {
			assert.Equalf(
				deps.Testing.Get(),
				*options.ExpectMutableField,
				strings.Join(state.mutableField, ","),
				"start is called strictly after all init",
			)
			return nil
		},
		Join:         nil,
		HealthChecks: nil,
	},
	func(d *component.Data[CounterArgs, CounterOptions, CounterDeps, CounterState]) CounterApi {
		return CounterApi{multiplier: *d.Options.Multiplier, state: d.State}
	},
)

type CounterArgs struct {
	Name string
}

type CounterOptions struct {
	Multiplier         *int
	ExpectMutableField *string
}

type CounterDeps struct {
	Testing component.Dep[*testing.T]
}

type CounterState struct {
	mutableField []string
}

type CounterApi struct {
	multiplier int
	state      *CounterState
}

func (api CounterApi) Update(value int) {
	api.state.mutableField = append(api.state.mutableField, fmt.Sprint(api.multiplier*value))
}

// the pointer is intentionally added to trigger panic when it is unused despite expected to be used.
func Mutator(mutatorArgs MutatorArgs, counterDepName *string, mutatorDepNames *[]string) component.Declared[util.Empty] {
	return component.Declare(
		func(args MutatorArgs) string { return args.Name },
		func(_ MutatorArgs, fs *flag.FlagSet) MutatorOptions {
			return MutatorOptions{Value: fs.Int("value", 0, "")}
		},
		func(_ MutatorArgs, reqs *component.DepRequests) MutatorDeps {
			counterDep := component.DepPtr(reqs, Counter(CounterArgs{Name: *counterDepName}))

			for _, mutatorDepName := range *mutatorDepNames {
				component.DepPtr(reqs, Mutator(MutatorArgs{Name: mutatorDepName}, nil, nil))
			}

			return MutatorDeps{
				Counter: counterDep,
			}
		},
		func(_ context.Context, _ MutatorArgs, options MutatorOptions, deps MutatorDeps) (*util.Empty, error) {
			deps.Counter.Get().Update(*options.Value)
			return &util.Empty{}, nil
		},
		component.Lifecycle[MutatorArgs, MutatorOptions, MutatorDeps, util.Empty]{
			Start:        nil,
			Join:         nil,
			HealthChecks: nil,
		},
		func(*component.Data[MutatorArgs, MutatorOptions, MutatorDeps, util.Empty]) util.Empty {
			return util.Empty{}
		},
	)(mutatorArgs)
}

type MutatorArgs struct {
	Name string
}

type MutatorOptions struct {
	Value *int
}

type MutatorDeps struct {
	Counter component.Dep[CounterApi]
}

func TestDeduplication(t *testing.T) {
	t.Parallel()

	cmd.MockStartupWithCliArgs(
		context.Background(),
		[]func(*component.DepRequests){
			component.RequireDep(TestingT(t)),
			component.RequireDep(Mutator(MutatorArgs{
				Name: "mutator1",
			}, ptr.To("counter1"), ptr.To[[]string](nil))),
			component.RequireDep(Mutator(MutatorArgs{
				Name: "mutator2",
			}, ptr.To("counter1"), ptr.To([]string{"mutator1"}))),
			component.RequireDep(Mutator(MutatorArgs{
				Name: "mutator3",
			}, ptr.To("counter2"), ptr.To([]string{"mutator1"}))),
		},
		[]string{
			"--counter1-multiplier=100",
			"--counter1-expect=100,200",
			"--counter2-expect=30",
			"--mutator1-value=1",
			"--mutator2-value=2",
			"--mutator3-value=3",
		},
	)
}

func Cyclic(name string, deps ...func() component.Declared[util.Empty]) component.Declared[util.Empty] {
	return component.Declare(
		func(_ util.Empty) string { return name },
		func(_ util.Empty, _ *flag.FlagSet) util.Empty { return util.Empty{} },
		func(_ util.Empty, reqs *component.DepRequests) util.Empty {
			for _, dep := range deps {
				component.DepPtr(reqs, dep())
			}

			return util.Empty{}
		},
		func(_ context.Context, _ util.Empty, _ util.Empty, _ util.Empty) (*util.Empty, error) {
			return &util.Empty{}, nil
		},
		component.Lifecycle[util.Empty, util.Empty, util.Empty, util.Empty]{
			Start:        nil,
			Join:         nil,
			HealthChecks: nil,
		},
		func(d *component.Data[util.Empty, util.Empty, util.Empty, util.Empty]) util.Empty { return d.Args },
	)(util.Empty{})
}

func Cyclic1() component.Declared[util.Empty] { return Cyclic("cyclic1", Cyclic2) }
func Cyclic2() component.Declared[util.Empty] { return Cyclic("cyclic2", Cyclic1) }

func TestCyclicDependency(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, `cyclic dependency detected: "cyclic1"`, func() {
		cmd.MockStartupWithCliArgs(
			context.Background(),
			[]func(*component.DepRequests){
				component.RequireDep(Cyclic1()),
				component.RequireDep(Cyclic2()),
			},
			[]string{},
		)
	})
}

type MuxInterface interface{ Which() int }

var RequestMuxInterface = component.ProvideMux[MuxInterface]("select", "usage")

type MuxImpl1 struct{}

func (MuxImpl1) Which() int { return 1 }

type MuxImpl2 struct{}

func (MuxImpl2) Which() int { return 2 }

func declareMuxImpl(implName string, impl MuxInterface, isInited *int) func(*component.DepRequests) {
	return component.DeclareMuxImpl(
		"select", implName,
		func(*int, *flag.FlagSet) util.Empty { return util.Empty{} },
		func(*int, *component.DepRequests) util.Empty { return util.Empty{} },
		func(_ context.Context, isInited *int, _ util.Empty, _ util.Empty) (*util.Empty, error) {
			*isInited |= impl.Which()
			return &util.Empty{}, nil
		},
		util.Zero[component.Lifecycle[*int, util.Empty, util.Empty, util.Empty]](),
		func(*component.Data[*int, util.Empty, util.Empty, util.Empty]) MuxInterface { return impl },
	)(isInited)
}

type muxInterfaceUserArgs struct {
	initWrite  *int
	startWrite *int
}

var muxInterfaceUser = component.Declare(
	func(muxInterfaceUserArgs) string { return "user" },
	func(muxInterfaceUserArgs, *flag.FlagSet) util.Empty { return util.Empty{} },
	func(_ muxInterfaceUserArgs, reqs *component.DepRequests) component.Dep[MuxInterface] {
		return component.DepPtr(reqs, RequestMuxInterface())
	},
	func(_ context.Context, args muxInterfaceUserArgs, _ util.Empty, dep component.Dep[MuxInterface]) (*util.Empty, error) {
		*args.initWrite = dep.Get().Which()
		return &util.Empty{}, nil
	},
	component.Lifecycle[muxInterfaceUserArgs, util.Empty, component.Dep[MuxInterface], util.Empty]{
		Start: func(_ context.Context, args *muxInterfaceUserArgs, _ *util.Empty, dep *component.Dep[MuxInterface], _ *util.Empty) error {
			*args.startWrite = (*dep).Get().Which()
			return nil
		},
		Join:         nil,
		HealthChecks: nil,
	},
	func(*component.Data[muxInterfaceUserArgs, util.Empty, component.Dep[MuxInterface], util.Empty]) util.Empty {
		return util.Empty{}
	},
)

func TestMuxResolveDefault(t *testing.T) {
	t.Parallel()

	initWrite := 0
	startWrite := 0
	isImplInited := 0

	cmd.MockStartupWithCliArgs(context.Background(), []func(*component.DepRequests){
		declareMuxImpl("impl1", MuxImpl1{}, &isImplInited),
		declareMuxImpl("impl2", MuxImpl2{}, &isImplInited),
		component.RequireDep(muxInterfaceUser(muxInterfaceUserArgs{initWrite: &initWrite, startWrite: &startWrite})),
	}, []string{})

	assert.Equal(t, 1, initWrite)
	assert.Equal(t, 1, startWrite)
	assert.Equal(t, 1, isImplInited)
}

func TestMuxResolveSpecified(t *testing.T) {
	t.Parallel()

	initWrite := 0
	startWrite := 0
	isImplInited := 0

	cmd.MockStartupWithCliArgs(context.Background(), []func(*component.DepRequests){
		declareMuxImpl("impl1", MuxImpl1{}, &isImplInited),
		declareMuxImpl("impl2", MuxImpl2{}, &isImplInited),
		component.RequireDep(muxInterfaceUser(muxInterfaceUserArgs{initWrite: &initWrite, startWrite: &startWrite})),
	}, []string{"--select=impl2"})

	assert.Equal(t, 2, initWrite)
	assert.Equal(t, 2, startWrite)
	assert.Equal(t, 2, isImplInited)
}

func TestMuxInvalidOption(t *testing.T) {
	t.Parallel()

	assert.PanicsWithError(t, `invalid argument "nosuchimpl" for "--select" flag: unknown option "nosuchimpl"`, func() {
		cmd.MockStartupWithCliArgs(context.Background(), []func(*component.DepRequests){
			declareMuxImpl("impl1", MuxImpl1{}, new(int)),
			declareMuxImpl("impl2", MuxImpl2{}, new(int)),
			component.RequireDep(muxInterfaceUser(muxInterfaceUserArgs{initWrite: new(int), startWrite: new(int)})),
		}, []string{"--select=nosuchimpl"})
	})
}
