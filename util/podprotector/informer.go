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

package pprutil

import (
	"context"
	"flag"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
	podseidoninformers "github.com/kubewharf/podseidon/client/informers/externalversions"
	podseidonv1a1informers "github.com/kubewharf/podseidon/client/informers/externalversions/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/labelindex"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/podprotector/observer"
	"github.com/kubewharf/podseidon/util/util"
)

func NewIndexedInformer(
	args IndexedInformerArgs,
) component.Declared[IndexedInformer] {
	return component.Declare(
		func(args IndexedInformerArgs) string {
			return fmt.Sprintf("ppr-indexed-informer-%s-%s", args.ClusterName, args.InformerPhase)
		},
		func(_ IndexedInformerArgs, _ *flag.FlagSet) IndexedInformerOptions {
			return IndexedInformerOptions{}
		},
		func(args IndexedInformerArgs, requests *component.DepRequests) IndexedInformerDeps {
			return IndexedInformerDeps{
				coreInformers: component.DepPtr(
					requests,
					kube.NewInformers(
						kube.PodseidonInformers(
							args.ClusterName,
							args.InformerPhase,
							args.Elector,
						),
					),
				),
				elector: optional.Map(
					args.Elector,
					func(elector kube.ElectorArgs) component.Dep[*kube.Elector] {
						return component.DepPtr(
							requests,
							kube.NewElector(
								kube.ElectorArgs{
									ClusterName: elector.ClusterName,
									ElectorName: elector.ElectorName,
								},
							),
						)
					},
				),
				observer: o11y.Request[observer.IndexedInformerObserver](requests),
			}
		},
		func(
			ctx context.Context,
			_ IndexedInformerArgs,
			_ IndexedInformerOptions,
			deps IndexedInformerDeps,
		) (*IndexedInformerState, error) {
			informer := deps.coreInformers.Get().Podseidon().V1alpha1().PodProtectors()
			selectorIndex := NewSelectorIndex()

			postHandlers := new([]func(types.NamespacedName))

			if err := SetupPprInformer(
				ctx,
				informer,
				func(nsName types.NamespacedName) {
					for _, handler := range *postHandlers {
						handler(nsName)
					}
				},
				selectorIndex,
				func(ctx context.Context, nsName types.NamespacedName) (context.Context, context.CancelFunc) {
					return deps.observer.Get().StartHandleEvent(ctx, nsName)
				},
				func(ctx context.Context) {
					deps.observer.Get().EndHandleEvent(ctx, util.Empty{})
				},
				func(ctx context.Context, nsName types.NamespacedName, err error) {
					deps.observer.Get().HandleEventError(ctx, observer.HandleEventError{Namespace: nsName.Namespace, Name: nsName.Name, Err: err})
				},
			); err != nil {
				return nil, errors.TagWrapf("SetupPprInformer", err, "start PodProtector informer")
			}

			return &IndexedInformerState{
				informer: informer,
				informerStarted: func() bool {
					if elector, hasElector := deps.elector.Get(); hasElector {
						return elector.Get().HasElected()
					}

					return true
				},
				selectorIndex: selectorIndex,
				postHandlers:  postHandlers,
			}, nil
		},
		component.Lifecycle[IndexedInformerArgs, IndexedInformerOptions, IndexedInformerDeps, IndexedInformerState]{
			Start: nil,
			Join:  nil,
			HealthChecks: func(state *IndexedInformerState) component.HealthChecks {
				return component.HealthChecks{
					"informer-synced": func() error {
						if state.informerStarted() && !state.informer.Informer().HasSynced() {
							return errors.TagErrorf("InformerNotSynced", "informer not synced yet")
						}

						return nil
					},
				}
			},
		},
		func(
			d *component.Data[IndexedInformerArgs, IndexedInformerOptions, IndexedInformerDeps, IndexedInformerState],
		) IndexedInformer {
			return IndexedInformer{state: d.State}
		},
	)(
		args,
	)
}

