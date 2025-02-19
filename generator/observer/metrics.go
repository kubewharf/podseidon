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

package observer

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	"github.com/kubewharf/podseidon/util/util"
)

func ProvideMetrics() component.Declared[Observer] {
	return o11y.Provide(
		metrics.MakeObserverDeps,
		func(deps metrics.ObserverDeps) Observer {
			type reconcileGvrKey struct{}

			type ReconcileGvr struct{ Group, Version, Resource string }

			type reconcileStartTime struct{}

			type generatorReconcileTags struct {
				ReconcileGvr

				Error string
			}

			type generatorActionTags struct {
				ReconcileGvr

				Action string
			}

			reconcileHandle := metrics.Register(
				deps.Registry(),
				"generator_reconcile",
				"Duration of generator reconcile runs.",
				metrics.FunctionDurationHistogram(),
				metrics.NewReflectTags[generatorReconcileTags](),
			)

			actionTypeHandle := metrics.Register(
				deps.Registry(),
				"generator_action",
				"Number of generator reconcile runs by action.",
				metrics.IntCounter(),
				metrics.NewReflectTags[generatorActionTags](),
			)
			emitAction := func(ctx context.Context, action string) {
				actionTypeHandle.Emit(1, generatorActionTags{
					ReconcileGvr: ctx.Value(reconcileGvrKey{}).(ReconcileGvr),
					Action:       action,
				})
			}

			monitorWorkloadsHandle := makeMonitorWorkloadsHandle(deps.Registry())

			return Observer{
				InterpretProtectors: func(context.Context, InterpretProtectors) {},
				StartReconcile: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
					ctx = context.WithValue(ctx, reconcileGvrKey{}, ReconcileGvr{
						Group:    arg.Group,
						Version:  arg.Version,
						Resource: arg.Resource,
					})
					ctx = context.WithValue(ctx, reconcileStartTime{}, time.Now())

					return ctx, util.NoOp
				},
				EndReconcile: func(ctx context.Context, arg EndReconcile) {
					duration := time.Since(ctx.Value(reconcileStartTime{}).(time.Time))

					reconcileHandle.Emit(duration, generatorReconcileTags{
						ReconcileGvr: ctx.Value(reconcileGvrKey{}).(ReconcileGvr),
						Error:        errors.SerializeTags(arg.Err),
					})
				},
				DanglingProtector: func(ctx context.Context, ppr *podseidonv1a1.PodProtector) {
					if ppr.DeletionTimestamp.IsZero() {
						emitAction(ctx, "Dangling")
					} else {
						emitAction(ctx, "Finalizing")
					}
				},
				CreateProtector: func(ctx context.Context, _ util.Empty) (context.Context, context.CancelFunc) {
					emitAction(ctx, "Create")
					return ctx, util.NoOp
				},
				SyncProtector: func(ctx context.Context, _ *podseidonv1a1.PodProtector) (context.Context, context.CancelFunc) {
					emitAction(ctx, "Sync")
					return ctx, util.NoOp
				},
				DeleteProtector: func(ctx context.Context, _ *podseidonv1a1.PodProtector) (context.Context, context.CancelFunc) {
					emitAction(ctx, "Delete")
					return ctx, util.NoOp
				},
				CleanSourceFinalizer: func(ctx context.Context, arg StartReconcile) (context.Context, context.CancelFunc) {
					actionTypeHandle.Emit(1, generatorActionTags{
						ReconcileGvr: ReconcileGvr{
							Group:    arg.Group,
							Version:  arg.Version,
							Resource: arg.Resource,
						},
						Action: "CleanSourceFinalizer",
					})
					return ctx, util.NoOp
				},
				MonitorWorkloads: func(ctx context.Context, _ util.Empty, getter func() MonitorWorkloads) {
					metrics.Repeating(ctx, deps, monitorWorkloadsHandle.With(util.Empty{}), getter)
				},
			}
		},
	)
}

