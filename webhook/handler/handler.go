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

package handler

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	podseidon "github.com/kubewharf/podseidon/apis"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
	"github.com/kubewharf/podseidon/util/retrybatch"
	retrybatchobserver "github.com/kubewharf/podseidon/util/retrybatch/observer"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/webhook/observer"
)

const batchGoroutineIdleTimeout = time.Second

var New = component.Declare(
	func(_ Args) string { return "webhook-handler" },
	func(_ Args, fs *flag.FlagSet) Options {
		return Options{
			ColdStartDelay: fs.Duration(
				"cold-start-delay",
				0,
				"delay period before the first update of an inactive PodProtector",
			),
			RetryBackoffBase: fs.Duration(
				"retry-backoff-base",
				time.Millisecond*100,
				"base retry period after a PodProtector update fails due to conflict",
			),
			RetryJitter: fs.Duration(
				"retry-jitter",
				time.Millisecond*100,
				"the actual retry backoff is uniformly distributed between [base, base+jitter)",
			),
		}
	},
	func(_ Args, requests *component.DepRequests) Deps {
		return Deps{
			sourceProvider: component.DepPtr(requests, pprutil.RequestSourceProvider()),
			pprInformer: component.DepPtr(
				requests,
				pprutil.NewIndexedInformer(pprutil.IndexedInformerArgs{
					Suffix:  "",
					Elector: optional.None[kube.ElectorArgs](),
				}),
			),
			observer:        o11y.Request[observer.Observer](requests),
			requiresPodName: component.DepPtr(requests, RequestRequiresPodName()),
			retrybatchObs:   o11y.Request[retrybatchobserver.Observer](requests),
			defaultConfig:   component.DepPtr(requests, defaultconfig.New(util.Empty{})),
		}
	},
	func(_ context.Context, args Args, options Options, deps Deps) (*State, error) {
		sourceProvider := deps.sourceProvider.Get()

		poolReader, poolWriter := util.NewLateInit[retrybatch.Pool[pprutil.PodProtectorKey, BatchArg, pprutil.DisruptionResult]]()

		return &State{
			sourceProvider:    sourceProvider,
			informerHasSynced: deps.pprInformer.Get().HasSynced,
			poolConfig: retrybatch.NewPool(
				deps.retrybatchObs.Get(),
				PoolAdapter{
					sourceProvider:  sourceProvider,
					pprInformer:     deps.pprInformer.Get(),
					observer:        deps.observer.Get(),
					clock:           args.Clock,
					requiresPodName: deps.requiresPodName.Get(),
					retryBackoff: func() time.Duration {
						return jitterDuration(
							*options.RetryBackoffBase,
							*options.RetryBackoffBase+*options.RetryJitter,
						)
					},
					defaultConfig: deps.defaultConfig.Get(),
				},
				*options.ColdStartDelay, batchGoroutineIdleTimeout,
			),
			poolWriter: poolWriter,
			poolReader: poolReader,
		}, nil
	},
	component.Lifecycle[Args, Options, Deps, State]{
		Start: func(ctx context.Context, _ *Args, _ *Options, _ *Deps, state *State) error {
			pool := state.poolConfig.Create(ctx)
			pool.StartMonitor(ctx)

			state.poolWriter(pool)

			return nil
		},
		Join:         nil,
		HealthChecks: nil,
	},
	func(d *component.Data[Args, Options, Deps, State]) Api {
		return Api{
			state:       d.State,
			clk:         d.Args.Clock,
			observer:    d.Deps.observer.Get(),
			pprInformer: d.Deps.pprInformer.Get(),
		}
	},
)

type Args struct {
	Clock clock.Clock
}

type Options struct {
	ColdStartDelay   *time.Duration
	RetryBackoffBase *time.Duration
	RetryJitter      *time.Duration
}

type Deps struct {
	sourceProvider  component.Dep[pprutil.SourceProvider]
	pprInformer     component.Dep[pprutil.IndexedInformer]
	observer        component.Dep[observer.Observer]
	requiresPodName component.Dep[RequiresPodName]
	retrybatchObs   component.Dep[retrybatchobserver.Observer]
	defaultConfig   component.Dep[*defaultconfig.Options]
}

type State struct {
	sourceProvider    pprutil.SourceProvider
	informerHasSynced cache.InformerSynced

	poolConfig retrybatch.PoolConfig[pprutil.PodProtectorKey, BatchArg, pprutil.DisruptionResult]
	poolWriter util.LateInitWriter[retrybatch.Pool[pprutil.PodProtectorKey, BatchArg, pprutil.DisruptionResult]]
	poolReader util.LateInitReader[retrybatch.Pool[pprutil.PodProtectorKey, BatchArg, pprutil.DisruptionResult]]
}

