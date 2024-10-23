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

package resource

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	podseidon "github.com/kubewharf/podseidon/apis"
	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/worker"
)

type TypeDef interface {
	GroupVersionResource() schema.GroupVersionResource
	GroupVersionKind() schema.GroupVersionKind
}

type TypeProvider interface {
	TypeDef

	// Retrieves the named object from informer cache.
	GetObject(ctx context.Context, nsName types.NamespacedName) SourceObject

	// The underlying informer can push reconcile events to the handler.
	//
	// The NamespacedName is the name of the underlying workload.
	AddEventHandler(handler func(types.NamespacedName)) error

	// Adds prerequisite conditions (e.g. informer sync) before the worker can start running.
	AddPrereqs(prereqs map[string]worker.Prereq)
}

// Performs type-agnostic operations on an object.
type SourceObject interface {
	metav1.Object

	// Mutates the receiver, setting the wrapped object to a new deep clone.
	//
	// By default, the source object shares object reference with the informer cache.
	// MakeDeepCopy must be called before mutating the data in `Object`.
	MakeDeepCopy()

	// Back-references the type definition that provided this object.
	TypeDef() TypeDef

	// Computes all protectors required for this object.
	GetRequiredProtectors(ctx context.Context) []RequiredProtector

	// Writes the local state of the object to apiserver.
	//
	// If the operation is successful, the local copy in the receiver is updated to the new version on the apiserver,
	// including the new resource version, generation, updated fields from webhooks, etc.
	// Thus, SourceObject may be more updated than the informer.
	Update(ctx context.Context, options metav1.UpdateOptions) error
}

type RequiredProtector interface {
	Name() string
	Spec() (podseidonv1a1.PodProtectorSpec, error)
}

func GeneratePodProtector(
	sourceObject SourceObject,
	rqmt RequiredProtector,
) (*podseidonv1a1.PodProtector, error) {
	spec, err := rqmt.Spec()
	if err != nil {
		return nil, errors.TagWrapf("ReplicaSpec", err, "inferring replica spec from source object")
	}

	return &podseidonv1a1.PodProtector{
		TypeMeta: metav1.TypeMeta{
			APIVersion: podseidonv1a1.SchemeGroupVersion.String(),
			Kind:       podseidonv1a1.PodProtectorKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rqmt.Name(),
			Namespace: sourceObject.GetNamespace(),
			Labels: map[string]string{
				podseidon.SourceObjectNameLabel: sourceObject.GetName(),
				podseidon.SourceObjectKindLabel: sourceObject.TypeDef().GroupVersionKind().Kind,
				podseidon.SourceObjectResourceLabel: sourceObject.TypeDef().
					GroupVersionResource().Resource,
				podseidon.SourceObjectGroupLabel: sourceObject.TypeDef().
					GroupVersionResource().Group,
			},
			Finalizers: []string{
				podseidon.GeneratorFinalizer,
			},
			OwnerReferences: []metav1.OwnerReference{
				// We add an OwnerReference from the source object,
				// but the finalizer will protect the PodProtector object from direct deletion
				// without checking for the source object first.
				// Aggregator and webhook should continue processing requests from finalizing objects.
				{
					APIVersion: sourceObject.TypeDef().GroupVersionKind().GroupVersion().String(),
					Kind:       sourceObject.TypeDef().GroupVersionKind().Kind,
					Name:       sourceObject.GetName(),
					UID:        sourceObject.GetUID(),
					Controller: ptr.To(true),
				},
			},
		},
		Spec: spec,
	}, nil
}
