// Copyright 2026 The Podseidon Authors.
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

package handler //nolint:testpackage // Exercise the retry adapter without starting the HTTP server.

import (
	"context"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	podutil "github.com/kubewharf/podseidon/util/pod"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
	"github.com/kubewharf/podseidon/util/retrybatch"

	"github.com/kubewharf/podseidon/webhook/handler/batchitem"
	"github.com/kubewharf/podseidon/webhook/handler/disruptionquota"
	"github.com/kubewharf/podseidon/webhook/handler/healthcriterion"
	"github.com/kubewharf/podseidon/webhook/observer"
)

func TestPoolAdapterCellInitialization(t *testing.T) {
	t.Parallel()

	const (
		firstCell  = "worker-1"
		secondCell = "worker-2"
		otherCell  = "worker-3"
	)

	for _, test := range []struct {
		name          string
		initialCellID optional.Optional[string]
		available     int32
		unhealthy     bool
		result        batchitem.Result
	}{
		{name: "rejected", result: batchitem.ResultRejected},
		{name: "retry", initialCellID: optional.Some(otherCell), available: 2, result: batchitem.ResultNeedRetry},
		{name: "admitted", initialCellID: optional.Some(otherCell), available: 8, result: batchitem.ResultNewDisruption},
		{name: "unhealthy", unhealthy: true, result: batchitem.ResultAlreadyUnhealthy},
		{name: "existing-cell", initialCellID: optional.Some(firstCell), unhealthy: true, result: batchitem.ResultAlreadyUnhealthy},
		{name: "historical-empty-cell", initialCellID: optional.Some(""), result: batchitem.ResultRejected},
		{name: "admitted-with-empty-cell", initialCellID: optional.Some(""), unhealthy: true, result: batchitem.ResultAlreadyUnhealthy},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, time.September, 10, 0, 0, 0, 0, time.UTC)
			ppr := &podseidonv1a1.PodProtector{
				ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "test"},
				Spec:       podseidonv1a1.PodProtectorSpec{MinAvailable: 1},
			}
			if cellID, present := test.initialCellID.Get(); present {
				ppr.Status.Cells = []podseidonv1a1.PodProtectorCellStatus{{
					CellId: cellID,
					Aggregation: podseidonv1a1.PodProtectorAggregation{
						TotalReplicas:     test.available,
						AvailableReplicas: test.available,
					},
					History: podseidonv1a1.PodProtectorAdmissionHistory{
						Buckets: []podseidonv1a1.PodProtectorAdmissionBucket{{
							StartTime: metav1.NewMicroTime(now.Add(-time.Second)),
							PodUid:    ptr.To(types.UID("old-pod")),
						}},
					},
				}}
			}
			allow := test.result == batchitem.ResultNewDisruption || test.result == batchitem.ResultAlreadyUnhealthy

			store := &adapterPprStore{ppr: ppr}
			adapter := PoolAdapter{
				sourceProvider:  store,
				pprInformer:     store,
				observer:        o11y.ReflectNoop[observer.Observer](),
				clock:           clocktesting.NewFakeClock(now),
				requiresPodName: ConstantRequiresPodName(false),
				retryBackoff:    func() time.Duration { return time.Second },
				defaultConfig: &defaultconfig.Options{
					MaxConcurrentLag: ptr.To[int32](0),
					CompactThreshold: ptr.To[int32](100),
					AggregationRate:  ptr.To(time.Second),
				},
			}
			key := pprutil.PodProtectorKey{
				NamespacedName: types.NamespacedName{Namespace: ppr.Namespace, Name: ppr.Name},
			}
			args := []BatchItem{}
			for index, cellID := range []string{firstCell, firstCell, secondCell} {
				args = append(args, BatchItem{
					BatchItem: observer.BatchItem{CellId: cellID},
					PodUid:    types.UID([]string{"pod-1", "pod-2", "pod-3"}[index]),
					PodStatus: disruptionquota.PodStatus{
						HealthCriterion: healthcriterion.Available,
						Status:          podutil.PodStatus{IsAvailable: !test.unhealthy},
					},
				})
			}

			config := adapter.defaultConfig.Compute(optional.Some(ppr.Spec.AdmissionHistoryConfig))
			pprutil.Summarize(config, ppr)
			expected := ppr.DeepCopy()
			if allow {
				expected.Status.Cells = append(expected.Status.Cells,
					podseidonv1a1.PodProtectorCellStatus{CellId: secondCell})
				if test.initialCellID != optional.Some(firstCell) {
					expected.Status.Cells = append(expected.Status.Cells,
						podseidonv1a1.PodProtectorCellStatus{CellId: firstCell})
				}
				for index := len(args) - 1; index >= 0; index-- {
					for cellIndex := range expected.Status.Cells {
						cell := &expected.Status.Cells[cellIndex]
						if cell.CellId == args[index].CellId {
							cell.History.Buckets = append(cell.History.Buckets,
								podseidonv1a1.PodProtectorAdmissionBucket{
									StartTime: metav1.NewMicroTime(now),
									PodUid:    ptr.To(args[index].PodUid),
								})
						}
					}
				}
			}
			pprutil.Summarize(config, expected)

			for attempt := range 2 {
				cached := store.ppr
				before := cached.DeepCopy()
				result := adapter.tryExecute(t.Context(), key, args)
				assert.Equal(t, retrybatch.ExecuteResultVariantSuccess, result.Variant)
				for index := range args {
					want := test.result
					if allow && attempt > 0 {
						want = batchitem.ResultAlreadyHasBucket
					}
					assert.Equal(t, want, result.Success(index))
				}
				assert.Equal(t, expected.Status, store.ppr.Status)
				assert.Equal(t, before, cached, "informer objects must not be mutated")
				wantWrites := 0
				if allow {
					wantWrites = 1
				}
				assert.Equal(t, wantWrites, store.writes)
			}
		})
	}
}

// Only the cache read and status write boundaries are replaced; admission logic is real.
type adapterPprStore struct {
	pprutil.IndexedInformer
	pprutil.SourceProvider
	ppr    *podseidonv1a1.PodProtector
	writes int
}

func (store *adapterPprStore) Get(pprutil.PodProtectorKey) (optional.Optional[*podseidonv1a1.PodProtector], error) {
	return optional.Some(store.ppr), nil
}

func (store *adapterPprStore) UpdateStatus(_ context.Context, _ pprutil.SourceName, ppr *podseidonv1a1.PodProtector) error {
	store.ppr = ppr.DeepCopy()
	store.writes++
	return nil
}