type Api struct {
	state       *State
	clk         clock.Clock
	observer    observer.Observer
	pprInformer pprutil.IndexedInformer
}

type HandleResult struct {
	Status    observer.RequestStatus
	Rejection optional.Optional[Rejection]
	Err       error
}

func errHandleResult(err error) (HandleResult, bool) {
	return HandleResult{
		Status:    observer.RequestStatusError,
		Rejection: optional.None[Rejection](),
		Err:       err,
	}, false
}

//nolint:cyclop // Mostly just top-level error branches. Further abstraction does not improve readability.
func (api Api) Handle(
	ctx context.Context,
	req *admissionv1.AdmissionRequest,
	cellId string,
	auditAnnotations map[string]string,
) (_ HandleResult, _preferDryRun bool) {
	if !isRelevantRequest(req) {
		return HandleResult{
			Status: observer.RequestStatusNotRelevant,
			Rejection: optional.Some(Rejection{
				Code:    http.StatusInternalServerError,
				Message: "Unexpected review subject; only pod deletions are handled by this webhook",
			}),
			Err: nil,
		}, false
	}

	podJson := req.OldObject.Raw
	if len(podJson) == 0 {
		return errHandleResult(errors.TagErrorf(
			"EmptyOldObject",
			"oldObject is missing in delete review request",
		))
	}

	var subject *corev1.Pod
	if err := json.Unmarshal(podJson, &subject); err != nil {
		return errHandleResult(errors.TagErrorf(
			"OldObjectJsonError",
			"cannot unmarshal oldObject as a *corev1.Pod",
		))
	}

	_, preferDryRun := subject.Annotations[podseidon.PodAnnotationForceDelete]

	if !subject.DeletionTimestamp.IsZero() {
		// Pods that are already terminating should not contribute twice to the admission history.
		return HandleResult{
			Status:    observer.RequestStatusAlreadyTerminating,
			Rejection: optional.None[Rejection](),
			Err:       nil,
		}, preferDryRun
	}

	podReadyTimeOpt := optional.None[time.Duration]()

	if readyConditionIndex := util.FindInSliceWith(
		subject.Status.Conditions,
		func(condition corev1.PodCondition) bool { return condition.Type == corev1.PodReady },
	); readyConditionIndex != -1 {
		condition := subject.Status.Conditions[readyConditionIndex]
		if condition.Status == corev1.ConditionTrue {
			podReadyTimeOpt = optional.Some(api.clk.Since(condition.LastTransitionTime.Time))
		}
	}

	podReadyTime, isPodReady := podReadyTimeOpt.Get()
	if !isPodReady {
		// Do not reject pods that are already unready anyway.
		// We expect that aggregator should have concluded the unavailability event.
		// If aggregator is not working, webhook is not a reliable way to stack up the admission history either,
		// since the webhook timestamp is not the timestamp that the pod became unavailable.
		return HandleResult{
			Status:    observer.RequestStatusAlreadyUnready,
			Rejection: optional.None[Rejection](),
			Err:       nil,
		}, preferDryRun
	}

	if !api.state.informerHasSynced() {
		return errHandleResult(
			errors.TagErrorf("InformerNotSynced", "PodProtector informer is not synced yet"),
		)
	}

	admitted := 0

	for _, pprRef := range api.pprInformer.Query(subject.Namespace, subject.Labels) {
		// If multiple PodProtector are matched, short circuit when any of them fails.
		// Ideally we should roll back previous PodProtectors,
		// but it is currently unimplemented because
		// there are no pods matching multiple PodProtectors in practice.
		result, canContinue := api.handlePodInPpr(ctx, pprRef, subject, podReadyTime, req.UserInfo, cellId)

		if !canContinue {
			auditAnnotations[podseidon.AuditAnnotationRejectByPpr] = pprRef.Name

			return result, preferDryRun
		}

		admitted++
	}

	result := HandleResult{
		Status:    observer.RequestStatusAdmittedAll,
		Rejection: optional.None[Rejection](),
		Err:       nil,
	}

	if admitted == 0 {
		result.Status = observer.RequestStatusUnmatched
	}

	return result, preferDryRun
}

