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

package webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	podseidon "github.com/kubewharf/podseidon/apis"
	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/optional"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"

	"github.com/kubewharf/podseidon/tests/fixtures"
	"github.com/kubewharf/podseidon/tests/provision"
	testutil "github.com/kubewharf/podseidon/tests/util"
)

var _ = ginkgo.Describe("Webhook", func() {
	const pprName = "protector"

	const aggregatorReconcileTimeout = time.Second * 2

	var env provision.Env
	provision.RegisterHooks(&env, provision.NewRequest(2, func(cluster testutil.ClusterId, req *provision.ClusterRequest) {
		if cluster.IsWorker() {
			req.EnableAggregatorUpdateTrigger = true
		}
	}))

	for _, dm := range DefaultDeletionMethods {
		ginkgo.It(
			fmt.Sprintf("allows normal %s and rejects extra %s", dm.Noun(), dm.Noun()),
			func(ctx ginkgo.SpecContext) {
				ginkgo.By("Setup PodProtector and worker pods", func() {
					fixtures.CreatePodProtectorAndPods(
						ctx, &env, pprName,
						testutil.PodCounts{1: 3, 2: 4},
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

					for _, podId := range (testutil.PodCounts{1: 3, 2: 4}).PodIds() {
						fixtures.MarkPodAsReady(ctx, &env, podId, readyTime)
					}
				})

				ginkgo.By("Wait for PodProtector state to converge", func() {
					testutil.ExpectObject[*podseidonv1a1.PodProtector](
						ctx,
						env.PprClient().Watch,
						pprName,
						aggregatorReconcileTimeout,
						testutil.MatchPprStatus(
							7,
							7,
							7,
							map[testutil.ClusterId]int32{1: 3, 2: 4},
							map[testutil.ClusterId]int32{1: 3, 2: 4},
						),
					)
				})

				ginkgo.By(fmt.Sprintf("Validate that one pod %s is allowed from each cluster", dm.Noun()), func() {
					for _, cluster := range env.WorkerClusters() {
						err := dm.Delete(ctx, &env, testutil.PodId{Cluster: cluster.Id, Pod: 0})
						gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
					}
				})

				ginkgo.By(fmt.Sprintf("Validate that excessive %s is rejected", dm.Noun()), func() {
					err := dm.Delete(ctx, &env, testutil.PodId{Cluster: 1, Pod: 1})
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
			},
		)
	}

	ginkgo.It("rejects bulk deletions", func(ctx ginkgo.SpecContext) {
		ginkgo.By("Setup PodProtector and worker pods", func() {
			fixtures.CreatePodProtectorAndPods(
				ctx, &env, pprName, testutil.PodCounts{1: 3, 2: 4},
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

			for _, podId := range (testutil.PodCounts{1: 3, 2: 4}).PodIds() {
				fixtures.MarkPodAsReady(ctx, &env, podId, readyTime)
			}
		})

		ginkgo.By("Wait for PodProtector state to converge", func() {
			testutil.ExpectObject[*podseidonv1a1.PodProtector](
				ctx,
				env.PprClient().Watch,
				pprName,
				aggregatorReconcileTimeout,
				testutil.MatchPprStatus(7, 7, 7, map[testutil.ClusterId]int32{1: 3, 2: 4}, map[testutil.ClusterId]int32{1: 3, 2: 4}),
			)
		})

		ginkgo.By("Validate that full DeleteCollection is rejected", func() {
			err := fixtures.TryDeleteAllPodsIn(ctx, &env, 1)
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
			ginkgo.By("Setup PodProtector and worker pods", func() {
				var pprUid types.UID

				fixtures.CreatePodProtector(
					ctx,
					&env,
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

				for podIndex := range uint32(5) {
					fixtures.CreatePod(
						ctx,
						&env,
						pprName,
						pprUid,
						testutil.PodId{Cluster: 1, Pod: podIndex},
						func(*corev1.Pod) {},
					)
				}
			})

			readyTime := time.Now()

			ginkgo.By("Mark pods as ready", func() {
				for podIndex := range uint32(5) {
					fixtures.MarkPodAsReady(ctx, &env, testutil.PodId{Cluster: 1, Pod: podIndex}, readyTime)
				}
			})

			ginkgo.By("Update PodProtector status manually", func() {
				testutil.DoUpdate(
					ctx,
					env.PprClient().Get,
					env.PprClient().UpdateStatus,
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
									ReadyReplicas:     5,
									ScheduledReplicas: 5,
									RunningReplicas:   5,
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
					env.PprClient().Watch,
					pprName,
					aggregatorReconcileTimeout,
					testutil.MatchPprStatus(5, 5, 5, map[testutil.ClusterId]int32{1: 5, 2: 0}, map[testutil.ClusterId]int32{1: 5, 2: 0}),
				)
			})

			ginkgo.By("Validate that we can delete the first pod", func() {
				err := env.PodClient(1).
					Delete(ctx, testutil.PodId{Cluster: 1, Pod: 0}.PodName(), metav1.DeleteOptions{})
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			})

			for _, dm := range DefaultDeletionMethods {
				ginkgo.By(fmt.Sprintf("Validate that excessive %s is rejected", dm.Noun()), func() {
					err := dm.Delete(ctx, &env, testutil.PodId{Cluster: 1, Pod: 1})
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
			}
		},
	)

	ginkgo.It("allows unready pod deletion", func(ctx ginkgo.SpecContext) {
		ginkgo.By("Setup PodProtector and worker pods", func() {
			fixtures.CreatePodProtectorAndPods(
				ctx,
				&env,
				pprName,
				testutil.PodCounts{1: 1, 2: 0},
				2, 0,
				podseidonv1a1.AdmissionHistoryConfig{
					MaxConcurrentLag:      nil,
					CompactThreshold:      ptr.To[int32](100),
					AggregationRateMillis: ptr.To[int32](2000),
				},
			)
		})

		ginkgo.By("Wait for PodProtector state to converge", func() {
			testutil.ExpectObject[*podseidonv1a1.PodProtector](
				ctx,
				env.PprClient().Watch,
				pprName,
				aggregatorReconcileTimeout,
				testutil.MatchPprStatus(1, 0, 0, map[testutil.ClusterId]int32{1: 1, 2: 0}, map[testutil.ClusterId]int32{1: 0, 2: 0}),
			)
		})

		ginkgo.By("Validate that the unready pod can be deleted", func() {
			err := env.PodClient(1).Delete(ctx, testutil.PodId{Cluster: 1, Pod: 0}.PodName(), metav1.DeleteOptions{})
			gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
		})
	})

	ginkgo.It("allows ready but unavailable pod deletion", func(ctx ginkgo.SpecContext) {
		ginkgo.By("Setup PodProtector and worker pods", func() {
			fixtures.CreatePodProtectorAndPods(
				ctx,
				&env,
				pprName,
				testutil.PodCounts{1: 1, 2: 0},
				2, 600,
				podseidonv1a1.AdmissionHistoryConfig{
					MaxConcurrentLag:      nil,
					CompactThreshold:      ptr.To[int32](100),
					AggregationRateMillis: ptr.To[int32](2000),
				},
			)
		})

		ginkgo.By("Mark pods as ready", func() {
			// Assumption: this test must complete within 10 minutes after time.Now().
			readyTime := time.Now()

			for _, podId := range (testutil.PodCounts{1: 1, 2: 0}).PodIds() {
				fixtures.MarkPodAsReady(ctx, &env, podId, readyTime)
			}
		})

		ginkgo.By("Wait for PodProtector state to converge", func() {
			testutil.ExpectObject[*podseidonv1a1.PodProtector](
				ctx,
				env.PprClient().Watch,
				pprName,
				aggregatorReconcileTimeout,
				gomega.SatisfyAll(
					gomega.WithTransform(
						func(ppr *podseidonv1a1.PodProtector) int32 {
							return testutil.GetPprCell(ppr, 1).GetOrZero().Aggregation.ReadyReplicas
						},
						gomega.Equal(int32(1)),
					),
					testutil.MatchPprStatus(1, 0, 0, map[testutil.ClusterId]int32{1: 1, 2: 0}, map[testutil.ClusterId]int32{1: 0, 2: 0}),
				),
			)
		})

		ginkgo.By("Validate that the unavailable pod can be deleted", func() {
			err := env.PodClient(1).Delete(ctx, testutil.PodId{Cluster: 1, Pod: 0}.PodName(), metav1.DeleteOptions{})
			gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
		})
	})

	for _, tc := range []struct {
		dm       DeletionMethod
		annotate bool
	}{
		{dm: DeletionMethodDelete{}, annotate: true},
		{dm: DeletionMethodEvict{}, annotate: true},
		{dm: DeletionMethodAnnotatedEvict{
			Annotations: map[string]string{
				podseidon.EvictionAnnotationHealthCriterion: podseidon.HealthCriterionRunning,
			},
		}, annotate: false},
	} {
		ginkgo.It(fmt.Sprintf("allows annotated pod %s when peer is running", tc.dm.Noun()), func(ctx ginkgo.SpecContext) {
			ginkgo.By("Setup PodProtector and worker pods", func() {
				fixtures.CreatePodProtectorAndPods(
					ctx,
					&env,
					pprName,
					testutil.PodCounts{1: 1, 2: 1},
					1, 0,
					podseidonv1a1.AdmissionHistoryConfig{
						MaxConcurrentLag:      nil,
						CompactThreshold:      ptr.To[int32](100),
						AggregationRateMillis: ptr.To[int32](2000),
					},
				)
			})

			ginkgo.By("Mark pods as ready and running respectively", func() {
				readyTime := time.Now()

				fixtures.MarkPodAsReady(ctx, &env, testutil.PodId{Cluster: 1, Pod: 0}, readyTime)

				fixtures.MarkPodConditions(
					ctx,
					&env,
					testutil.PodId{Cluster: 2, Pod: 0},
					fixtures.PodConditions{
						Phase:       corev1.PodRunning,
						Scheduled:   optional.Some(readyTime),
						Initialized: optional.Some(readyTime),
						Ready:       optional.None[time.Time](),
					},
				)
			})

			ginkgo.By("Wait for PodProtector state to converge", func() {
				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					env.PprClient().Watch,
					pprName,
					aggregatorReconcileTimeout,
					testutil.MatchPprStatus(2, 1, 1, map[testutil.ClusterId]int32{1: 1, 2: 1}, map[testutil.ClusterId]int32{1: 1, 2: 0}),
				)
			})

			if tc.annotate {
				ginkgo.By(fmt.Sprintf("Validate that available pod %s is rejected", tc.dm.Noun()), func() {
					err := tc.dm.Delete(ctx, &env, testutil.PodId{Cluster: 1, Pod: 0})
					gomega.Expect(err).Should(gomega.HaveOccurred())
					gomega.Expect(err).
						Should(gomega.MatchError(gomega.ContainSubstring("too few available replicas to admit pod deletion")))
				})

				ginkgo.By("Annotate criterion annotation", func() {
					fixtures.UpdatePod(ctx, &env, testutil.PodId{Cluster: 1, Pod: 0}, func(pod *corev1.Pod) {
						if pod.Annotations == nil {
							pod.Annotations = map[string]string{}
						}

						pod.Annotations[podseidon.EvictionAnnotationHealthCriterion] = podseidon.HealthCriterionRunning
					})
				})
			}

			ginkgo.By(fmt.Sprintf("Validate that available pod %s is allowed", tc.dm.Noun()), func() {
				err := tc.dm.Delete(ctx, &env, testutil.PodId{Cluster: 1, Pod: 0})
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			})
		})
	}
})

type DeletionMethod interface {
	Noun() string

	Delete(ctx context.Context, env *provision.Env, podId testutil.PodId) error
}

type DeletionMethodDelete struct{}

func (DeletionMethodDelete) Noun() string { return "deletion" }

func (DeletionMethodDelete) Delete(ctx context.Context, env *provision.Env, podId testutil.PodId) error {
	return env.PodClient(podId.Cluster).Delete(ctx, podId.PodName(), metav1.DeleteOptions{})
}

type DeletionMethodEvict struct{}

func (DeletionMethodEvict) Noun() string { return "eviction" }

func (DeletionMethodEvict) Delete(ctx context.Context, env *provision.Env, podId testutil.PodId) error {
	return env.PodClient(podId.Cluster).EvictV1(ctx, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name: podId.PodName(),
		},
	})
}

type DeletionMethodAnnotatedEvict struct {
	Annotations map[string]string
}

func (DeletionMethodAnnotatedEvict) Noun() string {
	return "eviction with annotation in Eviction object"
}

func (dm DeletionMethodAnnotatedEvict) Delete(ctx context.Context, env *provision.Env, podId testutil.PodId) error {
	return env.PodClient(podId.Cluster).EvictV1(ctx, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podId.PodName(),
			Annotations: dm.Annotations,
		},
	})
}

var DefaultDeletionMethods = []DeletionMethod{
	DeletionMethodDelete{},
	DeletionMethodEvict{},
}
