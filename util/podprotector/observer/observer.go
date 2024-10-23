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

package observer

import (
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/util"
)

var ProvideInformer = component.RequireDeps(
	component.RequireDep(ProvideInformerLogging()),
	component.RequireDep(ProvideInformerMetrics()),
)

type IndexedInformerObserver struct {
	StartHandleEvent o11y.ObserveScopeFunc[types.NamespacedName]
	EndHandleEvent   o11y.ObserveFunc[util.Empty]
	HandleEventError o11y.ObserveFunc[HandleEventError]
}

func (IndexedInformerObserver) ComponentName() string { return "ppr-informer" }

func (observer IndexedInformerObserver) Join(
	other IndexedInformerObserver,
) IndexedInformerObserver {
	return o11y.ReflectJoin(observer, other)
}

type HandleEventError struct {
	Namespace string
	Name      string
	Err       error
}
