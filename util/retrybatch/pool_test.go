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

package retrybatch_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/retrybatch"
	"github.com/kubewharf/podseidon/util/util"
)

type Key string

const key1 Key = "key1"

type Arg uint32

type Result uint8

const (
	rejectedResult = Result(1)
	admittedResult = Result(2)
)

const (
	testColdStartDelay            = time.Second * 5
	testBatchGoroutineIdleTimeout = time.Second * 60
)

type TestAdapter struct {
	t       *testing.T
	clk     *clocktesting.FakeClock
	blockWg *sync.WaitGroup

	nextExec atomic.Pointer[Execution]

	hooks
}

type hooks struct {
	startCheckCanceled   func(Key)
	finishCheckCanceled  func(Key)
	startDispatchResult  func(Key)
	finishDispatchResult func(Key)
	prepareFuseAfterIdle func(Key)
	afterFuse            func(Key, bool)
	beforeHandleAcquire  func(Key)
	afterHandleAcquire   func(Key)
}

func (adapter *TestAdapter) step(duration time.Duration, expectedBlock int) {
	if adapter.blockWg != nil {
		adapter.blockWg.Add(expectedBlock)
	}

	adapter.clk.Step(duration + time.Nanosecond)

	if adapter.blockWg != nil {
		adapter.blockWg.Wait()
	}
}

type Execution struct {
	expectKey     Key
	expectArgsLen int

	latency time.Duration

	retAcceptedArgs optional.Optional[func(Arg) bool]
	retNeedRetry    optional.Optional[time.Duration]
	retErr          error
}

func (*TestAdapter) PoolName() string { return "test" }

func (adapter *TestAdapter) Execute(
	_ context.Context,
	key Key,
	args []Arg,
) retrybatch.ExecuteResult[Result] {
	exec := adapter.nextExec.Load()
	require.NotNil(adapter.t, exec)
	require.True(adapter.t, adapter.nextExec.CompareAndSwap(exec, nil))

	assert.Equal(adapter.t, exec.expectKey, key)
	assert.Len(adapter.t, args, exec.expectArgsLen)

	util.NotifyOnBlock(adapter, adapter.TimeAfter(exec.latency))

	if exec.retErr != nil {
		return retrybatch.ExecuteResultErr[Result](exec.retErr)
	}

	if backoff, needRetry := exec.retNeedRetry.Get(); needRetry {
		return retrybatch.ExecuteResultNeedRetry[Result](backoff)
	}

	predicate := exec.retAcceptedArgs.MustGet("retNeedRetry and retErr are zero")

	return retrybatch.ExecuteResultSuccess(func(i int) Result {
		if !predicate(args[i]) {
			return rejectedResult
		}

		return admittedResult
	})
}

func (adapter *TestAdapter) TimeAfter(duration time.Duration) <-chan time.Time {
	return adapter.clk.After(duration)
}

func (adapter *TestAdapter) OnBlock() {
	if adapter.blockWg != nil {
		adapter.blockWg.Done()
	}
}

func (*TestAdapter) OnUnblock() {}

func (adapter *TestAdapter) StartCheckCanceled(key Key) {
	if adapter.startCheckCanceled != nil {
		adapter.startCheckCanceled(key)
	}
}

func (adapter *TestAdapter) FinishCheckCanceled(key Key) {
	if adapter.finishCheckCanceled != nil {
		adapter.finishCheckCanceled(key)
	}
}

func (adapter *TestAdapter) StartDispatchResult(key Key) {
	if adapter.startDispatchResult != nil {
		adapter.startDispatchResult(key)
	}
}

func (adapter *TestAdapter) FinishDispatchResult(key Key) {
	if adapter.finishDispatchResult != nil {
		adapter.finishDispatchResult(key)
	}
}

func (adapter *TestAdapter) PrepareFuseAfterIdle(key Key) {
	if adapter.prepareFuseAfterIdle != nil {
		adapter.prepareFuseAfterIdle(key)
	}
}

func (adapter *TestAdapter) AfterFuse(key Key, success bool) {
	if adapter.afterFuse != nil {
		adapter.afterFuse(key, success)
	}
}

func (adapter *TestAdapter) BeforeHandleAcquire(key Key) {
	if adapter.beforeHandleAcquire != nil {
		adapter.beforeHandleAcquire(key)
	}
}

func (adapter *TestAdapter) AfterHandleAcquire(key Key) {
	if adapter.afterHandleAcquire != nil {
		adapter.afterHandleAcquire(key)
	}
}

func newTestingPool(t *testing.T) (retrybatch.Pool[Key, Arg, Result], *TestAdapter) {
	adapter := &TestAdapter{
		t:        t,
		clk:      clocktesting.NewFakeClock(time.Time{}),
		blockWg:  new(sync.WaitGroup),
		nextExec: atomic.Pointer[Execution]{},
		hooks:    util.Zero[hooks](),
	}

	return retrybatch.NewTestingPool(
		adapter,
		adapter,
		testColdStartDelay,
		testBatchGoroutineIdleTimeout,
	), adapter
}

