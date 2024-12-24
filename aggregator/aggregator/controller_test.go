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

package aggregator_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/cmd"
	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/optional"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"

	"github.com/kubewharf/podseidon/aggregator/aggregator"
	"github.com/kubewharf/podseidon/aggregator/observer"
	"github.com/kubewharf/podseidon/aggregator/synctime"
)

func TestReconcileEmpty(t *testing.T) {
	t.Parallel()

	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 0,
		Pods: map[PodSetup]uint64{
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second),
			}: 0,
		},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               0,
				AggregatedAvailable: 0,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  0,
			},
		},
		ExpectLater: optional.None[ExpectLaterState](),
	})
}

func TestReconcileAllAvailable(t *testing.T) {
	t.Parallel()

	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 0,
		Pods: map[PodSetup]uint64{
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second),
			}: 5,
		},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               5,
				AggregatedAvailable: 5,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  5,
			},
		},
		ExpectLater: optional.None[ExpectLaterState](),
	})
}

func TestReconcileSomeReadyButUnavailable(t *testing.T) {
	t.Parallel()

	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 3,
		Pods: map[PodSetup]uint64{
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second),
			}: 2,
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second * 5),
			}: 3,
		},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               5,
				AggregatedAvailable: 3,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  3,
			},
		},
		ExpectLater: optional.Some(ExpectLaterState{
			Delay: time.Second * 2,
			ExpectState: ExpectState{
				ErrTag: optional.None[string](),
				Summary: podseidonv1a1.PodProtectorStatusSummary{
					Total:               5,
					AggregatedAvailable: 5,
					MaxLatencyMillis:    0,
					EstimatedAvailable:  5,
				},
			},
		}),
	})
}

type PodSetup struct {
	IsDeleted       bool
	IsAggregateOnly bool
	ReadyFor        optional.Optional[time.Duration]
}

func (setup PodSetup) makePod(clk clock.Clock) *corev1.Pod {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test",
			Labels: map[string]string{
				"test":            "true",
				"triggersWebhook": fmt.Sprint(!setup.IsAggregateOnly),
			},
		},
		// We remove the whole Spec field in the informer anyway
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodReady,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: clk.Now()},
				},
			},
		},
	}

	if setup.IsDeleted {
		pod.DeletionTimestamp = &metav1.Time{Time: clk.Now().Add(-time.Second)}
	}

	if readyTime, isReady := setup.ReadyFor.Get(); isReady {
		pod.Status.Conditions[0].Status = corev1.ConditionTrue
		pod.Status.Conditions[0].LastTransitionTime.Time = clk.Now().Add(-readyTime)
	}

	return pod
}

type TestCase struct {
	MinAvailable    int32
	MinReadySeconds int32
	Pods            map[PodSetup]uint64

	ExpectInitial ExpectState
	ExpectLater   optional.Optional[ExpectLaterState]
}

type ExpectState struct {
	ErrTag  optional.Optional[string]
	Summary podseidonv1a1.PodProtectorStatusSummary
}

type ExpectLaterState struct {
	Delay time.Duration
	ExpectState
}

const TestPprName = "test"

func (tc TestCase) CoreClient() *kube.Client {
	return kube.MockClient(
		&podseidonv1a1.PodProtector{
			TypeMeta: metav1.TypeMeta{
				APIVersion: podseidonv1a1.SchemeGroupVersion.String(),
				Kind:       podseidonv1a1.PodProtectorKind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: metav1.NamespaceDefault,
				Name:      TestPprName,
			},
			Spec: podseidonv1a1.PodProtectorSpec{
				MinAvailable:    tc.MinAvailable,
				MinReadySeconds: tc.MinReadySeconds,
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"test":            "true",
						"triggersWebhook": "true",
					},
				},
				AggregationSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"test": "true",
					},
				},
			},
		},
	)
}

func (tc TestCase) WorkerClient(clk clock.Clock) *kube.Client {
	return kube.MockClient(
		iter.Map(
			iter.Enumerate(
				iter.FlatMap(
					iter.MapKvs(tc.Pods),
					func(kv iter.Pair[PodSetup, uint64]) iter.Iter[*corev1.Pod] {
						setup := kv.Left
						count := kv.Right

						return iter.Map(
							iter.Repeat(setup.makePod(clk), count),
							(*corev1.Pod).DeepCopy,
						)
					},
				),
			),
			func(pair iter.Pair[uint64, *corev1.Pod]) runtime.Object {
				counter := pair.Left
				pod := pair.Right

				pod.Name = fmt.Sprintf("test-%d", counter)

				return pod
			},
		).CollectSlice()...,
	)
}

