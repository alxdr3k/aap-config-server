package metrics

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	httpRequestsMetric         = "aap_config_server_http_requests_total"
	httpRequestDurationMetric  = "aap_config_server_http_request_duration_seconds"
	reloadAttemptsMetric       = "aap_config_server_reload_attempts_total"
	reloadDurationMetric       = "aap_config_server_reload_duration_seconds"
	gitOperationsMetric        = "aap_config_server_git_operations_total"
	gitOperationDurationMetric = "aap_config_server_git_operation_duration_seconds"
	watchWaitsMetric           = "aap_config_server_watch_waits_total"
	watchWaitDurationMetric    = "aap_config_server_watch_wait_duration_seconds"
	// DegradedStateMetric is the dynamic gauge metric name for degraded components.
	DegradedStateMetric = "aap_config_server_degraded_state"
)

var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

// GaugeSample is a dynamic gauge value rendered with the default registry.
type GaugeSample struct {
	Name   string
	Labels []Label
	Value  float64
}

// Label is a Prometheus label pair.
type Label struct {
	Name  string
	Value string
}

type descriptor struct {
	name string
	help string
	kind string
}

var descriptors = []descriptor{
	{
		name: httpRequestsMetric,
		help: "Total HTTP requests served by method, route template, and status code.",
		kind: "counter",
	},
	{
		name: httpRequestDurationMetric,
		help: "HTTP request latency in seconds by method, route template, and status code.",
		kind: "histogram",
	},
	{
		name: reloadAttemptsMetric,
		help: "Total config reload attempts by mode and outcome.",
		kind: "counter",
	},
	{
		name: reloadDurationMetric,
		help: "Config reload duration in seconds by mode and outcome.",
		kind: "histogram",
	},
	{
		name: gitOperationsMetric,
		help: "Total Git operations by operation and outcome.",
		kind: "counter",
	},
	{
		name: gitOperationDurationMetric,
		help: "Git operation duration in seconds by operation and outcome.",
		kind: "histogram",
	},
	{
		name: watchWaitsMetric,
		help: "Total config watch waits by resource and outcome.",
		kind: "counter",
	},
	{
		name: watchWaitDurationMetric,
		help: "Config watch wait duration in seconds by resource and outcome.",
		kind: "histogram",
	},
	{
		name: DegradedStateMetric,
		help: "Current degraded state by component, where 1 means degraded.",
		kind: "gauge",
	},
}

var defaultRegistry = NewRegistry()

// Registry stores process-local counters and histograms.
type Registry struct {
	mu         sync.Mutex
	counters   map[string]counterSample
	histograms map[string]*histogramSample
}

type counterSample struct {
	name   string
	labels []Label
	value  float64
}

type histogramSample struct {
	name    string
	labels  []Label
	buckets []float64
	counts  []uint64
	count   uint64
	sum     float64
}

// NewRegistry creates an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   map[string]counterSample{},
		histograms: map[string]*histogramSample{},
	}
}

// RecordHTTPRequest records one completed HTTP request.
func RecordHTTPRequest(method, route string, code int, duration time.Duration) {
	if route == "" {
		route = "unknown"
	}
	labels := []Label{
		{Name: "code", Value: strconv.Itoa(code)},
		{Name: "method", Value: method},
		{Name: "route", Value: route},
	}
	defaultRegistry.addCounter(httpRequestsMetric, labels, 1)
	defaultRegistry.observeHistogram(httpRequestDurationMetric, labels, duration.Seconds())
}

// RecordReload records a store reload attempt.
func RecordReload(mode, outcome string, duration time.Duration) {
	labels := []Label{
		{Name: "mode", Value: mode},
		{Name: "outcome", Value: outcome},
	}
	defaultRegistry.addCounter(reloadAttemptsMetric, labels, 1)
	defaultRegistry.observeHistogram(reloadDurationMetric, labels, duration.Seconds())
}

// RecordGitOperation records a Git operation attempt.
func RecordGitOperation(operation, outcome string, duration time.Duration) {
	labels := []Label{
		{Name: "operation", Value: operation},
		{Name: "outcome", Value: outcome},
	}
	defaultRegistry.addCounter(gitOperationsMetric, labels, 1)
	defaultRegistry.observeHistogram(gitOperationDurationMetric, labels, duration.Seconds())
}

// RecordWatchWait records a watch wait attempt.
func RecordWatchWait(resource, outcome string, duration time.Duration) {
	labels := []Label{
		{Name: "outcome", Value: outcome},
		{Name: "resource", Value: resource},
	}
	defaultRegistry.addCounter(watchWaitsMetric, labels, 1)
	defaultRegistry.observeHistogram(watchWaitDurationMetric, labels, duration.Seconds())
}

// WritePrometheus renders the default registry in Prometheus text format.
func WritePrometheus(w http.ResponseWriter, gauges []GaugeSample) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write(RenderPrometheus(gauges))
}

// RenderPrometheus renders the default registry and dynamic gauges.
func RenderPrometheus(gauges []GaugeSample) []byte {
	return defaultRegistry.RenderPrometheus(gauges)
}

