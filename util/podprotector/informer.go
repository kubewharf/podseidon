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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
	podseidonv1a1informers "github.com/kubewharf/podseidon/client/informers/externalversions/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/labelindex"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

type PodProtectorKey struct {
	SourceName SourceName
	types.NamespacedName
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

type IndexedInformer interface {
	// Register a function that gets called when a PodProtector has been received,
	// after the index has been updated for the object.
	//
	// The index may be updated again by another event by the time the function is called.
	// There is no guarantee on the thread safety of `handler`.
	AddPostHandler(handler func(PodProtectorKey))

	// Whether the informer state represents some consistent snapshot of the cluster.
	HasSynced() bool

	// Gets a PodProtector by name if it exists.
	Get(nsName PodProtectorKey) (optional.Optional[*podseidonv1a1.PodProtector], error)

	// Queries for PodProtector under the namespace matching the label selector.
	Query(namespace string, labels map[string]string) []PodProtectorKey
}
