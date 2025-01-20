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
	"sync/atomic"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
	podseidonclient "github.com/kubewharf/podseidon/client/clientset/versioned"
	podseidoninformers "github.com/kubewharf/podseidon/client/informers/externalversions"
	podseidonv1a1listers "github.com/kubewharf/podseidon/client/listers/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/labelindex"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/podprotector/observer"
	"github.com/kubewharf/podseidon/util/util"
)

// Multiplexes to a dynamic number of source clusters for PodProtector lookup.
var NewIndexedInformer = component.Declare[IndexedInformerArgs, informerOptions, informerDeps, informerState, IndexedInformer](
	func(args IndexedInformerArgs) string {
		return fmt.Sprintf("podprotector-indexed-informer-%s", args.Suffix)
	},
	func(_ IndexedInformerArgs, _ *flag.FlagSet) informerOptions {
		return informerOptions{}
	},
	func(args IndexedInformerArgs, requests *component.DepRequests) informerDeps {
		return informerDeps{
			observer:       o11y.Request[observer.IndexedInformerObserver](requests),
			sourceProvider: component.DepPtr(requests, RequestSourceProvider()),
			elector: optional.Map(args.Elector, func(electorArgs kube.ElectorArgs) component.Dep[*kube.Elector] {
				return component.DepPtr(requests, kube.NewElector(electorArgs))
			}),
		}
	},
	func(_ context.Context, _ IndexedInformerArgs, _ informerOptions, deps informerDeps) (*informerState, error) {
		return &informerState{
			started:       atomic.Bool{},
			isSourceIdent: deps.sourceProvider.Get().IsSourceIdentifying(),
			sources:       atomic.Pointer[map[SourceName]*sourceState]{},
			postHandlers:  nil,
		}, nil
	},
	component.Lifecycle[IndexedInformerArgs, informerOptions, informerDeps, informerState]{
		Start: func(
			ctx context.Context,
			_ *IndexedInformerArgs,
			_ *informerOptions,
			deps *informerDeps,
			state *informerState,
		) error {
			go func() {
				if elector, hasElector := deps.elector.Get(); hasElector {
					leaderCtx, err := elector.Get().Await(ctx)
					if err != nil {
						return
					}

					ctx = leaderCtx
				}

				state.started.Store(true)

				updateCh := deps.sourceProvider.Get().Watch(ctx)

				for {
					select {
					case initialMap := <-updateCh:
						state.updateSourceList(ctx, initialMap, deps.observer.Get())
					case <-ctx.Done():
						return
					}
				}
			}()

			return nil
		},
		Join: nil,
		HealthChecks: func(state *informerState) component.HealthChecks {
			return component.HealthChecks{
				"informers-synced": func() error {
					if !state.started.Load() {
						return nil // report as healthy if leader is not elected
					}

					if !state.HasSynced() {
						return errors.TagErrorf("InformersNotSynced", "some informers have not synced yet")
					}

					return nil
				},
			}
		},
	},
	func(d *component.Data[IndexedInformerArgs, informerOptions, informerDeps, informerState]) IndexedInformer {
		return d.State
	},
)

type IndexedInformerArgs struct {
	// Suffix of the component.
	Suffix string

	Elector optional.Optional[kube.ElectorArgs]
}

type informerOptions struct{}

type informerDeps struct {
	observer       component.Dep[observer.IndexedInformerObserver]
	sourceProvider component.Dep[SourceProvider]
	elector        optional.Optional[component.Dep[*kube.Elector]]
}

const SourceProviderMuxName = "podprotector-source-provider"

var RequestSourceProvider = component.ProvideMux[SourceProvider](SourceProviderMuxName, "provider for clusters hosting PodProtectors")

