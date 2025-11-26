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

package disruptionquota_test

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alecthomas/assert/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/iter"
	podutil "github.com/kubewharf/podseidon/util/pod"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"

	"github.com/kubewharf/podseidon/webhook/handler/disruptionquota"
	"github.com/kubewharf/podseidon/webhook/handler/healthcriterion"
)

type PprFixture struct {
	MinAvailable int32

	Scheduled int32
	Running   int32
	Ready     int32
	Available int32

	CurrentLag int32
	MaxLag     int32
}

func doTestAdmitBatch(
	t *testing.T,
	pprFixture PprFixture,
	podStatuses []disruptionquota.PodStatus,
	expectResults []disruptionquota.Result,
) {
	t.Helper()

	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		config := defaultconfig.Computed{
			MaxConcurrentLag: pprFixture.MaxLag,
			CompactThreshold: 1000,
			AggregationRate:  time.Second,
		}

		ppr := &podseidonv1a1.PodProtector{
			Spec: podseidonv1a1.PodProtectorSpec{
				MinAvailable: pprFixture.MinAvailable,
			},
			Status: podseidonv1a1.PodProtectorStatus{
				Cells: []podseidonv1a1.PodProtectorCellStatus{{
					CellId: "a",
					Aggregation: podseidonv1a1.PodProtectorAggregation{
						TotalReplicas:     100000,
						ScheduledReplicas: pprFixture.Scheduled,
						RunningReplicas:   pprFixture.Running,
						ReadyReplicas:     pprFixture.Ready,
						AvailableReplicas: pprFixture.Available,
						LastEventTime:     metav1.NewMicroTime(time.Now().Add(-time.Second)),
					},
					History: podseidonv1a1.PodProtectorAdmissionHistory{
						Buckets: iter.Map(
							iter.Range(0, pprFixture.CurrentLag),
							func(id int32) podseidonv1a1.PodProtectorAdmissionBucket {
								return podseidonv1a1.PodProtectorAdmissionBucket{
									StartTime: metav1.NowMicro(),
									PodName:   fmt.Sprint(id),
									PodUid: ptr.To(types.UID(fmt.Sprintf(
										"00000000-0000-0000-0000-%012d",
										id,
									))),
								}
							},
						).CollectSlice(),
					},
				}},
			},
		}
		pprutil.Summarize(config, ppr)

		actualResults := disruptionquota.AdmitBatch(ppr, podStatuses, config)

		assert.Equal(t, expectResults, actualResults)
	})
}

func TestAdmitBatchAllDeny(t *testing.T) {
	t.Parallel()
	doTestAdmitBatch(
		t,
		PprFixture{
			MinAvailable: 10,
			Scheduled:    10,
			Running:      10,
			Ready:        10,
			Available:    10,
			CurrentLag:   0,
			MaxLag:       100,
		},
		[]disruptionquota.PodStatus{
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
		},
		[]disruptionquota.Result{
			disruptionquota.DisruptionResultDenied,
			disruptionquota.DisruptionResultDenied,
		},
	)
}

func TestAdmitBatchAllAllow(t *testing.T) {
	t.Parallel()
	doTestAdmitBatch(
		t,
		PprFixture{
			MinAvailable: 8,
			Scheduled:    10,
			Running:      10,
			Ready:        10,
			Available:    10,
			CurrentLag:   0,
			MaxLag:       100,
		},
		[]disruptionquota.PodStatus{
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
		},
		[]disruptionquota.Result{
			disruptionquota.DisruptionResultOk,
			disruptionquota.DisruptionResultOk,
		},
	)
}

func TestAdmitBatchSingleTransition(t *testing.T) {
	t.Parallel()
	doTestAdmitBatch(
		t,
		PprFixture{
			MinAvailable: 10,
			Scheduled:    12,
			Running:      12,
			Ready:        12,
			Available:    12,
			CurrentLag:   1,
			MaxLag:       100,
		},
		[]disruptionquota.PodStatus{
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
		},
		[]disruptionquota.Result{
			// 2., 3.: both pods receive Retry even though there is only one lag,
			// because we don't know if either would retry and shouldn't reject in advance.
			disruptionquota.DisruptionResultRetry,
			disruptionquota.DisruptionResultRetry,
			// 1. 12 > 10+1, ok to disrupt.
			disruptionquota.DisruptionResultOk,
		},
	)
}

func TestAdmitBatchAllowAlreadyUnavailable(t *testing.T) {
	t.Parallel()
	doTestAdmitBatch(
		t,
		PprFixture{
			MinAvailable: 10,
			Scheduled:    10,
			Running:      10,
			Ready:        10,
			Available:    10,
			CurrentLag:   1,
			MaxLag:       100,
		},
		[]disruptionquota.PodStatus{
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: false,
				},
			},
		},
		[]disruptionquota.Result{
			// no further disruption caused by evicting an unavailable pod
			disruptionquota.DisruptionResultAlreadyUnhealthy,
		},
	)
}

func TestAdmitBatchAllowAlreadyRunningWithCriterion(t *testing.T) {
	t.Parallel()
	doTestAdmitBatch(
		t,
		PprFixture{
			MinAvailable: 10,
			Scheduled:    11,
			Running:      11,
			Ready:        8,
			Available:    8,
			CurrentLag:   0,
			MaxLag:       100,
		},
		[]disruptionquota.PodStatus{
			{
				HealthCriterion: healthcriterion.Running,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: false,
				},
			},
		},
		[]disruptionquota.Result{
			// health criterion
			disruptionquota.DisruptionResultOk,
		},
	)
}

func TestAdmitBatchMultiCriteria(t *testing.T) {
	t.Parallel()
	doTestAdmitBatch(
		t,
		PprFixture{
			MinAvailable: 10,
			Scheduled:    13,
			Running:      11,
			Ready:        11,
			Available:    11,
			CurrentLag:   0,
			MaxLag:       100,
		},
		[]disruptionquota.PodStatus{
			{
				HealthCriterion: healthcriterion.Scheduled,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: false,
				},
			},
			{
				HealthCriterion: healthcriterion.Running,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: false,
				},
			},
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     false,
					IsAvailable: false,
				},
			},
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
			{
				HealthCriterion: healthcriterion.Available,
				Status: podutil.PodStatus{
					IsScheduled: true,
					IsRunning:   true,
					IsReady:     true,
					IsAvailable: true,
				},
			},
		},
		[]disruptionquota.Result{
			// 5. Allowed because health criterion (Scheduled) is 11 > 10.
			disruptionquota.DisruptionResultOk,
			// 4. Denied because health criterion (Running) is 9 <= 10.
			disruptionquota.DisruptionResultDenied,
			// 3. Allowed because pod is not available and criterion is availability.
			// Disrupts Scheduled and Running, now (11, 9, 10, 10).
			disruptionquota.DisruptionResultAlreadyUnhealthy,
			// 2. Denied because Available is now 10.
			disruptionquota.DisruptionResultDenied,
			// 1. Disrupt all conditions successfully, now (12, 10, 10, 10).
			disruptionquota.DisruptionResultOk,
		},
	)
}
