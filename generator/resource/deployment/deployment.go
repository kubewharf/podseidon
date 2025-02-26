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

package deployment

import (
	"context"
	"flag"
	"fmt"
	"math"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubeinformers "k8s.io/client-go/informers"
	appsv1informers "k8s.io/client-go/informers/apps/v1"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	utilflag "github.com/kubewharf/podseidon/util/flag"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
	"github.com/kubewharf/podseidon/util/worker"

	"github.com/kubewharf/podseidon/generator/constants"
	"github.com/kubewharf/podseidon/generator/observer"
	"github.com/kubewharf/podseidon/generator/resource"
)

var New = component.Declare(
	func(util.Empty) string { return "deployment-plugin" },
	func(_ util.Empty, fs *flag.FlagSet) Options {
		return Options{
			labelSelector: utilflag.LabelSelectorEverything(
				fs,
				"protection-selector",
				"only enable protection for objects matching this selector",
			),
			avoidNonZeroDeletion: fs.Bool("protect-non-zero", false, "prevent cascade deletion when the deployment has non-zero replicas"),
		}
	},
	func(_ util.Empty, requests *component.DepRequests) Deps {
		return Deps{
			observer: o11y.Request[observer.Observer](requests),
			client: component.DepPtr(requests, kube.NewClient(kube.ClientArgs{
				ClusterName: constants.CoreClusterName,
			})),
			informers: component.DepPtr(requests, kube.NewInformers(kube.NativeInformers(
				constants.CoreClusterName,
				constants.LeaderPhase,
				optional.Some(constants.GeneratorElectorArgs),
			))),
		}
	},
	func(context.Context, util.Empty, Options, Deps) (*State, error) { return &State{}, nil },
	component.Lifecycle[util.Empty, Options, Deps, State]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(data *component.Data[util.Empty, Options, Deps, State]) resource.TypeProvider {
		return &TypeProvider{
			TypeDef:  TypeDef{},
			options:  &data.Options,
			observer: data.Deps.observer.Get(),
			cluster:  data.Deps.client.Get(),
			informer: data.Deps.informers.Get().Factory.Apps().V1().Deployments(),
		}
	},
)

type Options struct {
	labelSelector        *labels.Selector
	avoidNonZeroDeletion *bool
}

type Deps struct {
	observer  component.Dep[observer.Observer]
	client    component.Dep[*kube.Client]
	informers component.Dep[kube.Informers[kubeinformers.SharedInformerFactory]]
}

type State struct{}

type TypeDef struct{}

type TypeProvider struct {
	TypeDef
	options  *Options
	observer observer.Observer
	cluster  *kube.Client
	informer appsv1informers.DeploymentInformer
}

func (*TypeDef) GroupVersionResource() schema.GroupVersionResource {
	return appsv1.SchemeGroupVersion.WithResource("deployments")
}

func (*TypeDef) GroupVersionKind() schema.GroupVersionKind {
	return appsv1.SchemeGroupVersion.WithKind("Deployment")
}

func (ty *TypeProvider) GetObject(_ context.Context, nsName types.NamespacedName) resource.SourceObject {
	obj, err := ty.informer.Lister().Deployments(nsName.Namespace).Get(nsName.Name)
	if err == nil && obj != nil {
		return &sourceObject{
			Deployment: obj,
			options:    ty.options,
			client:     ty.cluster.NativeClientSet().AppsV1(),
			observer:   ty.observer,
		}
	}

	return nil
}

func (ty *TypeProvider) AddEventHandler(
	handler func(types.NamespacedName),
) error {
	_, err := ty.informer.Informer().AddEventHandler(kube.GenericEventHandler(handler))
	if err != nil {
		return errors.TagWrapf(
			"AddDeploymentEventHandler",
			err,
			"add event handler to deployment informer",
		)
	}

	return nil
}

func (ty *TypeProvider) AddPrereqs(prereqs map[string]worker.Prereq) {
	prereqs["deployment/informer-sync"] = worker.InformerPrereq(ty.informer.Informer())
}

