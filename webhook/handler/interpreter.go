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

package handler

import (
	"context"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/utils/clock"

	podseidon "github.com/kubewharf/podseidon/apis"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/optional"
	podutil "github.com/kubewharf/podseidon/util/pod"
	"github.com/kubewharf/podseidon/util/util"
)

type InterpretedRequest interface {
	RequestType() string
	PrefersDryRun() bool
	IsAlreadyTerminating() bool
	DurationSinceReady(clk clock.Clock) optional.Optional[time.Duration]
	GetMetadata() *metav1.ObjectMeta
	GetAnnotation(key string) optional.Optional[string]
	GetPodStatus(minReadySeconds int32) podutil.PodStatus
}

type interpretedPodRequest struct {
	pod *corev1.Pod
}

func (interpretedPodRequest) RequestType() string {
	return "DeletePod"
}

func (req interpretedPodRequest) PrefersDryRun() bool {
	_, prefersDryRun := req.pod.Annotations[podseidon.PodAnnotationForceDelete]
	return prefersDryRun
}

func (req interpretedPodRequest) IsAlreadyTerminating() bool {
	return !req.pod.DeletionTimestamp.IsZero()
}

func (req interpretedPodRequest) DurationSinceReady(clk clock.Clock) optional.Optional[time.Duration] {
	conditions := req.pod.Status.Conditions
	if readyConditionIndex := util.FindInSliceWith(
		conditions,
		func(condition corev1.PodCondition) bool { return condition.Type == corev1.PodReady },
	); readyConditionIndex != -1 {
		condition := conditions[readyConditionIndex]
		if condition.Status == corev1.ConditionTrue {
			return optional.Some(clk.Since(condition.LastTransitionTime.Time))
		}
	}

	return optional.None[time.Duration]()
}

func (req interpretedPodRequest) GetMetadata() *metav1.ObjectMeta {
	return &req.pod.ObjectMeta
}

func (req interpretedPodRequest) GetAnnotation(key string) optional.Optional[string] {
	return optional.GetMap(req.pod.Annotations, key)
}

func (req interpretedPodRequest) GetPodStatus(minReadySeconds int32) podutil.PodStatus {
	return podutil.GetPodStatus(clock.RealClock{}, req.pod, minReadySeconds, nil)
}

type interpretedEvictionRequest struct {
	subjectPod *corev1.Pod
	eviction   *policyv1.Eviction
}

func (interpretedEvictionRequest) RequestType() string {
	return "EvictPod"
}

func (req interpretedEvictionRequest) PrefersDryRun() bool {
	_, prefersDryRun := req.subjectPod.Annotations[podseidon.PodAnnotationForceDelete]
	return prefersDryRun
}

func (req interpretedEvictionRequest) IsAlreadyTerminating() bool {
	return !req.subjectPod.DeletionTimestamp.IsZero()
}

func (req interpretedEvictionRequest) DurationSinceReady(clk clock.Clock) optional.Optional[time.Duration] {
	conditions := req.subjectPod.Status.Conditions
	if readyConditionIndex := util.FindInSliceWith(
		conditions,
		func(condition corev1.PodCondition) bool { return condition.Type == corev1.PodReady },
	); readyConditionIndex != -1 {
		condition := conditions[readyConditionIndex]
		if condition.Status == corev1.ConditionTrue {
			return optional.Some(clk.Since(condition.LastTransitionTime.Time))
		}
	}

	return optional.None[time.Duration]()
}

func (req interpretedEvictionRequest) GetMetadata() *metav1.ObjectMeta {
	return &req.subjectPod.ObjectMeta
}

func (req interpretedEvictionRequest) GetAnnotation(key string) optional.Optional[string] {
	if value, ok := req.eviction.Annotations[key]; ok {
		return optional.Some(value)
	}

	if value, ok := req.subjectPod.Annotations[key]; ok {
		return optional.Some(value)
	}

	return optional.None[string]()
}

func (req interpretedEvictionRequest) GetPodStatus(minReadySeconds int32) podutil.PodStatus {
	return podutil.GetPodStatus(clock.RealClock{}, req.subjectPod, minReadySeconds, nil)
}

func interpretRequest(
	ctx context.Context,
	req *admissionv1.AdmissionRequest,
	podClientGetter func() (corev1client.PodsGetter, error),
) (InterpretedRequest, error) {
	if req == nil {
		//nolint:nilnil // by design, caller does not use errors
		return nil, nil
	}

	podGvr := metav1.GroupVersionResource{
		Group:    corev1.SchemeGroupVersion.Group,
		Version:  corev1.SchemeGroupVersion.Version, // we required matchPolicy=Equivalent
		Resource: "pods",
	}

	if req.Operation == admissionv1.Delete && req.Resource == podGvr && req.SubResource == "" {
		return interpretPodRequest(req)
	}

	if req.Operation == admissionv1.Create && req.Resource == podGvr && req.SubResource == "eviction" {
		return interpretEvictionRequest(ctx, req, podClientGetter)
	}

	//nolint:nilnil // by design, caller does not use errors
	return nil, nil
}

func interpretPodRequest(req *admissionv1.AdmissionRequest) (InterpretedRequest, error) {
	podJson := req.OldObject.Raw
	if len(podJson) == 0 {
		return nil, errors.TagErrorf(
			"EmptyOldObject",
			"oldObject is missing in delete review request",
		)
	}

	var subject *corev1.Pod
	if err := json.Unmarshal(podJson, &subject); err != nil {
		return nil, errors.TagWrapf(
			"OldObjectJsonError",
			err,
			"cannot unmarshal oldObject as a *corev1.Pod",
		)
	}
	if subject == nil {
		return nil, errors.TagErrorf("NilOldObject", "oldObject is nil")
	}

	return interpretedPodRequest{pod: subject}, nil
}

func interpretEvictionRequest(
	ctx context.Context,
	req *admissionv1.AdmissionRequest,
	podClientGetter func() (corev1client.PodsGetter, error),
) (InterpretedRequest, error) {
	podClient, err := podClientGetter()
	if err != nil {
		return nil, errors.TagWrapf("GetClientForEvictedPod", err, "get subject pod of eviction from apiserver")
	}

	fetchedPod, err := podClient.Pods(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{ResourceVersion: "0"})
	if err != nil {
		return nil, errors.TagWrapf("GetEvictedPod", err, "get subject pod of eviction from apiserver")
	}

	evictionJson := req.Object.Raw
	if len(evictionJson) == 0 {
		return nil, errors.TagErrorf(
			"EmptyObject",
			"object is missing in eviction review request",
		)
	}

	var eviction *policyv1.Eviction
	if err := json.Unmarshal(evictionJson, &eviction); err != nil {
		return nil, errors.TagWrapf(
			"ObjectJsonError",
			err,
			"cannot unmarshal object as a *policyv1.Eviction",
		)
	}
	if eviction == nil {
		return nil, errors.TagErrorf("NilObject", "object is nil")
	}

	return interpretedEvictionRequest{subjectPod: fetchedPod, eviction: eviction}, nil
}
