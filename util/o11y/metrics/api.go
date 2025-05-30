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

package metrics

import (
	"context"
	"fmt"
	"reflect"
	"time"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubewharf/podseidon/util/util"
)

func Register[Value any, Tags any](
	registry Registry,
	name string,
	help string,
	metricType Type[Value],
	tagsDesc TagsDesc[Tags],
) Handle[Tags, Value] {
	return registerWith(registry.Prometheus, registry.TagFilter, name, help, metricType, tagsDesc)
}

func registerWith[Value any, Tags any](
	registry *prometheus.Registry,
	filter *TagFilter,
	name string,
	help string,
	metricType Type[Value],
	tagsDesc TagsDesc[Tags],
) Handle[Tags, Value] {
	tagsDesc = filteredTagDesc(tagsDesc, filter)

	registry.MustRegister(metricType.InitCollector(name, help, tagsDesc.TagKeys()))

	return Handle[Tags, Value]{
		metricType: metricType,
		tagsDesc:   tagsDesc,
	}
}

func RegisterFlushable[Value any, Tags any](
	registry Registry,
	name string,
	help string,
	metricType FlushableType[Value],
	tagsDesc TagsDesc[Tags],
	frequency time.Duration,
) Handle[Tags, Value] {
	*registry.flushables = append(*registry.flushables, func(ctx context.Context) {
		go func() {
			wait.UntilWithContext(
				ctx, func(_ context.Context) { metricType.Flush() },
				frequency,
			)

			metricType.Flush()
		}()
	})

	return Register(registry, name, help, metricType.AsType(), tagsDesc)
}

func filteredTagDesc[Tags any](tagsDesc TagsDesc[Tags], filter *TagFilter) TagsDesc[Tags] {
	keys := []string{}
	selected := []int{}

	for index, key := range tagsDesc.TagKeys() {
		if filter.Test(key) {
			keys = append(keys, key)
			selected = append(selected, index)
		}
	}

	return tagsDescSubset[Tags]{base: tagsDesc, keys: keys, subset: selected}
}

type tagsDescSubset[Tags any] struct {
	base   TagsDesc[Tags]
	keys   []string
	subset []int
}

func (desc tagsDescSubset[Tags]) TagKeys() []string {
	return desc.keys
}

func (desc tagsDescSubset[Tags]) TagValues(instance Tags) []string {
	superset := desc.base.TagValues(instance)
	subset := make([]string, len(desc.subset))

	for subIndex, superIndex := range desc.subset {
		subset[subIndex] = superset[superIndex]
	}

	return subset
}

type Handle[Tags any, Value any] struct {
	metricType Emitter[Value]
	tagsDesc   TagsDesc[Tags]
}

func (handle Handle[Tags, Value]) Emit(value Value, tags Tags) {
	handle.With(tags).Emit(value)
}

func (handle Handle[Tags, Value]) With(tags Tags) TaggedHandle[Value] {
	return TaggedHandle[Value]{
		metricType: handle.metricType,
		tagValues:  handle.tagsDesc.TagValues(tags),
	}
}

type TaggedHandle[Value any] struct {
	metricType Emitter[Value]
	tagValues  []string
}

func (handle TaggedHandle[Value]) Emit(value Value) {
	handle.metricType.Emit(handle.tagValues, value)
}

type Type[Value any] interface {
	InitCollector(name string, help string, tagKeys []string) prometheus.Collector
	Emitter[Value]
}

type Flushable interface {
	Flush()
}

type FlushableType[Value any] interface {
	// Do not extend Type[Value] directly to avoid accidentally passing into `Register.
	AsType() Type[Value]
	Flushable
}

type Emitter[Value any] interface {
	Emit(tagValues []string, value Value)
}

type typeGauge[Value any] struct {
	typeConv  func(Value) float64
	collector *prometheus.GaugeVec
}

func (ty *typeGauge[Value]) InitCollector(
	name string,
	help string,
	tagKeys []string,
) prometheus.Collector {
	//nolint:exhaustruct
	ty.collector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	}, tagKeys)

	return ty.collector
}

func (ty *typeGauge[Value]) Emit(tags []string, value Value) {
	ty.collector.WithLabelValues(tags...).Set(ty.typeConv(value))
}

func IntGauge() Type[int] { return &typeGauge[int]{typeConv: intToFloat64, collector: nil} }

func Int32Gauge() Type[int32] { return &typeGauge[int32]{typeConv: int32ToFloat64, collector: nil} }

func Int64Gauge() Type[int64] { return &typeGauge[int64]{typeConv: int64ToFloat64, collector: nil} }

func FloatGauge() Type[float64] {
	return &typeGauge[float64]{typeConv: util.Identity[float64], collector: nil}
}

func DurationGauge() Type[time.Duration] {
	return &typeGauge[time.Duration]{typeConv: time.Duration.Seconds, collector: nil}
}