type sourceObject struct {
	*appsv1.Deployment
	options  *Options
	client   appsv1client.AppsV1Interface
	observer observer.Observer
}

func (*sourceObject) TypeDef() resource.TypeDef {
	return &TypeDef{}
}

func (obj *sourceObject) MakeDeepCopy() {
	obj.Deployment = obj.Deployment.DeepCopy()
}

func (obj *sourceObject) GetRequiredProtectors(ctx context.Context) []resource.RequiredProtector {
	decisions := []string{}
	output := []resource.RequiredProtector{}

	defer func() {
		obj.observer.InterpretProtectors(ctx, observer.InterpretProtectors{
			Group:              obj.TypeDef().GroupVersionResource().Group,
			Version:            obj.TypeDef().GroupVersionResource().Version,
			Resource:           obj.TypeDef().GroupVersionResource().Resource,
			Kind:               obj.TypeDef().GroupVersionKind().Kind,
			Namespace:          obj.Deployment.Namespace,
			Name:               obj.Deployment.Name,
			RequiredProtectors: output,
			Decisions:          decisions,
		})
	}()

	if !obj.Deployment.DeletionTimestamp.IsZero() {
		avoidNonZero := false

		if *obj.options.avoidNonZeroDeletion {
			// inherited logic from deployment controller
			totalReplicas := ptr.Deref(obj.Deployment.Spec.Replicas, 1)

			if totalReplicas > 0 {
				avoidNonZero = true
			}
		}

		if !avoidNonZero {
			decisions = append(decisions, "Terminating")
			return nil
		}

		decisions = append(decisions, "TerminatingButNonZero")
	}

	if !(*obj.options.labelSelector).Matches(labels.Set(obj.Deployment.Labels)) {
		decisions = append(decisions, "LabelSelectorMismatch")
		return nil
	}

	decisions = append(decisions, "Normal")
	output = append(output, &defaultReqmt{obj: obj})

	return output
}

func (obj *sourceObject) Update(ctx context.Context, options metav1.UpdateOptions) error {
	newObj, err := obj.client.Deployments(obj.Deployment.Namespace).
		Update(ctx, obj.Deployment, options)
	if err != nil {
		return errors.TagWrapf("UpdateObject", err, "apiserver error for update request")
	}

	obj.Deployment = newObj

	return nil
}

type defaultReqmt struct {
	obj *sourceObject
}

func (reqmt *defaultReqmt) Name() string {
	return fmt.Sprintf("deployment-%s", reqmt.obj.Name)
}

func (reqmt *defaultReqmt) Spec() (_zero podseidonv1a1.PodProtectorSpec, _ error) {
	// inherited logic from deployment controller
	totalReplicas := ptr.Deref(reqmt.obj.Spec.Replicas, 1)

	maxUnavailableIs := intstr.FromString("25%")

	if strategy := reqmt.obj.Deployment.Spec.Strategy.RollingUpdate; strategy != nil {
		if maxUnavailablePtr := strategy.MaxUnavailable; maxUnavailablePtr != nil {
			maxUnavailableIs = *maxUnavailablePtr
		}
	}

	maxUnavailableInt, err := intstr.GetValueFromIntOrPercent(
		intstr.ValueOrDefault(&maxUnavailableIs, intstr.FromInt(0)),
		int(totalReplicas),
		true,
	)
	if err != nil {
		return _zero, errors.TagWrapf(
			"ParseMaxUnavailable",
			err,
			"parse max unavailable from deployment",
		)
	}

	if maxUnavailableInt > math.MaxInt32 {
		return _zero, errors.TagWrapf(
			"TooManyReplicas",
			err,
			"maxUnavailable overflows after resolution",
		)
	}

	// #nosec G115 -- overflow has been checked
	requiredReplicas := totalReplicas - int32(maxUnavailableInt)

	return podseidonv1a1.PodProtectorSpec{
		MinAvailable:    max(requiredReplicas, 0),
		MinReadySeconds: reqmt.obj.Spec.MinReadySeconds,
		Selector:        *reqmt.obj.Spec.Selector,
	}, nil
}
