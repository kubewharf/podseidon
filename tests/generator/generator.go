package generator

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	podseidon "github.com/kubewharf/podseidon/apis"
	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/iter"

	testutil "github.com/kubewharf/podseidon/tests/util"
)

const (
	synchronousReconcileTimeout         time.Duration = time.Second
	negativeSynchronousReconcileTimeout time.Duration = time.Second * 5
)

var _ = ginkgo.Describe("Generator", func() {
	setup := testutil.SetupBeforeEach()

	ginkgo.Context("Deployment", func() {
		ginkgo.It("maintains the lifecycle of a child PodProtector", func(ctx ginkgo.SpecContext) {
			deployClient := setup.CoreNativeClient.AppsV1().Deployments(setup.Namespace)

			const workloadName string = "workload"

			var err error

			ginkgo.By("Creating Deployment", func() {
				setup.ReportKelemetryTrace(
					ctx,
					"core",
					appsv1.SchemeGroupVersion.WithResource("deployments"),
					workloadName,
				)

				_, err = deployClient.Create(ctx, &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:   workloadName,
						Labels: map[string]string{"test": setup.Namespace},
					},
					Spec: appsv1.DeploymentSpec{
						Replicas:        ptr.To[int32](10),
						MinReadySeconds: 15,
						Strategy: appsv1.DeploymentStrategy{
							Type: appsv1.RollingUpdateDeploymentStrategyType,
							RollingUpdate: &appsv1.RollingUpdateDeployment{
								MaxUnavailable: ptr.To(intstr.FromString("30%")),
							},
						},
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"test": setup.Namespace},
						},
						Template: corev1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{"test": setup.Namespace},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  "container",
										Image: "example.com/kwok/no:image",
									},
								},
							},
						},
					},
				}, metav1.CreateOptions{})
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			})

			ginkgo.By("Waiting for finalizer on deployment", func() {
				testutil.ExpectObject[*appsv1.Deployment](
					ctx,
					deployClient.Watch,
					workloadName,
					synchronousReconcileTimeout,
					gomega.WithTransform(
						(*appsv1.Deployment).GetFinalizers,
						gomega.ContainElement(podseidon.GeneratorFinalizer),
					),
				)
			})

			pprName := fmt.Sprintf("deployment-%s", workloadName)

			ginkgo.By("Waiting for PodProtector creation", func() {
				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					setup.PprClient().Watch,
					pprName,
					synchronousReconcileTimeout,
					gomega.WithTransform(
						(*podseidonv1a1.PodProtector).GetFinalizers,
						gomega.ContainElement(podseidon.GeneratorFinalizer),
					),
					gomega.WithTransform(
						func(ppr *podseidonv1a1.PodProtector) any { return ppr.Spec.MinAvailable },
						gomega.Equal(int32(7)),
					),
					gomega.WithTransform(
						func(ppr *podseidonv1a1.PodProtector) any { return ppr.Spec.MinReadySeconds },
						gomega.Equal(int32(15)),
					),
				)
			})

			ginkgo.By("Scaling PodProtector", func() {
				testutil.DoUpdate(
					ctx,
					deployClient.Get,
					deployClient.Update,
					workloadName,
					func(deploy *appsv1.Deployment) {
						deploy.Spec.Replicas = ptr.To(int32(20))
					},
				)
			})

			ginkgo.By("Waiting for PodProtector update", func() {
				testutil.ExpectObject[*podseidonv1a1.PodProtector](
					ctx,
					setup.PprClient().Watch,
					pprName,
					synchronousReconcileTimeout,
					gomega.WithTransform(
						func(ppr *podseidonv1a1.PodProtector) any { return ppr.Spec.MinAvailable },
						gomega.Equal(int32(14)),
					),
				)
			})

			ginkgo.By("Deleting PodProtector", func() {
				err := setup.PprClient().Delete(ctx, pprName, metav1.DeleteOptions{})
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			})

			ginkgo.By("PodProtector should remain alive due to finalizer", func() {
				gomega.Consistently(ctx, func() error {
					_, err := setup.PprClient().Get(ctx, pprName, metav1.GetOptions{})
					return err
				}).WithTimeout(negativeSynchronousReconcileTimeout).ShouldNot(gomega.HaveOccurred())
			})

			ginkgo.By("Deleting workload", func() {
				err := deployClient.Delete(ctx, workloadName, metav1.DeleteOptions{})
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			})

			ginkgo.By("PodProtector should disappear", func() {
				gomega.Eventually(ctx, func() error {
					_, err := setup.PprClient().Get(ctx, pprName, metav1.GetOptions{})
					return err
				}).WithTimeout(synchronousReconcileTimeout).Should(
					gomega.WithTransform(apierrors.ReasonForError, gomega.Equal(metav1.StatusReasonNotFound)),
				)
			})

			ginkgo.By("Workload should be deleted", func() {
				gomega.Eventually(ctx, func() iter.Pair[*appsv1.Deployment, error] {
					deploy, err := deployClient.Get(ctx, pprName, metav1.GetOptions{})
					return iter.NewPair(deploy, err)
				}).WithTimeout(synchronousReconcileTimeout).Should(gomega.SatisfyAny(
					gomega.WithTransform(func(pair iter.Pair[*appsv1.Deployment, error]) []string {
						if err == nil {
							return pair.Left.GetFinalizers()
						}

						return nil
					}, gomega.Not(gomega.ContainElement(podseidon.GeneratorFinalizer))),
					gomega.WithTransform(
						func(pair iter.Pair[*appsv1.Deployment, error]) metav1.StatusReason {
							return apierrors.ReasonForError(pair.Right)
						},
						gomega.Equal(metav1.StatusReasonNotFound),
					),
				))
			})
		})
	})
})