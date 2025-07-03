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

package updatetrigger

import (
	"context"
	"flag"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	utilflag "github.com/kubewharf/podseidon/util/flag"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/aggregator/constants"
	"github.com/kubewharf/podseidon/aggregator/observer"
)

var New = component.Declare(
	func(Args) string { return "update-trigger" },
	func(_ Args, fs *flag.FlagSet) Options {
		return Options{
			Enable:          fs.Bool("enable", false, "trigger a dummy pod update periodically to keep the watch stream alive"),
			UpdateFrequency: fs.Duration("update-frequency", time.Second, "frequency of generated update requests"),
			PodName:         fs.String("dummy-pod-name", "podseidon-update-trigger", "name of the dummy pod"),
			PodNamespace:    fs.String("dummy-pod-namespace", metav1.NamespaceAll, "namespace of the dummy pod"),
			PodLabels: utilflag.Map(
				fs, "dummy-pod-labels", map[string]string{}, "labels of the dummy pod",
				utilflag.StringParser, utilflag.StringParser,
			),
			PodAnnotations: utilflag.Map(
				fs, "dummy-pod-annotations", map[string]string{}, "annotations of the dummy pod",
				utilflag.StringParser, utilflag.StringParser,
			),
			PodImage: fs.String("dummy-pod-image", ":", "image field of the dummy pod; would not get pulled if pod is not scheduled"),
			PodScheduler: fs.String(
				"dummy-pod-scheduler",
				"do-not-schedule",
				"schedulerName field of the dummy pod; keep unchanged to avoid scheduling the pod to a node",
			),
		}
	},
	func(_ Args, requests *component.DepRequests) Deps {
		return Deps{
			workerClient: component.DepPtr(
				requests,
				kube.NewClient(kube.ClientArgs{ClusterName: constants.WorkerClusterName}),
			),
			elector:  component.DepPtr(requests, kube.NewElector(constants.ElectorArgs)),
			observer: o11y.Request[observer.Observer](requests),
		}
	},
	func(context.Context, Args, Options, Deps) (*State, error) {
		return &State{}, nil
	},
	component.Lifecycle[Args, Options, Deps, State]{
		Start: func(ctx context.Context, _ *Args, options *Options, deps *Deps, _ *State) error {
			if !*options.Enable {
				return nil
			}

			go func() {
				ctx, err := deps.elector.Get().Await(ctx)
				if err != nil {
					return
				}

				spin := spinCreate
				obs := func(err error) {
					deps.observer.Get().TriggerPodCreate(ctx, observer.TriggerPodCreate{Err: err})
				}

				wait.UntilWithContext(ctx, func(ctx context.Context) {
					err := spin(ctx, deps.workerClient.Get(), options)
					obs(err)

					if err == nil {
						spin = spinUpdate
						obs = func(err error) {
							deps.observer.Get().TriggerPodUpdate(ctx, observer.TriggerPodUpdate{Err: err})
						}
					}
				}, *options.UpdateFrequency)
			}()

			return nil
		},
		Join:         nil,
		HealthChecks: nil,
	},
	func(*component.Data[Args, Options, Deps, State]) util.Empty {
		return util.Empty{}
	},
)

type Args struct{}

type Options struct {
	Enable          *bool
	UpdateFrequency *time.Duration

	PodName        *string
	PodNamespace   *string
	PodLabels      *map[string]string
	PodAnnotations *map[string]string
	PodImage       *string
	PodScheduler   *string
}

type Deps struct {
	workerClient component.Dep[*kube.Client]
	elector      component.Dep[*kube.Elector]
	observer     component.Dep[observer.Observer]
}

type State struct{}

func spinCreate(ctx context.Context, client *kube.Client, options *Options) error {
	annotations := map[string]string{
		constants.AnnotUpdateTriggerTime: time.Now().Format(time.RFC3339Nano),
	}
	for k, v := range *options.PodAnnotations {
		annotations[k] = v
	}

	_, err := client.NativeClientSet().CoreV1().Pods(*options.PodNamespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   *options.PodNamespace,
			Name:        *options.PodName,
			Labels:      *options.PodLabels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main", Image: *options.PodImage},
			},
			SchedulerName: *options.PodScheduler,
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return errors.TagWrapf("CreatePod", err, "create trigger pod on worker apiserver")
	}

	return nil
}

func spinUpdate(ctx context.Context, client *kube.Client, options *Options) error {
	patchJson, err := json.Marshal([]map[string]string{{
		"op":    "replace",
		"path":  "/metadata/annotations/" + strings.ReplaceAll(constants.AnnotUpdateTriggerTime, "/", "~1"),
		"value": time.Now().Format(time.RFC3339Nano),
	}})
	if err != nil {
		return errors.TagWrapf("EncodePatch", err, "encode patch json")
	}

	_, err = client.NativeClientSet().
		CoreV1().
		Pods(*options.PodNamespace).
		Patch(ctx, *options.PodName, types.JSONPatchType, patchJson, metav1.PatchOptions{})
	if err != nil {
		return errors.TagWrapf("PatchPod", err, "patch trigger pod on worker apiserver")
	}

	return nil
}
