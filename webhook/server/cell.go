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

package server

import (
	"context"
	"flag"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/util"
)

// Identifies the cell that a pod from a webhook request belongs to.
type CellResolver interface {
	Resolve(input CellResolveInput) (string, error)
}

type CellResolveInput struct {
	Path       string
	RemoteAddr string
	Headers    http.Header
	Review     *admissionv1.AdmissionRequest
}

const CellResolverMuxName = "webhook-cell-resolver"

var RequestCellResolver = component.ProvideMux[CellResolver](CellResolverMuxName, "how to resolve the cell from a pod webhook request")

var ProvidePathCellResolver = component.DeclareMuxImpl(
	CellResolverMuxName,
	func(util.Empty) string { return "path" },
	func(util.Empty, *flag.FlagSet) util.Empty { return util.Empty{} },
	func(util.Empty, *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, util.Empty, util.Empty, util.Empty) (*util.Empty, error) {
		return &util.Empty{}, nil
	},
	component.Lifecycle[util.Empty, util.Empty, util.Empty, util.Empty]{Start: nil, Join: nil, HealthChecks: nil},
	func(*component.Data[util.Empty, util.Empty, util.Empty, util.Empty]) CellResolver {
		return PathCellResolver{}
	},
)

type PathCellResolver struct{}

func (PathCellResolver) Resolve(input CellResolveInput) (string, error) {
	return input.Path, nil
}
