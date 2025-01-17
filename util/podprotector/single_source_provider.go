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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/kube"
)

var RequireSingleSourceProvider = component.DeclareMuxImpl(
	SourceProviderMuxName,
	func(args SingleSourceProviderArgs) string { return fmt.Sprintf("%s-only", args.ClusterName) },
	func(SingleSourceProviderArgs, *flag.FlagSet) SingleSourceProviderOptions {
		return SingleSourceProviderOptions{}
	},
	func(args SingleSourceProviderArgs, reqs *component.DepRequests) SingleSourceProviderDeps {
		return SingleSourceProviderDeps{
			client: component.DepPtr(reqs, kube.NewClient(kube.ClientArgs{
				ClusterName: args.ClusterName,
			})),
		}
	},
	func(
		_ context.Context,
		_ SingleSourceProviderArgs,
		_ SingleSourceProviderOptions,
		_ SingleSourceProviderDeps,
	) (*singleSourceProviderState, error) {
		return &singleSourceProviderState{}, nil
	},
	component.Lifecycle[SingleSourceProviderArgs, SingleSourceProviderOptions, SingleSourceProviderDeps, singleSourceProviderState]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(
		d *component.Data[SingleSourceProviderArgs, SingleSourceProviderOptions, SingleSourceProviderDeps, singleSourceProviderState],
	) SourceProvider {
		return singleSourceProvider{client: d.Deps.client.Get()}
	},
)

type SingleSourceProviderArgs struct {
	ClusterName kube.ClusterName
}

type SingleSourceProviderOptions struct{}

type SingleSourceProviderDeps struct {
	client component.Dep[*kube.Client]
}

type singleSourceProviderState struct{}

type singleSourceProvider struct {
	client *kube.Client
}

func (singleSourceProvider) IsSourceIdentifying() bool { return false }

func (provider singleSourceProvider) Watch(_ context.Context) <-chan map[SourceName]SourceDesc {
	ch := make(chan map[SourceName]SourceDesc, 1)
	ch <- map[SourceName]SourceDesc{
		"": {
			PodseidonClient: provider.client.PodseidonClientSet(),
		},
	}

	return ch
}

func (provider singleSourceProvider) UpdateStatus(ctx context.Context, _ SourceName, ppr *podseidonv1a1.PodProtector) error {
	_, err := provider.client.PodseidonClientSet().
		PodseidonV1alpha1().
		PodProtectors(ppr.Namespace).
		UpdateStatus(ctx, ppr, metav1.UpdateOptions{})
	if err != nil {
		return errors.TagWrapf("ClientUpdateStatus", err, "call update status on client")
	}

	return nil
}
