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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/optional"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
	"github.com/kubewharf/podseidon/util/retrybatch"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/webhook/observer"
)

// Hack: another type alias to resolve import cycle issues.
type BatchArg = observer.BatchArg

type PoolAdapter struct {
	sourceProvider pprutil.SourceProvider
	pprInformer    pprutil.IndexedInformer
	observer       observer.Observer
	clock          clock.Clock
	retryBackoff   func() time.Duration
	defaultConfig  *defaultconfig.Options
}

func (PoolAdapter) PoolName() string {
	return "podprotector-update"
}

func (adapter PoolAdapter) Execute(
	ctx context.Context,
	key pprutil.PodProtectorKey,
	args []BatchArg,
) retrybatch.ExecuteResult[pprutil.DisruptionResult] {
	ctx, cancelFunc := adapter.observer.StartExecuteRetry(
		ctx,
		observer.StartExecuteRetry{Key: key, Args: args},
	)
	defer cancelFunc()

	result := adapter.tryExecute(ctx, key, args)

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

// Three possible returns:
// - uninit, uninit, uninit, non-nil: an error occurred
// - uninit, value, false, nil: need retry
// - value, uninit, true, nil: successful
// "uninit" values must not be used.
func (adapter PoolAdapter) tryExecute(
	ctx context.Context,
	key pprutil.PodProtectorKey,
	args []BatchArg,
) retrybatch.ExecuteResult[pprutil.DisruptionResult] {
	pprOptional, err := adapter.pprInformer.Get(key)
	if err != nil {
		return retrybatch.ExecuteResultErr[pprutil.DisruptionResult](
			errors.TagWrapf("GetListerPpr", err, "get ppr from lister"),
		)
	}

	ppr, present := pprOptional.Get()
	if !present {
		return retrybatch.ExecuteResultSuccess(
			func(int) pprutil.DisruptionResult { return pprutil.DisruptionResultOk },
		)
	}

	ppr = ppr.DeepCopy()

	config := adapter.defaultConfig.Compute(optional.Some(ppr.Spec.AdmissionHistoryConfig))

	executeTime := adapter.clock.Now()

	pprutil.Summarize(config, ppr)
	quota := pprutil.ComputeDisruptionQuota(ppr.Spec.MinAvailable, config, ppr.Status.Summary)

	initialQuota := quota // value copy

	results := make([]pprutil.DisruptionResult, len(args))

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
			results[argIndex] = pprutil.DisruptionResultOk // already disrupted

			continue
		}

		result := quota.Disrupt()
		results[argIndex] = result

		if result == pprutil.DisruptionResultOk {
			cellStatus.History.Buckets = append(
				cellStatus.History.Buckets,
				podseidonv1a1.PodProtectorAdmissionBucket{
					StartTime: metav1.MicroTime{Time: executeTime},
					PodUid:    ptr.To(arg.PodUid),
				},
			)
		}
	}

	adapter.observer.ExecuteRetryQuota(ctx, observer.ExecuteRetryQuota{
		Before: initialQuota,
		After:  quota,
	})

	pprutil.Summarize(config, ppr)

	if err := adapter.sourceProvider.UpdateStatus(ctx, key.SourceName, ppr); err != nil {
		if apierrors.IsConflict(err) {
			return retrybatch.ExecuteResultNeedRetry[pprutil.DisruptionResult](
				adapter.retryBackoff(),
			)
		}

		return retrybatch.ExecuteResultErr[pprutil.DisruptionResult](errors.TagWrapf(
			"BatchUpdatePprStatus",
			err,
			"unable to update PodProtector status",
		))
	}

	return retrybatch.ExecuteResultSuccess(
		func(i int) pprutil.DisruptionResult { return results[i] },
	)
}