// Provides a dynamic list of source clusters to watch.
type SourceProvider interface {
	// If this returns true, objects with the same (namespace, name) from different sources are considered different.
	//
	// If this returns false, objects with the same (namespace, name) from different sources are considered homogeneous.
	// When informers from multiple sources yield the same object, only the object with the newest creation timestamp is used.
	// Callers of `IndexedInformer` will receive `SourceName = ""`, and the `sourceName` passed to `UpdateStatus` will also be `""`.
	IsSourceIdentifying() bool

	// Watches for source cluster list changes.
	//
	// Returns a channel that gets sent to when a new list of source clusters is available.
	// Each item in the channel invalidates the previous map.
	//
	// A SourceName -> SourceDesc mapping is considered immutable;
	// if a SourceName may refer to a different SourceDesc in the future,
	// consider appending the SourceDesc version to the name.
	//
	// The aggregate indexed informer is only considered synced when both conditions are met:
	// 1. The Watch() channel has transferred at least one map.
	// 2. The informers of all sources in the latest transferred map from the channel are simultaneously synced.
	Watch(ctx context.Context) <-chan map[SourceName]SourceDesc

	// Updates the status of a PodProtector object as received from `sourceName`.
	UpdateStatus(ctx context.Context, sourceName SourceName, ppr *podseidonv1a1.PodProtector) error
}

// Identifies a source for PodProtector.
type SourceName string

// Describes how to access a source cluster.
type SourceDesc struct {
	// A client to access the PodProtectors in the cluster.
	//
	// Only List and Watch methods of the interface are used.
	// Updates will go through [SourceProvider.UpdateStatus] instead.
	PodseidonClient podseidonclient.Interface
}

type informerState struct {
	started atomic.Bool

	isSourceIdent bool
	sources       atomic.Pointer[map[SourceName]*sourceState]
	postHandlers  []func(PodProtectorKey)
}

type sourceState struct {
	lister        podseidonv1a1listers.PodProtectorLister
	hasSynced     cache.InformerSynced
	selectorIndex SelectorIndex
	cancelFunc    context.CancelFunc
}

func (state *informerState) updateSourceList(
	ctx context.Context,
	cellList map[SourceName]SourceDesc,
	obs observer.IndexedInformerObserver,
) {
	if err := state.tryUpdateSourceList(ctx, cellList, obs); err != nil {
		obs.UpdateSourceListError(ctx, observer.UpdateSourceListError{Err: err})
	}
}

func (state *informerState) tryUpdateSourceList(
	ctx context.Context,
	newDescs map[SourceName]SourceDesc,
	obs observer.IndexedInformerObserver,
) error {
	prevMapPtr := state.sources.Load()
	prevMap := ptr.Deref(prevMapPtr, nil)

	nextMap := map[SourceName]*sourceState{}
	uncommitted := []context.CancelFunc{}

	defer func() {
		for _, cancelFunc := range uncommitted {
			cancelFunc()
		}
	}()

	for name, desc := range newDescs {
		sourceState, hasName := prevMap[name]
		if !hasName {
			newSourceState, err := startSourceInformer(ctx, obs, name, desc, state.postHandlers)
			if err != nil {
				return err
			}

			sourceState = newSourceState
			uncommitted = append(uncommitted, newSourceState.cancelFunc)
		}

		nextMap[name] = sourceState
	}

	additions := len(uncommitted)

	{
		// atomically uncommit prevMap and nextMap committed
		// thus, the uncommitted list changes from (nextMap - prevMap) to (prevMap - nextMap).
		swapped := state.sources.CompareAndSwap(prevMapPtr, ptr.To(nextMap))

		if !swapped {
			panic("unexpected informer map pointer write from another goroutine")
		}

		uncommitted = nil

		for name, item := range prevMap {
			if _, stillHasName := nextMap[name]; !stillHasName {
				uncommitted = append(uncommitted, item.cancelFunc)
			}
		}
	}

	removals := len(uncommitted)

	obs.UpdateSourceList(ctx, observer.UpdateSourceList{
		NewLength: len(nextMap),
		Additions: additions,
		Removals:  removals,
	})

	return nil
}

