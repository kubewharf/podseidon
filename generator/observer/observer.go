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
	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/generator/resource"
)

var Provide = component.RequireDeps(
	component.RequireDep(ProvideLogging()),
	component.RequireDep(ProvideMetrics()),
)

type Observer struct {
	InterpretProtectors o11y.ObserveFunc[InterpretProtectors]

	StartReconcile o11y.ObserveScopeFunc[StartReconcile]
	EndReconcile   o11y.ObserveFunc[EndReconcile]

	DanglingProtector    o11y.ObserveFunc[*podseidonv1a1.PodProtector]
	CreateProtector      o11y.ObserveScopeFunc[util.Empty]
	SyncProtector        o11y.ObserveScopeFunc[*podseidonv1a1.PodProtector]
	DeleteProtector      o11y.ObserveScopeFunc[*podseidonv1a1.PodProtector]
	CleanSourceFinalizer o11y.ObserveScopeFunc[StartReconcile]
}

func (Observer) ComponentName() string { return "generator" }

func (observer Observer) Join(other Observer) Observer { return o11y.ReflectJoin(observer, other) }

type InterpretProtectors struct {
	Group     string
	Version   string
	Resource  string
	Kind      string
	Namespace string
	Name      string

	RequiredProtectors []resource.RequiredProtector

	// Short strings describing the justification for the selection of protectors
	Decisions []string
}

type StartReconcile struct {
	Group     string
	Version   string
	Resource  string
	Kind      string
	Namespace string
	Name      string
}

type EndReconcile struct {
	PprName string
	Action  Action
	Err     error
}

type Action string

const (
	ActionNeitherObjectExists Action = "NeitherObjectExists"
	ActionCreatingProtector   Action = "CreatingProtector"
	ActionNoPprNeeded         Action = "NoPprNeeded"
	ActionDanglingProtector   Action = "DanglingProtector"
	ActionSyncProtector       Action = "SyncProtector"
	ActionDeleteProtector     Action = "DeleteProtector"
	ActionError               Action = "Error"
)
