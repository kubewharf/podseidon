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

package o11yklog

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"k8s.io/klog/v2"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/util"
)

func RequestKlogArgs(requests *component.DepRequests) {
	component.DepPtr(requests, component.Declare(
		func(util.Empty) string { return "klog" },
		func(_ util.Empty, fs *flag.FlagSet) util.Empty {
			klog.InitFlags(fs)

			fs.VisitAll(func(f *flag.Flag) {
				f.Name = strings.ReplaceAll(f.Name, "_", "-")
			})

			return util.Empty{}
		},
		func(util.Empty, *component.DepRequests) util.Empty { return util.Empty{} },
		func(context.Context, util.Empty, util.Empty, util.Empty) (*util.Empty, error) {
			return &util.Empty{}, nil
		},
		component.Lifecycle[util.Empty, util.Empty, util.Empty, util.Empty]{
			Start:        nil,
			Join:         nil,
			HealthChecks: nil,
		},
		func(*component.Data[util.Empty, util.Empty, util.Empty, util.Empty]) util.Empty { return util.Empty{} },
	)(
		util.Empty{},
	))
}

func ErrTagKvs(err error) []any {
	errTags := []any{}

	for i, tag := range errors.GetTags(err) {
		errTags = append(errTags, fmt.Sprintf("errorTag%d", i), tag)
	}

	return errTags
}
