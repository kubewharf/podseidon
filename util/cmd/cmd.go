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

// Main process entrypoint.
package cmd

import (
	"context"
	goerrors "errors"
	"os"
	"sync"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	utilflag "github.com/kubewharf/podseidon/util/flag"
	utilhealthz "github.com/kubewharf/podseidon/util/healthz"
	"github.com/kubewharf/podseidon/util/shutdown"
	"github.com/kubewharf/podseidon/util/util"
)

const shutdownTimeout = time.Second * 10

// Main entrypoint of an executable.
//
// `dependencies` are required components that the executable needs to load.
// Transitive dependencies are resolved automatically.
//
// See the `component` package for more information.
func Run(dependencies ...func(*component.DepRequests)) {
	if err := tryRun(dependencies); err != nil {
		panic(err)
	}
}

func tryRun(requests []func(*component.DepRequests)) error {
	healthzHandler := &healthz.Handler{Checks: map[string]healthz.Checker{}}

	var (
		healthzServer    component.Dep[*utilhealthz.Api]
		shutdownNotifier component.Dep[*shutdown.Notifier]
	)

	requests = util.AppendSliceCopy(
		requests,
		func(reqs *component.DepRequests) {
			healthzServer = component.DepPtr(reqs, utilhealthz.NewServer(utilhealthz.Args{
				Handler: healthzHandler,
			}))
		},
		func(reqs *component.DepRequests) {
			shutdownNotifier = component.DepPtr(reqs, shutdown.New(util.Empty{}))
		},
	)

	components := component.ResolveList(requests)

	setupFlags(components, pflag.CommandLine)

	if err := pflag.CommandLine.Parse(os.Args[1:]); err != nil {
		return errors.TagWrapf("ParseArgs", err, "parse args")
	}

	components = component.TrimNonRequested(components)

	baseCtx := context.Background()

	ctx, cancelFunc := context.WithCancel(baseCtx)
	defer cancelFunc()

	pflag.CommandLine.VisitAll(func(f *pflag.Flag) {
		klog.FromContext(ctx).WithValues("name", f.Name, "value", f.Value.String()).Info("flag")
	})

	if err := initComponents(ctx, components); err != nil {
		return err
	}

	for _, comp := range components {
		comp.Component.RegisterHealthChecks(healthzHandler, func(name string, err error) {
			healthzServer.Get().OnHealthCheckFailed(ctx, name, err)
		})
	}

	shutdownNotifier.Get().CallOnStop(cancelFunc)

	if err := startComponents(ctx, components); err != nil {
		return err
	}

	klog.Info("Startup complete")

	<-ctx.Done()

	shutdownCtx, shutdownCancelFunc := context.WithTimeout(baseCtx, shutdownTimeout)
	defer shutdownCancelFunc()

	for i := len(components) - 1; i >= 0; i-- {
		comp := components[i]

		klog.InfoS("joining", "component", comp.Name)

		if err := comp.Component.Join(shutdownCtx); err != nil {
			return errors.TagWrapf("Join", err, "%s join error", comp.Name)
		}
	}

	return nil
}

// Used for component integration tests.
//
// Performs the same chores as `Run`,
// but does not accept configuring flags, does not expose a health check server
// and does not block until shutdown.
// Returns as soon as startup completes to allow the caller to orchestrate integration tests.
func MockStartupWithCliArgs(ctx context.Context, requests []func(*component.DepRequests), cliArgs []string) component.ApiMap {
	components := component.ResolveList(requests)

	fs := new(pflag.FlagSet)
	setupFlags(components, fs)

	if err := fs.Parse(cliArgs); err != nil {
		panic(err)
	}

	components = component.TrimNonRequested(components)

	if err := initComponents(ctx, components); err != nil {
		panic(err)
	}

	if err := startComponents(ctx, components); err != nil {
		panic(err)
	}

	return component.NamedComponentsToApiMap(components)
}

func MockStartup(ctx context.Context, requests []func(*component.DepRequests)) component.ApiMap {
	return MockStartupWithCliArgs(ctx, requests, []string{})
}

func setupFlags(components []component.NamedComponent, fs *pflag.FlagSet) {
	for _, comp := range components {
		utilflag.AddSubset(fs, comp.Name, comp.Component.AddFlags)
	}
}

func initComponents(ctx context.Context, components []component.NamedComponent) error {
	for _, comp := range components {
		klog.InfoS("initializing", "component", comp.Name)

		if err := comp.Component.Init(ctx); err != nil {
			return errors.TagWrapf("Init", err, "%s init error", comp.Name)
		}
	}

	return nil
}

func startComponents(ctx context.Context, components []component.NamedComponent) error {
	var wg sync.WaitGroup

	errorList := []error{}
	errorListMu := sync.Mutex{}

	for _, comp := range components {
		wg.Add(1)

		go func(comp component.NamedComponent) {
			defer wg.Done()

			if err := comp.Component.Start(ctx); err != nil {
				errorListMu.Lock()
				defer errorListMu.Unlock() // errorListMu must be released before calling wg.Done()

				errorList = append(
					errorList,
					errors.TagWrapf("Start", err, "%s start error", comp.Name),
				)

				return
			}

			klog.InfoS("started", "component", comp.Name)
		}(comp)
	}

	wg.Wait()

	if len(errorList) > 0 {
		return goerrors.Join(errorList...)
	}

	return nil
}
