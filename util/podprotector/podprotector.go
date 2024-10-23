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
	"fmt"
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
	}

	for cellId := range ppr.Status.Cells {
		cell := &ppr.Status.Cells[cellId]

		summary.Total += cell.Aggregation.TotalReplicas
		summary.AggregatedAvailable += cell.Aggregation.AvailableReplicas

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

func ComputeDisruptionQuota(
	minAvailable int32,
	config defaultconfig.Computed,
	summary podseidonv1a1.PodProtectorStatusSummary,
) DisruptionQuota {
	var quota DisruptionQuota

	switch {
	case summary.AggregatedAvailable < minAvailable: // Case 1: est < agg < min
		quota = DisruptionQuota{Cleared: 0, Transitional: 0}
	case summary.EstimatedAvailable < minAvailable: // Case 2: est < min < agg
		quota = DisruptionQuota{
			Cleared:      0,
			Transitional: summary.AggregatedAvailable - minAvailable,
		}
	default: // Case 3: min < est < agg
		quota = DisruptionQuota{
			Cleared:      summary.EstimatedAvailable - minAvailable,
			Transitional: summary.AggregatedAvailable - summary.EstimatedAvailable,
		}
	}

	if config.MaxConcurrentLag > 0 {
		// number of entries available in the admission history
		admissionQuota := max(
			0,
			config.MaxConcurrentLag-(summary.AggregatedAvailable-summary.EstimatedAvailable),
		)

		if quota.Cleared > admissionQuota {
			delta := quota.Cleared - admissionQuota
			quota.Cleared = admissionQuota
			quota.Transitional += delta
		}
	}

	return quota
}

type DisruptionQuota struct {
	// Number of replicas that can be disrupted without any risk.
	Cleared int32
	// Number of replicas that cannot be disrupted due to admission history,
	// but can be disrupted if admission history is emptied.
	Transitional int32
}

func (quota *DisruptionQuota) Disrupt() DisruptionResult {
	if quota.Cleared > 0 {
		quota.Cleared--
		return DisruptionResultOk
	}

	if quota.Transitional > 0 {
		quota.Transitional--
		return DisruptionResultRetry
	}

	return DisruptionResultDenied
}

type DisruptionResult uint8

const (
	DisruptionResultOk = DisruptionResult(iota)
	DisruptionResultRetry
	DisruptionResultDenied
)

func (result DisruptionResult) String() string {
	switch result {
	case DisruptionResultOk:
		return "Ok"
	case DisruptionResultRetry:
		return "Retry"
	case DisruptionResultDenied:
		return "Denied"
	}

	panic(fmt.Sprintf("unknown enum value %#v", result))
}
