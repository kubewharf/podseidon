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
	"os"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

// Api type for `NewElector`.
type Elector struct {
	electedCh       <-chan util.Empty
	electionErr     *error
	electionCloseCh *<-chan util.Empty
}

// Request an event recorder for objects in the specified cluster.
var NewElector = component.Declare(
	func(args ElectorArgs) string { return fmt.Sprintf("%s-leader-elector", args.ElectorName) },
	func(args ElectorArgs, fs *flag.FlagSet) ElectorOptions {
		return ElectorOptions{
			Enabled: fs.Bool(
				"enable",
				true,
				fmt.Sprintf("whether to enable %s leader election", args.ElectorName),
			),
			Namespace: fs.String(
				"namespace",
				metav1.NamespaceDefault,
				fmt.Sprintf("namespace for the lease object for %s", args.ElectorName),
			),
			Name: fs.String(
				"name",
				fmt.Sprintf("podseidon-%s", args.ElectorName),
				fmt.Sprintf("name of the lease object for %s", args.ElectorName),
			),
			LeaseDuration: fs.Duration(
				"lease-duration",
				time.Second*15,
				"leader election lease duration",
			),
			RenewDeadline: fs.Duration(
				"renew-deadline",
				time.Second*10,
				"leader election renew deadline",
			),
			RetryPeriod: fs.Duration(
				"retry-period", time.Second*2, "leader election retry period",
			),
		}
	},
	func(args ElectorArgs, requests *component.DepRequests) ElectorDeps {
		return ElectorDeps{
			KubeConfig: component.DepPtr(
				requests,
				NewClient(ClientArgs{ClusterName: args.ClusterName}),
			),
			EventRecorder: component.DepPtr(requests, NewEventRecorder(EventRecorderArgs{
				ClusterName: args.ClusterName,
				Component:   fmt.Sprintf("%s-leader-election", args.ElectorName),
			})),
			Observer: o11y.Request[ElectorObserver](requests),
		}
	},
	func(_ context.Context, _ ElectorArgs, options ElectorOptions, _ ElectorDeps) (*ElectorState, error) {
		electorReaderOpt := optional.None[util.LateInitReader[*leaderelection.LeaderElector]]()
		electorWriterOpt := optional.None[util.LateInitWriter[*leaderelection.LeaderElector]]()
		if *options.Enabled {
			electorReader, electorWriter := util.NewLateInit[*leaderelection.LeaderElector]()
			electorReaderOpt = optional.Some(electorReader)
			electorWriterOpt = optional.Some(electorWriter)
		}

		return &ElectorState{
			electedCh:         make(chan util.Empty),
			electorReader:     electorReaderOpt,
			electorWriter:     electorWriterOpt,
			electionErr:       nil,
			electionCloseCh:   nil,
			cancelElectorFunc: atomic.Pointer[context.CancelFunc]{},
		}, nil
	},
	component.Lifecycle[ElectorArgs, ElectorOptions, ElectorDeps, ElectorState]{
		Start: runElector,
		Join: func(_ context.Context, _ *ElectorArgs, _ *ElectorOptions, _ *ElectorDeps, state *ElectorState) error {
			cancelFunc := state.cancelElectorFunc.Load()
			if cancelFunc != nil {
				(*cancelFunc)()
			}

			return nil
		},
		HealthChecks: func(state *ElectorState) component.HealthChecks {
			return component.HealthChecks{
				"elector": optional.Map(state.electorReader, func(reader util.LateInitReader[*leaderelection.LeaderElector]) func() error {
					return func() error {
						if err := reader.Get().Check(0); err != nil {
							return errors.Tag("ElectorTimeout", err)
						}

						return nil
					}
				}).
					GetOrZero(),
			}
		},
	},
	func(d *component.Data[ElectorArgs, ElectorOptions, ElectorDeps, ElectorState]) *Elector {
		return &Elector{
			electedCh:       d.State.electedCh,
			electionErr:     &d.State.electionErr,
			electionCloseCh: &d.State.electionCloseCh,
		}
	},
)

type ElectorName string

type ElectorArgs struct {
	ElectorName ElectorName
	ClusterName ClusterName
}

type ElectorOptions struct {
	Enabled       *bool
	Namespace     *string
	Name          *string
	LeaseDuration *time.Duration
	RenewDeadline *time.Duration
	RetryPeriod   *time.Duration
}

type ElectorDeps struct {
	KubeConfig    component.Dep[*Client]
	EventRecorder component.Dep[record.EventRecorder]
	Observer      component.Dep[ElectorObserver]
}

type ElectorState struct {
	// A channel closed when the leader lease is acquired or fails.
	electedCh chan util.Empty

	electorReader optional.Optional[util.LateInitReader[*leaderelection.LeaderElector]]
	electorWriter optional.Optional[util.LateInitWriter[*leaderelection.LeaderElector]]

	// The error from leader election.
	// Exclusively locked by the Start goroutine until electedCh is closed.
	electionErr error
	// A channel closed when the leader lease is lost.
	// Exclusively locked by the Start goroutine until electedCh is closed.
	electionCloseCh <-chan util.Empty

	// A function that is atomically assigned right before elector starts.
	cancelElectorFunc atomic.Pointer[context.CancelFunc]
}