func testReconcile(
	t *testing.T,
	tc TestCase,
) {
	clk := clocktesting.NewFakeClock(time.Now())
	ctx := context.Background()

	reconcileCh := make(chan error)

	//nolint:exhaustruct
	obs := observer.Observer{
		EndReconcile: func(_ context.Context, arg observer.EndReconcile) {
			reconcileCh <- arg.Err
		},
	}.Join(observer.NewLoggingObserver())

	coreClient := tc.CoreClient()

	cmd.MockStartup(ctx, []func(*component.DepRequests){
		component.ApiOnly("core-kube", coreClient),
		component.ApiOnly("worker-kube", tc.WorkerClient(clk)),
		component.ApiOnly("observer-aggregator", obs),
		component.ApiOnly("default-admission-history-config", &defaultconfig.Options{
			MaxConcurrentLag: ptr.To(int32(0)),
			CompactThreshold: ptr.To(int32(100)),
			AggregationRate:  ptr.To(time.Duration(0)),
		}),
		component.RequireDep(aggregator.NewController(aggregator.ControllerArgs{
			InformerSyncTimeAlgos: map[string]synctime.PodInterpreter{
				"fake-clock": &synctime.ClockPodInterpreter{Clock: clk},
			},
			DefaultInformerSyncTimeAlgo: "fake-clock",
			Clock:                       clk,
			SourceProvider:              pprutil.RequestSingleSourceProvider("core"),
		})),
	})

	waitAndAssert(ctx, t, coreClient, reconcileCh, tc.ExpectInitial)

	if later, expectsLater := tc.ExpectLater.Get(); expectsLater {
		clk.Sleep(later.Delay)
		waitAndAssert(ctx, t, coreClient, reconcileCh, later.ExpectState)
	}
}

func waitAndAssert(
	ctx context.Context,
	t *testing.T,
	coreClient *kube.Client,
	reconcileCh <-chan error,
	expected ExpectState,
) {
	var initialErr error

	require.Eventually(
		t,
		func() bool {
			select {
			case err := <-reconcileCh:
				initialErr = err
				return true
			default:
				return false
			}
		},
		time.Second*5,
		// The latency is bounded by syncedPollPeriod
		// from client-go/tools/cache/shared_informer.go,
		// defined to be a constant 100ms.
		// We set 10ms here to poll the successful result quickly after the 100ms wait.
		time.Millisecond*10,
	)

	if expectErrTag, isErr := expected.ErrTag.Get(); isErr {
		assert.Contains(t, errors.GetTags(initialErr), expectErrTag)

		return
	}

	require.NoError(t, initialErr)

	// We have finished reconcile, but we still need to wait for the client to be updated.
	assertSummaryEventuallyEqual(
		ctx, t, coreClient, expected,
		func(summary *podseidonv1a1.PodProtectorStatusSummary) int32 { return summary.Total },
		"summary.total",
	)
	assertSummaryEventuallyEqual(
		ctx, t, coreClient, expected,
		func(summary *podseidonv1a1.PodProtectorStatusSummary) int32 { return summary.AggregatedAvailable },
		"summary.aggregatedAvailable",
	)
	assertSummaryEventuallyEqual(
		ctx, t, coreClient, expected,
		func(summary *podseidonv1a1.PodProtectorStatusSummary) int64 { return summary.MaxLatencyMillis },
		"summary.maxLatencyMillis",
	)
	assertSummaryEventuallyEqual(
		ctx, t, coreClient, expected,
		func(summary *podseidonv1a1.PodProtectorStatusSummary) int32 { return summary.EstimatedAvailable },
		"summary.estimatedAvailable",
	)
}

func assertSummaryEventuallyEqual[R comparable](
	ctx context.Context, t *testing.T, coreClient *kube.Client, expected ExpectState,
	field func(*podseidonv1a1.PodProtectorStatusSummary) R,
	msg string,
) {
	assert.Eventually(
		t,
		func() bool {
			actual, err := coreClient.PodseidonClientSet().
				PodseidonV1alpha1().
				PodProtectors(metav1.NamespaceDefault).
				Get(ctx, TestPprName, metav1.GetOptions{})
			require.NoError(t, err)

			return field(&expected.Summary) == field(&actual.Status.Summary)
		},
		time.Millisecond*10,
		time.Microsecond*100,
		msg,
	)
}