type IndexedInformerArgs struct {
	ClusterName   kube.ClusterName
	InformerPhase kube.InformerPhase
	Elector       optional.Optional[kube.ElectorArgs]
}

type IndexedInformerOptions struct{}

type IndexedInformerDeps struct {
	observer      component.Dep[observer.IndexedInformerObserver]
	coreInformers component.Dep[podseidoninformers.SharedInformerFactory]
	elector       optional.Optional[component.Dep[*kube.Elector]]
}

type IndexedInformerState struct {
	informer        podseidonv1a1informers.PodProtectorInformer
	informerStarted func() bool
	selectorIndex   SelectorIndex
	postHandlers    *[]func(types.NamespacedName)
}

type SelectorIndex = *labelindex.Locked[
	types.NamespacedName, metav1.LabelSelector, labelindex.NamespacedQuery[map[string]string], error, util.Empty,
	*labelindex.Namespaced[
		metav1.LabelSelector, map[string]string, error, util.Empty,
		*labelindex.Selectors[string],
	],
]

func NewSelectorIndex() SelectorIndex {
	return labelindex.NewLocked(
		labelindex.NewNamespaced(
			labelindex.NewSelectors[string],
			labelindex.EmptyErrAdapter{},
		),
		labelindex.EmptyErrAdapter{},
	)
}

func SetupPprInformer(
	ctx context.Context,
	pprInformer podseidonv1a1informers.PodProtectorInformer,
	postHandler func(types.NamespacedName),
	selectorIndex SelectorIndex,
	observeStartEnqueue o11y.ObserveScopeFunc[types.NamespacedName],
	observeEndEnqueue func(context.Context),
	observeEnqueueError func(context.Context, types.NamespacedName, error),
) error {
	handler := func(ppr *podseidonv1a1.PodProtector, stillPresent bool) {
		nsName := types.NamespacedName{Namespace: ppr.Namespace, Name: ppr.Name}

		ctx, cancelFunc := observeStartEnqueue(ctx, nsName)
		defer cancelFunc()

		defer observeEndEnqueue(ctx)

		if stillPresent {
			if err := selectorIndex.Track(nsName, GetAggregationSelector(ppr)); err != nil {
				observeEnqueueError(ctx, nsName, err)
			}
		} else {
			selectorIndex.Untrack(nsName)
		}

		postHandler(nsName)
	}

	_, err := pprInformer.
		Informer().
		AddEventHandler(kube.GenericEventHandlerWithStaleState(handler))
	if err != nil {
		return errors.TagWrapf(
			"AddPodProtectorEventHandler",
			err,
			"add event handler to PodProtector informer",
		)
	}

	return nil
}

type IndexedInformer struct {
	state *IndexedInformerState
}

func (ii IndexedInformer) AddPostHandler(handler func(types.NamespacedName)) {
	*ii.state.postHandlers = append(*ii.state.postHandlers, handler)
}

func (ii IndexedInformer) Get(
	nsName types.NamespacedName,
) (optional.Optional[*podseidonv1a1.PodProtector], error) {
	ppr, err := ii.state.informer.Lister().PodProtectors(nsName.Namespace).Get(nsName.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return optional.None[*podseidonv1a1.PodProtector](), nil
		}

		return optional.None[*podseidonv1a1.PodProtector](), errors.TagWrapf(
			"GetListerPpr",
			err,
			"get ppr from lister",
		)
	}

	return optional.Some(ppr), nil
}

func (ii IndexedInformer) Query(namespace string, labels map[string]string) []types.NamespacedName {
	namesIter, _ := ii.state.selectorIndex.Query(labelindex.NamespacedQuery[map[string]string]{
		Namespace: namespace,
		Query:     labels,
	})

	return namesIter.CollectSlice() // to unlock selectorIndex and reduce contention
}

func (ii IndexedInformer) HasSynced() bool {
	return ii.state.informer.Informer().HasSynced()
}
