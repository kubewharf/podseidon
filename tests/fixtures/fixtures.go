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

package fixtures

import (
	"context"
	"time"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/tests/provision"
	testutil "github.com/kubewharf/podseidon/tests/util"
)

const TestPodLabelKey = "podseidon-e2e/test"

func CreatePodProtector(
	ctx context.Context,
	env *provision.Env,
	pprName string,
	pprUid *types.UID,
	minAvailable int32,
	minReadySeconds int32,
	config podseidonv1a1.AdmissionHistoryConfig,
	pprModifier func(*podseidonv1a1.PodProtector),
) {
	env.ReportKelemetryTrace(
		testutil.CoreClusterId,
		podseidonv1a1.SchemeGroupVersion.WithResource("podprotectors"),
		pprName,
	)

	ppr := &podseidonv1a1.PodProtector{
		ObjectMeta: metav1.ObjectMeta{
			Name: pprName,
		},
		Spec: podseidonv1a1.PodProtectorSpec{
			MinAvailable:    minAvailable,
			MinReadySeconds: minReadySeconds,
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{TestPodLabelKey: env.Namespace},
			},
			AdmissionHistoryConfig: config,
		},
	}
	pprModifier(ppr)

	ppr, err := env.PprClient().
		Create(ctx, ppr, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	*pprUid = ppr.UID
}

func CreatePod(
	ctx context.Context,
	env *provision.Env,
	pprName string,
	pprUid types.UID,
	podId testutil.PodId,
	podModifier func(*corev1.Pod),
) {
	env.ReportKelemetryTrace(
		podId.Cluster,
		corev1.SchemeGroupVersion.WithResource("pods"),
		podId.PodName(),
	)

	parentLink, err := json.Marshal(
		map[string]string{
			"cluster":   "core",
			"group":     podseidonv1a1.SchemeGroupVersion.Group,
			"version":   podseidonv1a1.SchemeGroupVersion.Version,
			"resource":  "podprotectors",
			"namespace": env.Namespace,
			"name":      pprName,
			"uid":       string(pprUid),
		},
	)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   podId.PodName(),
			Labels: map[string]string{TestPodLabelKey: env.Namespace},
			Annotations: map[string]string{
				"kelemetry.kubewharf.io/parent-link": string(parentLink),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "example.com/e2e/no:image",
				},
			},
			SchedulerName: "e2e-no-schedule",
		},
	}
	podModifier(pod)

	_, err = env.PodClient(podId.Cluster).Create(ctx, pod, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
}

func CreatePodProtectorAndPods(
	ctx context.Context,
	env *provision.Env,
	pprName string,
	replicas testutil.PodCounts,
	minAvailable int32,
	minReadySeconds int32,
	config podseidonv1a1.AdmissionHistoryConfig,
) {
	var pprUid types.UID

	CreatePodProtector(
		ctx,
		env,
		pprName,
		&pprUid,
		minAvailable,
		minReadySeconds,
		config,
		func(*podseidonv1a1.PodProtector) {},
	)

	for _, podCount := range replicas.PodIds() {
		CreatePod(ctx, env, pprName, pprUid, podCount, func(*corev1.Pod) {})
	}
}

func MarkPodAsReady(
	ctx context.Context,
	env *provision.Env,
	podId testutil.PodId,
	readyTime time.Time,
) {
	MarkPodConditions(ctx, env, podId, PodConditions{
		Phase:       corev1.PodRunning,
		Scheduled:   optional.Some(readyTime),
		Initialized: optional.Some(readyTime),
		Ready:       optional.Some(readyTime),
	})
}

type PodConditions struct {
	Phase corev1.PodPhase

	Scheduled   optional.Optional[time.Time]
	Initialized optional.Optional[time.Time]
	Ready       optional.Optional[time.Time]
}

func MarkPodConditions(
	ctx context.Context,
	env *provision.Env,
	podId testutil.PodId,
	podConditions PodConditions,
) {
	err := env.PodClient(podId.Cluster).
		Bind(ctx, &corev1.Binding{
			ObjectMeta: metav1.ObjectMeta{
				Name: podId.PodName(),
			},
			Target: corev1.ObjectReference{
				Kind: "Node",
				Name: "e2e-node-1",
			},
		}, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, err := env.PodClient(podId.Cluster).Get(ctx, podId.PodName(), metav1.GetOptions{})
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

		updatePodCondition(pod, corev1.PodInitialized, podConditions.Initialized)
		updatePodCondition(pod, corev1.ContainersReady, podConditions.Ready)
		updatePodCondition(pod, corev1.PodReady, podConditions.Ready)
		updatePodCondition(pod, corev1.PodScheduled, podConditions.Scheduled)
		pod.Status.Phase = podConditions.Phase

		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name:    "main",
			Ready:   podConditions.Ready.IsSome(),
			Started: ptr.To(true),
		}}

		_, err = env.PodClient(podId.Cluster).UpdateStatus(ctx, pod, metav1.UpdateOptions{})
		return err
	})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
}

func updatePodCondition(
	pod *corev1.Pod,
	conditionType corev1.PodConditionType,
	transitionTime optional.Optional[time.Time],
) {
	condition := util.GetOrAppendSliceWith(
		&pod.Status.Conditions,
		func(condition *corev1.PodCondition) bool { return condition.Type == conditionType },
		func() corev1.PodCondition { return corev1.PodCondition{Type: conditionType} },
	)

	if transitionTime, transitioned := transitionTime.Get(); transitioned {
		condition.Status = corev1.ConditionTrue
		condition.LastTransitionTime.Time = transitionTime
	} else {
		condition.Status = corev1.ConditionFalse
	}
}

func TryDeleteAllPodsIn(ctx context.Context, env *provision.Env, cluster testutil.ClusterId) error {
	return env.PodClient(cluster).
		DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
			LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{
				MatchLabels: map[string]string{TestPodLabelKey: env.Namespace},
			}),
		})
}

func UpdatePod(ctx context.Context, env *provision.Env, podId testutil.PodId, mutateFn func(*corev1.Pod)) {
	pod, err := env.PodClient(podId.Cluster).Get(ctx, podId.PodName(), metav1.GetOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	mutateFn(pod)

	_, err = env.PodClient(podId.Cluster).Update(ctx, pod, metav1.UpdateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
}
