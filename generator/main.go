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
	kubeobserver "github.com/kubewharf/podseidon/util/kube/observer"
	"github.com/kubewharf/podseidon/util/o11y/metrics"
	pprutilobserver "github.com/kubewharf/podseidon/util/podprotector/observer"
	"github.com/kubewharf/podseidon/util/pprof"
	"github.com/kubewharf/podseidon/util/util"
	workerobserver "github.com/kubewharf/podseidon/util/worker/observer"

	"github.com/kubewharf/podseidon/generator/generator"
	"github.com/kubewharf/podseidon/generator/monitor"
	generatorobserver "github.com/kubewharf/podseidon/generator/observer"
	"github.com/kubewharf/podseidon/generator/resource"
	"github.com/kubewharf/podseidon/generator/resource/deployment"
)

func main() {
	cmd.Run(
		component.RequireDep(pprof.New(util.Empty{})),
		component.RequireDep(metrics.NewHttp(metrics.HttpArgs{})),
		healthzobserver.Provide,
		workerobserver.Provide,
		kubeobserver.ProvideElector,
		pprutilobserver.ProvideInformer,
		generatorobserver.Provide,
		component.RequireDep(generator.NewController(
			generator.ControllerArgs{
				Types: []component.Declared[resource.TypeProvider]{
					deployment.New(util.Empty{}),
				},
			},
		)),
		component.RequireDep(monitor.New(monitor.Args{})),
	)
}
