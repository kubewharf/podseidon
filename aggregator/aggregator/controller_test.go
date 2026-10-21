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
	o11yklog "github.com/kubewharf/podseidon/util/o11y/klog"
	"github.com/kubewharf/podseidon/util/optional"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"

	"github.com/kubewharf/podseidon/aggregator/aggregator"
	"github.com/kubewharf/podseidon/aggregator/observer"
	"github.com/kubewharf/podseidon/aggregator/synctime"
)

//nolint:paralleltest // klog InitFlags cannot be called in parallel
func TestReconcileEmpty(t *testing.T) {
	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 0,
		Pods: map[PodSetup]uint64{
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second),
				Phase:           corev1.PodRunning,
				IsUnscheduled:   false,
				NodeName:        "worker-1",
			}: 0,
		},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               0,
				AggregatedAvailable: 0,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  0,

				AggregatedReady:     0,
				AggregatedScheduled: 0,
				AggregatedRunning:   0,
			},
			AssertNoCells: true,
		},
		ExpectLater: optional.None[ExpectLaterState](),
	})
}

//nolint:paralleltest // klog InitFlags cannot be called in parallel
func TestReconcileEmptyExistingCell(t *testing.T) {
	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 0,
		InitialCells: []podseidonv1a1.PodProtectorCellStatus{{
			CellId: "default",
			Aggregation: podseidonv1a1.PodProtectorAggregation{
				TotalReplicas:              1,
				PendingReplicas:            ptr.To(int32(1)),
				UnscheduledPendingReplicas: ptr.To(int32(1)),
			},
		}},
		Pods: map[PodSetup]uint64{},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               0,
				AggregatedAvailable: 0,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  0,

				AggregatedReady:     0,
				AggregatedScheduled: 0,
				AggregatedRunning:   0,
			},
			AssertNoCells:              false,
			AssertPendingReplicas:      true,
			PendingReplicas:            ptr.To(int32(0)),
			UnscheduledPendingReplicas: ptr.To(int32(0)),
		},
		ExpectLater: optional.None[ExpectLaterState](),
	})
}

//nolint:paralleltest // klog InitFlags cannot be called in parallel
func TestReconcileAllAvailable(t *testing.T) {
	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 0,
		Pods: map[PodSetup]uint64{
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second),
				Phase:           corev1.PodRunning,
				IsUnscheduled:   false,
				NodeName:        "worker-1",
			}: 5,
		},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               5,
				AggregatedAvailable: 5,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  5,

				AggregatedReady:     5,
				AggregatedScheduled: 5,
				AggregatedRunning:   5,
			},
			AssertPendingReplicas:      true,
			PendingReplicas:            ptr.To(int32(0)),
			UnscheduledPendingReplicas: ptr.To(int32(0)),
		},
		ExpectLater: optional.None[ExpectLaterState](),
	})
}

//nolint:paralleltest // klog InitFlags cannot be called in parallel
func TestReconcileSomeReadyButUnavailable(t *testing.T) {
	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 3,
		Pods: map[PodSetup]uint64{
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second),
				Phase:           corev1.PodRunning,
				IsUnscheduled:   false,
				NodeName:        "worker-1",
			}: 2,
			{
				IsDeleted:       false,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second * 5),
				Phase:           corev1.PodRunning,
				IsUnscheduled:   false,
				NodeName:        "worker-1",
			}: 3,
			{
				IsDeleted:       true,
				IsAggregateOnly: true,
				ReadyFor:        optional.Some(time.Second * 5),
				Phase:           corev1.PodRunning,
				IsUnscheduled:   false,
				NodeName:        "worker-1",
			}: 2,
		},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               5,
				AggregatedAvailable: 3,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  3,

				AggregatedReady:     5,
				AggregatedScheduled: 5,
				AggregatedRunning:   5,
			},
			AssertPendingReplicas:      true,
			PendingReplicas:            ptr.To(int32(0)),
			UnscheduledPendingReplicas: ptr.To(int32(0)),
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

					AggregatedReady:     5,
					AggregatedScheduled: 5,
					AggregatedRunning:   5,
				},
				AssertPendingReplicas:      true,
				PendingReplicas:            ptr.To(int32(0)),
				UnscheduledPendingReplicas: ptr.To(int32(0)),
			},
		}),
	})
}

//nolint:paralleltest // klog InitFlags cannot be called in parallel
func TestReconcilePendingReplicas(t *testing.T) {
	testReconcile(t, TestCase{
		MinAvailable:    5,
		MinReadySeconds: 0,
		Pods: map[PodSetup]uint64{
			{
				IsDeleted:       false,
				IsAggregateOnly: false,
				ReadyFor:        optional.None[time.Duration](),
				Phase:           corev1.PodPending,
				IsUnscheduled:   false,
				NodeName:        "",
			}: 2,
			{
				IsDeleted:       false,
				IsAggregateOnly: false,
				ReadyFor:        optional.None[time.Duration](),
				Phase:           corev1.PodPending,
				IsUnscheduled:   true,
				NodeName:        "worker-1",
			}: 3,
			{
				IsDeleted:       false,
				IsAggregateOnly: false,
				ReadyFor:        optional.None[time.Duration](),
				Phase:           corev1.PodRunning,
				IsUnscheduled:   true,
				NodeName:        "",
			}: 4,
			{
				IsDeleted:       false,
				IsAggregateOnly: false,
				ReadyFor:        optional.None[time.Duration](),
				Phase:           corev1.PodRunning,
				IsUnscheduled:   false,
				NodeName:        "worker-1",
			}: 1,
			{
				IsDeleted:       true,
				IsAggregateOnly: false,
				ReadyFor:        optional.None[time.Duration](),
				Phase:           corev1.PodPending,
				IsUnscheduled:   true,
				NodeName:        "",
			}: 2,
		},
		ExpectInitial: ExpectState{
			ErrTag: optional.None[string](),
			Summary: podseidonv1a1.PodProtectorStatusSummary{
				Total:               10,
				AggregatedAvailable: 0,
				MaxLatencyMillis:    0,
				EstimatedAvailable:  0,
				AggregatedReady:     0,
				AggregatedScheduled: 3,
				AggregatedRunning:   5,
			},
			AssertPendingReplicas:      true,
			PendingReplicas:            ptr.To(int32(5)),
			UnscheduledPendingReplicas: ptr.To(int32(2)),
		},
		ExpectLater: optional.None[ExpectLaterState](),
	})
}