func TestSingleRetry(t *testing.T) {
	const (
		executeLatency = time.Second * 2
		retryLatency   = time.Second * 4
	)

	t.Parallel()

	pool, adapter := newTestingPool(t)

	submitReturned := make(chan util.Empty)
	adapter.blockWg = new(sync.WaitGroup)

	go func(initialSetup <-chan time.Time) {
		<-initialSetup // wait for blockWg to set up first

		result, err := pool.Submit(context.Background(), key1, 1)
		assert.NoError(t, err)
		assert.Equal(t, admittedResult, result)

		close(submitReturned)
	}(adapter.TimeAfter(time.Nanosecond))

	adapter.step(0, 2)

	// Do not set nextExec until this point, after which coldStartDelay elapsed and execution starts.
	adapter.nextExec.Store(&Execution{
		expectKey:       key1,
		expectArgsLen:   1,
		latency:         executeLatency,
		retAcceptedArgs: optional.None[func(Arg) bool](),
		retNeedRetry:    optional.Some(retryLatency),
		retErr:          nil,
	})

	adapter.step(testColdStartDelay, 1)
	// At this point, execution started, so nextExec should be nil.
	assert.Eventually(t, func() bool { return adapter.nextExec.Load() == nil }, time.Second, time.Millisecond)
	// We keep it as nil since a retry is not expected until retryLatency later.

	adapter.step(executeLatency, 1)
	// The batch is now waiting for the retry backoff.
	assert.True(t, adapter.nextExec.CompareAndSwap(nil, &Execution{
		expectKey:       key1,
		expectArgsLen:   1,
		latency:         executeLatency,
		retAcceptedArgs: optional.Some(func(Arg) bool { return true }),
		retNeedRetry:    optional.None[time.Duration](),
		retErr:          nil,
	}))

	adapter.step(retryLatency, 1)
	// Second execution started, waiting for submit to return soon.
	require.True(t, util.TryRecv(submitReturned).NoData)
	assert.Nil(t, adapter.nextExec.Load())

	adapter.step(executeLatency, 1)

	_, chOpen := <-submitReturned
	assert.False(t, chOpen)
}

const singleExecuteLatency = time.Second

func doSingleExecute(adapter *TestAdapter) {
	adapter.nextExec.Store(&Execution{
		expectKey:       key1,
		expectArgsLen:   1,
		latency:         singleExecuteLatency,
		retAcceptedArgs: optional.Some(func(Arg) bool { return true }),
		retNeedRetry:    optional.None[time.Duration](),
		retErr:          nil,
	})

	adapter.step(0, 2)
	adapter.step(testColdStartDelay, 1)
	adapter.step(singleExecuteLatency, 1)
}

func poolWithOneRun(t *testing.T) (retrybatch.Pool[Key, Arg, Result], *TestAdapter) {
	pool, adapter := newTestingPool(t)
	adapter.blockWg = new(sync.WaitGroup)

	firstSubmitDone := make(chan util.Empty)

	go func(initialSetup <-chan time.Time) {
		<-initialSetup

		result, err := pool.Submit(context.Background(), key1, 1)
		assert.NoError(t, err)
		assert.Equal(t, admittedResult, result)

		close(firstSubmitDone)
	}(adapter.TimeAfter(time.Nanosecond))

	doSingleExecute(adapter)

	<-firstSubmitDone

	return pool, adapter
}

func TestBatchGoroutineAbortFuse(t *testing.T) {
	t.Parallel()

	pool, adapter := poolWithOneRun(t)

	canTryFuse := make(chan util.Empty)
	preparingFuse := make(chan util.Empty)

	adapter.hooks.prepareFuseAfterIdle = func(key Key) {
		assert.Equal(t, key1, key)
		close(preparingFuse)
		<-canTryFuse
	}

	adapter.clk.Step(time.Minute) // this will trigger prepareFuseAfterIdle
	<-preparingFuse

	acquiredHandle := make(chan util.Empty)
	adapter.hooks.afterHandleAcquire = func(key Key) {
		assert.Equal(t, key1, key)
		close(acquiredHandle)
	}

	submitDone := make(chan util.Empty)
	go func() {
		result, err := pool.Submit(
			context.Background(),
			key1,
			2,
		) // this will trigger afterHandleAcquire
		assert.NoError(t, err)
		assert.Equal(t, admittedResult, result)

		close(submitDone)
	}()

	<-acquiredHandle

	adapter.hooks.afterFuse = func(key Key, success bool) {
		assert.Equal(t, key1, key)
		assert.False(t, success)
	}

	close(canTryFuse)

	doSingleExecute(adapter)

	<-submitDone
}
