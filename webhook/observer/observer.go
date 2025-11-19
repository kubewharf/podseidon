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
	Cell       string
	RemoteAddr string
}

type RequestComplete struct {
	Request *admissionv1.AdmissionRequest
	Status  RequestStatus
}

type RequestStatus string

const (
	// Subject pod does not match any PodProtector.
	RequestStatusUnmatched = RequestStatus("Unmatched")
	// The admission review involves a resource not handled by Podseidon.
	RequestStatusNotRelevant = RequestStatus("NotRelevant")
	// The subject pod is already terminating and further deletion has no effect.
	RequestStatusAlreadyTerminating = RequestStatus("AlreadyTerminating")
	// The subject pod is already unready and deletion does not disrupt any PodProtector quota.
	RequestStatusAlreadyUnready = RequestStatus("AlreadyUnready")
	// The subject pod has not reached minReadySeconds of a matched PodProtector
	// and deletion does not disrupt its quota.
	//
	// This status is only used in HandlePodInPpr.
	// AdmittedAll is returned instead for the HttpRequest status.
	RequestStatusStillUnavailable = RequestStatus("StillUnavailable")
	// The request matches more than one PodProtector,
	// all of which allows the request to proceed,
	// either because minReadySeconds has not been reached
	// or because the new disruption can be and has been recorded into the PodProtector's status.
	RequestStatusAdmittedAll = RequestStatus("AdmittedAll")
	// The request has been rejected by a PodProtector
	// because it violates its estimated availability,
	// but the aggregated availability is not violated.
	RequestStatusRetryAdvised = RequestStatus("RetryAdvised")
	// The subject pod has been rejected by a PodProtector
	// because it violates its aggregated availability.
	RequestStatusRejected = RequestStatus("Rejected")
	// An unclassified error has occurred while processing the request.
	// This may be due to a webhook internal error or due to an invalid request.
	RequestStatusError = RequestStatus("Error")
	// The request is an eviction request,
	// but the pod to be evicted cannot be found in the apiserver cache.
	RequestStatusNotFound = RequestStatus("NotFound")
)

type HttpError struct {
	Err error
}

type StartHandlePodInPpr struct {
	Namespace   string
	PprName     string
	PodName     string
	PodCell     string
	RequestType string

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
	CellId  string
	PodUid  types.UID
	PodName string
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
