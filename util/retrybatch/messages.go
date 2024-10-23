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
	"time"

	"github.com/kubewharf/podseidon/util/util"
)

type operationRequest[Arg any, Result any] struct {
	arg      Arg
	retries  int
	resultCh chan<- submitResult[Result]
	ctxErr   func() error
}

// Result to be returned by Pool.Submit.
//
// The field ExecuteResult is only usable when Err is nil.
type submitResult[Result any] struct {
	ExecuteResult Result
	Retries       int
	Err           error
}

func errorSubmitResult[Result any](retries int, err error) submitResult[Result] {
	return submitResult[Result]{
		ExecuteResult: util.Zero[Result](),
		Retries:       retries,
		Err:           err,
	}
}

// Only one of the two optionals is present.
type ExecuteResult[Result any] struct {
	Variant ExecuteResultVariant

	Success   func(i int) Result
	NeedRetry time.Duration
	Err       error
}

type ExecuteResultVariant uint8

func (variant ExecuteResultVariant) String() string {
	switch variant {
	case ExecuteResultVariantSuccess:
		return "Success"
	case ExecuteResultVariantNeedRetry:
		return "NeedRetry"
	case ExecuteResultVariantErr:
		return "Err"
	}

	panic("invalid variant")
}

const (
	ExecuteResultVariantSuccess = ExecuteResultVariant(iota + 1)
	ExecuteResultVariantNeedRetry
	ExecuteResultVariantErr
)

func ExecuteResultSuccess[Result any](resultFn func(i int) Result) ExecuteResult[Result] {
	var output ExecuteResult[Result]
	output.Variant = ExecuteResultVariantSuccess
	output.Success = resultFn

	return output
}

func ExecuteResultNeedRetry[Result any](retryDelay time.Duration) ExecuteResult[Result] {
	var output ExecuteResult[Result]
	output.Variant = ExecuteResultVariantNeedRetry
	output.NeedRetry = retryDelay

	return output
}

func ExecuteResultErr[Result any](err error) ExecuteResult[Result] {
	var output ExecuteResult[Result]
	output.Variant = ExecuteResultVariantErr
	output.Err = err

	return output
}
