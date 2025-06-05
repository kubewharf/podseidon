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

package testutil

import "fmt"

type ClusterCount struct {
	Workers int
}

func (setup ClusterCount) ClusterIds() []ClusterId {
	output := make([]ClusterId, setup.Workers+1)

	for i := range output {
		output[i] = ClusterId(i)
	}

	return output
}

func (setup ClusterCount) WorkerClusterIds() []ClusterId {
	output := make([]ClusterId, setup.Workers)

	for i := range output {
		output[i] = ClusterId(i + 1)
	}

	return output
}

type ClusterId uint32

const CoreClusterId = ClusterId(0)

func (id ClusterId) IsCore() bool { return id == 0 }

func (id ClusterId) IsWorker() bool { return id > 0 }

func (id ClusterId) String() string {
	if id == 0 {
		return "core"
	}

	return fmt.Sprintf("worker-%d", uint32(id))
}

func (id ClusterId) KwokName(namespace string) string {
	return fmt.Sprintf("%s-%s", namespace, id.String())
}

func (id ClusterId) Index() int { return int(id) }

type PodId struct {
	Cluster ClusterId
	Pod     uint32
}

func (id PodId) PodName() string {
	return fmt.Sprintf("%s-pod-%d", id.Cluster.String(), id.Pod)
}

type PodCounts map[ClusterId]int32

func (counts PodCounts) PodIds() []PodId {
	output := []PodId(nil)
	for cluster, count := range counts {
		for podIndex := range count {
			output = append(output, PodId{Cluster: cluster, Pod: uint32(podIndex)})
		}
	}
	return output
}
