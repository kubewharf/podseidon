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

package component

import (
	"context"
	"flag"
	"fmt"

	"github.com/kubewharf/podseidon/util/util"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// A dummy component that does nothing, used for `ApiOnly`.
type emptyComponent struct {
	name string
}

func (comp emptyComponent) Name() string {
	return comp.name
}

func (comp emptyComponent) dependencies() []*depRequest{
	return []*depRequest{}
}

func (comp emptyComponent) mergeInto(other Component) []*depRequest {
	panic(fmt.Sprintf("component %q is already registered as %T", comp.name, other))
}

func (emptyComponent) AddFlags(*flag.FlagSet) {}

func (emptyComponent) Init(context.Context) error { return nil }

func (emptyComponent) Start(context.Context) error { return nil }

func (emptyComponent) Join(context.Context) error { return nil }

func (emptyComponent) RegisterHealthChecks(
	*healthz.Handler,
	func(name string, err error),
) {
}

// Provides a named component with a different implementation of its API.
// Used for mocking components in integration tests.
func ApiOnly[Api any](name string, api Api) func(*DepRequests) {
	return RequireDep(&declaredImpl[util.Empty, util.Empty, Api]{
		comp: emptyComponent{name: name},
		api: func() Api {
			return api
		},
	})
}
