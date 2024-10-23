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
	"strings"
)

// Registers a flag that accepts a comma-separated list of strings.
func StringSlice(fs *flag.FlagSet, name string, defaultValue []string, usage string) *[]string {
	value := &stringSliceValue{value: defaultValue}
	fs.Var(value, name, usage)

	return &value.value
}

type stringSliceValue struct {
	value []string
}

func (value *stringSliceValue) String() string { return strings.Join(value.value, ",") }

func (value *stringSliceValue) Set(input string) error {
	if input == "" {
		value.value = []string{}
	} else {
		value.value = strings.Split(input, ",")
	}

	return nil
}
