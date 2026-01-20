package exporter

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimestampedCollector_AddSample(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := NewTimestampedCollector(time.Minute, reg)
	collector.RegisterMetric("test_metric", "Test metric", []string{"label1"})

	// Add a sample
	collector.AddSample("test_metric", []string{"value1"}, 42.0, 1000)

	// Verify sample is in buffer
	collector.mu.RLock()
	key := seriesKey{name: "test_metric", labels: "value1"}
	samples := collector.samples[key]
	collector.mu.RUnlock()

	require.Len(t, samples, 1)
	assert.Equal(t, 42.0, samples[0].value)
	assert.Equal(t, int64(1000), samples[0].timestamp)
}

func TestTimestampedCollector_DuplicateTimestamp(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := NewTimestampedCollector(time.Minute, reg)
	collector.RegisterMetric("test_metric", "Test metric", []string{"label1"})

	// Add first sample
	collector.AddSample("test_metric", []string{"value1"}, 42.0, 1000)

	// Add duplicate timestamp with different value
	collector.AddSample("test_metric", []string{"value1"}, 99.0, 1000)

	// Verify only first sample is kept
	collector.mu.RLock()
	key := seriesKey{name: "test_metric", labels: "value1"}
	samples := collector.samples[key]
	collector.mu.RUnlock()

	require.Len(t, samples, 1)
	assert.Equal(t, 42.0, samples[0].value) // First value kept
}

func TestTimestampedCollector_Prune(t *testing.T) {
	reg := prometheus.NewRegistry()
	retention := 100 * time.Millisecond
	collector := NewTimestampedCollector(retention, reg)
	collector.RegisterMetric("test_metric", "Test metric", []string{"label1"})

	now := time.Now()

	// Add old sample (should be pruned)
	oldTs := now.Add(-200 * time.Millisecond).UnixMilli()
	collector.AddSample("test_metric", []string{"value1"}, 1.0, oldTs)

	// Add recent sample (should be kept)
	recentTs := now.UnixMilli()
	collector.AddSample("test_metric", []string{"value1"}, 2.0, recentTs)

	// Prune
	collector.Prune()

	// Verify only recent sample remains
	collector.mu.RLock()
	key := seriesKey{name: "test_metric", labels: "value1"}
	samples := collector.samples[key]
	collector.mu.RUnlock()

	require.Len(t, samples, 1)
	assert.Equal(t, 2.0, samples[0].value)
}

func TestTimestampedCollector_CollectWithFixedTimestamps(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := NewTimestampedCollector(time.Minute, reg)
	collector.RegisterMetric("test_metric", "Test metric", []string{"device"})

	// Add multiple samples
	collector.AddSample("test_metric", []string{"device1"}, 10.0, 1000)
	collector.AddSample("test_metric", []string{"device1"}, 20.0, 2000)
	collector.AddSample("test_metric", []string{"device1"}, 30.0, 3000)

	// Collect with fixed timestamps
	ch := make(chan prometheus.Metric, 10)
	go func() {
		collector.Collect(ch, true)
		close(ch)
	}()

	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}

	// Should have 3 samples with timestamps
	require.Len(t, metrics, 3)

	// Verify timestamps are present and in order
	var timestamps []int64
	for _, m := range metrics {
		var dto dto.Metric
		err := m.Write(&dto)
		require.NoError(t, err)
		require.NotNil(t, dto.TimestampMs)
		timestamps = append(timestamps, *dto.TimestampMs)
	}

	assert.Equal(t, []int64{1000, 2000, 3000}, timestamps)
}

func TestTimestampedCollector_CollectWithoutFixedTimestamps(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := NewTimestampedCollector(time.Minute, reg)
	collector.RegisterMetric("test_metric", "Test metric", []string{"device"})

	// Add multiple samples
	collector.AddSample("test_metric", []string{"device1"}, 10.0, 1000)
	collector.AddSample("test_metric", []string{"device1"}, 20.0, 2000)
	collector.AddSample("test_metric", []string{"device1"}, 30.0, 3000)

	// Collect without fixed timestamps (default mode)
	ch := make(chan prometheus.Metric, 10)
	go func() {
		collector.Collect(ch, false)
		close(ch)
	}()

	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}

	// Should have only 1 sample (latest)
	require.Len(t, metrics, 1)

	// Verify no timestamp and value is latest
	var dto dto.Metric
	err := metrics[0].Write(&dto)
	require.NoError(t, err)
	assert.Nil(t, dto.TimestampMs) // No timestamp in default mode
	assert.Equal(t, 30.0, *dto.Gauge.Value)
}

func TestTimestampedCollector_OutOfOrderSamples(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := NewTimestampedCollector(time.Minute, reg)
	collector.RegisterMetric("test_metric", "Test metric", []string{"device"})

	// Add samples in order
	collector.AddSample("test_metric", []string{"device1"}, 10.0, 1000)
	collector.AddSample("test_metric", []string{"device1"}, 20.0, 2000)

	// First collect - should emit both
	ch := make(chan prometheus.Metric, 10)
	collector.Collect(ch, true)
	close(ch)

	var count1 int
	for range ch {
		count1++
	}
	assert.Equal(t, 2, count1)

	// Add more samples including one that would be out of order
	collector.AddSample("test_metric", []string{"device1"}, 15.0, 1500) // Out of order (before 2000)
	collector.AddSample("test_metric", []string{"device1"}, 30.0, 3000) // In order

	// Second collect - should only emit the new in-order sample
	ch2 := make(chan prometheus.Metric, 10)
	collector.Collect(ch2, true)
	close(ch2)

	var count2 int
	var values []float64
	for m := range ch2 {
		count2++
		var dto dto.Metric
		_ = m.Write(&dto)
		values = append(values, *dto.Gauge.Value)
	}

	// Only the sample at ts=3000 should be emitted (ts=1500 is out of order)
	assert.Equal(t, 1, count2)
	assert.Equal(t, []float64{30.0}, values)
}

func TestTimestampedCollector_MultipleDevices(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := NewTimestampedCollector(time.Minute, reg)
	collector.RegisterMetric("test_metric", "Test metric", []string{"device"})

	// Add samples for different devices
	collector.AddSample("test_metric", []string{"device1"}, 10.0, 1000)
	collector.AddSample("test_metric", []string{"device2"}, 20.0, 1000)
	collector.AddSample("test_metric", []string{"device1"}, 30.0, 2000)
	collector.AddSample("test_metric", []string{"device2"}, 40.0, 2000)

	// Collect without fixed timestamps
	ch := make(chan prometheus.Metric, 10)
	go func() {
		collector.Collect(ch, false)
		close(ch)
	}()

	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}

	// Should have 2 samples (latest for each device)
	require.Len(t, metrics, 2)

	// Verify values are the latest for each device
	values := make(map[string]float64)
	for _, m := range metrics {
		var dto dto.Metric
		err := m.Write(&dto)
		require.NoError(t, err)
		// Extract device label
		for _, lp := range dto.Label {
			if *lp.Name == "device" {
				values[*lp.Value] = *dto.Gauge.Value
			}
		}
	}

	assert.Equal(t, 30.0, values["device1"])
	assert.Equal(t, 40.0, values["device2"])
}

func TestBuildLabelKey(t *testing.T) {
	tests := []struct {
		name     string
		labels   []string
		expected string
	}{
		{"empty", []string{}, ""},
		{"single", []string{"a"}, "a"},
		{"multiple", []string{"a", "b", "c"}, "a\x00b\x00c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildLabelKey(tt.labels)
			assert.Equal(t, tt.expected, result)
		})
	}
}
