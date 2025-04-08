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

	"github.com/spf13/pflag"
)

// Special string used to construct a flag in a subset that expect to be equal to the parent set name.
const EmptyFlagName string = "zzz-empty-flag-name"

func AddSubset(super *pflag.FlagSet, prefix string, subsetFn func(*flag.FlagSet)) {
	compFs := flag.FlagSet{Usage: nil}
	subsetFn(&compFs)

	compFs.VisitAll(func(fl *flag.Flag) {
		if fl.Name == EmptyFlagName {
			fl.Name = prefix
		} else {
			fl.Name = fmt.Sprintf("%s-%s", prefix, fl.Name)
		}

		super.AddGoFlag(fl)
	})
}
