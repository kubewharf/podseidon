package aggregator

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
	"github.com/kubewharf/podseidon/util/util"

	testutil "github.com/kubewharf/podseidon/tests/util"
)

var _ = ginkgo.Describe("Aggregator", func() {
	const pprName = "protector"

	const aggregatorReconcileTimeout = time.Second * 2

	setup := testutil.SetupBeforeEach()

	ginkgo.It("aggregates pods of the cell", func(ctx ginkgo.SpecContext) {
		ginkgo.By("Setup Podprotector and worker pods", func() {
			setup.CreatePodProtectorAndPods(
				ctx, pprName, testutil.PodCounts{3, 4},
				1, 5,
				podseidonv1a1.AdmissionHistoryConfig{
					AggregationRateMillis: ptr.To[int32](1000),
				},
			)
		})

		ginkgo.By("Validate aggregated PodProtector status after initial pod creation", func() {
			testutil.ExpectObject[*podseidonv1a1.PodProtector](
				ctx,
				setup.PprClient().Watch,
				pprName,
				aggregatorReconcileTimeout,
				testutil.MatchPprStatus(7, 0, 0, [2]int32{3, 4}, [2]int32{0, 0}),
			)
		})

		readyTime := time.Now()

		ginkgo.By("Marking pods as ready", func() {
			setup.MarkPodAsReady(ctx, testutil.PodId{Worker: 0, Pod: 0}, readyTime)
			setup.MarkPodAsReady(ctx, testutil.PodId{Worker: 0, Pod: 1}, readyTime)
			setup.MarkPodAsReady(ctx, testutil.PodId{Worker: 1, Pod: 0}, readyTime)
			setup.MarkPodAsReady(ctx, testutil.PodId{Worker: 1, Pod: 1}, readyTime)
		})

		ginkgo.By(
			"Validate that PodProtector status aggregation has a does not treat ready as available",
			func() {
				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					setup.PprClient().Watch,
					pprName,
					aggregatorReconcileTimeout,
					testutil.MatchPprStatus(7, 0, 0, [2]int32{3, 4}, [2]int32{0, 0}),
				)
			},
		)

		ginkgo.By(
			"Validate that PodProtector status aggregation spontaneously reconciles after available",
			func() {
				time.Sleep(time.Until(readyTime.Add(time.Second * 5)))

				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					setup.PprClient().Watch,
					pprName,
					aggregatorReconcileTimeout,
					testutil.MatchPprStatus(7, 4, 4, [2]int32{3, 4}, [2]int32{2, 2}),
				)
			},
		)

		var expireTime time.Time

		ginkgo.By("Insert fake deletion admission history into cell", func() {
			testutil.DoUpdate(
				ctx,
				setup.PprClient().Get,
				setup.PprClient().UpdateStatus,
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

					expireTime = admissionTime.Add(config.AggregationRate * 2)
				},
			)
		})

		ginkgo.By(
			"Validate that PodProtector aggregation does not immediately ignore the admission bucket",
			func() {
				testutil.ExpectObjectNot[*podseidonv1a1.PodProtector](
					ctx, setup.PprClient().Watch, pprName, time.Until(expireTime),
					testutil.MatchPprStatus(7, 4, 4, [2]int32{3, 4}, [2]int32{2, 2}),
				)
			},
		)

		ginkgo.By(
			"Validate that PodProtector aggregation spontaneously drops the admission bucket upon external pod modification",
			func() {
				// Keep patching the external pod annotations.
				repeatCtx, cancelFunc := context.WithCancel(ctx)
				defer cancelFunc()

				go setup.MaintainExternalUnmatchedPod(repeatCtx, 1, "external-unmatched-pod")

				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx, setup.PprClient().Watch, pprName, time.Until(expireTime),
					testutil.MatchPprStatus(7, 4, 4, [2]int32{3, 4}, [2]int32{2, 2}),
				)
			},
		)
	})
})