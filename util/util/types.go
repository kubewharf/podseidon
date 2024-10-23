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

package util

import "reflect"

type Empty = struct{}

// Returns a zero value to be initialized later.
func Zero[T any]() T {
	var t T
	return t
}

func IsZero[T comparable](value T) bool {
	return value == Zero[T]()
}

func Type[T any]() reflect.Type {
	return reflect.TypeOf([0]T{}).Elem()
}

func TypeName[T any]() string {
	return Type[T]().Name()
}

func Identity[T any](t T) T { return t }

func NoOp() {}