func startSourceInformer(
	ctx context.Context,
	obs observer.IndexedInformerObserver,
	sourceName SourceName,
	desc SourceDesc,
	postHandlers []func(PodProtectorKey),
) (*sourceState, error) {
	informerFactory := podseidoninformers.NewSharedInformerFactory(desc.PodseidonClient, 0)
	pprInformer := informerFactory.Podseidon().V1alpha1().PodProtectors()
	selectorIndex := NewSelectorIndex()

	ctx, cancelFunc := context.WithCancel(ctx)
	informerStarted := false

	defer func() {
		if !informerStarted {
			cancelFunc()
		}
	}()

	if err := SetupPprInformer(
		ctx, pprInformer,
		func(nn types.NamespacedName) {
			for _, postHandler := range postHandlers {
				postHandler(PodProtectorKey{
					SourceName:     sourceName,
					NamespacedName: nn,
				})
			}
		},
		selectorIndex,
		func(ctx context.Context, nsName types.NamespacedName) (context.Context, context.CancelFunc) {
			return obs.StartHandleEvent(ctx, nsName)
		},
		func(ctx context.Context) {
			obs.EndHandleEvent(ctx, util.Empty{})
		},
		func(ctx context.Context, nsName types.NamespacedName, err error) {
			obs.HandleEventError(
				ctx,
				observer.HandleEventError{Namespace: nsName.Namespace, Name: nsName.Name, Err: err},
			)
		},
	); err != nil {
		return nil, errors.TagWrapf("SetupPprInformer", err, "create ppr informer for cell")
	}

	{
		informerFactory.Start(ctx.Done())

		informerStarted = true
	}

	return &sourceState{
		lister:        pprInformer.Lister(),
		hasSynced:     pprInformer.Informer().HasSynced,
		selectorIndex: selectorIndex,
		cancelFunc:    cancelFunc,
	}, nil
}

func (state *informerState) AddPostHandler(handler func(PodProtectorKey)) {
	state.postHandlers = append(state.postHandlers, handler)
}

func (state *informerState) HasSynced() bool {
	sources := state.sources.Load()
	if sources == nil {
		return false // no source list yet
	}

	for _, cell := range *sources {
		if !cell.hasSynced() {
			return false
		}
	}

	return true
}

func (state *informerState) Get(key PodProtectorKey) (optional.Optional[*podseidonv1a1.PodProtector], error) {
	// If there are multiple versions from different cells, select the version with the latest creationTimestamp
	out := optional.None[*podseidonv1a1.PodProtector]()

	sources := ptr.Deref(state.sources.Load(), nil)

	if state.isSourceIdent {
		sources = iter.CollectMap(iter.FromMap(sources).Filter(
			func(p iter.Pair[SourceName, *sourceState]) bool { return p.Left == key.SourceName },
		))
	}

	for _, cellState := range sources {
		ppr, err := cellState.lister.PodProtectors(key.Namespace).Get(key.Name)
		if err != nil && !apierrors.IsNotFound(err) {
			return out, errors.TagWrapf("GetListerPpr", err, "get ppr from lister")
		}

		if err == nil && ppr != nil {
			out.SetOrChoose(ppr, func(pp1, pp2 *podseidonv1a1.PodProtector) bool {
				return pp2.CreationTimestamp.Time.After(pp1.CreationTimestamp.Time)
			})
		}
	}

	return out, nil
}

func (state *informerState) Query(namespace string, labels map[string]string) []PodProtectorKey {
	out := sets.New[PodProtectorKey]()

	for sourceName, cellState := range ptr.Deref(state.sources.Load(), nil) {
		nameIter, _ := cellState.selectorIndex.Query(labelindex.NamespacedQuery[map[string]string]{
			Namespace: namespace,
			Query:     labels,
		})
		nameIter(func(nn types.NamespacedName) iter.Flow {
			key := PodProtectorKey{
				SourceName:     "",
				NamespacedName: nn,
			}

			if state.isSourceIdent {
				key.SourceName = sourceName
			}

			out.Insert(key)

			return iter.Continue
		})
	}

	return out.UnsortedList()
}
