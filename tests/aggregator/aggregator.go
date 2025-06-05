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

package aggregator

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/tests/fixtures"
	"github.com/kubewharf/podseidon/tests/provision"
	testutil "github.com/kubewharf/podseidon/tests/util"
)

var _ = ginkgo.Describe("Aggregator", func() {
	const pprName = "protector"

	const aggregatorReconcileTimeout = time.Second * 2

	var env provision.Env
	provision.RegisterHooks(&env, provision.NewRequest(2, func(cluster testutil.ClusterId, req *provision.ClusterRequest) {
		if cluster.IsWorker() {
			req.EnableAggregatorUpdateTrigger = true
		}
	}))

	ginkgo.It("aggregates pods of the cell", func(ctx ginkgo.SpecContext) {
		ginkgo.By("Setup Podprotector and worker pods", func() {
			fixtures.CreatePodProtectorAndPods(
				ctx, &env, pprName,
				testutil.PodCounts{1: 3, 2: 4},
				1, 5,
				podseidonv1a1.AdmissionHistoryConfig{
					AggregationRateMillis: ptr.To[int32](1000),
				},
			)
		})

		ginkgo.By("Validate aggregated PodProtector status after initial pod creation", func() {
			testutil.ExpectObject[*podseidonv1a1.PodProtector](
				ctx,
				env.PprClient().Watch,
				pprName,
				aggregatorReconcileTimeout,
				testutil.MatchPprStatus(7, 0, 0, map[testutil.ClusterId]int32{1: 3, 2: 4}, map[testutil.ClusterId]int32{1: 0, 2: 0}),
			)
		})

		readyTime := time.Now()

		ginkgo.By("Marking pods as ready", func() {
			fixtures.MarkPodAsReady(ctx, &env, testutil.PodId{Cluster: 1, Pod: 0}, readyTime)
			fixtures.MarkPodAsReady(ctx, &env, testutil.PodId{Cluster: 1, Pod: 1}, readyTime)
			fixtures.MarkPodAsReady(ctx, &env, testutil.PodId{Cluster: 2, Pod: 0}, readyTime)
			fixtures.MarkPodAsReady(ctx, &env, testutil.PodId{Cluster: 2, Pod: 1}, readyTime)
		})

		ginkgo.By(
			"Validate that PodProtector status aggregation does not treat ready as available",
			func() {
				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					env.PprClient().Watch,
					pprName,
					aggregatorReconcileTimeout,
					testutil.MatchPprStatus(7, 0, 0, map[testutil.ClusterId]int32{1: 3, 2: 4}, map[testutil.ClusterId]int32{1: 0, 2: 0}),
				)
			},
		)

		ginkgo.By(
			"Validate that PodProtector status aggregation spontaneously reconciles after available",
			func() {
				time.Sleep(time.Until(readyTime.Add(time.Second * 5)))

				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					env.PprClient().Watch,
					pprName,
					aggregatorReconcileTimeout,
					testutil.MatchPprStatus(7, 4, 4, map[testutil.ClusterId]int32{1: 3, 2: 4}, map[testutil.ClusterId]int32{1: 2, 2: 2}),
				)
			},
		)

		var expireTime time.Time

		ginkgo.By("Insert fake deletion admission history into cell", func() {
			testutil.DoUpdate(
				ctx,
				env.PprClient().Get,
				env.PprClient().UpdateStatus,
				pprName,
				func(ppr *podseidonv1a1.PodProtector) {
					config := defaultconfig.MustComputeDefaultSetup(ppr.Spec.AdmissionHistoryConfig)
					admissionTime := time.Now()

					cellIndex := util.FindInSliceWith(
						ppr.Status.Cells,
						func(cell podseidonv1a1.PodProtectorCellStatus) bool { return cell.CellId == "worker-2" },
					)
					ppr.Status.Cells[cellIndex].History.Buckets = []podseidonv1a1.PodProtectorAdmissionBucket{
						{
							StartTime: metav1.NewMicroTime(admissionTime),
							PodUid:    ptr.To(types.UID("00000000-0000-0000-0000-000000000000")),
						},
						{
							StartTime: metav1.NewMicroTime(admissionTime),
							PodUid:    ptr.To(types.UID("00000000-0000-0000-0000-000000000001")),
						},
					}

					pprutil.Summarize(config, ppr)

					ginkgo.GinkgoLogr.Info("debug", "aggregation rate", config.AggregationRate, "spec", ppr.Spec.AdmissionHistoryConfig)
					expireTime = admissionTime.Add(config.AggregationRate * 2)
				},
			)
		})

		ginkgo.By(
			"Validate that PodProtector aggregation spontaneously drops the admission bucket due to update-trigger",
			func() {
				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx, env.PprClient().Watch, pprName, time.Until(expireTime),
					testutil.MatchPprStatus(7, 4, 4, map[testutil.ClusterId]int32{1: 3, 2: 4}, map[testutil.ClusterId]int32{1: 2, 2: 2}),
				)
			},
		)
	})
})
