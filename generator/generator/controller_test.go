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

package generator_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	podseidon "github.com/kubewharf/podseidon/apis"
	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
	podseidonfakeclient "github.com/kubewharf/podseidon/client/clientset/versioned/fake"
	podseidonv1a1informers "github.com/kubewharf/podseidon/client/informers/externalversions/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/util"
	"github.com/kubewharf/podseidon/util/worker"

	"github.com/kubewharf/podseidon/generator/generator"
	"github.com/kubewharf/podseidon/generator/observer"
	"github.com/kubewharf/podseidon/generator/resource"
)

const (
	testNamespace = "test-namespace"
	testObjName   = "test-name"
	testPprName   = "pp-for-" + testObjName
)

var testPprLabels = map[string]string{
	podseidon.SourceObjectGroupLabel:    (&typeDef{}).GroupVersionKind().Group,
	podseidon.SourceObjectKindLabel:     (&typeDef{}).GroupVersionKind().Kind,
	podseidon.SourceObjectResourceLabel: (&typeDef{}).GroupVersionResource().Resource,
	podseidon.SourceObjectNameLabel:     testObjName,
}

func TestReconcileNoCreate(t *testing.T) {
	t.Parallel()

	testReconcile(t, testReconcileArgs{
		srcObjects: []*testObject{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      testObjName,
				},
				protect: false,
				spec:    podseidonv1a1.PodProtectorSpec{MinAvailable: 10},
			},
		},
		pprs: []*podseidonv1a1.PodProtector{},
		expectSrcs: map[types.NamespacedName]checkSrc{
			{Namespace: testNamespace, Name: testObjName}: func(t *testing.T, src *testObject) {
				assert.NotContains(t, src.Finalizers, podseidon.GeneratorFinalizer)
			},
		},
		expectPprs: map[types.NamespacedName]checkPpr{
			{Namespace: testNamespace, Name: testPprName}: func(t *testing.T, ppr *podseidonv1a1.PodProtector) {
				assert.Nil(t, ppr)
			},
		},
		expectActions:               []observer.Action{},
		expectRemoveSourceFinalizer: false,
	})
}

func TestReconcileRemoveFinalizer(t *testing.T) {
	t.Parallel()

	testReconcile(t, testReconcileArgs{
		srcObjects: []*testObject{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  testNamespace,
					Name:       testObjName,
					Finalizers: []string{podseidon.GeneratorFinalizer},
				},
				protect: false,
				spec:    podseidonv1a1.PodProtectorSpec{MinAvailable: 10},
			},
		},
		pprs: []*podseidonv1a1.PodProtector{},
		expectSrcs: map[types.NamespacedName]checkSrc{
			{Namespace: testNamespace, Name: testObjName}: func(t *testing.T, src *testObject) {
				assert.NotContains(t, src.Finalizers, podseidon.GeneratorFinalizer)
			},
		},
		expectPprs: map[types.NamespacedName]checkPpr{
			{Namespace: testNamespace, Name: testPprName}: func(t *testing.T, ppr *podseidonv1a1.PodProtector) {
				assert.Nil(t, ppr)
			},
		},
		expectActions:               []observer.Action{},
		expectRemoveSourceFinalizer: true,
	})
}

func TestReconcileCreate(t *testing.T) {
	t.Parallel()

	testReconcile(t, testReconcileArgs{
		srcObjects: []*testObject{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      testObjName,
				},
				protect: true,
				spec:    podseidonv1a1.PodProtectorSpec{MinAvailable: 10},
			},
		},
		pprs: []*podseidonv1a1.PodProtector{},
		expectSrcs: map[types.NamespacedName]checkSrc{
			{Namespace: testNamespace, Name: testObjName}: func(t *testing.T, src *testObject) {
				assert.Contains(t, src.Finalizers, podseidon.GeneratorFinalizer)
			},
		},
		expectPprs: map[types.NamespacedName]checkPpr{
			{Namespace: testNamespace, Name: testPprName}: func(t *testing.T, ppr *podseidonv1a1.PodProtector) {
				if assert.NotNil(t, ppr) {
					assert.Contains(t, ppr.Finalizers, podseidon.GeneratorFinalizer)
					assert.Equal(t, int32(10), ppr.Spec.MinAvailable)
				}
			},
		},
		expectActions:               []observer.Action{observer.ActionCreatingProtector},
		expectRemoveSourceFinalizer: false,
	})
}

