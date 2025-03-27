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

package observer

import (
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/o11y"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
)

var Provide = component.RequireDeps(
	component.RequireDep(ProvideLogging()),
	component.RequireDep(ProvideMetrics()),
)

type Observer struct {
	HttpRequest         o11y.ObserveScopeFunc[Request]
	HttpRequestComplete o11y.ObserveFunc[RequestComplete]
	HttpError           o11y.ObserveFunc[HttpError]

	RequestFromCell o11y.ObserveScopeFunc[RequestFromCell]

	StartHandlePodInPpr o11y.ObserveScopeFunc[StartHandlePodInPpr]
	EndHandlePodInPpr   o11y.ObserveFunc[EndHandlePodInPpr]

	StartExecuteRetry      o11y.ObserveScopeFunc[StartExecuteRetry]
	EndExecuteRetrySuccess o11y.ObserveFunc[EndExecuteRetrySuccess]
	EndExecuteRetryRetry   o11y.ObserveFunc[EndExecuteRetryRetry]
	EndExecuteRetryErr     o11y.ObserveFunc[EndExecuteRetryErr]
	ExecuteRetryQuota      o11y.ObserveFunc[ExecuteRetryQuota]
}

func (Observer) ComponentName() string { return "webhook" }

func (observer Observer) Join(other Observer) Observer { return o11y.ReflectJoin(observer, other) }

type Request struct {
	CellPath   string
	RemoteAddr string
}

type RequestComplete struct {
	Request *admissionv1.AdmissionRequest
	Status  RequestStatus
}

type RequestStatus string

const (
	RequestStatusUnmatched          = RequestStatus("Unmatched")
	RequestStatusNotRelevant        = RequestStatus("NotRelevant")
	RequestStatusAlreadyTerminating = RequestStatus("AlreadyTerminating")
	RequestStatusAlreadyUnavailable = RequestStatus("AlreadyUnavailable")
	RequestStatusAdmittedAll        = RequestStatus("AdmittedAll")
	RequestStatusRetryAdvised       = RequestStatus("RetryAdvised")
	RequestStatusRejected           = RequestStatus("Rejected")
	RequestStatusError              = RequestStatus("Error")
)

type HttpError struct {
	Err error
}

type RequestFromCell struct {
	CellId string
}

type StartHandlePodInPpr struct {
	Namespace        string
	PprName          string
	PodName          string
	DeleteUserName   string
	DeleteUserGroups []string
}

type EndHandlePodInPpr struct {
	Rejected bool
	Code     uint16
	Err      string
}

type StartExecuteRetry struct {
	Key  pprutil.PodProtectorKey
	Args []BatchArg
}

// Argument for webhook retry-batch-pool, moved to this package to hack import cycles.
type BatchArg struct {
	CellId string
	PodUid types.UID
}

type EndExecuteRetrySuccess struct {
	Results func(int) pprutil.DisruptionResult
}

type EndExecuteRetryRetry struct {
	Delay time.Duration
}

type EndExecuteRetryErr struct {
	Err error
}

type ExecuteRetryQuota struct {
	Before pprutil.DisruptionQuota
	After  pprutil.DisruptionQuota
}
