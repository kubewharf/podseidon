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

package handler

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/optional"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
	"github.com/kubewharf/podseidon/util/retrybatch"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/webhook/handler/batchitem"
	"github.com/kubewharf/podseidon/webhook/handler/disruptionquota"
	"github.com/kubewharf/podseidon/webhook/observer"
)

// Hack: another type alias to resolve import cycle issues.
type BatchItem struct {
	observer.BatchItem

	PodUid  types.UID
	PodName string

	PodStatus disruptionquota.PodStatus
}

type PoolAdapter struct {
	sourceProvider  pprutil.SourceProvider
	pprInformer     pprutil.IndexedInformer
	observer        observer.Observer
	clock           clock.Clock
	requiresPodName RequiresPodName
	retryBackoff    func() time.Duration
	defaultConfig   *defaultconfig.Options
}

func (PoolAdapter) PoolName() string {
	return "podprotector-update"
}

func (adapter PoolAdapter) Execute(
	ctx context.Context,
	key pprutil.PodProtectorKey,
	args []BatchItem,
) retrybatch.ExecuteResult[batchitem.Result] {
	ctx, cancelFunc := adapter.observer.StartExecuteRetry(
		ctx,
		observer.StartExecuteRetry{Key: key, Args: util.MapSlice(args, func(arg BatchItem) observer.BatchItem { return arg.BatchItem })},
	)
	defer cancelFunc()

	result := adapter.tryExecute(ctx, key, args)

	//revive:disable-next-line:enforce-switch-style
	switch result.Variant {
	case retrybatch.ExecuteResultVariantSuccess:
		adapter.observer.EndExecuteRetrySuccess(
			ctx,
			observer.EndExecuteRetrySuccess{Results: result.Success},
		)

	case retrybatch.ExecuteResultVariantNeedRetry:
		adapter.observer.EndExecuteRetryRetry(
			ctx,
			observer.EndExecuteRetryRetry{Delay: result.NeedRetry},
		)

	case retrybatch.ExecuteResultVariantErr:
		adapter.observer.EndExecuteRetryErr(ctx, observer.EndExecuteRetryErr{Err: result.Err})
	}

	return result
}

func (adapter PoolAdapter) tryExecute(
	ctx context.Context,
	key pprutil.PodProtectorKey,
	args []BatchItem,
) retrybatch.ExecuteResult[batchitem.Result] {
	pprOptional, err := adapter.pprInformer.Get(key)
	if err != nil {
		return retrybatch.ExecuteResultErr[batchitem.Result](
			errors.TagWrapf("GetListerPpr", err, "get ppr from lister"),
		)
	}

	originalPpr, present := pprOptional.Get()
	if !present {
		return retrybatch.ExecuteResultSuccess(
			func(int) batchitem.Result { return batchitem.ResultNoPpr },
		)
	}

	ppr := originalPpr.DeepCopy()
	config := adapter.defaultConfig.Compute(optional.Some(ppr.Spec.AdmissionHistoryConfig))
	pprutil.Summarize(config, ppr)

	results := make([]batchitem.Result, len(args))

	type undeterminedItem struct {
		podStatus   disruptionquota.PodStatus
		resultEntry *batchitem.Result

		podName string
		podUid  types.UID
		cellId  string
	}

	executeTime := adapter.clock.Now()

	undetermined := make([]undeterminedItem, 0, len(args))
	for argIndex, arg := range args {
		cellStatus := util.GetOrAppend(
			&ppr.Status.Cells,
			func(cell *podseidonv1a1.PodProtectorCellStatus) bool { return cell.CellId == arg.CellId },
		)

		duplicateBucket := util.FindInSliceWith(
			cellStatus.History.Buckets,
			func(bucket podseidonv1a1.PodProtectorAdmissionBucket) bool {
				return bucket.PodUid != nil && *bucket.PodUid == arg.PodUid
			},
		)
		if duplicateBucket != -1 {
			cellStatus.History.Buckets[duplicateBucket].StartTime = metav1.MicroTime{
				Time: executeTime,
			}
			results[argIndex] = batchitem.ResultAlreadyHasBucket

			continue
		}

		undetermined = append(undetermined, undeterminedItem{
			podStatus:   arg.PodStatus,
			resultEntry: &results[argIndex],
			podName:     arg.PodName,
			podUid:      arg.PodUid,
			cellId:      arg.CellId,
			// do not leak cellStatus here, since it may become a dangling pointer if ppr.Status.Cells is appended
		})
	}

	if len(undetermined) > 0 {
		state := disruptionquota.MakeState(ppr, config)

		adapter.observer.AdmitBatchState(ctx, observer.AdmitBatchState{
			PprKey:    key,
			State:     state,
			BatchSize: len(undetermined),
			After:     false,
		})

		for undeterminedIndex := len(undetermined) - 1; undeterminedIndex >= 0; undeterminedIndex-- {
			item := undetermined[undeterminedIndex]
			admitResult := state.AdmitBatchItem(item.podStatus)

			*item.resultEntry = admitResultToBatchItemResult(admitResult)

			if admitResult.ShouldDisrupt() {
				writePodName := ""
				if adapter.requiresPodName.RequiresPodName(RequiresPodNameArg{CellId: item.cellId, PodName: item.podName}) {
					writePodName = item.podName
				}

				cellIndex := util.FindInSliceWith(
					ppr.Status.Cells,
					func(cell podseidonv1a1.PodProtectorCellStatus) bool { return cell.CellId == item.cellId },
				)
				cellStatus := &ppr.Status.Cells[cellIndex]
				cellStatus.History.Buckets = append(
					cellStatus.History.Buckets,
					podseidonv1a1.PodProtectorAdmissionBucket{
						StartTime: metav1.MicroTime{Time: executeTime},
						PodUid:    ptr.To(item.podUid),
						PodName:   writePodName,
					},
				)
			}
		}

		adapter.observer.AdmitBatchState(ctx, observer.AdmitBatchState{
			PprKey:    key,
			State:     state,
			BatchSize: len(undetermined),
			After:     true,
		})
	}

	pprutil.Summarize(config, ppr)

	for argIndex, arg := range args {
		adapter.observer.ExecuteBatchItem(ctx, observer.ExecuteBatchItem{
			PodName:         arg.PodName,
			PodUid:          arg.PodUid,
			CellId:          arg.CellId,
			Result:          results[argIndex],
			HealthCriterion: arg.PodStatus.HealthCriterion,
		})
	}

	if !equality.Semantic.DeepEqual(originalPpr, ppr) {
		if err := adapter.sourceProvider.UpdateStatus(ctx, key.SourceName, ppr); err != nil {
			if apierrors.IsConflict(err) {
				return retrybatch.ExecuteResultNeedRetry[batchitem.Result](
					adapter.retryBackoff(),
				)
			}

			return retrybatch.ExecuteResultErr[batchitem.Result](errors.TagWrapf(
				"BatchUpdatePprStatus",
				err,
				"unable to update PodProtector status",
			))
		}
	}

	return retrybatch.ExecuteResultSuccess(
		func(i int) batchitem.Result { return results[i] },
	)
}

func admitResultToBatchItemResult(result disruptionquota.Result) batchitem.Result {
	switch result {
	case disruptionquota.DisruptionResultOk:
		return batchitem.ResultNewDisruption
	case disruptionquota.DisruptionResultAlreadyUnhealthy:
		return batchitem.ResultAlreadyUnhealthy
	case disruptionquota.DisruptionResultRetry:
		return batchitem.ResultNeedRetry
	case disruptionquota.DisruptionResultDenied:
		return batchitem.ResultRejected
	default:
		return 0 // GIGO
	}
}
