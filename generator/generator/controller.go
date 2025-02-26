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

package generator

import (
	"context"
	"flag"
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	podseidon "github.com/kubewharf/podseidon/apis"
	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
	podseidonv1a1client "github.com/kubewharf/podseidon/client/clientset/versioned/typed/apis/v1alpha1"
	podseidoninformers "github.com/kubewharf/podseidon/client/informers/externalversions"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
	"github.com/kubewharf/podseidon/util/worker"

	"github.com/kubewharf/podseidon/generator/constants"
	"github.com/kubewharf/podseidon/generator/observer"
	"github.com/kubewharf/podseidon/generator/resource"
)

// Name of the [GroupKindNamespaceNameIndexFunc] index.
const GroupKindNamespaceNameIndexName = "podseidon-generator/group-kind-namespace-name"

// Index function to extract PodProtectors relevant to an object from an informer.
func GroupKindNamespaceNameIndexFunc(obj any) ([]string, error) {
	objectMeta, err := meta.Accessor(obj)
	if err != nil {
		return []string{""}, errors.TagWrapf("GetMetaForIndex", err, "object has no meta")
	}

	return []string{fmt.Sprintf("%s/%s/%s/%s",
		objectMeta.GetLabels()[podseidon.SourceObjectGroupLabel],
		objectMeta.GetLabels()[podseidon.SourceObjectKindLabel],
		objectMeta.GetNamespace(),
		objectMeta.GetLabels()[podseidon.SourceObjectNameLabel],
	)}, nil
}

var NewController = component.Declare(
	func(ControllerArgs) string { return "generator" },
	func(ControllerArgs, *flag.FlagSet) ControllerOptions {
		return ControllerOptions{}
	},
	func(args ControllerArgs, requests *component.DepRequests) ControllerDeps {
		typeProviders := make([]component.Dep[resource.TypeProvider], len(args.Types))
		for i, ty := range args.Types {
			typeProviders[i] = component.DepPtr(requests, ty)
		}

		return ControllerDeps{
			cluster: component.DepPtr(requests, kube.NewClient(kube.ClientArgs{
				ClusterName: constants.CoreClusterName,
			})),
			elector: component.DepPtr(requests, kube.NewElector(constants.GeneratorElectorArgs)),
			podseidonInformers: component.DepPtr(requests, kube.NewInformers(kube.PodseidonInformers(
				constants.CoreClusterName,
				constants.LeaderPhase,
				optional.Some(constants.GeneratorElectorArgs),
			))),
			observer: o11y.Request[observer.Observer](requests),
			worker: component.DepPtr(requests, worker.New[QueueKey](
				"generator",
				clock.RealClock{},
			)),
			types: typeProviders,
		}
	},
	func(_ context.Context, _ ControllerArgs, _ ControllerOptions, deps ControllerDeps) (*ControllerState, error) {
		queue := deps.worker.Get()

		pprInformer := deps.podseidonInformers.Get().Factory.Podseidon().V1alpha1().PodProtectors()

		if err := pprInformer.Informer().AddIndexers(cache.Indexers{
			GroupKindNamespaceNameIndexName: GroupKindNamespaceNameIndexFunc,
		}); err != nil {
			return nil, errors.TagWrapf("AddIndexers", err, "add pod indexer to ppr informer")
		}

		prereqs := map[string]worker.Prereq{
			"ppr-informer-synced": worker.InformerPrereq(pprInformer.Informer()),
		}
		for _, ty := range deps.types {
			ty.Get().AddPrereqs(prereqs)
		}

		queue.SetExecutor(
			func(ctx context.Context, item QueueKey) error {
				return ReconcileItem(
					ctx,
					util.MapSlice(deps.types, component.Dep[resource.TypeProvider].Get),
					deps.observer.Get(),
					deps.cluster.Get().PodseidonClientSet().PodseidonV1alpha1(),
					pprInformer.Informer().GetIndexer(),
					item,
				)
			},
			prereqs,
		)
		queue.SetBeforeStart(deps.elector.Get().Await)

		for index, ty := range deps.types {
			err := ty.Get().AddEventHandler(func(name types.NamespacedName) {
				deps.worker.Get().Enqueue(QueueKey{
					NsName:       name,
					TypeDefIndex: index,
				})
			})
			if err != nil {
				return nil, errors.TagWrapf(
					"AddPprEventHandler",
					err,
					"add event handler to PodProtector informer",
				)
			}
		}

		return &ControllerState{}, nil
	},
	component.Lifecycle[ControllerArgs, ControllerOptions, ControllerDeps, ControllerState]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(*component.Data[ControllerArgs, ControllerOptions, ControllerDeps, ControllerState]) util.Empty {
		return util.Empty{}
	},
)

