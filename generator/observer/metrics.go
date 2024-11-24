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
	"time"

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

			monitorWorkloadsHandle := metrics.RegisterMultiField(
				deps.Registry(),
				"generator_monitor_workloads",
				"Global workload status",
				metrics.NewReflectTags[util.Empty](),
				metrics.NewField(
					"num_workloads",
					"Number of workload objects managed by this generator",
					metrics.IntGauge(),
					func(status MonitorWorkloads) int { return status.NumWorkloads },
				),
				metrics.NewField(
					"min_available",
					"Sum of minAvailable over workloads managed by this generator",
					metrics.Int64Gauge(),
					func(status MonitorWorkloads) int64 { return status.MinAvailable },
				),
				metrics.NewField(
					"current_total_replicas",
					"Current aggregated sum of total replicas over workloads managed by this generator.",
					metrics.Int64Gauge(),
					func(status MonitorWorkloads) int64 { return status.TotalReplicas },
				),
				metrics.NewField(
					"current_aggregated_available_replicas",
					"Current aggregated sum of available replicas over workloads managed by this generator.",
					metrics.Int64Gauge(),
					func(status MonitorWorkloads) int64 { return status.AggregatedAvailableReplicas },
				),
				metrics.NewField(
					"current_estimated_available_replicas",
					"Current estimated sum of available replicas over workloads managed by this generator.",
					metrics.Int64Gauge(),
					func(status MonitorWorkloads) int64 { return status.EstimatedAvailableReplicas },
				),
				metrics.NewField(
					"sum_available_proportion_ppm",
					"Sum of the proportion of aggregated available replicas, saturated at 1, rounded to nearest ppm (parts-per-million).",
					metrics.Int64Gauge(),
					func(status MonitorWorkloads) int64 { return status.SumAvailableProportionPpm },
				),
				metrics.NewField(
					"avg_available_proportion_ppm",
					"Unweighted average of service availability for every PodProtector (0 to 1).",
					metrics.FloatGauge(),
					func(status MonitorWorkloads) float64 {
						return float64(status.SumAvailableProportionPpm) / 1e6 / float64(status.NumWorkloads)
					},
				),
				metrics.NewField(
					"sum_latency_millis",
					"Sum of the .status.summary.maxLatencyMillis field over workloads managed by this generator.",
					metrics.Int64Gauge(),
					func(status MonitorWorkloads) int64 { return status.SumLatencyMillis },
				),
			)

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
