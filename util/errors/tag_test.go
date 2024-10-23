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

package errors_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kubewharf/podseidon/util/errors"
)

func TestGetTags(t *testing.T) {
	t.Parallel()

	k8sErr1 := apierrors.NewBadRequest("test1")
	k8sErr2 := apierrors.NewResourceExpired("test2")
	normalErr3 := errors.Errorf("test3")
	tagErr4 := errors.TagErrorf("Test4", "test4")

	wrap1 := fmt.Errorf("wrap %w", k8sErr1)
	wrap2 := errors.Wrapf(k8sErr2, "wrap")

	tag1 := errors.TagWrapf("Tag1", wrap1, "tag 1")
	tag2 := errors.TagWrapf("Tag2", wrap2, "tag 2")

	joined := errors.Join(tag1, tag2, normalErr3, tagErr4)
	tagJoined := errors.TagWrapf("Joined", joined, "joined error")

	assert.Equal(
		t,
		[]string{"Joined", "Tag1", "BadRequest", "Tag2", "Expired", "Test4"},
		errors.GetTags(tagJoined),
	)
	assert.Equal(t, "Joined/Tag1/BadRequest/Tag2/Expired/Test4", errors.SerializeTags(tagJoined))
}
