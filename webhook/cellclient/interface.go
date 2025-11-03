// Copyright 2025 The Podseidon Authors.
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

package cellclient

import (
	"context"
	"flag"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	utilflag "github.com/kubewharf/podseidon/util/flag"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/util"
)

const ProviderMuxName = "webhook-worker-client-provider"

var Request = component.ProvideMux[Provider](
	ProviderMuxName,
	"provider for worker cluster client, used for pod lookup during eviction requests",
)

var DefaultImpls = component.RequireDeps(
	ProvideNone(util.Empty{}, true),
	ProvideSingle(util.Empty{}, false),
	ProvideMap(util.Empty{}, false),
)

type Provider interface {
	Provide(cellId string) (kubernetes.Interface, error)
}

// Provide no worker clients at all,
// suitable for webhooks that do not support eviction.
var ProvideNone = component.DeclareMuxImpl(
	ProviderMuxName,
	func(util.Empty) string { return "none" },
	func(_ util.Empty, _ *flag.FlagSet) util.Empty { return util.Empty{} },
	func(_ util.Empty, _ *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, util.Empty, util.Empty, util.Empty) (*util.Empty, error) {
		return &util.Empty{}, nil
	},
	component.Lifecycle[util.Empty, util.Empty, util.Empty, util.Empty]{Start: nil, Join: nil, HealthChecks: nil},
	func(_ *component.Data[util.Empty, util.Empty, util.Empty, util.Empty]) Provider {
		return NoneProvider{}
	},
)

type NoneProvider struct{}

func (NoneProvider) Provide(_ string) (kubernetes.Interface, error) {
	return nil, errors.TagErrorf("NoneProvider", "--webhook-worker-client-provider is set to \"none\"")
}

var ProvideSingle = component.DeclareMuxImpl(
	ProviderMuxName,
	func(util.Empty) string { return "single" },
	func(_ util.Empty, _ *flag.FlagSet) util.Empty { return util.Empty{} },
	func(_ util.Empty, reqs *component.DepRequests) SingleDeps {
		return SingleDeps{
			WorkerClient: component.DepPtr(reqs, kube.NewClient(kube.ClientArgs{
				ClusterName: "webhook-worker-client-provider.single",
			})),
		}
	},
	func(context.Context, util.Empty, util.Empty, SingleDeps) (*util.Empty, error) {
		return &util.Empty{}, nil
	},
	component.Lifecycle[util.Empty, util.Empty, SingleDeps, util.Empty]{Start: nil, Join: nil, HealthChecks: nil},
	func(d *component.Data[util.Empty, util.Empty, SingleDeps, util.Empty]) Provider {
		return SingleProvider{client: d.Deps.WorkerClient.Get()}
	},
)

type SingleDeps struct {
	WorkerClient component.Dep[*kube.Client]
}

type SingleProvider struct {
	client *kube.Client
}

func (provider SingleProvider) Provide(_ string) (kubernetes.Interface, error) {
	return provider.client.NativeClientSet(), nil
}

var ProvideMap = component.DeclareMuxImpl(
	ProviderMuxName,
	func(util.Empty) string { return "map" },
	func(_ util.Empty, fs *flag.FlagSet) MapOptions { return newMapOptions(fs) },
	func(_ util.Empty, _ *component.DepRequests) util.Empty { return util.Empty{} },
	func(_ context.Context, _ util.Empty, options MapOptions, _ util.Empty) (*MapProvider, error) {
		clients := map[string]kubernetes.Interface{}

		for cellId := range *options.KubeconfigPaths {
			client, err := options.buildClient(cellId)
			if err != nil {
				return nil, errors.TagWrapf("buildClient", err, "build client for cell %q", cellId)
			}

			clients[cellId] = client
		}

		return &MapProvider{clients: clients}, nil
	},
	component.Lifecycle[util.Empty, MapOptions, util.Empty, MapProvider]{Start: nil, Join: nil, HealthChecks: nil},
	func(d *component.Data[util.Empty, MapOptions, util.Empty, MapProvider]) Provider {
		return d.State
	},
)

type MapOptions struct {
	Global          kube.GlobalClientOptions
	KubeconfigPaths *map[string]string
	MasterUrls      *map[string]string
}

func newMapOptions(fs *flag.FlagSet) MapOptions {
	return MapOptions{
		Global: kube.NewGlobalClientOptions(fs),
		KubeconfigPaths: utilflag.Map(
			fs,
			"configs",
			nil,
			"each key represents one supported cell, where the value is either a local path to kubeconfig or empty string",
			utilflag.StringParser,
			utilflag.StringParser,
		),
		MasterUrls: utilflag.Map(
			fs,
			"masters",
			nil,
			"overrides the master URL of configs if specified for a key",
			utilflag.StringParser,
			utilflag.StringParser,
		),
	}
}

func (options MapOptions) buildClient(cellId string) (kubernetes.Interface, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags(
		(*options.MasterUrls)[cellId],
		(*options.KubeconfigPaths)[cellId],
	)
	if err != nil {
		return nil, errors.TagWrapf("BuildConfig", err, "build rest config from arguments")
	}

	options.Global.PatchRestConfig(restConfig)

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, errors.TagWrapf("NewForConfig", err, "build kubernetes client from rest config")
	}

	return client, nil
}

type MapProvider struct {
	clients map[string]kubernetes.Interface
}

func (provider *MapProvider) Provide(cellId string) (kubernetes.Interface, error) {
	client, hasCell := provider.clients[cellId]
	if !hasCell {
		return nil, errors.TagErrorf("UnknownCell", "unknown cell %q", cellId)
	}

	return client, nil
}
