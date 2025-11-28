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

package pprutil

import (
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/defaultconfig"
)

func GetAggregationSelector(ppr *podseidonv1a1.PodProtector) metav1.LabelSelector {
	return ptr.Deref(ppr.Spec.AggregationSelector, ppr.Spec.Selector)
}

// Reconcile the .status field of a PodProtector.
//
// This function should not be called unless the PodProtector status is otherwise updated locally.
//
// The caller is responsible for cleaning up obsolete buckets.
// This function assumes that all remaining buckets are lagging buckets.
// Failure to do so would result in incorrect EstimatedAvailable computation.
func Summarize(config defaultconfig.Computed, ppr *podseidonv1a1.PodProtector) {
	summary := &ppr.Status.Summary
	*summary = podseidonv1a1.PodProtectorStatusSummary{
		Total:               0,
		AggregatedAvailable: 0,
		MaxLatencyMillis:    0,
		EstimatedAvailable:  0,
		AggregatedReady:     0,
		AggregatedScheduled: 0,
		AggregatedRunning:   0,
	}

	for cellId := range ppr.Status.Cells {
		cell := &ppr.Status.Cells[cellId]

		summary.Total += cell.Aggregation.TotalReplicas
		summary.AggregatedAvailable += cell.Aggregation.AvailableReplicas
		summary.AggregatedReady += cell.Aggregation.ReadyReplicas
		summary.AggregatedScheduled += cell.Aggregation.ScheduledReplicas
		summary.AggregatedRunning += cell.Aggregation.RunningReplicas

		CompactBuckets(config, &cell.History.Buckets)

		if len(cell.History.Buckets) > 0 {
			lagDuration := cell.History.Buckets[len(cell.History.Buckets)-1].StartTime.Sub(
				cell.Aggregation.LastEventTime.Time,
			)
			summary.MaxLatencyMillis = max(
				summary.MaxLatencyMillis,
				lagDuration.Milliseconds(),
			)
		}
	}

	summary.EstimatedAvailable = summary.AggregatedAvailable

	for _, cell := range ppr.Status.Cells {
		for _, bucket := range cell.History.Buckets {
			summary.EstimatedAvailable -= ptr.Deref(bucket.Counter, 1)
		}
	}
}

func CompactBuckets(
	config defaultconfig.Computed,
	buckets *[]podseidonv1a1.PodProtectorAdmissionBucket,
) {
	sort.Slice(
		*buckets,
		func(i, j int) bool { return (*buckets)[i].StartTime.Time.Before((*buckets)[j].StartTime.Time) },
	)

	delta := len(*buckets) - int(config.CompactThreshold)
	if delta > 0 && len(*buckets) > 0 {
		if firstBucket := (*buckets)[0]; firstBucket.PodUid != nil {
			(*buckets)[0] = podseidonv1a1.PodProtectorAdmissionBucket{
				StartTime: firstBucket.StartTime,
				Counter:   ptr.To(int32(1)),
				// ptr.To is a misleading name; it actually allocates a new box and copies the value to the box.
				EndTime: ptr.To(firstBucket.StartTime),
			}
		}

		compactBucket := &(*buckets)[0]

		for bucketId := 1; bucketId <= delta; bucketId++ {
			bucket := (*buckets)[bucketId]

			bucketEndTime := bucket.StartTime.Time
			if bucket.EndTime != nil {
				// There shouldn't be a compact bucket that isn't the oldest bucket,
				// but let's handle this case anyway.
				bucketEndTime = bucket.EndTime.Time
			}

			if bucketEndTime.After(compactBucket.EndTime.Time) {
				compactBucket.EndTime.Time = bucketEndTime
			}
		}

		copyCount := len(*buckets) - 1 - delta
		copy((*buckets)[1:copyCount+1], (*buckets)[delta+1:])
		*buckets = (*buckets)[:copyCount+1]
	}
}