func TestReconcileCreateTerminating(t *testing.T) {
	t.Parallel()

	testReconcile(t, testReconcileArgs{
		srcObjects: []*testObject{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         testNamespace,
					Name:              testObjName,
					DeletionTimestamp: &metav1.Time{Time: time.Now().Add(-time.Minute)},
					Finalizers:        []string{"example.com/not-managed-by-podseidon"},
				},
				protect: true,
				spec:    podseidonv1a1.PodProtectorSpec{MinAvailable: 10},
			},
		},
		pprs: []*podseidonv1a1.PodProtector{},
		expectSrcs: map[types.NamespacedName]checkSrc{
			{Namespace: testNamespace, Name: testObjName}: func(t *testing.T, src *testObject) {
				assert.NotContains(t, src.Finalizers, podseidon.GeneratorFinalizer)
			},
		},
		expectPprs: map[types.NamespacedName]checkPpr{
			{Namespace: testNamespace, Name: testPprName}: func(t *testing.T, ppr *podseidonv1a1.PodProtector) {
				assert.Nil(t, ppr)
			},
		},
		expectActions:               []observer.Action{observer.ActionCreatingProtector},
		expectRemoveSourceFinalizer: false,
	})
}

func TestReconcileUpdate(t *testing.T) {
	t.Parallel()

	testReconcile(t, testReconcileArgs{
		srcObjects: []*testObject{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  testNamespace,
					Name:       testObjName,
					Finalizers: []string{podseidon.GeneratorFinalizer},
				},
				protect: true,
				spec:    podseidonv1a1.PodProtectorSpec{MinAvailable: 10},
			},
		},
		pprs: []*podseidonv1a1.PodProtector{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  testNamespace,
					Name:       testPprName,
					Labels:     testPprLabels,
					Finalizers: []string{podseidon.GeneratorFinalizer},
				},
				Spec: podseidonv1a1.PodProtectorSpec{
					MinAvailable: 20,
				},
			},
		},
		expectSrcs: map[types.NamespacedName]checkSrc{
			{Namespace: testNamespace, Name: testObjName}: func(t *testing.T, src *testObject) {
				assert.Contains(t, src.Finalizers, podseidon.GeneratorFinalizer)
			},
		},
		expectPprs: map[types.NamespacedName]checkPpr{
			{Namespace: testNamespace, Name: testPprName}: func(t *testing.T, ppr *podseidonv1a1.PodProtector) {
				if assert.NotNil(t, ppr) {
					assert.Contains(t, ppr.Finalizers, podseidon.GeneratorFinalizer)
					assert.Equal(t, int32(10), ppr.Spec.MinAvailable)
				}
			},
		},
		expectActions:               []observer.Action{observer.ActionSyncProtector},
		expectRemoveSourceFinalizer: false,
	})
}

