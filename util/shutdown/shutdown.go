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

package shutdown

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/util"
)

var New = component.Declare(
	func(util.Empty) string { return "shutdown-notifier" },
	func(util.Empty, *flag.FlagSet) util.Empty { return util.Empty{} },
	func(util.Empty, *component.DepRequests) util.Empty { return util.Empty{} },
	func(context.Context, util.Empty, util.Empty, util.Empty) (*Notifier, error) {
		return &Notifier{
			stopped: atomic.Bool{},
			watchCh: make(chan util.Empty),
		}, nil
	},
	component.Lifecycle[util.Empty, util.Empty, util.Empty, Notifier]{
		Start: func(_ context.Context, _ *util.Empty, _ *util.Empty, _ *util.Empty, state *Notifier) error {
			state.addSignalHandler()
			return nil
		},
		Join:         nil,
		HealthChecks: nil,
	},
	func(d *component.Data[util.Empty, util.Empty, util.Empty, Notifier]) *Notifier {
		return d.State
	},
)

type Notifier struct {
	stopped atomic.Bool
	watchCh chan util.Empty
}

func (notifier *Notifier) Close() {
	if notifier.stopped.CompareAndSwap(false, true) {
		close(notifier.watchCh)
	}
}

func (notifier *Notifier) StopChan() <-chan util.Empty {
	return notifier.watchCh
}

func (notifier *Notifier) CallOnStop(fn func()) {
	go func() {
		<-notifier.watchCh
		fn()
	}()
}

func (notifier *Notifier) addSignalHandler() {
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signalCh
		notifier.Close()
	}()
}