// ResetForTest clears the default registry.
func ResetForTest() {
	defaultRegistry.reset()
}

func (r *Registry) addCounter(name string, labels []Label, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	labels = normalizeLabels(labels)
	key := sampleKey(name, labels)
	sample := r.counters[key]
	if sample.name == "" {
		sample = counterSample{name: name, labels: labels}
	}
	sample.value += delta
	r.counters[key] = sample
}

func (r *Registry) observeHistogram(name string, labels []Label, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	labels = normalizeLabels(labels)
	key := sampleKey(name, labels)
	sample := r.histograms[key]
	if sample == nil {
		buckets := append([]float64(nil), defaultBuckets...)
		sample = &histogramSample{
			name:    name,
			labels:  labels,
			buckets: buckets,
			counts:  make([]uint64, len(buckets)),
		}
		r.histograms[key] = sample
	}
	for i, upper := range sample.buckets {
		if value <= upper {
			sample.counts[i]++
		}
	}
	sample.count++
	sample.sum += value
}

func (r *Registry) RenderPrometheus(gauges []GaugeSample) []byte {
	r.mu.Lock()
	counters := make([]counterSample, 0, len(r.counters))
	for _, sample := range r.counters {
		counters = append(counters, sample)
	}
	histograms := make([]histogramSample, 0, len(r.histograms))
	for _, sample := range r.histograms {
		histograms = append(histograms, histogramSample{
			name:    sample.name,
			labels:  append([]Label(nil), sample.labels...),
			buckets: append([]float64(nil), sample.buckets...),
			counts:  append([]uint64(nil), sample.counts...),
			count:   sample.count,
			sum:     sample.sum,
		})
	}
	r.mu.Unlock()

	sortCounters(counters)
	sortHistograms(histograms)
	gauges = normalizeGauges(gauges)

	var b bytes.Buffer
	for _, d := range descriptors {
		fmt.Fprintf(&b, "# HELP %s %s\n", d.name, d.help)
		fmt.Fprintf(&b, "# TYPE %s %s\n", d.name, d.kind)
		switch d.kind {
		case "counter":
			for _, sample := range counters {
				if sample.name != d.name {
					continue
				}
				writeSample(&b, sample.name, sample.labels, sample.value)
			}
		case "histogram":
			for _, sample := range histograms {
				if sample.name != d.name {
					continue
				}
				writeHistogram(&b, sample)
			}
		case "gauge":
			for _, sample := range gauges {
				if sample.Name != d.name {
					continue
				}
				writeSample(&b, sample.Name, sample.Labels, sample.Value)
			}
		}
	}
	return b.Bytes()
}

func (r *Registry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.counters = map[string]counterSample{}
	r.histograms = map[string]*histogramSample{}
}

func writeHistogram(b *bytes.Buffer, sample histogramSample) {
	for i, upper := range sample.buckets {
		labels := append([]Label(nil), sample.labels...)
		labels = append(labels, Label{Name: "le", Value: formatBucket(upper)})
		writeSample(b, sample.name+"_bucket", normalizeLabels(labels), float64(sample.counts[i]))
	}
	labels := append([]Label(nil), sample.labels...)
	labels = append(labels, Label{Name: "le", Value: "+Inf"})
	writeSample(b, sample.name+"_bucket", normalizeLabels(labels), float64(sample.count))
	writeSample(b, sample.name+"_sum", sample.labels, sample.sum)
	writeSample(b, sample.name+"_count", sample.labels, float64(sample.count))
}

func writeSample(b *bytes.Buffer, name string, labels []Label, value float64) {
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i, label := range labels {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(label.Name)
			b.WriteString("=\"")
			b.WriteString(escapeLabelValue(label.Value))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	b.WriteByte('\n')
}

func normalizeLabels(labels []Label) []Label {
	out := make([]Label, 0, len(labels))
	for _, label := range labels {
		if label.Name == "" {
			continue
		}
		out = append(out, label)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func normalizeGauges(gauges []GaugeSample) []GaugeSample {
	out := make([]GaugeSample, 0, len(gauges))
	for _, sample := range gauges {
		if sample.Name == "" {
			continue
		}
		sample.Labels = normalizeLabels(sample.Labels)
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return labelsKey(out[i].Labels) < labelsKey(out[j].Labels)
	})
	return out
}

func sampleKey(name string, labels []Label) string {
	return name + "{" + labelsKey(labels) + "}"
}

func labelsKey(labels []Label) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, len(labels))
	for i, label := range labels {
		parts[i] = label.Name + "=" + label.Value
	}
	return strings.Join(parts, ",")
}

func sortCounters(samples []counterSample) {
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].name != samples[j].name {
			return samples[i].name < samples[j].name
		}
		return labelsKey(samples[i].labels) < labelsKey(samples[j].labels)
	})
}

func sortHistograms(samples []histogramSample) {
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].name != samples[j].name {
			return samples[i].name < samples[j].name
		}
		return labelsKey(samples[i].labels) < labelsKey(samples[j].labels)
	})
}

func formatBucket(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}