func TestReconcileDelete(t *testing.T) {
	t.Parallel()

	testReconcile(t, testReconcileArgs{
		srcObjects: []*testObject{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  testNamespace,
					Name:       testObjName,
					Finalizers: []string{podseidon.GeneratorFinalizer},
				},
				protect: false,
				spec:    podseidonv1a1.PodProtectorSpec{MinAvailable: 10},
			},
		},
		pprs: []*podseidonv1a1.PodProtector{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  testNamespace,
					Name:       testPprName,
					Labels:     testPprLabels,
					Finalizers: []string{podseidon.GeneratorFinalizer},
				},
				Spec: podseidonv1a1.PodProtectorSpec{
					MinAvailable: 10,
				},
			},
		},
		expectSrcs: map[types.NamespacedName]checkSrc{
			{Namespace: testNamespace, Name: testObjName}: func(t *testing.T, src *testObject) {
				assert.NotContains(t, src.Finalizers, podseidon.GeneratorFinalizer)
			},
		},
		expectPprs: map[types.NamespacedName]checkPpr{
			{Namespace: testNamespace, Name: testPprName}: func(t *testing.T, ppr *podseidonv1a1.PodProtector) {
				assert.Nil(t, ppr)
			},
		},
		expectActions:               []observer.Action{observer.ActionDeleteProtector},
		expectRemoveSourceFinalizer: true,
	})
}

func TestReconcileDisappeared(t *testing.T) {
	t.Parallel()

	testReconcile(t, testReconcileArgs{
		srcObjects: []*testObject{},
		pprs: []*podseidonv1a1.PodProtector{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  testNamespace,
					Name:       testPprName,
					Labels:     testPprLabels,
					Finalizers: []string{podseidon.GeneratorFinalizer},
				},
				Spec: podseidonv1a1.PodProtectorSpec{
					MinAvailable: 10,
				},
			},
		},
		expectSrcs: map[types.NamespacedName]checkSrc{
			{Namespace: testNamespace, Name: testObjName}: func(t *testing.T, src *testObject) {
				assert.Nil(t, src)
			},
		},
		expectPprs: map[types.NamespacedName]checkPpr{
			{Namespace: testNamespace, Name: testPprName}: func(t *testing.T, ppr *podseidonv1a1.PodProtector) {
				// do not delete ppr even if src disappeared, since there is no explicit acknowledgement of deletion
				if assert.NotNil(t, ppr) {
					assert.True(t, ppr.DeletionTimestamp.IsZero())
				}
			},
		},
		expectActions:               []observer.Action{observer.ActionDanglingProtector},
		expectRemoveSourceFinalizer: false,
	})
}

type (
	checkPpr func(*testing.T, *podseidonv1a1.PodProtector)
	checkSrc func(*testing.T, *testObject)
)

type testReconcileArgs struct {
	srcObjects []*testObject
	pprs       []*podseidonv1a1.PodProtector

	expectSrcs                  map[types.NamespacedName]checkSrc
	expectPprs                  map[types.NamespacedName]checkPpr
	expectActions               []observer.Action
	expectRemoveSourceFinalizer bool
}