func (api Api) handlePodInPpr(
	ctx context.Context,
	pprRef pprutil.PodProtectorKey,
	pod *corev1.Pod,
	podReadyTime time.Duration,
	user authenticationv1.UserInfo,
	cellId string,
) (_ HandleResult, _canContinue bool) {
	ctx, cancelFunc := api.observer.StartHandlePodInPpr(ctx, observer.StartHandlePodInPpr{
		Namespace:        pod.Namespace,
		PprName:          pprRef.Name,
		PodName:          pod.Name,
		DeleteUserName:   user.Username,
		DeleteUserGroups: user.Groups,
	})
	defer cancelFunc()

	result := api.determineRejection(ctx, pprRef, podReadyTime, pod, cellId)

	// code is only used for o11y.
	{
		code := uint16(http.StatusOK)
		if reject, present := result.Rejection.Get(); present {
			code = reject.Code
		}

		api.observer.EndHandlePodInPpr(ctx, observer.EndHandlePodInPpr{
			Rejected: result.Rejection.IsSome(),
			Code:     code,
			Err:      errors.SerializeTags(result.Err),
		})
	}

	return result, result.Err == nil && !result.Rejection.IsSome()
}

func isRelevantRequest(req *admissionv1.AdmissionRequest) bool {
	return req != nil &&
		req.Operation == admissionv1.Delete && // TODO do we also handle CREATE /eviction?
		req.Resource == metav1.GroupVersionResource{
			Group:    corev1.SchemeGroupVersion.Group,
			Version:  corev1.SchemeGroupVersion.Version, // we required matchPolicy=Equivalent
			Resource: "pods",
		} &&
		req.SubResource == ""
}

func (api Api) determineRejection(
	ctx context.Context,
	pprRef pprutil.PodProtectorKey,
	podReadyTime time.Duration,
	pod *corev1.Pod,
	cellId string,
) HandleResult {
	ppr, err := api.pprInformer.Get(pprRef)
	if err != nil || ppr.IsNone() {
		return HandleResult{
			Status: observer.RequestStatusError,
			Rejection: optional.Some(Rejection{
				Code:    http.StatusInternalServerError,
				Message: fmt.Sprintf("Cannot fetch ppr from informer store: %s", err.Error()),
			}),
			Err: errors.TagWrapf("GetPprFromInformer", err, "cannot fetch PodProtector from informer store"),
		}
	}

	// Pods that are still unavailable do not contribute to the availability of the PodProtector and can be safely deleted.
	// There is a possible race condition where the pod becomes available just after this request gets admitted
	// e.g. due to clock skew or network latency in replicaset/deployment controller allowing the next pod to roll,
	// but this marginal case is exceptionally rare and is impractical to prevent.
	minReadySeconds := ppr.MustGet("checked !ppr.IsNone()").Spec.MinReadySeconds
	if podReadyTime < time.Duration(minReadySeconds)*time.Second {
		return HandleResult{
			Status:    observer.RequestStatusStillUnavailable,
			Rejection: optional.None[Rejection](),
			Err:       nil,
		}
	}

	result, err := api.state.poolReader.Get().Submit(ctx, pprRef, BatchArg{CellId: cellId, PodUid: pod.UID, PodName: pod.Name})
	if err != nil {
		return HandleResult{
			Status: observer.RequestStatusError,
			Rejection: optional.Some(Rejection{
				Code:    http.StatusInternalServerError,
				Message: fmt.Sprintf("Cannot reserve PodProtector admission: %s", err.Error()),
			}),
			Err: errors.TagWrapf("ReserveAdmission", err, "cannot reserve PodProtector admission"),
		}
	}

	switch result {
	case pprutil.DisruptionResultOk:
		return HandleResult{
			Status:    observer.RequestStatusAdmittedAll,
			Rejection: optional.None[Rejection](),
			Err:       nil,
		}
	case pprutil.DisruptionResultDenied:
		return HandleResult{
			Status: observer.RequestStatusRejected,
			Rejection: optional.Some(Rejection{
				Code: http.StatusBadRequest,
				Message: fmt.Sprintf(
					"PodProtector %s/%s reports too few available replicas to admit pod deletion",
					pprRef.Namespace, pprRef.Name,
				),
			}),
			Err: nil,
		}
	case pprutil.DisruptionResultRetry:
		return HandleResult{
			Status: observer.RequestStatusRetryAdvised,
			Rejection: optional.Some(Rejection{
				Code: http.StatusConflict,
				Message: fmt.Sprintf(
					"PodProtector %s/%s has full admission buffer and is temporarily unable to admit pod deletion",
					pprRef.Namespace,
					pprRef.Name,
				),
			}),
			Err: nil,
		}
	default:
		panic("invalid DisruptionResult value")
	}
}

type Rejection struct {
	Code    uint16
	Message string
}

func (rejection Rejection) ToStatus() *metav1.Status {
	return &metav1.Status{
		Code:    int32(rejection.Code),
		Message: rejection.Message,
	}
}

func jitterDuration(base, jitter time.Duration) time.Duration {
	return base + time.Duration(rand.Int63n(int64(jitter)))
}
