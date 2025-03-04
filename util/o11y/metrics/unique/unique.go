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

package unique

import (
	"strings"
	"sync"

	"github.com/axiomhq/hyperloglog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/puzpuzpuz/xsync/v3"

	"github.com/kubewharf/podseidon/util/o11y/metrics"
)

type counterVec struct {
	collector *prometheus.GaugeVec
	counters  *xsync.MapOf[string, *counter]
}

func (ty *counterVec) InitCollector(name string, help string, tagKeys []string) prometheus.Collector {
	//nolint:exhaustruct
	ty.collector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	}, tagKeys)

	return ty.collector
}

func (ty *counterVec) Emit(tags []string, value string) {
	counter, _ := ty.counters.LoadOrCompute(strings.Join(tags, "|"), func() *counter {
		return &counter{
			hllMu: sync.Mutex{},
			hll:   hyperloglog.New16(),
			gauge: ty.collector.WithLabelValues(tags...),
		}
	})
	counter.observe(value)
}

func (ty *counterVec) AsType() metrics.Type[string] { return ty }

func (ty *counterVec) Flush() {
	ty.counters.Range(func(_ string, value *counter) bool {
		value.gauge.Set(float64(value.reportAndReset()))

		return true
	})
}

func NewCounterVec() metrics.FlushableType[string] {
	return &counterVec{
		collector: nil,
		counters:  xsync.NewMapOf[string, *counter](),
	}
}

type counter struct {
	hllMu sync.Mutex
	hll   *hyperloglog.Sketch
	gauge prometheus.Gauge
}

func (counter *counter) observe(value string) {
	counter.hllMu.Lock()
	defer counter.hllMu.Unlock()

	counter.hll.Insert([]byte(value))
}

func (counter *counter) reportAndReset() uint64 {
	counter.hllMu.Lock()
	defer counter.hllMu.Unlock()

	estimate := counter.hll.Estimate()
	counter.hll = hyperloglog.New16()

	return estimate
}
