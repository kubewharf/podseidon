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

package worker

import (
	"context"
	"flag"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/worker/observer"
)

//revive:disable-next-line:function-length
func New[QueueItem comparable](
	name string,
	clk clock.WithTicker,
) component.Declared[Api[QueueItem]] {
	return component.Declare(
		func(args Args) string { return fmt.Sprintf("%s-worker", args.Name) },
		func(args Args, fs *flag.FlagSet) Options {
			return Options{
				WorkerCount: fs.Int(
					"concurrency",
					1,
					fmt.Sprintf("maximum concurrency for %s reconciliation", args.Name),
				),
				BaseDelay: fs.Duration(
					"backoff-base",
					time.Millisecond*100,
					fmt.Sprintf("error exponential backoff base for %s reconciliation", args.Name),
				),
				MaxDelay: fs.Duration(
					"backoff-max",
					time.Minute,
					fmt.Sprintf("error exponential backoff max for %s reconciliation", args.Name),
				),
			}
		},
		func(_ Args, requests *component.DepRequests) Deps {
			return Deps{
				Observer: o11y.Request[observer.Observer](requests),
			}
		},
		func(_ context.Context, args Args, options Options, _ Deps) (*State[QueueItem], error) {
			rl := workqueue.NewTypedItemExponentialFailureRateLimiter[QueueItem](
				*options.BaseDelay,
				*options.MaxDelay,
			)
			queue := workqueue.NewTypedRateLimitingQueueWithConfig[QueueItem](
				rl,
				//nolint:exhaustruct // leave as default
				workqueue.TypedRateLimitingQueueConfig[QueueItem]{
					Clock: args.Clock,
				},
			)

			return &State[QueueItem]{
				completion:  sync.WaitGroup{},
				queue:       queue,
				executor:    nil,
				prereqs:     nil,
				beforeStart: nil,
				started:     atomic.Bool{},
			}, nil
		},
		component.Lifecycle[Args, Options, Deps, State[QueueItem]]{
			Start: func(ctx context.Context, args *Args, options *Options, deps *Deps, state *State[QueueItem]) error {
				go start(
					ctx,
					args,
					options,
					deps,
					state,
				) // startup should not block phase detection
				return nil
			},
			Join: func(_ context.Context, _ *Args, _ *Options, _ *Deps, state *State[QueueItem]) error {
				state.completion.Wait()
				return nil
			},
			HealthChecks: func(s *State[QueueItem]) component.HealthChecks {
				checks := component.HealthChecks{}

				for name, prereq := range s.prereqs {
					checks[name] = func() error {
						if !s.started.Load() {
							return nil // report as healthy if leader is not elected
						}

						if !prereq.IsReady() {
							return errors.TagErrorf(
								"PrereqNotReady",
								"prerequisite %s returned false",
								name,
							)
						}

						return nil
					}
				}

				return checks
			},
		},
		func(d *component.Data[Args, Options, Deps, State[QueueItem]]) Api[QueueItem] {
			return Api[QueueItem]{workerName: d.Args.Name, state: d.State}
		},
	)(
		Args{
			Name:  name,
			Clock: clk,
		},
	)
}

type Args struct {
	Name  string
	Clock clock.WithTicker
}

type Options struct {
	WorkerCount *int
	BaseDelay   *time.Duration
	MaxDelay    *time.Duration
}

type Deps struct {
	Observer component.Dep[observer.Observer]
}

type State[QueueItem comparable] struct {
	completion sync.WaitGroup
	queue      workqueue.TypedRateLimitingInterface[QueueItem]

	executor    Executor[QueueItem]
	prereqs     map[string]Prereq
	beforeStart func(context.Context) (context.Context, error)

	started atomic.Bool
}

type Api[QueueItem comparable] struct {
	workerName string
	state      *State[QueueItem]
}

type Executor[QueueItem comparable] func(ctx context.Context, item QueueItem) error

func (api Api[QueueItem]) SetExecutor(executor Executor[QueueItem], prereqs map[string]Prereq) {
	if api.state.executor != nil {
		panic(fmt.Sprintf("Attempt to set executor for %s worker multiple times", api.workerName))
	}

	api.state.executor = executor
	api.state.prereqs = prereqs
}

type BeforeStart func(context.Context) (context.Context, error)

func (api Api[QueueItem]) SetBeforeStart(beforeStart BeforeStart) {
	if api.state.beforeStart != nil {
		panic(
			fmt.Sprintf("Attempt to set beforeStart for %s worker multiple times", api.workerName),
		)
	}

	api.state.beforeStart = beforeStart
}

func (api Api[QueueItem]) Enqueue(item QueueItem) {
	api.state.queue.Add(item)
}

func (api Api[QueueItem]) EnqueueDelayed(item QueueItem, delay time.Duration) {
	api.state.queue.AddAfter(item, delay)
}

func start[QueueItem comparable](
	ctx context.Context,
	args *Args,
	options *Options,
	deps *Deps,
	state *State[QueueItem],
) {
	if state.executor == nil {
		panic(
			fmt.Sprintf(
				"worker.Api.SetExecutor is not called for %s worker",
				args.Name,
			),
		)
	}

	if state.beforeStart != nil {
		runCtx, err := state.beforeStart(ctx)
		if err != nil {
			return
		}

		ctx = runCtx
	}

	state.started.Store(true)

	for _, prereq := range state.prereqs {
		if !prereq.Wait(ctx) {
			return
		}
	}

	state.completion.Add(*options.WorkerCount)

	for range *options.WorkerCount {
		go runWorker(
			ctx,
			&state.completion,
			state.queue,
			state.executor,
			args.Name,
			deps.Observer.Get(),
		)
	}

	go func() {
		<-ctx.Done()
		state.queue.ShutDown()
	}()
}

func runWorker[QueueItem comparable](
	ctx context.Context,
	wg *sync.WaitGroup,
	queue workqueue.TypedRateLimitingInterface[QueueItem],
	executor Executor[QueueItem],
	workerName string,
	obs observer.Observer,
) {
	defer wg.Done()

	for {
		queueItem, shutdown := queue.Get()
		if shutdown {
			return
		}

		spinOnce(ctx, queue, queueItem, executor, workerName, obs)
	}
}

func spinOnce[QueueItem comparable](
	ctx context.Context,
	queue workqueue.TypedRateLimitingInterface[QueueItem],
	queueItem QueueItem,
	executor Executor[QueueItem],
	workerName string,
	obs observer.Observer,
) {
	defer queue.Done(queueItem)

	ctx, cancelFunc := obs.StartReconcile(ctx, observer.StartReconcile{WorkerName: workerName})
	defer cancelFunc()

	err := executor(ctx, queueItem)

	obs.EndReconcile(ctx, observer.EndReconcile{Err: err})

	if err != nil {
		queue.AddRateLimited(queueItem)
	}
}
