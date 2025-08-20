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
