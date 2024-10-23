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

	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubewharf/podseidon/util/errors"
)

type labelSelectorValue struct {
	selector labels.Selector
}

func (value *labelSelectorValue) String() string {
	return value.selector.String()
}

func (value *labelSelectorValue) Set(input string) error {
	selector, err := labels.Parse(input)
	if err != nil {
		return errors.TagWrapf("ParseLabelSelector", err, "invalid label seoector")
	}

	value.selector = selector

	return nil
}

// Registers a flag that parses into a LabelSelector using the labels.Parse syntax
// and resolves into labels.Everything if unspecified.
func LabelSelectorEverything(fs *flag.FlagSet, name string, usage string) *labels.Selector {
	return LabelSelector(fs, name, labels.Everything(), usage)
}

// Registers a flag that parses into a LabelSelector using the labels.Parse syntax
// and resolves into labels.Nothing if unspecified.
func LabelSelectorNothing(fs *flag.FlagSet, name string, usage string) *labels.Selector {
	return LabelSelector(fs, name, labels.Nothing(), usage)
}

// Registers a flag that parses into a LabelSelector using the labels.Parse syntax.
func LabelSelector(
	fs *flag.FlagSet,
	name string,
	defaultValue labels.Selector,
	usage string,
) *labels.Selector {
	value := &labelSelectorValue{selector: defaultValue}

	fs.Var(value, name, usage)

	return &value.selector
}
