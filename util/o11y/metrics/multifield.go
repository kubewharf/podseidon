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

	"github.com/prometheus/client_golang/prometheus"
)

func RegisterMultiField[Value any, Tags any](
	registry Registry,
	namePrefix string,
	helpPrefix string,
	tagsDesc TagsDesc[Tags],
	fields ...AnyField[Value],
) Handle[Tags, Value] {
	tagsDesc = filteredTagDesc(tagsDesc, registry.TagFilter)

	tagKeys := tagsDesc.TagKeys()
	for _, field := range fields {
		registry.Prometheus.MustRegister(field.InitFieldCollector(namePrefix, helpPrefix, tagKeys))
	}

	return Handle[Tags, Value]{
		metricType: multiFieldType[Value](fields),
		tagsDesc:   tagsDesc,
	}
}

type Field[StructType any, FieldType any] struct {
	Name       string
	Help       string
	MetricType Type[FieldType]
	Extractor  func(StructType) FieldType
}

func NewField[StructType any, FieldType any](
	name string,
	help string,
	metricType Type[FieldType],
	extractor func(StructType) FieldType,
) AnyField[StructType] {
	return Field[StructType, FieldType]{
		Name:       name,
		Help:       help,
		MetricType: metricType,
		Extractor:  extractor,
	}
}

// A type-erased interface for Field[StructType, *].
type AnyField[StructType any] interface {
	InitFieldCollector(namePrefix string, helpPrefix string, tagKeys []string) prometheus.Collector
	EmitField(tagValues []string, value StructType)
}

func (field Field[StructType, FieldType]) InitFieldCollector(namePrefix string, helpPrefix string, tagKeys []string) prometheus.Collector {
	return field.MetricType.InitCollector(
		fmt.Sprintf("%s_%s", namePrefix, field.Name),
		fmt.Sprintf("%s: %s", helpPrefix, field.Help),
		tagKeys,
	)
}

func (field Field[StructType, FieldType]) EmitField(tagValues []string, value StructType) {
	field.MetricType.Emit(tagValues, field.Extractor(value))
}

type multiFieldType[Value any] []AnyField[Value]

func (fields multiFieldType[Value]) Emit(tagValues []string, value Value) {
	for _, field := range fields {
		field.EmitField(tagValues, value)
	}
}
