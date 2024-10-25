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

package retrybatch

import (
	"context"
	"time"

	"github.com/puzpuzpuz/xsync/v3"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/retrybatch/fusedrc"
	"github.com/kubewharf/podseidon/util/retrybatch/observer"
	"github.com/kubewharf/podseidon/util/retrybatch/syncbarrier"
	"github.com/kubewharf/podseidon/util/util"
)

// Allows batch multiple operations with the same Key and avoids concurrent operations.
//
// Initially, updates are buffered for coldStartDelay and executed together atomically.
// If other operations are submitted for the same key during the execution,
// they are buffered until the current operation completes and executed together in the same batch.
// If the execution indicates that retry is needed, the old batch is merged into the new pending batch.
type Pool[Key comparable, Arg any, Result any] interface {
	// Starts the monitoring goroutines of this pool.
	StartMonitor(ctx context.Context)

	// Request that an operation be executed.
	//
	// Blocks until the operation completes or unrecoverably fails, or the operation context or the pool context is canceled.
	// If the contexts are canceled, an error wrapping ctx.Err() is returned.
	// Otherwise, the result from the underlying adapter is returned.
	//
	// A nil error will be returned if the operation has completed, even if the context may have timed out
	// (possible during the last operation).
	Submit(ctx context.Context, key Key, arg Arg) (Result, error)

	Close()
}

type poolImpl[
	Key comparable, Arg any, Result any,
	AdapterT Adapter[Key, Arg, Result],
	syncBarrierT syncbarrier.Interface[Key],
] struct {
	observer observer.Observer

	//nolint:containedctx // this context is not function-scoped; it is used for running batch goroutines.
	batchCtx       context.Context
	batchCtxCancel context.CancelFunc

	adapter     Adapter[Key, Arg, Result]
	syncBarrier syncBarrierT

	coldStartDelay            time.Duration
	batchGoroutineIdleTimeout time.Duration

	handles *xsync.MapOf[Key, *keyHandle[Arg, Result]]
}

type PoolConfig[Key comparable, Arg any, Result any] struct {
	createPool func(ctx context.Context) Pool[Key, Arg, Result]
}

func (config PoolConfig[Key, Arg, Result]) Create(ctx context.Context) Pool[Key, Arg, Result] {
	return config.createPool(ctx)
}

// Constructs a new Pool.
func NewPool[Key comparable, Arg any, Result any, AdapterT Adapter[Key, Arg, Result]](
	obs observer.Observer,
	adapter AdapterT,
	coldStartDelay, batchGoroutineIdleTimeout time.Duration,
) PoolConfig[Key, Arg, Result] {
	return PoolConfig[Key, Arg, Result]{
		createPool: func(ctx context.Context) Pool[Key, Arg, Result] {
			batchCtx, batchCtxCancel := context.WithCancel(ctx)
			return &poolImpl[Key, Arg, Result, AdapterT, syncbarrier.Empty[Key]]{
				observer:                  obs,
				batchCtx:                  batchCtx,
				batchCtxCancel:            batchCtxCancel,
				adapter:                   adapter,
				syncBarrier:               syncbarrier.Empty[Key]{},
				coldStartDelay:            coldStartDelay,
				batchGoroutineIdleTimeout: batchGoroutineIdleTimeout,
				handles:                   xsync.NewMapOf[Key, *keyHandle[Arg, Result]](),
			}
		},
	}
}

// Internal: Constructs a new pool with custom synchronization barriers.
func NewTestingPool[
	Key comparable, Arg any, Result any,
	AdapterT Adapter[Key, Arg, Result],
	BarrierT syncbarrier.Interface[Key],
](
	adapter AdapterT,
	syncBarrier BarrierT,
	coldStartDelay, batchGoroutineIdleTimeout time.Duration,
) Pool[Key, Arg, Result] {
	return &poolImpl[Key, Arg, Result, AdapterT, BarrierT]{
		observer:                  o11y.ReflectNoop[observer.Observer](),
		batchCtx:                  context.Background(),
		batchCtxCancel:            util.NoOp,
		adapter:                   adapter,
		syncBarrier:               syncBarrier,
		coldStartDelay:            coldStartDelay,
		batchGoroutineIdleTimeout: batchGoroutineIdleTimeout,
		handles:                   xsync.NewMapOf[Key, *keyHandle[Arg, Result]](),
	}
}

type Adapter[Key comparable, Arg any, Result any] interface {
	// Executes all requests in a single atomic operation.
	//
	// arg must be nonempty.
	//
	// A non-nil error indicates an unrecoverable error,
	// on which the whole batch should return as failure with the same error.
	// Otherwise, NeedRetry indicates the duration after which the operation shall repeat.
	Execute(ctx context.Context, key Key, args []Arg) ExecuteResult[Result]

	// Name of this retrybatch pool, used for o11y.
	PoolName() string
}

type keyHandle[Arg any, Result any] struct {
	rc *fusedrc.Counter

	operationCh chan operationRequest[Arg, Result]
}

