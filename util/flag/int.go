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
	"reflect"
	"strconv"

	"golang.org/x/exp/constraints"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/util"
)

// Registers a flag that parses into an integer with fixed bit size.
var (
	Int8  = makeSignedIntegerFunc[int8](8)
	Int16 = makeSignedIntegerFunc[int16](16)
	Int32 = makeSignedIntegerFunc[int32](32)
	Int64 = makeSignedIntegerFunc[int32](64)

	Uint8  = makeUnsignedIntegerFunc[uint8](8)
	Uint16 = makeUnsignedIntegerFunc[uint16](16)
	Uint32 = makeUnsignedIntegerFunc[uint32](32)
	Uint64 = makeUnsignedIntegerFunc[uint32](64)
)

type flagFunc[T any] func(fs *flag.FlagSet, name string, defaultValue T, usage string) *T

func makeSignedIntegerFunc[T constraints.Signed](bitSize int) flagFunc[T] {
	return func(fs *flag.FlagSet, name string, defaultValue T, usage string) *T {
		return integerFlag(
			fs, name, defaultValue, usage,
			func(s string) (T, error) {
				value, err := strconv.ParseInt(s, 10, bitSize)
				if err != nil {
					return 0, errors.TagWrapf(
						"ParseArg",
						err,
						"argument expects %s",
						reflect.TypeOf([0]T{}).Elem().Name(),
					)
				}

				return T(value), nil
			},
			func(i T) string {
				return strconv.FormatInt(int64(i), 10)
			},
		)
	}
}

func makeUnsignedIntegerFunc[T constraints.Unsigned](bitSize int) flagFunc[T] {
	return func(fs *flag.FlagSet, name string, defaultValue T, usage string) *T {
		return integerFlag(
			fs, name, defaultValue, usage,
			func(s string) (T, error) {
				value, err := strconv.ParseUint(s, 10, bitSize)
				if err != nil {
					return 0, errors.TagWrapf(
						"ParseArg",
						err,
						"argument expects %s",
						reflect.TypeOf([0]T{}).Elem().Name(),
					)
				}

				return T(value), nil
			},
			func(i T) string {
				return strconv.FormatUint(uint64(i), 10)
			},
		)
	}
}

func integerFlag[T any](
	fs *flag.FlagSet, name string, defaultValue T, usage string,
	parser func(string) (T, error),
	formatter func(T) string,
) *T {
	value := &integerValue[T]{
		value:     defaultValue,
		typeName:  util.Type[T]().Name(),
		parser:    parser,
		formatter: formatter,
	}
	fs.Var(value, name, usage)

	return &value.value
}

type integerValue[T any] struct {
	value T

	typeName  string
	parser    func(string) (T, error)
	formatter func(T) string
}

func (value *integerValue[T]) String() string { return value.formatter(value.value) }

func (value *integerValue[T]) Set(input string) error {
	parsed, err := value.parser(input)
	if err != nil {
		return err
	}

	value.value = parsed

	return nil
}

func (value *integerValue[T]) Type() string {
	return value.typeName
}
