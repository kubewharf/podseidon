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

// Declares components that expose an HTTP server.
package http

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	utilflag "github.com/kubewharf/podseidon/util/flag"
)

// Declares a component that exposes an HTTP server.
//
// Semantics are similar to `component.Declare`;
// options and lifecycles for HTTP server are handled internally,
// and the caller only needs to manage their own lifecycle
// and register HTTP handlers into the `mux` in `init`.
//
//nolint:cyclop,gocognit // actual logic is in separate decoupled functions
func DeclareServer[Args any, Options any, Deps any, State any, Api any](
	nameFn func(args Args) string,
	defaultHttpPort, defaultHttpsPort uint16,
	newOptions func(args Args, fs *flag.FlagSet) Options,
	newDeps func(args Args, requests *component.DepRequests) Deps,
	registerHandler func(ctx context.Context, args Args, options Options, deps Deps, mux *http.ServeMux) (*State, error),
	lifecycle component.Lifecycle[Args, Options, Deps, State],
	api func(Args, Options, Deps, *State) Api,
) func(Args) component.Declared[Api] {
	return component.Declare(
		nameFn,
		func(args Args, fs *flag.FlagSet) wrappedOptions[Options] {
			name := nameFn(args)

			return wrappedOptions[Options]{
				Http: newServerOptions(fs, name, "", "HTTP", true, "::1", defaultHttpPort),
				Https: newServerOptions(
					fs,
					name,
					"https-",
					"HTTPS",
					false,
					"::1",
					defaultHttpsPort,
				),
				HttpsCert: fs.String(
					"https-cert-file",
					"",
					fmt.Sprintf("TLS certificate file for %s HTTPS server", name),
				),
				HttpsKey: fs.String(
					"https-key-file",
					"",
					fmt.Sprintf("TLS key file for %s HTTPS server", name),
				),
				ReadHeaderTimeout: fs.Duration(
					"read-header-timeout",
					time.Second*10,
					fmt.Sprintf("%s server read header timeout", name),
				),
				inner: newOptions(args, fs),
			}
		},
		newDeps,
		func(ctx context.Context, args Args, options wrappedOptions[Options], deps Deps) (*wrappedState[State], error) {
			mux := http.NewServeMux()

			innerState, err := registerHandler(ctx, args, options.inner, deps, mux)
			if err != nil {
				return nil, err
			}

			httpServer := newServer(options.Http, options, mux)
			httpsServer := newServer(options.Https, options, mux)

			return &wrappedState[State]{
				httpServer:  httpServer,
				httpsServer: httpsServer,
				inner:       innerState,
			}, nil
		},
		component.Lifecycle[Args, wrappedOptions[Options], Deps, wrappedState[State]]{
			Start: func(ctx context.Context, args *Args, options *wrappedOptions[Options], deps *Deps, state *wrappedState[State]) error {
				if lifecycle.Start != nil {
					if err := lifecycle.Start(ctx, args, &options.inner, deps, state.inner); err != nil {
						return errors.TagWrapf("StartInner", err, "start inner lifecycle")
					}
				}

				if state.httpServer != nil {
					go func(server *http.Server) {
						server.BaseContext = func(net.Listener) context.Context { return ctx }

						if err := server.ListenAndServe(); err != nil &&
							!errors.Is(err, http.ErrServerClosed) {
							klog.FromContext(ctx).
								Error(err, fmt.Sprintf("%s HTTP server error", nameFn(*args)))
						}
					}(state.httpServer)
				}

				if state.httpsServer != nil {
					go func(server *http.Server) {
						server.BaseContext = func(net.Listener) context.Context { return ctx }

						err := server.ListenAndServeTLS(*options.HttpsCert, *options.HttpsKey)
						if err != nil && !errors.Is(err, http.ErrServerClosed) {
							klog.FromContext(ctx).
								Error(err, fmt.Sprintf("%s HTTPS server error", nameFn(*args)))
						}
					}(state.httpsServer)
				}

				return nil
			},
			Join: func(ctx context.Context, args *Args, options *wrappedOptions[Options], deps *Deps, state *wrappedState[State]) error {
				for _, server := range []*http.Server{state.httpServer, state.httpsServer} {
					if server != nil {
						if err := server.Shutdown(ctx); err != nil {
							return errors.TagWrapf(
								"ServerShutdown",
								err,
								"shutdown %s server",
								nameFn(*args),
							)
						}
					}
				}

				if lifecycle.Join != nil {
					return lifecycle.Join(ctx, args, &options.inner, deps, state.inner)
				}

				return nil
			},
			HealthChecks: func(state *wrappedState[State]) component.HealthChecks {
				if lifecycle.HealthChecks != nil {
					return lifecycle.HealthChecks(state.inner)
				}

				return nil
			},
		},
		func(d *component.Data[Args, wrappedOptions[Options], Deps, wrappedState[State]]) Api {
			return api(d.Args, d.Options.inner, d.Deps, d.State.inner)
		},
	)
}

type serverOptions struct {
	Enable   *bool
	BindAddr *string
	Port     *uint16
}

func newServerOptions(
	fs *flag.FlagSet,
	name string,
	prefix string,
	display string,
	defaultEnable bool,
	defaultBindAddr string,
	defaultPort uint16,
) serverOptions {
	return serverOptions{
		Enable: fs.Bool(
			prefix+"enable",
			defaultEnable,
			fmt.Sprintf("expose %s %s server", name, display),
		),
		BindAddr: fs.String(
			prefix+"bind-addr",
			defaultBindAddr,
			fmt.Sprintf("%s %s server bind address", name, display),
		),
		Port: utilflag.Uint16(
			fs,
			prefix+"port",
			defaultPort,
			fmt.Sprintf("%s %s server bind port", name, display),
		),
	}
}

func newServer[Inner any](
	so serverOptions,
	options wrappedOptions[Inner],
	mux http.Handler,
) *http.Server {
	if !*so.Enable {
		return nil
	}

	//nolint:exhaustruct // http.Server config is not intended to be exhausted
	return &http.Server{
		Addr: net.JoinHostPort(
			*so.BindAddr,
			strconv.FormatUint(uint64(*so.Port), 10),
		),
		Handler:           mux,
		ReadHeaderTimeout: *options.ReadHeaderTimeout,
	}
}

type wrappedOptions[Inner any] struct {
	Http serverOptions

	Https     serverOptions
	HttpsCert *string
	HttpsKey  *string

	ReadHeaderTimeout *time.Duration

	inner Inner
}

type wrappedState[Inner any] struct {
	httpServer  *http.Server
	httpsServer *http.Server

	inner *Inner
}