func (pool *poolImpl[Key, Arg, Result, AdapterT, syncBarrierT]) StartMonitor(ctx context.Context) {
	go pool.observer.InFlightKeys(ctx, observer.InFlightKeys{PoolName: pool.adapter.PoolName()}, func() int {
		return pool.handles.Size()
	})
}

func (pool *poolImpl[Key, Arg, Result, AdapterT, syncBarrierT]) Submit(
	ctx context.Context,
	key Key,
	arg Arg,
) (Result, error) {
	ctx, cancelFunc := pool.observer.StartSubmit(ctx, observer.StartSubmit{
		PoolName: pool.adapter.PoolName(),
	})
	defer cancelFunc()

	var endSubmit observer.EndSubmit
	defer func() {
		pool.observer.EndSubmit(ctx, endSubmit)
	}()

	handle := pool.acquireLazyHandle(key)
	defer handle.rc.Release() // the handle does not need to be released until execution is complete and batchGoroutine is idle.

	resultCh := make(chan submitResult[Result], 1)
	request := operationRequest[Arg, Result]{
		arg:      arg,
		retries:  0,
		resultCh: resultCh,
		ctxErr:   ctx.Err,
	}
	handle.operationCh <- request

	result, _ := util.NotifyOnBlock(pool.syncBarrier, resultCh)
	endSubmit = observer.EndSubmit{
		RetryCount: result.Retries,
		Err:        result.Err,
	}

	return result.ExecuteResult, result.Err
}

// Lazily creates the key, and acquires the handle rc.
//
// Callers must call handle.rc.Release upon completion.
func (pool *poolImpl[Key, Arg, Result, AdapterT, syncBarrierT]) acquireLazyHandle(
	key Key,
) *keyHandle[Arg, Result] {
	for {
		handle, isOld := pool.handles.LoadOrCompute(key, func() *keyHandle[Arg, Result] {
			operationCh := make(chan operationRequest[Arg, Result])

			handle := &keyHandle[Arg, Result]{
				rc:          fusedrc.New(),
				operationCh: operationCh,
			}

			return handle
		})

		pool.syncBarrier.BeforeHandleAcquire(key)

		if !handle.rc.Acquire() {
			// handle is just fused, and batchGoroutine will delete handle from the pool and shut down shortly.
			// Busy-wait to initialize a new handle.
			continue
		}

		pool.syncBarrier.AfterHandleAcquire(key)

		if !isOld {
			go batchGoroutine(
				pool.batchCtx,
				pool.observer, pool.adapter, pool.syncBarrier,
				pool.coldStartDelay, pool.batchGoroutineIdleTimeout,
				key, handle,
				func() { pool.handles.Delete(key) },
			)
		}

		return handle
	}
}

func (pool *poolImpl[_, _, _, _, _]) Close() { pool.batchCtxCancel() }

//revive:disable-next-line:argument-limit // cannot reasonably reduce
func batchGoroutine[
	Key comparable, Arg any, Result any,
	AdapterT Adapter[Key, Arg, Result],
	syncBarrierT syncbarrier.Interface[Key],
](
	ctx context.Context,
	obs observer.Observer,
	adapter AdapterT,
	syncBarrier syncBarrierT,
	coldStartDelay, batchGoroutineIdleTimeout time.Duration,
	key Key,
	handle *keyHandle[Arg, Result],
	cleanup func(),
) {
	defer cleanup()

	for {
		// Always exhaust the event channel before executing a batch.
		select {
		case request := <-handle.operationCh:
			processBatch(ctx, obs, adapter, syncBarrier, coldStartDelay, key, handle.operationCh, request)

			continue
		default:
		}

		if util.SelectOrNotifyRRR(
			syncBarrier,
			handle.operationCh, func(request operationRequest[Arg, Result], _ bool) iter.Flow {
				processBatch(ctx, obs, adapter, syncBarrier, coldStartDelay, key, handle.operationCh, request)
				return iter.Continue
			},
			syncBarrier.TimeAfter(batchGoroutineIdleTimeout), func(time.Time, bool) iter.Flow {
				syncBarrier.PrepareFuseAfterIdle(key)

				if !handle.rc.Fuse() {
					// Someone just acquired the handle. Expect an operationRequest to come in soon.
					syncBarrier.AfterFuse(key, false)
					return iter.Continue
				}

				syncBarrier.AfterFuse(key, true)

				// Since fusing is successful, there are no more active references to this handle,
				// and we can gracefully shut down.

				return iter.Break
			},
			ctx.Done(), func(util.Empty, bool) iter.Flow {
				if !handle.rc.Fuse() {
					// the server is shutting down, so just busy-wait for requests to stop coming in.
					return iter.Continue
				}

				return iter.Break
			},
		).Break() {
			return
		}
	}
}

