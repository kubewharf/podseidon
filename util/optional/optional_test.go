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

package optional_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kubewharf/podseidon/util/optional"
)

func TestGetSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	value, isSome := opt.Get()
	assert.Equal(t, "foo", value)
	assert.True(t, isSome)
}

func TestGetNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	value, isSome := opt.Get()
	assert.Empty(t, value)
	assert.False(t, isSome)
}

func TestGetOrSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	assert.Equal(t, "foo", opt.GetOr("bar"))
}

func TestGetOrNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	assert.Equal(t, "bar", opt.GetOr("bar"))
}

func TestGetOrZeroSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	assert.Equal(t, "foo", opt.GetOrZero())
}

func TestGetOrZeroNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	assert.Empty(t, opt.GetOrZero())
}

func TestGetOrFnSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	assert.Equal(
		t,
		"foo",
		opt.GetOrFn(func() string { return unreachableDueTo[string](t, "Some") }),
	)
}

func TestGetOrFnNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	assert.Equal(t, "bar", opt.GetOrFn(func() string { return "bar" }))
}

func TestMustGetSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	assert.Equal(t, "foo", opt.MustGet("should not panic"))
}

func TestMustGetNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()

	assert.PanicsWithValue(
		t,
		"value must exist: should panic",
		func() { opt.MustGet("should panic") },
	)
}

func TestIsSomeAndSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	assert.True(t, opt.IsSomeAnd(func(s string) bool { return len(s) == 3 }))
	assert.False(t, opt.IsSomeAnd(func(s string) bool { return len(s) == 4 }))
}

func TestIsSomeAndNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	assert.False(t, opt.IsSomeAnd(func(_ string) bool { return unreachableDueTo[bool](t, "None") }))
}

func TestIsNoneOrSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	assert.True(t, opt.IsNoneOr(func(s string) bool { return len(s) == 3 }))
	assert.False(t, opt.IsNoneOr(func(s string) bool { return len(s) == 4 }))
}

func TestIsNoneOrNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	assert.True(t, opt.IsNoneOr(func(_ string) bool { return unreachableDueTo[bool](t, "None") }))
}

func TestSetOrChooseSomeUnchanged(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	opt.SetOrChoose("bar", func(left, right string) bool { return right > left })
	assert.Equal(t, "foo", opt.MustGet("should not be unset"))
}

func TestSetOrChooseSomeChanged(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	opt.SetOrChoose("bar", func(left, right string) bool { return right < left })
	assert.Equal(t, "bar", opt.MustGet("should not be unset"))
}

func TestSetOrChooseNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	opt.SetOrChoose(
		"bar",
		func(_, _ string) bool { return unreachableDueTo[bool](t, "None") },
	)
	assert.Equal(t, "bar", opt.MustGet("should not be unset"))
}

func TestSetOrFnSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	opt.SetOrFn("bar", func(a, b string) string { return a + b })
	assert.Equal(t, "foobar", opt.MustGet("should not be unset"))
}

func TestSetOrFnNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	opt.SetOrFn("bar", func(a, b string) string { return a + b })
	assert.Equal(t, "bar", opt.MustGet("should not be unset"))
}

func TestMapSome(t *testing.T) {
	t.Parallel()

	opt := optional.Some("foo")
	opt2 := optional.Map(opt, func(s string) int { return len(s) })
	assert.Equal(t, int(3), opt2.MustGet("should preeserve presence"))
}

func TestMapNone(t *testing.T) {
	t.Parallel()

	opt := optional.None[string]()
	opt2 := optional.Map(opt, func(s string) int { return len(s) })
	assert.Equal(t, optional.None[int](), opt2)
}

func TestGetMapSome(t *testing.T) {
	t.Parallel()

	assert.Equal(t, optional.Some[int32](0), optional.GetMap(map[string]int32{"": 0}, ""))
}

func TestGetMapNone(t *testing.T) {
	t.Parallel()

	assert.Equal(t, optional.None[int32](), optional.GetMap(map[string]int32{}, ""))
}

func unreachableDueTo[T any](t *testing.T, variant string) T {
	require.Failf(t, "this function should not be called on %s", variant)
	panic("unreachable")
}
