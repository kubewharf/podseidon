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

package errors

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kubewharf/podseidon/util/util"
)

type taggedError struct {
	err error
	tag string
}

// Attaches a tag to an error.
//
// A tag should be a string literal that describes the current function path concisely.
// It is used for reporting error type in metrics, so a small cardinality is expected.
func Tag(tag string, err error) error {
	if err == nil {
		return nil
	}

	return taggedError{
		err: err,
		tag: tag,
	}
}

func (err taggedError) Error() string {
	return err.err.Error()
}

func (err taggedError) Unwrap() error {
	return err.err
}

// Serializes the tags in an error to a single string of constant (known at compile time) cardinality.
func SerializeTags(err error) string {
	if err == nil {
		return "nil"
	}

	tags := GetTags(err)

	if len(tags) == 0 {
		return "unknown"
	}

	uniqueTags := make([]string, 0, len(tags))

	for i, tag := range tags {
		if util.FindInSlice(tags[:i], tag) == -1 {
			uniqueTags = append(uniqueTags, tag)
		}
	}

	return strings.Join(uniqueTags, "/")
}

// Scans all wrapped tags in an error.
func GetTags(err error) []string {
	output := []string{}

	recurseScanTags(err, &output)

	return output
}

// Scans all wrapped tags recursively and writes them into `tags`.
func recurseScanTags(err error, tags *[]string) {
	//nolint:errorlint // wrapped errors are checked below
	if tagged, isTagged := err.(taggedError); isTagged {
		*tags = append(*tags, tagged.tag)
	}

	if statusErr, isStatusErr := err.(apierrors.APIStatus); isStatusErr {
		*tags = append(*tags, string(statusErr.Status().Reason))
	}

	if Is(err, context.Canceled) {
		*tags = append(*tags, "ContextCancel")
	}
	if Is(err, context.DeadlineExceeded) {
		*tags = append(*tags, "ContextTimeout")
	}

	var children []error

	if multiUnwrap, isMultiUnwrap := err.(interface {
		Unwrap() []error
	}); isMultiUnwrap {
		children = multiUnwrap.Unwrap()
	}

	if singleUnwrap, isSingleUnwrap := err.(interface {
		Unwrap() error
	}); isSingleUnwrap {
		children = append(children, singleUnwrap.Unwrap())
	}

	for _, child := range children {
		recurseScanTags(child, tags)
	}
}

// Shorthand for Errorf + Tag.
func TagErrorf(tag string, format string, args ...any) error {
	return Tag(tag, Errorf(format, args...))
}

// Shorthand for Wrapf + Tag.
func TagWrapf(tag string, err error, format string, args ...any) error {
	return Tag(tag, Errorf("%s: %w", fmt.Sprintf(format, args...), err))
}
