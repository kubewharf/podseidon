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

// Observability interfaces.
package o11y

import (
	"context"
	"reflect"

	"github.com/kubewharf/podseidon/util/util"
)

// An observer that can be merged with other implementations.
type Observer[Self any] interface {
	// Component name of this observer, to be prefixed by "observer-".
	//
	// This method must be invocable on the zero value of the type.
	ComponentName() string

	// Returns a new observer of the same struct that
	// calls the observe functions on both the receiver and `other`.
	Join(other Self) Self
}

// Observes an event.
type ObserveFunc[Arg any] func(ctx context.Context, arg Arg)

func (fn ObserveFunc[Arg]) Join(other ObserveFunc[Arg]) ObserveFunc[Arg] {
	if fn == nil {
		if other == nil {
			return func(context.Context, Arg) {}
		}

		return other
	}

	if other == nil {
		return fn
	}

	return func(ctx context.Context, arg Arg) {
		fn(ctx, arg)
		other(ctx, arg)
	}
}

// Observes the start of a scope.
//
// May return a context that gets canceled when the scope completes.
type ObserveScopeFunc[Arg any] func(ctx context.Context, arg Arg) (context.Context, context.CancelFunc)

func (fn ObserveScopeFunc[Arg]) Join(other ObserveScopeFunc[Arg]) ObserveScopeFunc[Arg] {
	if fn == nil {
		if other == nil {
			return func(ctx context.Context, _ Arg) (context.Context, context.CancelFunc) {
				return ctx, util.NoOp
			}
		}

		return other
	}

	if other == nil {
		return fn
	}

	return func(ctx context.Context, arg Arg) (context.Context, context.CancelFunc) {
		ctx, cancelFunc1 := fn(ctx, arg)
		ctx, cancelFunc2 := other(ctx, arg)

		return ctx, func() {
			cancelFunc1()
			cancelFunc2()
		}
	}
}

// Continuously monitor a value until the context is done.
//
// The function blocks until the context has been canceled or until the monitor no longer blocks.
type MonitorFunc[Arg any, Sample any] func(ctx context.Context, arg Arg, getter func() Sample)

func (fn MonitorFunc[Arg, Sample]) Join(other MonitorFunc[Arg, Sample]) MonitorFunc[Arg, Sample] {
	if fn == nil {
		if other == nil {
			return func(_ context.Context, _ Arg, _ func() Sample) {}
		}

		return other
	}

	if other == nil {
		return fn
	}

	return func(ctx context.Context, event Arg, getter func() Sample) {
		doneCh := make(chan util.Empty, 1)
		go func() {
			other(ctx, event, getter)
			close(doneCh)
		}()
		fn(ctx, event, getter)
		<-doneCh
	}
}

// Joins two observer structs of the same type.
//
// `ObsT` must be a struct type that only contains exported fields of the following types:
// `ObserveFunc`, `ObserveScopeFunc`, `MonitorFunc`, and or another `Observer` implementor.
func ReflectJoin[ObsT Observer[ObsT]](left, right ObsT) ObsT {
	ty := util.Type[ObsT]()

	output := reflect.New(ty)

	for fieldIndex := range ty.NumField() {
		leftValue := reflect.ValueOf(left).Field(fieldIndex)
		rightValue := reflect.ValueOf(right).Field(fieldIndex)
		outputValue := leftValue.MethodByName("Join").Call([]reflect.Value{rightValue})[0]
		output.Elem().Field(fieldIndex).Set(outputValue)
	}

	return output.Elem().Interface().(ObsT)
}

// Returns a no-op observer struct.
//
// `ObsT` must follow the same rules as `ReflectJoin`.
func ReflectNoop[ObsT Observer[ObsT]]() ObsT {
	return ReflectJoin(util.Zero[ObsT](), util.Zero[ObsT]())
}

// Ensures all functions in the struct are callable.
func ReflectPopulate[ObsT Observer[ObsT]](obs ObsT) ObsT {
	return ReflectJoin(obs, util.Zero[ObsT]())
}
