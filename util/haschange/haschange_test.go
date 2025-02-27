// Copyright 2025 The Podseidon Authors.
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

package haschange_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/podseidon/util/haschange"
)

type Cause uint32

const (
	CauseFoo = Cause(1 << iota)
	CauseBar
	CauseQux
)

func (cause Cause) BitToString() string {
	switch cause {
	case CauseFoo:
		return "Foo"
	case CauseBar:
		return "Bar"
	case CauseQux:
		return "Qux"
	default:
		panic("bad receiver")
	}
}

func TestChangedString(t *testing.T) {
	t.Parallel()

	assertCauseString(t, "Foo+Bar+Qux", []Cause{CauseBar, CauseFoo, CauseQux})
	assertCauseString(t, "Foo+Qux", []Cause{CauseFoo, CauseQux})
	assertCauseString(t, "Qux", []Cause{CauseQux})
	assertCauseString(t, "nil", []Cause{})
}

func assertCauseString(t *testing.T, expected string, causes []Cause) {
	changed := haschange.New[Cause]()

	for _, cause := range causes {
		changed.Add(cause)
	}

	assert.Equal(t, expected, changed.String())
}
