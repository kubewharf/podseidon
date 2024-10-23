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

package utilflag

import (
	"flag"
	"fmt"
	"reflect"
	"sort"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/optional"
)

// A type of flag that must resolve to one of the known values of type T.
type EnumValue[T any] struct {
	typeName string

	options []string
	resolve map[string]T

	selectedKey   optional.Optional[string]
	selectedValue optional.Optional[T]
}

func _[T any](enum *EnumValue[T]) flag.Value { return enum }

// Constructs a flag that must resolve to one of the values in the map.
func EnumFromMap[T any](resolve map[string]T) *EnumValue[T] {
	options := make([]string, 0, len(resolve))

	for key := range resolve {
		options = append(options, key)
	}

	sort.Strings(options)

	return &EnumValue[T]{
		typeName:      reflect.TypeOf([0]T{}).Elem().Name(),
		options:       options,
		resolve:       resolve,
		selectedKey:   optional.None[string](),
		selectedValue: optional.None[T](),
	}
}

func (enum *EnumValue[T]) TypeName(typeName string) *EnumValue[T] {
	enum.typeName = typeName

	return enum
}

func (enum *EnumValue[T]) With(key string, value T) *EnumValue[T] {
	enum.options = append(enum.options, key)
	enum.resolve[key] = value

	return enum
}

// Assigns a default value for this flag.
func (enum *EnumValue[T]) Default(key string) *DefaultEnumValue[T] {
	enum.selectedKey = optional.Some(key)
	enum.selectedValue = optional.Some(enum.resolve[key])

	return (*DefaultEnumValue[T])(enum)
}

func (enum *EnumValue[T]) String() string {
	return enum.selectedKey.GetOr(`""`)
}

func (enum *EnumValue[T]) Set(input string) error {
	value, hasValue := enum.resolve[input]
	if !hasValue {
		return errors.TagErrorf("NoEnumVariant", "unknown option %q", input)
	}

	enum.selectedKey = optional.Some(input)
	enum.selectedValue = optional.Some(value)

	return nil
}

// Adds the flag to the flag set and returns a box that holds the selected value.
func (enum *EnumValue[T]) Flag(fs *flag.FlagSet, name string, usage string) *optional.Optional[T] {
	usageOptions := ""

	for i, option := range enum.options {
		if i > 0 {
			if i < len(enum.options)-1 {
				usageOptions += ", "
			} else {
				usageOptions += " or "
			}
		}

		usageOptions += fmt.Sprintf("%q", option)
	}

	fs.Var(enum, name, fmt.Sprintf("%s (%s)", usage, usageOptions))

	return &enum.selectedValue
}

// Implement pflag.Value.
func (enum *EnumValue[T]) Type() string {
	return enum.typeName
}

// Similar to EnumValue, but with a defined default.
type DefaultEnumValue[T any] EnumValue[T]

// Adds the flag to the flag set and returns a box that holds the selected value.
func (enum *DefaultEnumValue[T]) Flag(fs *flag.FlagSet, name string, usage string) *T {
	opt := (*EnumValue[T])(enum).Flag(fs, name, usage)
	return opt.GetValueRef()
}
