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

package defaultconfig

import (
	"context"
	"flag"
	"time"

	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	utilflag "github.com/kubewharf/podseidon/util/flag"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

var New = component.Declare(
	func(util.Empty) string { return "default-admission-history-config" },
	func(_ util.Empty, fs *flag.FlagSet) Options {
		return Options{
			MaxConcurrentLag: utilflag.Int32(
				fs,
				"max-concurrent-lag",
				0,
				"maximum sum of AdmissionCount.Counter at any point, 0 for unrestricted",
			),
			CompactThreshold: utilflag.Int32(
				fs,
				"compact-threshold",
				100,
				"number of admission events to retain before compating to a single bucket",
			),
			AggregationRate: fs.Duration(
				"aggregation-rate",
				time.Second*5,
				"Delay period between receiving pod event and triggered aggregation",
			),
		}
	},
	func(util.Empty, *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, util.Empty, Options, util.Empty) (*util.Empty, error) { return &util.Empty{}, nil },
	component.Lifecycle[util.Empty, Options, util.Empty, util.Empty]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(d *component.Data[util.Empty, Options, util.Empty, util.Empty]) *Options {
		return &d.Options
	},
)

type Options struct {
	MaxConcurrentLag *int32
	CompactThreshold *int32
	AggregationRate  *time.Duration
}

type Computed struct {
	MaxConcurrentLag int32
	CompactThreshold int32
	AggregationRate  time.Duration
}

func (options *Options) Compute(
	optionalConfig optional.Optional[podseidonv1a1.AdmissionHistoryConfig],
) Computed {
	config := optionalConfig.GetOrZero() // zero value has nil (*int32)s

	return Computed{
		MaxConcurrentLag: ptr.Deref(config.MaxConcurrentLag, *options.MaxConcurrentLag),
		CompactThreshold: ptr.Deref(config.CompactThreshold, *options.CompactThreshold),
		AggregationRate: optional.Map(optional.FromPtr(config.AggregationRateMillis), milliDuration).
			GetOr(*options.AggregationRate),
	}
}

func milliDuration(i int32) time.Duration {
	return time.Duration(i) * time.Millisecond
}

// Compute the config with the default setup with native parameters, used for testing only.
func MustComputeDefaultSetup(config podseidonv1a1.AdmissionHistoryConfig) Computed {
	comp, api := New(util.Empty{}).GetNew()

	fs := new(flag.FlagSet)
	comp.AddFlags(fs)

	if err := fs.Parse(nil); err != nil {
		panic(err)
	}

	return api().Compute(optional.Some(config))
}