type PodSetup struct {
	IsDeleted       bool
	IsAggregateOnly bool
	ReadyFor        optional.Optional[time.Duration]
	Phase           corev1.PodPhase
	IsUnscheduled   bool
	NodeName        string
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
		Spec: corev1.PodSpec{NodeName: setup.NodeName},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodReady,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: clk.Now()},
				},
				{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: clk.Now()},
				},
			},
			Phase: setup.Phase,
		},
	}

	if pod.Status.Phase == "" {
		pod.Status.Phase = corev1.PodRunning
	}

	if setup.IsUnscheduled {
		pod.Status.Conditions[1].Status = corev1.ConditionFalse
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
	InitialCells    []podseidonv1a1.PodProtectorCellStatus
	Pods            map[PodSetup]uint64

	ExpectInitial ExpectState
	ExpectLater   optional.Optional[ExpectLaterState]
}

type ExpectState struct {
	ErrTag  optional.Optional[string]
	Summary podseidonv1a1.PodProtectorStatusSummary

	AssertNoCells              bool
	AssertPendingReplicas      bool
	PendingReplicas            *int32
	UnscheduledPendingReplicas *int32
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
			Status: podseidonv1a1.PodProtectorStatus{
				Cells: tc.InitialCells,
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

	cmd.MockStartupWithCliArgs(ctx, []func(*component.DepRequests){
		o11yklog.RequestKlogArgs,
		component.ApiOnly("core-kube", coreClient),
		component.ApiOnly("worker-kube", tc.WorkerClient(clk)),
		component.ApiOnly("observer-aggregator", obs),
		component.ApiOnly("default-admission-history-config", &defaultconfig.Options{
			MaxConcurrentLag: ptr.To(int32(0)),
			CompactThreshold: ptr.To(int32(100)),
			AggregationRate:  ptr.To(time.Duration(0)),
		}),
		synctime.ProvideClock(clk, true),
		component.RequireDep(aggregator.NewController(aggregator.ControllerArgs{
			Clock: clk,
		})),
		pprutil.RequireSingleSourceProvider(pprutil.SingleSourceProviderArgs{ClusterName: "core"}, true),
	}, []string{"--klog-v=6"})

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
	assertSummaryEventuallyEqual(
		ctx, t, coreClient, expected,
		func(summary *podseidonv1a1.PodProtectorStatusSummary) int32 { return summary.AggregatedReady },
		"summary.aggregatedReady",
	)
	assertSummaryEventuallyEqual(
		ctx, t, coreClient, expected,
		func(summary *podseidonv1a1.PodProtectorStatusSummary) int32 { return summary.AggregatedScheduled },
		"summary.aggregatedScheduled",
	)
	assertSummaryEventuallyEqual(
		ctx, t, coreClient, expected,
		func(summary *podseidonv1a1.PodProtectorStatusSummary) int32 { return summary.AggregatedRunning },
		"summary.aggregatedRunning",
	)
	if expected.AssertNoCells {
		assertNoCellsEventually(ctx, t, coreClient)
	}
	if expected.AssertPendingReplicas {
		assertAggregationEventuallyEqual(ctx, t, coreClient, expected)
	}
}

func assertNoCellsEventually(
	ctx context.Context,
	t *testing.T,
	coreClient *kube.Client,
) {
	assert.Eventually(
		t,
		func() bool {
			actual, err := coreClient.PodseidonClientSet().
				PodseidonV1alpha1().
				PodProtectors(metav1.NamespaceDefault).
				Get(ctx, TestPprName, metav1.GetOptions{})
			require.NoError(t, err)

			return len(actual.Status.Cells) == 0
		},
		time.Millisecond*10,
		time.Microsecond*100,
		"no aggregation cell",
	)
}

func assertAggregationEventuallyEqual(
	ctx context.Context,
	t *testing.T,
	coreClient *kube.Client,
	expected ExpectState,
) {
	assert.Eventually(
		t,
		func() bool {
			actual, err := coreClient.PodseidonClientSet().
				PodseidonV1alpha1().
				PodProtectors(metav1.NamespaceDefault).
				Get(ctx, TestPprName, metav1.GetOptions{})
			require.NoError(t, err)
			require.Len(t, actual.Status.Cells, 1)

			aggregation := actual.Status.Cells[0].Aggregation
			return optionalInt32Equal(expected.PendingReplicas, aggregation.PendingReplicas) &&
				optionalInt32Equal(expected.UnscheduledPendingReplicas, aggregation.UnscheduledPendingReplicas)
		},
		time.Millisecond*10,
		time.Microsecond*100,
		"cell aggregation pending replicas",
	)
}

func optionalInt32Equal(left, right *int32) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
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
