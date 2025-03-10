package testutil

import (
	"context"
	"errors"
	"time"

	"github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/util/retry"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/optional"
)

// Expects that an object *eventually* satisfies all matchers.
//
// This function starts a watch request to receive changes as soon as possible.
func ExpectObject[T runtime.Object](
	ctx context.Context,
	watcherFn func(context.Context, metav1.ListOptions) (watch.Interface, error),
	objectName string,
	timeout time.Duration,
	matchers ...gomegatypes.GomegaMatcher,
) T {
	watcher, err := watcherFn(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(metav1.ObjectNameField, objectName).String(),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	defer watcher.Stop()

	var lastVersion T

	gomega.Eventually(ctx, watcher.ResultChan()).WithTimeout(timeout).Should(
		gomega.Receive(gomega.WithTransform(
			func(event watch.Event) any {
				lastVersion = event.Object.(T)
				return lastVersion
			},
			gomega.SatisfyAll(matchers...),
		)),
	)

	return lastVersion
}

// Expects that an object *consistently* does not satisfy any matchers.
//
// This function starts a watch request to receive all changes
// to ensure none of the versions during the period satisfies the object.
func ExpectObjectNot[T runtime.Object](
	ctx context.Context,
	watcherFn func(context.Context, metav1.ListOptions) (watch.Interface, error),
	objectName string,
	timeout time.Duration,
	matchers ...gomegatypes.GomegaMatcher,
) T {
	watcher, err := watcherFn(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(metav1.ObjectNameField, objectName).String(),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	defer watcher.Stop()

	var lastVersion T

	gomega.Consistently(ctx, watcher.ResultChan()).WithTimeout(timeout).ShouldNot(
		gomega.Receive(gomega.WithTransform(
			func(event watch.Event) any {
				lastVersion = event.Object.(T)
				return lastVersion
			},
			gomega.SatisfyAny(matchers...),
		)),
	)

	return lastVersion
}

// Retry performing an update operation on an object until it succeeds.
func DoUpdate[T runtime.Object](
	ctx context.Context,
	clientGet func(ctx context.Context, name string, options metav1.GetOptions) (T, error),
	clientUpdate func(ctx context.Context, object T, options metav1.UpdateOptions) (T, error),
	name string,
	mutateFn func(T),
) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := clientGet(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		mutateFn(obj)
		_, err = clientUpdate(ctx, obj, metav1.UpdateOptions{})

		return err
	})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
}

func getPprCell(ppr *podseidonv1a1.PodProtector, worker WorkerIndex) optional.Optional[podseidonv1a1.PodProtectorCellStatus] {
	for _, cell := range ppr.Status.Cells {
		if cell.CellId == worker.String() {
			return optional.Some(cell)
		}
	}

	return optional.None[podseidonv1a1.PodProtectorCellStatus]()
}

func MatchPprStatus(totalReplicas int32, aggregatedAvailable int32, estimatedAvailable int32,
	workerTotalReplicas [2]int32, workerAvailableReplicas [2]int32,
) gomega.OmegaMatcher {
	return gomega.SatisfyAll(
		gomega.WithTransform(
			func(ppr *podseidonv1a1.PodProtector) int32 { return ppr.Status.Summary.Total },
			gomega.Equal(totalReplicas),
		),
		gomega.WithTransform(
			func(ppr *podseidonv1a1.PodProtector) int32 { return ppr.Status.Summary.AggregatedAvailable },
			gomega.Equal(aggregatedAvailable),
		),
		gomega.WithTransform(
			func(ppr *podseidonv1a1.PodProtector) int32 { return ppr.Status.Summary.EstimatedAvailable },
			gomega.Equal(estimatedAvailable),
		),
		gomega.WithTransform(
			func(ppr *podseidonv1a1.PodProtector) int32 {
				return getPprCell(ppr, 0).GetOrZero().Aggregation.TotalReplicas
			},
			gomega.Equal(workerTotalReplicas[0]),
		),
		gomega.WithTransform(
			func(ppr *podseidonv1a1.PodProtector) int32 {
				return getPprCell(ppr, 0).GetOrZero().Aggregation.AvailableReplicas
			},
			gomega.Equal(workerAvailableReplicas[0]),
		),
		gomega.WithTransform(
			func(ppr *podseidonv1a1.PodProtector) int32 {
				return getPprCell(ppr, 1).GetOrZero().Aggregation.TotalReplicas
			},
			gomega.Equal(workerTotalReplicas[1]),
		),
		gomega.WithTransform(
			func(ppr *podseidonv1a1.PodProtector) int32 {
				return getPprCell(ppr, 1).GetOrZero().Aggregation.AvailableReplicas
			},
			gomega.Equal(workerAvailableReplicas[1]),
		),
	)
}

func ToStatusError(err error) *apierrors.StatusError {
	target := new(apierrors.StatusError)
	if errors.As(err, &target) {
		return target
	}

	return nil
}
