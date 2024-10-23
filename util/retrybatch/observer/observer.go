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
	component.RequireDep(ProvideMetrics()),
)

type Observer struct {
	StartSubmit o11y.ObserveScopeFunc[StartSubmit]
	EndSubmit   o11y.ObserveFunc[EndSubmit]

	StartBatch o11y.ObserveScopeFunc[StartBatch]
	EndBatch   o11y.ObserveFunc[EndBatch]

	StartExecute o11y.ObserveScopeFunc[StartExecute]
	EndExecute   o11y.ObserveFunc[EndExecute]

	InFlightKeys o11y.MonitorFunc[InFlightKeys, int]
}

func (Observer) ComponentName() string { return "retrybatch" }

func (observer Observer) Join(other Observer) Observer { return o11y.ReflectJoin(observer, other) }

type StartSubmit struct {
	PoolName string
}

type EndSubmit struct {
	RetryCount int
	Err        error
}

type StartBatch struct {
	PoolName string
}

type EndBatch struct {
	RetryCount int
}

type StartExecute struct {
	PoolName  string
	BatchSize int
}

type EndExecute struct {
	ExecuteResultVariant string
	Err                  error
}

type InFlightKeys struct {
	PoolName string
}