type typeCounter[Value any] struct {
	typeConv  func(Value) float64
	collector *prometheus.CounterVec
}

func (ty *typeCounter[Value]) InitCollector(
	name string,
	help string,
	tagKeys []string,
) prometheus.Collector {
	//nolint:exhaustruct
	ty.collector = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: name,
		Help: help,
	}, tagKeys)

	return ty.collector
}

func (ty *typeCounter[Value]) Emit(tags []string, value Value) {
	ty.collector.WithLabelValues(tags...).Add(ty.typeConv(value))
}

func IntCounter() Type[int] { return &typeCounter[int]{typeConv: intToFloat64, collector: nil} }

func FloatCounter() Type[float64] {
	return &typeCounter[float64]{typeConv: util.Identity[float64], collector: nil}
}

type typeHistogram[Value any] struct {
	typeConv  func(Value) float64
	buckets   []float64
	collector *prometheus.HistogramVec
}

func (ty *typeHistogram[Value]) InitCollector(
	name string,
	help string,
	tagKeys []string,
) prometheus.Collector {
	//nolint:exhaustruct
	ty.collector = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    name,
		Help:    help,
		Buckets: ty.buckets,
	}, tagKeys)

	return ty.collector
}

func (ty *typeHistogram[Value]) Emit(tags []string, value Value) {
	ty.collector.WithLabelValues(tags...).Observe(ty.typeConv(value))
}

// A histogram measuring the time of a function execution.
func FunctionDurationHistogram() Type[time.Duration] {
	return DurationHistogram(
		[]float64{
			.000001,
			.0000025,
			.000005,
			.00001,
			.000025,
			.00005,
			.0001,
			.00025,
			.0005,
			.001,
			.0025,
			.005,
			.01,
			.025,
			.05,
			.1,
			.25,
			.5,
			1.0,
			2.5,
			5.0,
			10.0,
		},
	)
}

// A histogram measuring the latency of an asynchronous operation.
func AsyncLatencyDurationHistogram() Type[time.Duration] {
	return DurationHistogram(
		[]float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1., 5., 20., 60., 300., 1800., 3600., 21600., 86400., 604800.},
	)
}

func DurationHistogram(buckets []float64) Type[time.Duration] {
	return &typeHistogram[time.Duration]{
		typeConv:  time.Duration.Seconds,
		buckets:   buckets,
		collector: nil,
	}
}

func ExponentialIntHistogram(numBuckets int) Type[int] {
	buckets := make([]int, numBuckets)
	for i := range numBuckets {
		buckets[i] = 1 << i
	}

	return IntHistogram(buckets)
}

func IntHistogram(buckets []int) Type[int] {
	return &typeHistogram[int]{
		typeConv:  intToFloat64,
		buckets:   util.MapSlice(buckets, intToFloat64),
		collector: nil,
	}
}

type TagsDesc[Instance any] interface {
	TagKeys() []string
	TagValues(instance Instance) []string
}

type reflectTags[T any] struct {
	fields [][]int

	names []string
}

func NewReflectTags[T any]() TagsDesc[T] {
	ty := util.Type[T]()
	fields := make([][]int, 0, ty.NumField())
	names := make([]string, 0, ty.NumField())

	discoverFields(&fields, &names, ty, []int{})

	return reflectTags[T]{fields: fields, names: names}
}

func discoverFields(fields *[][]int, names *[]string, ty reflect.Type, indexPrefix []int) {
	for fieldIndex := range ty.NumField() {
		field := ty.Field(fieldIndex)

		if field.IsExported() {
			indexList := util.AppendSliceCopy(indexPrefix, fieldIndex)

			if field.Anonymous {
				// recurse into embedded struct
				discoverFields(fields, names, field.Type, indexList)
			} else {
				*fields = append(*fields, indexList)
				*names = append(*names, formatTagName(field.Name))
			}
		}
	}
}

func formatTagName(name string) string {
	output := ""

	for _, char := range name {
		if unicode.IsUpper(char) {
			if len(output) != 0 {
				output += "_"
			}

			output += string(unicode.ToLower(char))
		} else {
			output += string(char)
		}
	}

	return output
}

func (def reflectTags[T]) TagKeys() []string {
	return def.names
}

func (def reflectTags[T]) TagValues(instance T) []string {
	value := reflect.ValueOf(instance)

	output := make([]string, len(def.fields))

	for tagIndex, fieldIndex := range def.fields {
		obj := value.FieldByIndex(fieldIndex).Interface()
		if stringer, isStringer := obj.(fmt.Stringer); isStringer {
			output[tagIndex] = stringer.String()
		} else {
			output[tagIndex] = fmt.Sprint(obj)
		}
	}

	return output
}

func intToFloat64(i int) float64 { return float64(i) }

func int32ToFloat64(i int32) float64 { return float64(i) }

func int64ToFloat64(i int64) float64 { return float64(i) }