func processBatch[
	Key comparable, Arg any, Result any,
	AdapterT Adapter[Key, Arg, Result],
	syncBarrierT syncbarrier.Interface[Key],
](
	ctx context.Context,
	obs observer.Observer,
	adapter AdapterT,
	syncBarrier syncBarrierT,
	coldStartDelay time.Duration,
	key Key,
	operationCh <-chan operationRequest[Arg, Result],
	initialRequest operationRequest[Arg, Result],
) {
	ctx, cancelFunc := obs.StartBatch(ctx, observer.StartBatch{
		PoolName: adapter.PoolName(),
	})
	defer cancelFunc()

	batch := []operationRequest[Arg, Result]{initialRequest}

	nextExecute := syncBarrier.TimeAfter(coldStartDelay)

	retryCount := 0

	for {
		retryCount++

		flow := util.SelectOrNotifyRRR(
			syncBarrier,
			ctx.Done(), func(util.Empty, bool) iter.Flow {
				for _, request := range batch {
					request.resultCh <- errorSubmitResult[Result](
						request.retries,
						errors.TagWrapf(
							"PoolCtxCancel", ctx.Err(), "pool context canceled while waiting for retry batch",
						),
					)
				}

				return iter.Break
			},
			operationCh, func(request operationRequest[Arg, Result], _ bool) iter.Flow {
				batch = append(batch, request)
				return iter.Continue
			},
			nextExecute, func(time.Time, bool) iter.Flow {
				if delay, shouldContinue := executeBatch(ctx, obs, adapter, syncBarrier, key, &batch).Get(); shouldContinue {
					nextExecute = syncBarrier.TimeAfter(delay)
					return iter.Continue
				}

				return iter.Break
			},
		)
		if flow.Break() {
			break
		}
	}

	obs.EndBatch(ctx, observer.EndBatch{RetryCount: retryCount})
}

func executeBatch[
	Key comparable, Arg any, Result any,
	AdapterT Adapter[Key, Arg, Result],
	syncBarrierT syncbarrier.Interface[Key],
](
	ctx context.Context,
	obs observer.Observer,
	adapter AdapterT,
	syncBarrier syncBarrierT,
	key Key,
	batch *[]operationRequest[Arg, Result],
) (_nextExecute optional.Optional[time.Duration]) {
	syncBarrier.StartCheckCanceled(key)

	util.DrainSliceUnordered(batch, func(request operationRequest[Arg, Result]) bool {
		ctxErr := request.ctxErr()
		if ctxErr == nil { // not canceled
			return true
		}

		request.resultCh <- errorSubmitResult[Result](
			request.retries,
			errors.TagWrapf(
				"OpCtxCancelDuringRetry", ctxErr, "operation context canceled while waiting for retry batch",
			),
		)

		return false
	})

	syncBarrier.FinishCheckCanceled(key)

	ctx, cancelFunc := obs.StartExecute(ctx, observer.StartExecute{
		PoolName:  adapter.PoolName(),
		BatchSize: len(*batch),
	})

	nextExecute, endExecute := tryExecuteBatch(ctx, adapter, syncBarrier, key, batch)
	obs.EndExecute(ctx, endExecute)
	cancelFunc()

	return nextExecute
}

func tryExecuteBatch[
	Key comparable, Arg any, Result any,
	AdapterT Adapter[Key, Arg, Result],
	syncBarrierT syncbarrier.Interface[Key],
](
	ctx context.Context,
	adapter AdapterT,
	syncBarrier syncBarrierT,
	key Key,
	batch *[]operationRequest[Arg, Result],
) (optional.Optional[time.Duration], observer.EndExecute) {
	if len(*batch) == 0 {
		// everything has expired, nothing to execute
		return optional.None[time.Duration](), observer.EndExecute{
			ExecuteResultVariant: ExecuteResultVariantSuccess.String(),
			Err:                  nil,
		}
	}

	args := util.MapSlice(
		*batch,
		func(request operationRequest[Arg, Result]) Arg { return request.arg },
	)

	util.ForEachRef(*batch, func(req *operationRequest[Arg, Result]) { req.retries++ })

	result := adapter.Execute(ctx, key, args)
	endExecute := observer.EndExecute{
		ExecuteResultVariant: result.Variant.String(),
		Err:                  result.Err,
	}

	switch result.Variant {
	case ExecuteResultVariantSuccess:
		syncBarrier.StartDispatchResult(key)

		for i, request := range *batch {
			request.resultCh <- submitResult[Result]{
				ExecuteResult: result.Success(i),
				Retries:       request.retries,
				Err:           nil,
			}
		}

		syncBarrier.FinishDispatchResult(key)

		return optional.None[time.Duration](), endExecute

	case ExecuteResultVariantNeedRetry:
		return optional.Some(result.NeedRetry), endExecute

	case ExecuteResultVariantErr:
		for _, request := range *batch {
			request.resultCh <- errorSubmitResult[Result](
				request.retries,
				errors.TagWrapf("ExecuteError", result.Err, "error executing retry batch"),
			)
		}

		util.ClearSlice(batch)

		return optional.None[time.Duration](), endExecute
	}

	panic("unknown execute result variant")
}
