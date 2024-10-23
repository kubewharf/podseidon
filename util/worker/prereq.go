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

package worker

import (
	"context"

	"k8s.io/client-go/tools/cache"
)

// A prerequisite condition before a worker can starts running.
//
// Worker starts running as long as Wait returns true the first time.
// IsReady may return false subsequently,
// but it would only affect health checks but not worker liveness.
type Prereq interface {
	Wait(ctx context.Context) bool

	IsReady() bool
}

func InformerPrereq(informer cache.SharedIndexInformer) Prereq {
	return HasSyncedPrereq(informer.HasSynced)
}

func HasSyncedPrereq(hasSynced cache.InformerSynced) Prereq {
	return &hasSyncedPrereq{hasSynced: hasSynced}
}

type hasSyncedPrereq struct {
	hasSynced cache.InformerSynced
}

func (prereq *hasSyncedPrereq) Wait(ctx context.Context) bool {
	return cache.WaitForCacheSync(ctx.Done(), prereq.hasSynced)
}

func (prereq *hasSyncedPrereq) IsReady() bool {
	return prereq.hasSynced()
}
