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

package podseidon

const (
	SourceObjectNameLabel     = "podseidon.kubewharf.io/source-name"
	SourceObjectGroupLabel    = "podseidon.kubewharf.io/source-group"
	SourceObjectKindLabel     = "podseidon.kubewharf.io/source-kind"
	SourceObjectResourceLabel = "podseidon.kubewharf.io/source-resource"
)

// The finalizer applied on both the workload and the PodProtector object.
//
// The finalizer on a workload ensures graceful deletion of the PodProtector object
// such that PodProtector is only safely deleted when
// generator witnesses an explicit non-zero deletionTimestamp on the workload object.
// Removal of this finalizer from the workload may result in a dangling PodProtector object.
//
// The finalizer on a PodProtector object prevents accidentally voiding protection
// due to GC controller or other manual operations deleting the PodProtector
// when the workload object is not explicitly deleted.
// Removal of this finalizer from a PodProtector object indicates an explicit intention
// to declare that a dangling PodProtector shall no longer be maintained.
const GeneratorFinalizer = "podseidon.kubewharf.io/generator"

// A convenience hack to remove the entries for cells that are no longer online.
//
// The value of this annotation is a comma-separated list of cell names.
//
// This is mostly useful when a cell is deleted and no aggregator instances are running.
// This removal is handled by the generator leader when it reconciles the object.
const PprAnnotationRemoveCellOnce = "podseidon.kubewharf.io/remove-cell-once"

// Annotates pods that are never rejected.
//
// Podseidon webhook still handles its deletion requests and reports metrics normally,
// but the admission review response is always positive.
// This has the same effect as enabling `--webhook-dry-run=true`,
// but only affects the pod that contains this annotation.
//
// This annotation takes effect as long as it exists under .metadata.annotations of a pod;
// regardless of the annotation value.
// It is RECOMMENDED that the annotation is a PascalCase string
// documenting why this pod should be exempted from protection.
// Annotation values starting with `{` are reserved for future extension;
// handling such pods in the current version of Podseidon webhook results in unspecified behavior.
const PodAnnotationForceDelete = "podseidon.kubewharf.io/force-delete"

const (
	// Indicates that the request went through a webhook dry-run.
	AuditAnnotationDryRun = "dry-run"
	// Indicates the PodProtector object that denied the request.
	AuditAnnotationRejectByPpr = "reject-by-podprotector"
)
