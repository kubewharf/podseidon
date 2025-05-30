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
	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
)

var Provide = component.RequireDeps(
	component.RequireDep(ProvideLogging()),
	component.RequireDep(ProvideMetrics()),
)

type Observer struct {
	StartReconcile o11y.ObserveScopeFunc[StartReconcile]
	EndReconcile   o11y.ObserveFunc[EndReconcile]
	QueueLength    o11y.MonitorFunc[QueueLength, int]
}

func (Observer) ComponentName() string { return "worker" }

func (observer Observer) Join(other Observer) Observer { return o11y.ReflectJoin(observer, other) }

type StartReconcile struct {
	WorkerName string
}

type EndReconcile struct {
	Err error
}

type QueueLength struct {
	WorkerName string
}
