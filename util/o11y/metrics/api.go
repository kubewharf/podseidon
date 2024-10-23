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
	"fmt"
	"reflect"
	"time"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/kubewharf/podseidon/util/util"
)

func Register[Value any, Tags any](
	registry *prometheus.Registry,
	name string,
	help string,
	metricType Type[Value],
	tagsDesc TagsDesc[Tags],
) Handle[Tags, Value] {
	registry.MustRegister(metricType.InitCollector(name, help, tagsDesc.TagKeys()))

	return Handle[Tags, Value]{
		metricType: metricType,
		tagsDesc:   tagsDesc,
	}
}

type Handle[Tags any, Value any] struct {
	metricType Type[Value]
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
	metricType Type[Value]
	tagValues  []string
}

func (handle TaggedHandle[Value]) Emit(value Value) {
	handle.metricType.Emit(handle.tagValues, value)
}

type Type[Value any] interface {
	InitCollector(name string, help string, tagKeys []string) prometheus.Collector
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
