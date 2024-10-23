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

package kube

import (
	"context"
	"flag"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	"github.com/kubewharf/podseidon/util/component"
)

// Request an event recorder for objects in the specified cluster.
// Exposes the EventRecorder directly.
var NewEventRecorder = component.Declare(
	func(args EventRecorderArgs) string { return fmt.Sprintf("event-recorder-%s", args.Component) },
	func(EventRecorderArgs, *flag.FlagSet) EventRecorderOptions { return EventRecorderOptions{} },
	func(args EventRecorderArgs, requests *component.DepRequests) EventRecorderDeps {
		return EventRecorderDeps{
			config: component.DepPtr(
				requests,
				NewClient(ClientArgs{ClusterName: args.ClusterName}),
			),
		}
	},
	func(_ context.Context, args EventRecorderArgs, _ EventRecorderOptions, deps EventRecorderDeps) (*EventRecorderState, error) {
		broadcaster := record.NewBroadcaster()
		broadcaster.StartStructuredLogging(klog.Level(3))
		broadcaster.StartRecordingToSink(
			&corev1client.EventSinkImpl{
				Interface: deps.config.Get().
					NativeClientSet().
					CoreV1().
					Events(deps.config.Get().TargetNamespace()),
			},
		)

		recorder := broadcaster.NewRecorder(
			scheme.Scheme,
			corev1.EventSource{Component: args.Component, Host: ""},
		)

		return &EventRecorderState{recorder: recorder}, nil
	},
	component.Lifecycle[EventRecorderArgs, EventRecorderOptions, EventRecorderDeps, EventRecorderState]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(d *component.Data[EventRecorderArgs, EventRecorderOptions, EventRecorderDeps, EventRecorderState]) record.EventRecorder {
		return d.State.recorder
	},
)

type EventRecorderArgs struct {
	ClusterName ClusterName
	Component   string
}

type EventRecorderOptions struct{}

type EventRecorderDeps struct {
	config component.Dep[*Client]
}

type EventRecorderState struct {
	recorder record.EventRecorder
}
