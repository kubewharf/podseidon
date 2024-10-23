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
	"context"
	"flag"
	"fmt"

	kubeinformers "k8s.io/client-go/informers"

	podseidoninformers "github.com/kubewharf/podseidon/client/informers/externalversions"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

// Request a shared informer factory corresponding to the specified cluster.
//
//revive:disable-next-line:function-length
func NewInformers[ClientT any, FactoryT InformerFactory](
	args InformersArgs[ClientT, FactoryT],
) component.Declared[FactoryT] {
	return component.Declare(
		func(args InformersArgs[ClientT, FactoryT]) string {
			return fmt.Sprintf(
				"%s-%s-%s-informers",
				args.ClusterName,
				args.Phase,
				args.InformerSetName,
			)
		},
		func(InformersArgs[ClientT, FactoryT], *flag.FlagSet) InformersOptions { return InformersOptions{} },
		func(args InformersArgs[ClientT, FactoryT], requests *component.DepRequests) InformersDeps[ClientT] {
			elector := optional.Map(
				args.Elector,
				func(elector ElectorArgs) component.Dep[*Elector] {
					return component.DepPtr(
						requests,
						NewElector(
							ElectorArgs{
								ClusterName: elector.ClusterName,
								ElectorName: elector.ElectorName,
							},
						),
					)
				},
			)

			return InformersDeps[ClientT]{
				client: component.DepPtr(
					requests,
					args.ClientDep(),
				),
				elector: elector,
			}
		},
		func(
			_ context.Context,
			args InformersArgs[ClientT, FactoryT],
			_ InformersOptions,
			deps InformersDeps[ClientT],
		) (*InformersState[FactoryT], error) {
			factory := args.InformerSetCtor(deps.client.Get())

			return &InformersState[FactoryT]{
				factory: factory,
			}, nil
		},
		component.Lifecycle[InformersArgs[ClientT, FactoryT], InformersOptions, InformersDeps[ClientT], InformersState[FactoryT]]{
			Start: func(
				ctx context.Context,
				_ *InformersArgs[ClientT, FactoryT],
				_ *InformersOptions,
				deps *InformersDeps[ClientT],
				state *InformersState[FactoryT],
			) error {
				// waiting for elector does not need to block startup detection
				go func() {
					if elector, hasElector := deps.elector.Get(); hasElector {
						leaderCtx, err := elector.Get().Await(ctx)
						if err != nil {
							// the error does not need to be handled
							return
						}

						ctx = leaderCtx
					}

					state.factory.Start(ctx.Done())
				}()

				return nil
			},
			Join: func(
				_ context.Context,
				_ *InformersArgs[ClientT, FactoryT],
				_ *InformersOptions,
				_ *InformersDeps[ClientT],
				state *InformersState[FactoryT],
			) error {
				state.factory.Shutdown()

				return nil
			},
			HealthChecks: nil, // There is no way to check HasSynced on all informers; do it in the user worker instead.
		},
		func(d *component.Data[InformersArgs[ClientT, FactoryT], InformersOptions, InformersDeps[ClientT], InformersState[FactoryT]]) FactoryT {
			return d.State.factory
		},
	)(
		args,
	)
}

type InformerPhase string

type InformerSetName string

type InformersArgs[ClientT any, FactoryT InformerFactory] struct {
	ClusterName ClusterName
	Phase       InformerPhase
	Elector     optional.Optional[ElectorArgs]

	ClientDep       func() component.Declared[ClientT]
	InformerSetName InformerSetName
	InformerSetCtor func(ClientT) FactoryT
}

// Pass to `NewInformers` to request a shared informer factory for native types corresponding to the specified cluster.
func NativeInformers(
	clusterName ClusterName,
	phase InformerPhase,
	elector optional.Optional[ElectorArgs],
) InformersArgs[*Client, kubeinformers.SharedInformerFactory] {
	return InformersArgs[*Client, kubeinformers.SharedInformerFactory]{
		ClusterName: clusterName,
		Phase:       phase,
		Elector:     elector,
		ClientDep: func() component.Declared[*Client] {
			return NewClient(ClientArgs{ClusterName: clusterName})
		},
		InformerSetName: "native",
		InformerSetCtor: func(client *Client) kubeinformers.SharedInformerFactory {
			return kubeinformers.NewSharedInformerFactory(client.NativeClientSet(), 0)
		},
	}
}

// Pass to `NewInformers` to request a shared informer factory for Podseidon types corresponding to the specified cluster.
func PodseidonInformers(
	clusterName ClusterName,
	phase InformerPhase,
	elector optional.Optional[ElectorArgs],
) InformersArgs[*Client, podseidoninformers.SharedInformerFactory] {
	return InformersArgs[*Client, podseidoninformers.SharedInformerFactory]{
		ClusterName: clusterName,
		Phase:       phase,
		Elector:     elector,
		ClientDep: func() component.Declared[*Client] {
			return NewClient(ClientArgs{ClusterName: clusterName})
		},
		InformerSetName: "podseidon",
		InformerSetCtor: func(client *Client) podseidoninformers.SharedInformerFactory {
			return podseidoninformers.NewSharedInformerFactory(client.PodseidonClientSet(), 0)
		},
	}
}

type InformersOptions struct{}

type InformersDeps[ClientT any] struct {
	client  component.Dep[ClientT]
	elector optional.Optional[component.Dep[*Elector]]
}

type InformersState[FactoryT InformerFactory] struct {
	factory FactoryT
}

type InformerFactory interface {
	Start(stopCh <-chan util.Empty)
	Shutdown()
}