func runElector(
	ctx context.Context,
	args *ElectorArgs,
	options *ElectorOptions,
	deps *ElectorDeps,
	state *ElectorState,
) error {
	if !*options.Enabled {
		close(state.electedCh)
		return nil
	}

	electorWriter := state.electorWriter.MustGet("elector writer exists iff options.Enabled")

	wroteElector := false
	defer func() {
		if !wroteElector {
			electorWriter(nil)
		}
	}()

	hostname, err := os.Hostname()
	if err != nil {
		return errors.TagWrapf("GetHostname", err, "get hostname")
	}

	identity := fmt.Sprintf("%s_%s", hostname, string(uuid.NewUUID()))

	lockConfig := resourcelock.ResourceLockConfig{
		Identity:      identity,
		EventRecorder: deps.EventRecorder.Get(),
	}

	resourceLock, err := resourcelock.New(
		resourcelock.LeasesResourceLock,
		*options.Namespace,
		*options.Name,
		deps.KubeConfig.Get().NativeClientSet().CoreV1(),
		deps.KubeConfig.Get().NativeClientSet().CoordinationV1(),
		lockConfig,
	)
	if err != nil {
		return errors.TagWrapf("CreateResourceLock", err, "creating resource lock")
	}

	config := leaderelection.LeaderElectionConfig{
		Lock:            resourceLock,
		LeaseDuration:   *options.LeaseDuration,
		RenewDeadline:   *options.RenewDeadline,
		RetryPeriod:     *options.RetryPeriod,
		ReleaseOnCancel: true,
		Callbacks: state.createCallbacks(
			ctx,
			deps.Observer.Get(),
			args.ElectorName,
			args.ClusterName,
			*options.RenewDeadline,
			identity,
		),
		WatchDog:    nil, // TODO
		Name:        string(args.ElectorName),
		Coordinated: false,
	}

	elector, err := leaderelection.NewLeaderElector(config)
	if err != nil {
		return errors.TagWrapf("NewLeaderElector", err, "create leader elector")
	}

	electorWriter(elector)
	wroteElector = true //nolint:wsl // wroteElector must be flagged immediately after calling electorWriter

	runCtx, cancelFunc := context.WithCancel(context.WithoutCancel(ctx))
	state.cancelElectorFunc.Store(&cancelFunc)
	select {
	case <-ctx.Done():
		// cancelElectorFunc might be assigned after Join is called
		return nil
	default:
		go elector.Run(runCtx)
	}

	return nil
}

func (state *ElectorState) createCallbacks(
	ctx context.Context,
	observer ElectorObserver,
	electorName ElectorName,
	clusterName ClusterName,
	renewDeadline time.Duration,
	identity string,
) leaderelection.LeaderCallbacks {
	arg := ElectorObserverArg{
		ElectorName: string(electorName),
		ClusterName: string(clusterName),
		Identity:    identity,
	}

	return leaderelection.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			ctx, cancelFunc := observer.Acquired(ctx, arg)
			defer cancelFunc()

			state.electionCloseCh = ctx.Done()
			close(state.electedCh)

			go wait.UntilWithContext(ctx, func(ctx context.Context) {
				observer.Heartbeat(ctx, arg)
			}, renewDeadline)
		},
		OnStoppedLeading: func() {
			observer.Lost(ctx, arg)
		},
		OnNewLeader: func(_ string) {},
	}
}

// Waits for the elector to acquire leader lease.
//
// Returns a child context that cancels when the acquired leader lease is lost.
// Returns an error if the election fails or if the context is canceled.
func (e *Elector) Await(ctx context.Context) (context.Context, error) {
	select {
	case <-e.electedCh:
		if err := *e.electionErr; err != nil {
			return nil, err
		}

		return util.ContextWithStopCh(ctx, *e.electionCloseCh), nil
	case <-ctx.Done():
		//nolint:wrapcheck // no need to wrap cancellation error
		return nil, ctx.Err()
	}
}

// Returns whether the elector has acquired the leader lease.
func (e *Elector) HasElected() bool {
	select {
	case <-e.electedCh:
		return true
	default:
		return false
	}
}

// A mock elector that always gets ready immediately.
func MockReadyElector(ctx context.Context) *Elector {
	return &Elector{
		electedCh:       util.ClosedChan[util.Empty](),
		electionCloseCh: ptr.To(ctx.Done()),
		electionErr:     ptr.To(error(nil)),
	}
}

type ElectorObserver struct {
	Acquired  o11y.ObserveScopeFunc[ElectorObserverArg]
	Lost      o11y.ObserveFunc[ElectorObserverArg]
	Heartbeat o11y.ObserveFunc[ElectorObserverArg]
}

func (ElectorObserver) ComponentName() string { return "elector" }

func (observer ElectorObserver) Join(other ElectorObserver) ElectorObserver {
	return o11y.ReflectJoin(observer, other)
}

type ElectorObserverArg struct {
	ElectorName string
	ClusterName string
	Identity    string
}
