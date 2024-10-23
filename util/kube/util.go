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

package kube

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

func GenericEventHandler(handler func(nsName types.NamespacedName)) cache.ResourceEventHandler {
	anyHandler := func(obj any) {
		if del, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = del.Obj
		}

		if accessor, err := meta.Accessor(obj); err == nil {
			handler(
				types.NamespacedName{Namespace: accessor.GetNamespace(), Name: accessor.GetName()},
			)
		}
	}

	return cache.ResourceEventHandlerFuncs{
		AddFunc:    anyHandler,
		UpdateFunc: func(_, newObj any) { anyHandler(newObj) },
		DeleteFunc: anyHandler,
	}
}

func GenericEventHandlerWithStaleState[InformerType any](
	handler func(obj InformerType, stillPresent bool),
) cache.ResourceEventHandler {
	anyHandler := func(stillPresent bool) func(any) {
		return func(obj any) {
			if del, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = del.Obj
			}

			handler(obj.(InformerType), stillPresent)
		}
	}

	return cache.ResourceEventHandlerFuncs{
		AddFunc:    anyHandler(true),
		UpdateFunc: func(_, newObj any) { anyHandler(true)(newObj) },
		DeleteFunc: anyHandler(false),
	}
}