type ControllerArgs struct {
	Types []component.Declared[resource.TypeProvider]
}

type ControllerOptions struct{}

type ControllerDeps struct {
	cluster            component.Dep[*kube.Client]
	elector            component.Dep[*kube.Elector]
	podseidonInformers component.Dep[kube.Informers[podseidoninformers.SharedInformerFactory]]
	observer           component.Dep[observer.Observer]
	worker             component.Dep[worker.Api[QueueKey]]

	types []component.Dep[resource.TypeProvider]
}

type ControllerState struct{}

type QueueKey struct {
	NsName       types.NamespacedName
	TypeDefIndex int
}

func ReconcileItem(
	ctx context.Context,
	typeProviders []resource.TypeProvider,
	obs observer.Observer,
	pprClient podseidonv1a1client.PodseidonV1alpha1Interface,
	pprIndex cache.Indexer,
	key QueueKey,
) error {
	ty := typeProviders[key.TypeDefIndex]

	sourceObject := ty.GetObject(ctx, key.NsName)

	var reqmts map[string]resource.RequiredProtector
	if sourceObject != nil {
		reqmts = util.SliceToMap(
			sourceObject.GetRequiredProtectors(ctx),
			resource.RequiredProtector.Name,
		)
	}

	relevantObjectsAny, err := pprIndex.ByIndex(
		GroupKindNamespaceNameIndexName,
		fmt.Sprintf("%s/%s/%s/%s",
			ty.GroupVersionKind().Group,
			ty.GroupVersionKind().Kind,
			key.NsName.Namespace,
			key.NsName.Name,
		),
	)
	if err != nil {
		return errors.TagWrapf(
			"ListPprFromIndexer",
			err,
			"find relevant PodProtector objects from indexer",
		)
	}

	relevantObjects := iter.CollectMap(
		iter.Map(
			iter.FromSlice(relevantObjectsAny),
			func(obj any) iter.Pair[string, *podseidonv1a1.PodProtector] {
				ppr := obj.(*podseidonv1a1.PodProtector)
				return iter.NewPair(ppr.Name, ppr)
			},
		),
	)

	startReconcile := observer.StartReconcile{
		Group:     ty.GroupVersionResource().Group,
		Version:   ty.GroupVersionResource().Version,
		Resource:  ty.GroupVersionResource().Resource,
		Kind:      ty.GroupVersionKind().Kind,
		Namespace: key.NsName.Namespace,
		Name:      key.NsName.Name,
	}

	needSourceFinalizer := false

	err = iter.FromMap2(
		reqmts,
		relevantObjects,
	).TryForEach(
		func(pair iter.Pair[string, iter.Pair[
			optional.Optional[resource.RequiredProtector],
			optional.Optional[*podseidonv1a1.PodProtector],
		]],
		) error {
			reconcileCtx, cancelFunc := obs.StartReconcile(ctx, startReconcile)
			defer cancelFunc()

			reqmt := pair.Right.Left
			currentPpr := pair.Right.Right

			action, err := tryReconcile(
				reconcileCtx,
				obs,
				pprClient,
				sourceObject,
				reqmt,
				currentPpr,
				&needSourceFinalizer,
			)
			if err != nil {
				action = observer.ActionError
			}

			reqmtName := optional.Map(reqmt, resource.RequiredProtector.Name).
				OrFn(func() optional.Optional[string] {
					return optional.Map(currentPpr, (*podseidonv1a1.PodProtector).GetName)
				}).
				GetOrZero()

			obs.EndReconcile(reconcileCtx, observer.EndReconcile{
				PprName: reqmtName,
				Action:  action,
				Err:     err,
			})

			return err
		},
	)
	if err != nil {
		return err
	}

	if !needSourceFinalizer && sourceObject != nil {
		if err := removeSourceFinalizer(ctx, obs, sourceObject, startReconcile); err != nil {
			return err
		}
	}

	return nil
}