func makeMonitorWorkloadsHandle(registry *prometheus.Registry) metrics.Handle[util.Empty, MonitorWorkloads] {
	fields := []metrics.AnyField[MonitorWorkloads]{
		metrics.NewField(
			"num_workloads",
			"Number of workload objects managed by this generator",
			metrics.IntGauge(),
			func(status MonitorWorkloads) int { return status.NumWorkloads },
		),
		metrics.NewField(
			"num_non_zero_workloads",
			"Number of workload objects managed by this generator with non-zero minAvailable",
			metrics.IntGauge(),
			func(status MonitorWorkloads) int { return status.NumNonZeroWorkloads },
		),
		metrics.NewField(
			"min_available",
			"Sum of minAvailable over workloads managed by this generator",
			metrics.Int64Gauge(),
			func(status MonitorWorkloads) int64 { return status.MinAvailable },
		),
		metrics.NewField(
			"current_estimated_available_replicas",
			"Current estimated sum of available replicas over workloads managed by this generator.",
			metrics.Int64Gauge(),
			func(status MonitorWorkloads) int64 { return status.EstimatedAvailableReplicas },
		),
		metrics.NewField(
			"sum_latency_millis",
			"Sum of the .status.summary.maxLatencyMillis field over workloads managed by this generator.",
			metrics.Int64Gauge(),
			func(status MonitorWorkloads) int64 { return status.SumLatencyMillis },
		),
		metrics.NewField(
			"total_outstanding_history",
			"Number of non-compacted admission history over all clusters",
			metrics.Int64Gauge(),
			func(status MonitorWorkloads) int64 {
				return status.Available.AggregatedReplicas - status.EstimatedAvailableReplicas
			},
		),
	}

	appendCountFields(&fields, "created", func(status MonitorWorkloads) StatusCount { return status.Created })
	appendCountFields(&fields, "available", func(status MonitorWorkloads) StatusCount { return status.Available })
	appendCountFields(&fields, "ready", func(status MonitorWorkloads) StatusCount { return status.Ready })
	appendCountFields(&fields, "scheduled", func(status MonitorWorkloads) StatusCount { return status.Scheduled })
	appendCountFields(&fields, "running", func(status MonitorWorkloads) StatusCount { return status.Running })

	return metrics.RegisterMultiField(
		registry,
		"generator_monitor_workloads",
		"Global workload status",
		metrics.NewReflectTags[util.Empty](),
		fields...,
	)
}

func appendCountFields(fields *[]metrics.AnyField[MonitorWorkloads], name string, field func(MonitorWorkloads) StatusCount) {
	*fields = append(
		*fields,
		metrics.NewField(
			fmt.Sprintf("num_%s_workloads", name),
			fmt.Sprintf("Number of workload objects managed by this generator with number of %s pods >= minAvailable.", name),
			metrics.IntGauge(),
			func(status MonitorWorkloads) int { return field(status).MeetsMinAvailable },
		),
		metrics.NewField(
			fmt.Sprintf("proportion_%s_workloads", name),
			fmt.Sprintf("Proportion of workload objects managed by this generator with number of %s pods >= minAvailable.", name),
			metrics.FloatGauge(),
			func(status MonitorWorkloads) float64 {
				return float64(field(status).MeetsMinAvailable) / float64(status.NumWorkloads)
			},
		),
		metrics.NewField(
			fmt.Sprintf("current_aggregated_%s_workloads", name),
			fmt.Sprintf("Current aggregated sum of %s replicas in workloads managed by this generator.", name),
			metrics.Int64Gauge(),
			func(status MonitorWorkloads) int64 { return field(status).AggregatedReplicas },
		),
		metrics.NewField(
			fmt.Sprintf("proportion_%s_pods", name),
			fmt.Sprintf("Ratio of the number of %s pods managed by this generator relative to the sum of minAvailable.", name),
			metrics.FloatGauge(),
			func(status MonitorWorkloads) float64 {
				return float64(field(status).AggregatedReplicas) / float64(status.MinAvailable)
			},
		),
		metrics.NewField(
			fmt.Sprintf("sum_%s_proportion_ppm", name),
			fmt.Sprintf(
				"Sum of the proportion of %s replicas, saturated at 1 for each non-zero workload, "+
					"rounded to nearest ppm (parts-per-million).",
				name,
			),
			metrics.Int64Gauge(),
			func(status MonitorWorkloads) int64 { return field(status).SumProportionPpm },
		),
		metrics.NewField(
			fmt.Sprintf("avg_%s_proportion_ppm", name),
			fmt.Sprintf(
				"Sum of the proportion of %s replicas, saturated at 1 for each non-zero workload, "+
					"rounded to nearest ppm (parts-per-million).",
				name,
			),
			metrics.FloatGauge(),
			func(status MonitorWorkloads) float64 {
				return float64(field(status).SumProportionPpm) / float64(status.NumNonZeroWorkloads)
			},
		),
	)
}
