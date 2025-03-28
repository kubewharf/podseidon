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
	"flag"
	"fmt"
	"io"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"

	podseidon "github.com/kubewharf/podseidon/apis"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/errors"
	utilhttp "github.com/kubewharf/podseidon/util/http"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"

	"github.com/kubewharf/podseidon/webhook/handler"
	"github.com/kubewharf/podseidon/webhook/observer"
)

const (
	defaultHttpPort  uint16 = 8080
	defaultHttpsPort uint16 = 8843
)

var New = utilhttp.DeclareServer(
	func(Args) string { return "webhook" },
	defaultHttpPort,
	defaultHttpsPort,
	func(_ Args, fs *flag.FlagSet) Options {
		return Options{
			pathPrefix: fs.String(
				"path-prefix",
				"",
				"Expected path prefix to strip from incoming requests, must start with a slash if set, e.g. `/webhook`",
			),
			dryRun: fs.Bool(
				"dry-run",
				false,
				"never reject any deletions, only update PodProtector and emit rejection metrics",
			),
		}
	},
	func(_ Args, reqs *component.DepRequests) Deps {
		return Deps{
			observer: o11y.Request[observer.Observer](reqs),
			handler:  component.DepPtr(reqs, handler.New(handler.Args{})),
		}
	},
	func(_ Args, options Options, deps Deps, mux *http.ServeMux) (*State, error) {
		mux.HandleFunc(
			fmt.Sprintf("POST %s/{cell}", *options.pathPrefix),
			func(resp http.ResponseWriter, req *http.Request) {
				cellId := req.PathValue("cell")

				ctx, cancelFunc := deps.observer.Get().HttpRequest(
					req.Context(),
					observer.Request{
						Cell:       cellId,
						RemoteAddr: req.RemoteAddr,
					},
				)
				defer cancelFunc()

				postBody, err := io.ReadAll(req.Body)
				if err != nil {
					err = errors.Tag("ReadBody", err)

					deps.observer.Get().HttpError(ctx, observer.HttpError{Err: err})

					resp.WriteHeader(http.StatusBadRequest)
					if _, err := resp.Write([]byte("Cannot read POST body")); err != nil {
						deps.observer.Get().HttpError(
							ctx,
							observer.HttpError{Err: err},
						)
					}

					return
				}

				reviewRequest := new(admissionv1.AdmissionReview)
				if err := json.Unmarshal(postBody, reviewRequest); err != nil {
					err = errors.Tag("UnmarshalBody", err)

					deps.observer.Get().HttpError(ctx, observer.HttpError{Err: err})

					resp.WriteHeader(http.StatusBadRequest)
					if _, err = resp.Write(fmt.Appendf(nil, "error parsing JSON body: %v", err)); err != nil {
						deps.observer.Get().HttpError(
							ctx,
							observer.HttpError{Err: err},
						)
					}

					return
				}

				auditAnnotations := map[string]string{}
				result, preferDryRun := deps.handler.Get().
					Handle(ctx, reviewRequest.Request, cellId, auditAnnotations)
				dryRun := *options.dryRun || preferDryRun

				if result.Err != nil {
					err = errors.Tag("Handle", result.Err)

					deps.observer.Get().HttpError(ctx, observer.HttpError{Err: err})

					if !dryRun {
						resp.WriteHeader(http.StatusInternalServerError)

						if _, err = resp.Write(fmt.Appendf(nil, "server internal error: %v", err)); err != nil {
							deps.observer.Get().HttpError(
								ctx,
								observer.HttpError{Err: err},
							)
						}

						return
					}
				} else {
					deps.observer.Get().HttpRequestComplete(ctx, observer.RequestComplete{
						Request: reviewRequest.Request,
						Status:  result.Status,
					})

					if !dryRun {
						_ = json.NewEncoder(resp).Encode(&admissionv1.AdmissionReview{
							TypeMeta: metav1.TypeMeta{
								APIVersion: admissionv1.SchemeGroupVersion.String(),
								Kind:       "AdmissionReview",
							},
							Response: &admissionv1.AdmissionResponse{
								UID:     reviewRequest.Request.UID,
								Allowed: !result.Rejection.IsSome(),
								Result: optional.Map(result.Rejection, handler.Rejection.ToStatus).
									GetOrZero(),
								AuditAnnotations: auditAnnotations,
							},
						})

						return
					}
				}

				// dry-run branch, always return successful result

				auditAnnotations[podseidon.AuditAnnotationDryRun] = "1"

				_ = json.NewEncoder(resp).Encode(&admissionv1.AdmissionReview{
					TypeMeta: metav1.TypeMeta{
						APIVersion: admissionv1.SchemeGroupVersion.String(),
						Kind:       "AdmissionReview",
					},
					Response: &admissionv1.AdmissionResponse{
						UID:              reviewRequest.Request.UID,
						Allowed:          true,
						AuditAnnotations: auditAnnotations,
					},
				})
			},
		)

		return &State{}, nil
	},
	component.Lifecycle[Args, Options, Deps, State]{
		Start:        nil,
		Join:         nil,
		HealthChecks: nil,
	},
	func(Args, Options, Deps, *State) util.Empty { return util.Empty{} },
)

type Args struct{}

type Options struct {
	pathPrefix *string
	dryRun     *bool
}

type Deps struct {
	observer component.Dep[observer.Observer]
	handler  component.Dep[handler.Api]
}

type State struct{}
