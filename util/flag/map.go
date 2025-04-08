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

package utilflag

import (
	"flag"
	"fmt"
	"strings"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/util"
)

var ErrNoEqual = errors.TagErrorf("ErrNoEqual", "map options expect values in the form k1=v1,k2=v2,k3=v3")

type Parser[T any] func(string) (T, error)

var StringParser Parser[string] = func(s string) (string, error) { return s, nil }

// Registers a flag that accepts a comma-separated list of equal-delimited map entries.
// If the same key is specified multiple times, the last occurrence wins.
func Map[K comparable, V any](
	fs *flag.FlagSet,
	name string,
	defaultValue map[K]V,
	usage string,
	keyParser Parser[K],
	valueParser Parser[V],
) *map[K]V {
	value := &mapValue[K, V]{value: defaultValue, keyParser: keyParser, valueParser: valueParser}
	fs.Var(value, name, usage)

	return &value.value
}

type mapValue[K comparable, V any] struct {
	value       map[K]V
	keyParser   Parser[K]
	valueParser Parser[V]
}

func (*mapValue[K, V]) Type() string {
	return fmt.Sprintf("map[%s]%s", util.TypeName[K](), util.TypeName[V]())
}

func (mv *mapValue[K, V]) String() string {
	var output strings.Builder

	for entryKey, entryValue := range mv.value {
		if output.Len() == 0 {
			_, _ = output.WriteRune(',')
		}

		// strings.Builder is infallible
		_, _ = output.WriteString(fmt.Sprint(entryKey))
		_, _ = output.WriteRune('=')
		_, _ = output.WriteString(fmt.Sprint(entryValue))
	}

	return output.String()
}

func (mv *mapValue[K, V]) Set(input string) error {
	if input == "" {
		mv.value = map[K]V{}
		return nil
	}

	entries := strings.Split(input, ",")
	mv.value = make(map[K]V, len(entries))

	for _, entry := range entries {
		eq := strings.IndexRune(entry, '=')
		if eq == -1 {
			return ErrNoEqual
		}

		entryKey, err := mv.keyParser(entry[:eq])
		if err != nil {
			return err
		}

		entryValue, err := mv.valueParser(entry[eq+1:])
		if err != nil {
			return err
		}

		mv.value[entryKey] = entryValue
	}

	return nil
}