//nolint:cyclop
//revive:disable-next-line:cyclomatic a single switch to match all combinations exhaustively and delegate out
func tryReconcile(
	ctx context.Context,
	obs observer.Observer,
	pprClient podseidonv1a1client.PodseidonV1alpha1Interface,
	sourceObject resource.SourceObject,
	reqmt optional.Optional[resource.RequiredProtector],
	currentPpr optional.Optional[*podseidonv1a1.PodProtector],
	needSourceFinalizer *bool,
) (observer.Action, error) {
	switch {
	case currentPpr.IsNone() && sourceObject == nil:
		// Neither object exists, nothing to process
		// Technically this case is unreachable since there is nothing to trigger FromMap2.
		return observer.ActionNeitherObjectExists, nil

	case currentPpr.IsNone() && sourceObject != nil && reqmt.IsSome():
		// Protector should be created
		reqmt := reqmt.MustGet("checked reqmt.IsSome() in case condition")

		ctx, cancelFunc := obs.CreateProtector(ctx, util.Empty{})
		defer cancelFunc()

		return observer.ActionCreatingProtector, createProtector(
			ctx,
			pprClient,
			sourceObject,
			reqmt,
			needSourceFinalizer,
		)

	case currentPpr.IsNone() && sourceObject != nil && reqmt.IsNone():
		// Protector does not exist and is not required
		// Technically this case is unreachable since there is nothing to trigger FromMap2.
		return observer.ActionNoPprNeeded, nil

	case currentPpr.IsSome() && sourceObject == nil:
		// We have a dangling protector object, possibly an error condition
		currentPpr := currentPpr.MustGet("checked currentPpr.IsSome() in case condition")

		obs.DanglingProtector(ctx, currentPpr)

		return observer.ActionDanglingProtector, nil

	case currentPpr.IsSome() && sourceObject != nil && reqmt.IsSome():
		// Try to sync the object spec
		currentPpr := currentPpr.MustGet("checked currentPpr.IsSome() in case condition")
		reqmt := reqmt.MustGet("checked reqmt.IsSome() in case condition")

		ctx, cancelFunc := obs.SyncProtector(ctx, currentPpr)
		defer cancelFunc()

		return observer.ActionSyncProtector, syncProtector(
			ctx,
			pprClient,
			sourceObject,
			currentPpr,
			reqmt,
			needSourceFinalizer,
		)

	case currentPpr.IsSome() && sourceObject != nil && reqmt.IsNone():
		// Protector should be deleted
		currentPpr := currentPpr.MustGet("checked currentPpr.IsSome() in case condition")

		ctx, cancelFunc := obs.DeleteProtector(ctx, currentPpr)
		defer cancelFunc()

		return observer.ActionDeleteProtector, deleteProtector(
			ctx,
			pprClient,
			currentPpr,
			needSourceFinalizer,
		)
	}

	panic("unreachable")
}

func ensureSourceFinalizer(ctx context.Context,
	sourceObject resource.SourceObject,
	needSourceFinalizer *bool,
) error {
	*needSourceFinalizer = true

	if util.FindInSlice(sourceObject.GetFinalizers(), podseidon.GeneratorFinalizer) != -1 {
		return nil
	}

	// Ensure source object is mutable.
	// It doesn't matter that this function is called multiple times
	// since multiple RequiredProtectors will reuse the same SourceObject.
	sourceObject.MakeDeepCopy()
	sourceObject.SetFinalizers(append(sourceObject.GetFinalizers(), podseidon.GeneratorFinalizer))

	if err := sourceObject.Update(ctx, metav1.UpdateOptions{}); err != nil {
		return errors.TagWrapf(
			"UpdateSourceFinalizer",
			err,
			"update source object with the generator finalizer",
		)
	}

	return nil
}

func createProtector(
	ctx context.Context,
	pprClient podseidonv1a1client.PodseidonV1alpha1Interface,
	sourceObject resource.SourceObject,
	pprReqmt resource.RequiredProtector,
	needSourceFinalizer *bool,
) error {
	if err := ensureSourceFinalizer(ctx, sourceObject, needSourceFinalizer); err != nil {
		return errors.TagWrapf("EnsureSourceFinalizer", err, "ensure finalizer in source object")
	}

	ppr, err := resource.GeneratePodProtector(sourceObject, pprReqmt)
	if err != nil {
		return errors.TagWrapf("GeneratePpr", err, "generating PodProtector from source object")
	}

	_, err = pprClient.PodProtectors(ppr.Namespace).
		Create(ctx, ppr, metav1.CreateOptions{})
	if err != nil {
		return errors.TagWrapf("CreateNewPpr", err, "create new PodProtector object")
	}

	return nil
}

