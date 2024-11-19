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

package util_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/constraints"

	"github.com/kubewharf/podseidon/util/util"
)

func testRoundedIntDiv[T constraints.Integer](t *testing.T, numerator T, divisor T, expect T) {
	t.Helper()
	assert.Equal(t, expect, util.RoundedIntDiv(numerator, divisor))
}

func TestRoundDownIntDiv(t *testing.T) {
	t.Parallel()
	testRoundedIntDiv(t, 14, 10, 1)
}

func TestRoundUpIntDiv(t *testing.T) {
	t.Parallel()
	testRoundedIntDiv(t, 16, 10, 2)
}

func TestRoundHalfUpIntDiv(t *testing.T) {
	t.Parallel()
	testRoundedIntDiv(t, 15, 10, 2)
}

func TestRoundNegativeHalfUpIntDiv(t *testing.T) {
	t.Parallel()
	testRoundedIntDiv(t, -5, 10, 0)
	testRoundedIntDiv(t, -15, 10, -1)
}

func TestRoundNegativeUpIntDiv(t *testing.T) {
	t.Parallel()
	testRoundedIntDiv(t, -4, 10, 0)
	testRoundedIntDiv(t, -14, 10, -1)
}

func TestRoundNegativeDownIntDiv(t *testing.T) {
	t.Parallel()
	testRoundedIntDiv(t, -6, 10, -1)
	testRoundedIntDiv(t, -16, 10, -2)
}
