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

// This file is mostly lifted and modified from k8s.io/component-base@v0.30.3/metrics/prometheus/restclient.
/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package kubemetrics

import (
	"context"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/metrics"
)

//nolint:exhaustruct // Prometheus options are not supposed to be exhaustive.
var (
	// requestLatency is a Prometheus Histogram metric type partitioned by
	// "verb", and "host" labels. It is used for the rest client latency metrics.
	requestLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kube_client_request_duration_seconds",
			Help:    "Request latency in seconds. Broken down by verb, and host.",
			Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1.0, 2.0, 4.0, 8.0, 15.0, 30.0, 60.0},
		},
		[]string{"verb", "host"},
	)

	// resolverLatency is a Prometheus Histogram metric type partitioned by
	// "host" labels. It is used for the rest client DNS resolver latency metrics.
	resolverLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kube_client_dns_resolution_duration_seconds",
			Help:    "DNS resolver latency in seconds. Broken down by host.",
			Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1.0, 2.0, 4.0, 8.0, 15.0, 30.0},
		},
		[]string{"host"},
	)

	requestSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "kube_client_request_size_bytes",
			Help: "Request size in bytes. Broken down by verb and host.",
			// 64 bytes to 16MB
			Buckets: []float64{
				64,
				256,
				512,
				1024,
				4096,
				16384,
				65536,
				262144,
				1048576,
				4194304,
				16777216,
			},
		},
		[]string{"verb", "host"},
	)

	responseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "kube_client_response_size_bytes",
			Help: "Response size in bytes. Broken down by verb and host.",
			// 64 bytes to 16MB
			Buckets: []float64{
				64,
				256,
				512,
				1024,
				4096,
				16384,
				65536,
				262144,
				1048576,
				4194304,
				16777216,
			},
		},
		[]string{"verb", "host"},
	)

	rateLimiterLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kube_client_rate_limiter_duration_seconds",
			Help:    "Client side rate limiter latency in seconds. Broken down by verb, and host.",
			Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1.0, 2.0, 4.0, 8.0, 15.0, 30.0, 60.0},
		},
		[]string{"verb", "host"},
	)

	requestResult = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kube_client_requests_total",
			Help: "Number of HTTP requests, partitioned by status code, method, and host.",
		},
		[]string{"code", "method", "host"},
	)

	requestRetry = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kube_client_request_retries_total",
			Help: "Number of request retries, partitioned by status code, verb, and host.",
		},
		[]string{"code", "verb", "host"},
	)

	transportCacheEntries = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kube_client_transport_cache_entries",
			Help: "Number of transport entries in the internal cache.",
		},
	)

	transportCacheCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kube_client_transport_create_calls_total",
			Help: "Number of calls to get a new transport, partitioned by the result of the operation " +
				"hit: obtained from the cache, miss: created and added to the cache, uncacheable: created and not cached",
		},
		[]string{"result"},
	)
)

//nolint:gochecknoinits // func init is the desired pattern for metrics.Register
func init() {
	metrics.Register(metrics.RegisterOpts{
		RequestLatency:        &latencyAdapter{m: requestLatency},
		ResolverLatency:       &resolverLatencyAdapter{m: resolverLatency},
		RequestSize:           &sizeAdapter{m: requestSize},
		ResponseSize:          &sizeAdapter{m: responseSize},
		RateLimiterLatency:    &latencyAdapter{m: rateLimiterLatency},
		RequestResult:         &resultAdapter{requestResult},
		RequestRetry:          &retryAdapter{requestRetry},
		TransportCacheEntries: &transportCacheAdapter{m: transportCacheEntries},
		TransportCreateCalls:  &transportCacheCallsAdapter{m: transportCacheCalls},
		ExecPluginCalls:       nil,
		ClientCertExpiry:      nil,
		ClientCertRotationAge: nil,
	})
}

func RegisterForPrometheus(registry *prometheus.Registry) {
	registry.MustRegister(requestLatency)
	registry.MustRegister(requestSize)
	registry.MustRegister(responseSize)
	registry.MustRegister(rateLimiterLatency)
	registry.MustRegister(requestResult)
	registry.MustRegister(requestRetry)
	registry.MustRegister(transportCacheEntries)
	registry.MustRegister(transportCacheCalls)
}

type latencyAdapter struct {
	m *prometheus.HistogramVec
}

func (l *latencyAdapter) Observe(
	_ context.Context,
	verb string,
	u url.URL,
	latency time.Duration,
) {
	l.m.WithLabelValues(verb, u.Host).Observe(latency.Seconds())
}

type resolverLatencyAdapter struct {
	m *prometheus.HistogramVec
}

func (l *resolverLatencyAdapter) Observe(_ context.Context, host string, latency time.Duration) {
	l.m.WithLabelValues(host).Observe(latency.Seconds())
}

type sizeAdapter struct {
	m *prometheus.HistogramVec
}

func (s *sizeAdapter) Observe(_ context.Context, verb string, host string, size float64) {
	s.m.WithLabelValues(verb, host).Observe(size)
}

type resultAdapter struct {
	m *prometheus.CounterVec
}

func (r *resultAdapter) Increment(_ context.Context, code, method, host string) {
	r.m.WithLabelValues(code, method, host).Inc()
}

type retryAdapter struct {
	m *prometheus.CounterVec
}

func (r *retryAdapter) IncrementRetry(_ context.Context, code, method, host string) {
	r.m.WithLabelValues(code, method, host).Inc()
}

type transportCacheAdapter struct {
	m prometheus.Gauge
}

func (t *transportCacheAdapter) Observe(value int) {
	t.m.Set(float64(value))
}

type transportCacheCallsAdapter struct {
	m *prometheus.CounterVec
}

func (t *transportCacheCallsAdapter) Increment(result string) {
	t.m.WithLabelValues(result).Inc()
}
