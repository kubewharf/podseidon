package webhook

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"

	testutil "github.com/kubewharf/podseidon/tests/util"
)

var _ = ginkgo.Describe("Webhook", func() {
	const pprName = "protector"

	const aggregatorReconcileTimeout = time.Second * 2

	setup := testutil.SetupBeforeEach()

	ginkgo.It("allows normal deletion and rejects extra deletions", func(ctx ginkgo.SpecContext) {
		ginkgo.By("Setup Podprotector and worker pods", func() {
			setup.CreatePodProtectorAndPods(
				ctx, pprName, testutil.PodCounts{3, 4},
				5, 0,
				podseidonv1a1.AdmissionHistoryConfig{
					MaxConcurrentLag:      nil,
					CompactThreshold:      ptr.To[int32](100),
					AggregationRateMillis: ptr.To[int32](2000),
				},
			)
		})

		ginkgo.By("Mark pods as ready", func() {
			readyTime := time.Now()

			for _, podId := range (testutil.PodCounts{3, 4}).Iter() {
				setup.MarkPodAsReady(ctx, podId, readyTime)
			}
		})

		ginkgo.By("Wait for PodProtector state to converge", func() {
			testutil.ExpectObject[*podseidonv1a1.PodProtector](
				ctx,
				setup.PprClient().Watch,
				pprName,
				aggregatorReconcileTimeout,
				testutil.MatchPprStatus(7, 7, 7, [2]int32{3, 4}, [2]int32{3, 4}),
			)
		})

		ginkgo.By("Validate that we can delete one pod from each cluster", func() {
			for workerIndex := range testutil.WorkerIndex(2) {
				err := setup.PodClient(workerIndex).
					Delete(ctx, testutil.PodId{Worker: workerIndex, Pod: 0}.PodName(), metav1.DeleteOptions{})
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			}
		})

		ginkgo.By("Validate that excessive deletion is rejected", func() {
			err := setup.PodClient(0).
				Delete(ctx, testutil.PodId{Worker: 0, Pod: 1}.PodName(), metav1.DeleteOptions{})
			gomega.Expect(err).Should(gomega.SatisfyAll(
				gomega.HaveOccurred(),
				gomega.WithTransform(
					testutil.ToStatusError,
					gomega.WithTransform(
						func(err *apierrors.StatusError) string { return err.ErrStatus.Message },
						gomega.SatisfyAny(
							gomega.ContainSubstring(
								"reports too few available replicas to admit pod deletion",
							),
							gomega.ContainSubstring(
								"has full admission buffer and is temporarily unable to admit pod deletion",
							),
						),
					),
				),
			))
		})
	})

	ginkgo.It("rejects bulk deletions", func(ctx ginkgo.SpecContext) {
		ginkgo.By("Setup Podprotector and worker pods", func() {
			setup.CreatePodProtectorAndPods(
				ctx, pprName, testutil.PodCounts{3, 4},
				5, 0,
				podseidonv1a1.AdmissionHistoryConfig{
					MaxConcurrentLag:      nil,
					CompactThreshold:      ptr.To[int32](100),
					AggregationRateMillis: ptr.To[int32](2000),
				},
			)
		})

		ginkgo.By("Mark pods as ready", func() {
			readyTime := time.Now()

			for _, podId := range (testutil.PodCounts{3, 4}).Iter() {
				setup.MarkPodAsReady(ctx, podId, readyTime)
			}
		})

		ginkgo.By("Wait for PodProtector state to converge", func() {
			testutil.ExpectObject[*podseidonv1a1.PodProtector](
				ctx,
				setup.PprClient().Watch,
				pprName,
				aggregatorReconcileTimeout,
				testutil.MatchPprStatus(7, 7, 7, [2]int32{3, 4}, [2]int32{3, 4}),
			)
		})

		ginkgo.By("Validate that full DeleteCollection is rejected", func() {
			err := setup.TryDeleteAllPodsIn(ctx, 0)
			gomega.Expect(err).Should(gomega.SatisfyAll(
				gomega.HaveOccurred(),
				gomega.WithTransform(
					testutil.ToStatusError,
					gomega.WithTransform(
						func(err *apierrors.StatusError) string { return err.ErrStatus.Message },
						gomega.SatisfyAny(
							gomega.ContainSubstring(
								"reports too few available replicas to admit pod deletion",
							),
							gomega.ContainSubstring(
								"has full admission buffer and is temporarily unable to admit pod deletion",
							),
						),
					),
				),
			))
		})
	})

	ginkgo.It(
		"rejects deletion exceeding MaxConcurrentLag even with available quota",
		func(ctx ginkgo.SpecContext) {
			ginkgo.By("Setup Podprotector and worker pods", func() {
				var pprUid types.UID

				setup.CreatePodProtector(
					ctx,
					pprName,
					&pprUid,
					1,
					0,
					podseidonv1a1.AdmissionHistoryConfig{
						MaxConcurrentLag:      ptr.To[int32](1),
						CompactThreshold:      ptr.To[int32](100),
						AggregationRateMillis: ptr.To[int32](2000),
					},
					func(ppr *podseidonv1a1.PodProtector) {
						// prevent aggregator from processing this PodProtector
						ppr.Labels = map[string]string{"aggregator-ignore-ppr": "true"}
					},
				)

				for podIndex := range testutil.PodIndex(5) {
					setup.CreatePod(
						ctx,
						pprName,
						pprUid,
						testutil.PodId{Worker: 0, Pod: podIndex},
						func(*corev1.Pod) {},
					)
				}
			})

			readyTime := time.Now()

			ginkgo.By("Mark pods as ready", func() {
				for podIndex := range testutil.PodIndex(5) {
					setup.MarkPodAsReady(ctx, testutil.PodId{Worker: 0, Pod: podIndex}, readyTime)
				}
			})

			ginkgo.By("Update PodProtector status manually", func() {
				testutil.DoUpdate(
					ctx,
					setup.PprClient().Get,
					setup.PprClient().UpdateStatus,
					pprName,
					func(ppr *podseidonv1a1.PodProtector) {
						config := defaultconfig.MustComputeDefaultSetup(
							ppr.Spec.AdmissionHistoryConfig,
						)

						ppr.Status.Cells = []podseidonv1a1.PodProtectorCellStatus{
							{
								CellId: "worker-1",
								Aggregation: podseidonv1a1.PodProtectorAggregation{
									TotalReplicas:     5,
									AvailableReplicas: 5,
									LastEventTime:     metav1.MicroTime{Time: readyTime},
								},
							},
						}

						pprutil.Summarize(config, ppr)
					},
				)

				// ensure apiserver has received the update so that it can propagate the watch event to webhook
				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					setup.PprClient().Watch,
					pprName,
					aggregatorReconcileTimeout,
					testutil.MatchPprStatus(5, 5, 5, [2]int32{5, 0}, [2]int32{5, 0}),
				)
			})

			ginkgo.By("Validate that we can delete the first pod", func() {
				err := setup.PodClient(0).
					Delete(ctx, testutil.PodId{Worker: 0, Pod: 0}.PodName(), metav1.DeleteOptions{})
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			})

			ginkgo.By("Validate that excessive deletion is rejected", func() {
				err := setup.PodClient(0).
					Delete(ctx, testutil.PodId{Worker: 0, Pod: 1}.PodName(), metav1.DeleteOptions{})
				gomega.Expect(err).Should(gomega.SatisfyAll(
					gomega.HaveOccurred(),
					gomega.WithTransform(
						testutil.ToStatusError,
						gomega.WithTransform(
							func(err *apierrors.StatusError) string { return err.ErrStatus.Message },
							gomega.ContainSubstring(
								"has full admission buffer and is temporarily unable to admit pod deletion",
							),
						),
					),
				))
			})
		},
	)
})