func testReconcile(
	t *testing.T,
	args testReconcileArgs,
) {
	t.Helper()

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	clientSet := podseidonfakeclient.NewSimpleClientset(util.MapSlice(
		args.pprs,
		func(obj *podseidonv1a1.PodProtector) runtime.Object { return obj },
	)...)
	pprClient := clientSet.PodseidonV1alpha1()

	pprInformer := podseidonv1a1informers.NewPodProtectorInformer(
		clientSet,
		metav1.NamespaceAll,
		0,
		cache.Indexers{
			generator.GroupKindNamespaceNameIndexName: generator.GroupKindNamespaceNameIndexFunc,
		},
	)
	go pprInformer.Run(ctx.Done())

	synced := cache.WaitForCacheSync(ctx.Done(), pprInformer.HasSynced)
	require.True(t, synced)

	srcStore := util.SliceToMap(
		args.srcObjects,
		func(obj *testObject) types.NamespacedName {
			return types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		},
	)

	reconcileActions := []observer.Action{}
	hasCleanSourceFinalizer := false

	err := generator.ReconcileItem(
		ctx,
		[]resource.TypeProvider{&typeProvider{
			typeDef: typeDef{},
			store:   srcStore,
		}},
		//nolint:exhaustruct // other fields are filled by ReflectPopulate
		o11y.ReflectPopulate(observer.Observer{
			EndReconcile: func(_ context.Context, arg observer.EndReconcile) {
				require.NoError(t, arg.Err)
				reconcileActions = append(reconcileActions, arg.Action)
			},
			CleanSourceFinalizer: func(ctx context.Context, _ observer.StartReconcile) (context.Context, context.CancelFunc) {
				assert.False(t, hasCleanSourceFinalizer, "clean source finalizer multiple times")
				hasCleanSourceFinalizer = true
				return ctx, util.NoOp
			},
		}),
		pprClient,
		pprInformer.GetIndexer(),
		generator.QueueKey{
			NsName: types.NamespacedName{
				Namespace: testNamespace,
				Name:      testObjName,
			},
			TypeDefIndex: 0,
		},
	)
	require.NoError(t, err)

	assert.Equal(t, args.expectActions, reconcileActions)

	for nsName, check := range args.expectSrcs {
		actual := srcStore[nsName]
		check(t, actual)
	}

	for nsName, check := range args.expectPprs {
		actual, err := pprClient.PodProtectors(nsName.Namespace).
			Get(ctx, nsName.Name, metav1.GetOptions{})
		require.True(
			t,
			err == nil || apierrors.IsNotFound(err),
			"unexpected error: %T %v",
			err,
			err,
		)

		if err != nil {
			check(t, nil)
		} else {
			check(t, actual)
		}
	}
}

type typeDef struct{}

type typeProvider struct {
	typeDef
	store map[types.NamespacedName]*testObject
}

func (*typeDef) GroupVersionResource() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "test", Version: "v1", Resource: "testobjects"}
}

func (*typeDef) GroupVersionKind() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "test", Version: "v1", Kind: "TestObject"}
}

func (ty *typeProvider) GetObject(_ context.Context, nsName types.NamespacedName) resource.SourceObject {
	obj := ty.store[nsName]
	if obj != nil {
		obj.store = ty.store

		return obj
	}

	return resource.SourceObject(nil) // make sure not to return (*testObject)(nil)
}

func (*typeDef) AddEventHandler(func(types.NamespacedName)) error {
	return nil
}

func (*typeDef) AddPrereqs(map[string]worker.Prereq) {}

type testObject struct {
	metav1.ObjectMeta
	protect bool
	spec    podseidonv1a1.PodProtectorSpec

	store map[types.NamespacedName]*testObject
}

func (obj *testObject) DeepCopyObject() runtime.Object {
	return &testObject{
		ObjectMeta: *obj.ObjectMeta.DeepCopy(),
		protect:    obj.protect,
		spec:       *obj.spec.DeepCopy(),
		store:      obj.store,
	}
}

func (*testObject) GetObjectKind() schema.ObjectKind {
	return &metav1.TypeMeta{
		APIVersion: (&typeDef{}).GroupVersionKind().GroupVersion().String(),
		Kind:       (&typeDef{}).GroupVersionKind().Kind,
	}
}

func (obj *testObject) MakeDeepCopy() {
	obj.ObjectMeta = *obj.ObjectMeta.DeepCopy()
}

func (*testObject) TypeDef() resource.TypeDef {
	return &typeDef{}
}

func (obj *testObject) GetRequiredProtectors(_ context.Context) []resource.RequiredProtector {
	if obj.protect {
		return []resource.RequiredProtector{&requiredProtector{obj: obj}}
	}

	return nil
}

type requiredProtector struct {
	obj *testObject
}

func (obj *testObject) Update(context.Context, metav1.UpdateOptions) error {
	obj.store[types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}] = obj

	return nil
}

func (reqmt *requiredProtector) Spec() (podseidonv1a1.PodProtectorSpec, error) {
	return reqmt.obj.spec, nil
}

func (reqmt *requiredProtector) Name() string {
	return fmt.Sprintf("pp-for-%s", reqmt.obj.ObjectMeta.Name)
}
