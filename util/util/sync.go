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
	"sync/atomic"
)

type LateInitReader[T any] struct {
	readyCh <-chan Empty
	value   *T
}

type LateInitWriter[T any] func(T)

func (reader LateInitReader[T]) Get() T {
	<-reader.readyCh
	return *reader.value
}

func NewLateInit[T any]() (_reader LateInitReader[T], _writer LateInitWriter[T]) {
	readyCh := make(chan Empty)
	box := new(T)

	written := new(atomic.Bool)

	reader := LateInitReader[T]{
		readyCh: readyCh,
		value:   box,
	}
	writer := func(value T) {
		if wasWritten := written.Swap(true); wasWritten {
			panic("LateInit cannot be written multiple times")
		}

		*box = value

		close(readyCh)
	}

	return reader, writer
}
