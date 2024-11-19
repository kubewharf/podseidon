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

package monitor_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/cmd"
	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/generator/monitor"
	"github.com/kubewharf/podseidon/generator/observer"
)

func TestMonitor(t *testing.T) {
	t.Parallel()

	setup := setup()
	//nolint:exhaustruct
	setup.Step(t, Step{
		Expect: observer.MonitorWorkloads{},
	})
	//nolint:exhaustruct
	setup.Step(t, Step{
		Create: []*podseidonv1a1.PodProtector{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "1",
				},
				Spec: podseidonv1a1.PodProtectorSpec{
					MinAvailable: 10,
				},
				Status: podseidonv1a1.PodProtectorStatus{
					Summary: podseidonv1a1.PodProtectorStatusSummary{
						Total:               13,
						AggregatedAvailable: 8,
						EstimatedAvailable:  4,
						MaxLatencyMillis:    300,
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "2",
				},
				Spec: podseidonv1a1.PodProtectorSpec{
					MinAvailable: 100,
				},
				Status: podseidonv1a1.PodProtectorStatus{
					Summary: podseidonv1a1.PodProtectorStatusSummary{
						Total:               200,
						AggregatedAvailable: 150,
						EstimatedAvailable:  140,
						MaxLatencyMillis:    70,
					},
				},
			},
		},
		Expect: observer.MonitorWorkloads{
			NumWorkloads:                2,
			MinAvailable:                10 + 100,
			TotalReplicas:               13 + 200,
			AggregatedAvailableReplicas: 8 + 150,
			EstimatedAvailableReplicas:  4 + 140,
			SumAvailableProportionPpm:   800000 + 1000000,
			SumLatencyMillis:            300 + 70,
		},
	})
	//nolint:exhaustruct
	setup.Step(t, Step{
		Update: map[types.NamespacedName]func(*podseidonv1a1.PodProtector){
			{Namespace: "default", Name: "2"}: func(ppr *podseidonv1a1.PodProtector) {
				ppr.Spec.MinAvailable = 200
			},
		},
		Delete: []types.NamespacedName{{Namespace: "default", Name: "1"}},
		Expect: observer.MonitorWorkloads{
			NumWorkloads:                1,
			MinAvailable:                200,
			TotalReplicas:               200,
			AggregatedAvailableReplicas: 150,
			EstimatedAvailableReplicas:  140,
			SumAvailableProportionPpm:   750000,
			SumLatencyMillis:            70,
		},
	})
}

type Step struct {
	Create       []*podseidonv1a1.PodProtector
	Update       map[types.NamespacedName]func(*podseidonv1a1.PodProtector)
	UpdateStatus map[types.NamespacedName]func(*podseidonv1a1.PodProtector)
	Delete       []types.NamespacedName

	Expect observer.MonitorWorkloads
}

type Setup struct {
	//nolint:containedctx // only to reduce boilerplate
	ctx          context.Context
	apiMap       component.ApiMap
	nextStepNo   int
	client       *kube.Client
	statusGetter util.LateInitReader[func() observer.MonitorWorkloads]
}

func setup() *Setup {
	ctx := context.Background()

	client := kube.MockClient()

	statusGetter, statusGetterWriter := util.NewLateInit[func() observer.MonitorWorkloads]()

	//nolint:exhaustruct
	obs := o11y.ReflectPopulate(observer.Observer{
		MonitorWorkloads: func(_ context.Context, _ util.Empty, getter func() observer.MonitorWorkloads) {
			statusGetterWriter(getter)
		},
	})

	apiMap := cmd.MockStartup(ctx, []func(*component.DepRequests){
		component.ApiOnly("core-kube", client),
		component.ApiOnly("generator-leader-elector", kube.MockReadyElector(ctx)),
		component.ApiOnly("observer-generator", obs),
		component.RequireDep(monitor.New(monitor.Args{})),
	})

	return &Setup{
		ctx:          ctx,
		apiMap:       apiMap,
		client:       client,
		statusGetter: statusGetter,
		nextStepNo:   0,
	}
}

func (setup *Setup) Step(t *testing.T, step Step) {
	t.Helper()

	stepNo := setup.nextStepNo
	setup.nextStepNo++

	pprClient := setup.client.PodseidonClientSet().PodseidonV1alpha1().PodProtectors

	for _, ppr := range step.Create {
		_, err := pprClient(ppr.Namespace).Create(setup.ctx, ppr, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	for nsName, patch := range step.Update {
		existing, err := pprClient(nsName.Namespace).Get(setup.ctx, nsName.Name, metav1.GetOptions{})
		require.NoError(t, err)

		existing = existing.DeepCopy()
		patch(existing)

		_, err = pprClient(nsName.Namespace).Update(setup.ctx, existing, metav1.UpdateOptions{})
		require.NoError(t, err)
	}

	for nsName, patch := range step.UpdateStatus {
		existing, err := pprClient(nsName.Namespace).Get(setup.ctx, nsName.Name, metav1.GetOptions{})
		require.NoError(t, err)

		existing = existing.DeepCopy()
		patch(existing)

		_, err = pprClient(nsName.Namespace).UpdateStatus(setup.ctx, existing, metav1.UpdateOptions{})
		require.NoError(t, err)
	}

	for _, nsName := range step.Delete {
		err := pprClient(nsName.Namespace).Delete(setup.ctx, nsName.Name, metav1.DeleteOptions{})
		require.NoError(t, err)
	}

	var actual observer.MonitorWorkloads

	assert.Eventuallyf(t, func() bool {
		actual = setup.statusGetter.Get()()
		return actual == step.Expect
	}, time.Second, time.Millisecond, "step #%d status mismatch:\nexpected\n\t%#v\ngot\n\t%#v", stepNo, &step.Expect, &actual)
}
