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

package util

import (
	"context"
)

func ContextWithStopCh(ctx context.Context, ch <-chan Empty) context.Context {
	newCtx, cancelFunc := context.WithCancel(ctx)

	go func(recv <-chan Empty, cancelFunc context.CancelFunc, shutdown <-chan Empty) {
		select {
		case <-recv:
			cancelFunc()
		case <-shutdown:
		}
	}(ch, cancelFunc, ctx.Done())

	return newCtx
}

func ClosedChan[T any]() <-chan T {
	ch := make(chan T)
	close(ch)

	return ch
}

type TryRecvResult[T any] struct {
	Received     bool
	ReceivedData T

	ChanClosed bool
	NoData     bool
}

func TryRecv[T any](ch <-chan T) TryRecvResult[T] {
	var result TryRecvResult[T]

	select {
	case item, chOpen := <-ch:
		if chOpen {
			result.Received = true
			result.ReceivedData = item
		} else {
			result.ChanClosed = true
		}
	default:
		result.NoData = true
	}

	return result
}

// Select-recvs from one of the channels, and call the specified function if it blocks (all channels have no data).
func SelectOrNotifyRR[T1, T2 any, R any](
	blockNotifier BlockNotifier,
	ch1 <-chan T1, then1 func(T1, bool) R,
	ch2 <-chan T2, then2 func(T2, bool) R,
) R {
	select {
	case v, ok := <-ch1:
		return then1(v, ok)
	case v, ok := <-ch2:
		return then2(v, ok)
	default:
	}

	blockNotifier.OnBlock()
	defer blockNotifier.OnUnblock()

	select {
	case v, ok := <-ch1:
		return then1(v, ok)
	case v, ok := <-ch2:
		return then2(v, ok)
	}
}

// Select-recvs from one of the channels, and call the specified function if it blocks (all channels have no data).
func SelectOrNotifyRRR[T1, T2, T3 any, R any](
	blockNotifier BlockNotifier,
	ch1 <-chan T1, then1 func(T1, bool) R,
	ch2 <-chan T2, then2 func(T2, bool) R,
	ch3 <-chan T3, then3 func(T3, bool) R,
) R {
	select {
	case v, ok := <-ch1:
		return then1(v, ok)
	case v, ok := <-ch2:
		return then2(v, ok)
	case v, ok := <-ch3:
		return then3(v, ok)
	default:
	}

	blockNotifier.OnBlock()
	defer blockNotifier.OnUnblock()

	select {
	case v, ok := <-ch1:
		return then1(v, ok)
	case v, ok := <-ch2:
		return then2(v, ok)
	case v, ok := <-ch3:
		return then3(v, ok)
	}
}

// Receives a value from `ch`.
// If this was a blocking operation, also calls the OnBlock/OnUnblock hooks on `blockNotifier`.
func NotifyOnBlock[T any](blockNotifier BlockNotifier, ch <-chan T) (T, bool) {
	select {
	case v, ok := <-ch:
		return v, ok
	default:
	}

	blockNotifier.OnBlock()
	defer blockNotifier.OnUnblock()

	v, ok := <-ch

	return v, ok
}

type BlockNotifier interface {
	OnBlock()
	OnUnblock()
}
