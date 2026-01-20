package exporter

import (
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// timestampedSample represents a single metric sample with its original timestamp.
type timestampedSample struct {
	value     float64
	timestamp int64 // Unix milliseconds
}

// seriesKey uniquely identifies a time series (metric name + label values).
type seriesKey struct {
	name   string
	labels string // Concatenated label values for uniqueness
}

// TimestampedCollector implements prometheus.Collector and maintains a rolling buffer
// of samples. It can emit samples either with their original timestamps (fixed timestamp mode)
// or just the latest value per series (default mode).
//
// The buffer maintains a rolling window of samples, pruning old samples based on
// the configured retention period (typically 3x sync interval).
type TimestampedCollector struct {
	mu sync.RWMutex

	// samples stores timestamped samples per series, keyed by metric name + labels
	samples map[seriesKey][]timestampedSample

	// lastEmittedTs tracks the last emitted timestamp per series to avoid out-of-order issues
	lastEmittedTs map[seriesKey]int64

	// descs holds the metric descriptors
	descs map[string]*prometheus.Desc

	// labelNames holds the label names for each metric
	labelNames map[string][]string

	// labelValues stores the label values for each series key
	labelValues map[seriesKey][]string

	// retention is how long to keep samples in the buffer
	retention time.Duration

	// Internal metrics
	samplesBuffered      prometheus.Gauge
	samplesDroppedOOO    prometheus.Counter
	samplesDroppedDup    prometheus.Counter
}

// NewTimestampedCollector creates a new TimestampedCollector with the given retention period.
// The retention period should typically be 3x the sync interval.
func NewTimestampedCollector(retention time.Duration, reg prometheus.Registerer) *TimestampedCollector {
	c := &TimestampedCollector{
		samples:       make(map[seriesKey][]timestampedSample),
		lastEmittedTs: make(map[seriesKey]int64),
		descs:         make(map[string]*prometheus.Desc),
		labelNames:    make(map[string][]string),
		labelValues:   make(map[seriesKey][]string),
		retention:     retention,
		samplesBuffered: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "qingping_exporter_samples_buffered",
			Help: "Current number of samples in the buffer",
		}),
		samplesDroppedOOO: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "qingping_exporter_samples_dropped_out_of_order_total",
			Help: "Total number of samples dropped due to out-of-order timestamps",
		}),
		samplesDroppedDup: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "qingping_exporter_samples_dropped_duplicate_total",
			Help: "Total number of samples dropped due to duplicate timestamps",
		}),
	}

	// Register internal metrics
	reg.MustRegister(c.samplesBuffered)
	reg.MustRegister(c.samplesDroppedOOO)
	reg.MustRegister(c.samplesDroppedDup)

	return c
}

// RegisterMetric registers a metric with its descriptor and label names.
// This must be called before adding samples for that metric.
func (c *TimestampedCollector) RegisterMetric(name, help string, labelNames []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.descs[name] = prometheus.NewDesc(name, help, labelNames, nil)
	c.labelNames[name] = labelNames
}

// AddSample adds a sample to the buffer. The timestamp should be in Unix milliseconds.
// Samples with duplicate timestamps (same series + timestamp) are dropped.
func (c *TimestampedCollector) AddSample(name string, labelValues []string, value float64, timestampMs int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build series key
	key := seriesKey{
		name:   name,
		labels: buildLabelKey(labelValues),
	}

	// Store label values for this series
	c.labelValues[key] = labelValues

	// Check for duplicate timestamp
	for _, s := range c.samples[key] {
		if s.timestamp == timestampMs {
			c.samplesDroppedDup.Inc()
			return
		}
	}

	// Add sample
	c.samples[key] = append(c.samples[key], timestampedSample{
		value:     value,
		timestamp: timestampMs,
	})
}

// Prune removes samples older than the retention period.
// This should be called periodically (e.g., after each sync).
func (c *TimestampedCollector) Prune() {
	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-c.retention).UnixMilli()
	totalSamples := 0

	for key, samples := range c.samples {
		// Filter out old samples
		filtered := samples[:0]
		for _, s := range samples {
			if s.timestamp >= cutoff {
				filtered = append(filtered, s)
			}
		}
		c.samples[key] = filtered
		totalSamples += len(filtered)

		// Clean up empty series
		if len(filtered) == 0 {
			delete(c.samples, key)
			delete(c.labelValues, key)
		}
	}

	c.samplesBuffered.Set(float64(totalSamples))
}

// Describe implements prometheus.Collector.
func (c *TimestampedCollector) Describe(ch chan<- *prometheus.Desc) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, desc := range c.descs {
		ch <- desc
	}
}

// Collect implements prometheus.Collector.
// When useFixedTimestamps is true, it emits all buffered samples with their original timestamps.
// When false, it emits only the latest sample per series without a timestamp.
func (c *TimestampedCollector) Collect(ch chan<- prometheus.Metric, useFixedTimestamps bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, samples := range c.samples {
		if len(samples) == 0 {
			continue
		}

		desc, ok := c.descs[key.name]
		if !ok {
			continue
		}

		labelVals := c.labelValues[key]

		// Sort samples by timestamp
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].timestamp < samples[j].timestamp
		})

		if useFixedTimestamps {
			// Emit all samples with their original timestamps
			for _, s := range samples {
				// Skip out-of-order samples
				if s.timestamp <= c.lastEmittedTs[key] {
					c.samplesDroppedOOO.Inc()
					continue
				}

				metric, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, s.value, labelVals...)
				if err != nil {
					continue
				}

				// Add timestamp (convert from ms to time.Time)
				ts := time.UnixMilli(s.timestamp)
				ch <- prometheus.NewMetricWithTimestamp(ts, metric)

				c.lastEmittedTs[key] = s.timestamp
			}
		} else {
			// Emit only the latest sample without timestamp
			latest := samples[len(samples)-1]
			metric, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, latest.value, labelVals...)
			if err != nil {
				continue
			}
			ch <- metric
		}
	}
}

// buildLabelKey creates a unique string key from label values.
func buildLabelKey(labelValues []string) string {
	if len(labelValues) == 0 {
		return ""
	}
	if len(labelValues) == 1 {
		return labelValues[0]
	}

	// Join with a separator that won't appear in label values
	result := labelValues[0]
	for i := 1; i < len(labelValues); i++ {
		result += "\x00" + labelValues[i]
	}
	return result
}