func syncProtector(
	ctx context.Context,
	pprClient podseidonv1a1client.PodseidonV1alpha1Interface,
	sourceObject resource.SourceObject,
	current *podseidonv1a1.PodProtector,
	pprReqmt resource.RequiredProtector,
	needSourceFinalizer *bool,
) error {
	if err := ensureSourceFinalizer(ctx, sourceObject, needSourceFinalizer); err != nil {
		return errors.TagWrapf("EnsureSourceFinalizer", err, "ensure finalizer in source object")
	}

	current, hasChange, err := tryRemoveCells(ctx, pprClient, current)
	if err != nil {
		return errors.TagWrapf(
			"RemoveCells",
			err,
			"removing obsolete cells from PodProtector status",
		)
	}

	expected, err := resource.GeneratePodProtector(sourceObject, pprReqmt)
	if err != nil {
		return errors.TagWrapf("GeneratePpr", err, "generating PodProtector from source object")
	}

	if hasChange || !reflect.DeepEqual(current.Spec, expected.Spec) {
		next := current.DeepCopy()
		next.Spec = expected.Spec

		_, err := pprClient.
			PodProtectors(next.Namespace).
			Update(ctx, next, metav1.UpdateOptions{})
		if err != nil {
			return errors.TagWrapf("UpdatePprSpec", err, "update PodProtector object spec")
		}
	}

	return nil
}

func tryRemoveCells(
	ctx context.Context,
	pprClient podseidonv1a1client.PodseidonV1alpha1Interface,
	current *podseidonv1a1.PodProtector,
) (_current *podseidonv1a1.PodProtector, _hasNonStatusChange bool, _ error) {
	removals, hasRemovals := current.Annotations[podseidon.PprAnnotationRemoveCellOnce]
	if !hasRemovals {
		return current, false, nil
	}

	current = current.DeepCopy()

	removalSet := sets.New(strings.Split(removals, ",")...)

	newCellList := iter.FromSlice(current.Status.Cells).
		Filter(func(cellStatus podseidonv1a1.PodProtectorCellStatus) bool { return !removalSet.Has(cellStatus.CellId) }).
		CollectSlice()

	if len(current.Status.Cells) != len(newCellList) {
		current.Status.Cells = newCellList

		next, err := pprClient.PodProtectors(current.Namespace).
			UpdateStatus(ctx, current, metav1.UpdateOptions{})
		if err != nil {
			return nil, false, errors.TagWrapf("UpdateStatus", err, "performing UpdateStatus call")
		}

		current = next
	}

	delete(current.Annotations, podseidon.PprAnnotationRemoveCellOnce)

	return current, true, nil
}

func deleteProtector(
	ctx context.Context,
	pprClient podseidonv1a1client.PodseidonV1alpha1Interface,
	currentPpr *podseidonv1a1.PodProtector,
	needSourceFinalizer *bool,
) error {
	success := false
	defer func() {
		if !success {
			*needSourceFinalizer = true
		}
	}()

	toDelete := util.FindInSlice(currentPpr.Finalizers, podseidon.GeneratorFinalizer)
	if toDelete != -1 {
		next := currentPpr.DeepCopy()
		util.SwapRemove(&next.Finalizers, toDelete)

		newPpr, err := pprClient.
			PodProtectors(next.Namespace).
			Update(ctx, next, metav1.UpdateOptions{})
		if err != nil {
			return errors.TagWrapf(
				"RemovePprFinalizer",
				err,
				"remove generator finalizer from PodProtector object",
			)
		}

		currentPpr = newPpr
	}

	if currentPpr.GetDeletionTimestamp().IsZero() {
		err := pprClient.PodProtectors(currentPpr.Namespace).
			Delete(ctx, currentPpr.Name, metav1.DeleteOptions{})
		if err != nil {
			return errors.TagWrapf("DeletePprObject", err, "delete ppr object")
		}
	}

	// We can remove the source finalizer as long as the PodProtector is marked for deletion.
	// No need to block the source object to wait for the PodProtector deletion..
	success = true

	return nil
}

func removeSourceFinalizer(
	ctx context.Context,
	obs observer.Observer,
	sourceObject resource.SourceObject,
	startReconcile observer.StartReconcile,
) error {
	toDelete := util.FindInSlice(sourceObject.GetFinalizers(), podseidon.GeneratorFinalizer)
	if toDelete == -1 {
		return nil
	}

	ctx, cancelFunc := obs.CleanSourceFinalizer(ctx, startReconcile)
	defer cancelFunc()

	sourceObject.MakeDeepCopy()

	finalizers := sourceObject.GetFinalizers()
	util.SwapRemove(&finalizers, toDelete)
	sourceObject.SetFinalizers(finalizers)

	if err := sourceObject.Update(ctx, metav1.UpdateOptions{}); err != nil {
		return errors.TagWrapf(
			"RemoveSourceFinalizer",
			err,
			"remove generator finalizer from source object",
		)
	}

	return nil
}
