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

package main

import (
	"github.com/kubewharf/podseidon/util/cmd"
	"github.com/kubewharf/podseidon/util/component"
	healthzobserver "github.com/kubewharf/podseidon/util/healthz/observer"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	pprutilobserver "github.com/kubewharf/podseidon/util/podprotector/observer"
	"github.com/kubewharf/podseidon/util/pprof"
	retrybatchobserver "github.com/kubewharf/podseidon/util/retrybatch/observer"
	"github.com/kubewharf/podseidon/util/util"

	webhookobserver "github.com/kubewharf/podseidon/webhook/observer"
	"github.com/kubewharf/podseidon/webhook/server"
)

func main() {
	cmd.Run(
		component.RequireDep(pprof.New(util.Empty{})),
		component.RequireDep(metrics.NewHttp(metrics.HttpArgs{})),
		healthzobserver.Provide,
		pprutilobserver.ProvideInformer,
		webhookobserver.Provide,
		retrybatchobserver.Provide,
		component.RequireDep(server.New(util.Empty{})),
	)
}
