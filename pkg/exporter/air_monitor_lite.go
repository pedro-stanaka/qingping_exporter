package exporter

import (
	"context"
	"strconv"
	"time"

	"github.com/efficientgo/core/runutil"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/pedro-stanaka/qingping_exporter/pkg/client"
)

const DeviceModel = "CGDN1"

// Metric names for air monitor data
const (
	metricTemperature = "air_monitor_temperature"
	metricHumidity    = "air_monitor_humidity"
	metricCO2         = "air_monitor_co2"
	metricPM25        = "air_monitor_pm25"
	metricPM10        = "air_monitor_pm10"
	metricBattery     = "air_monitor_battery"
)

// metrics holds metrics that don't need fixed timestamps
type metrics struct {
	deviceInfo        *prometheus.GaugeVec
	syncDuration      *prometheus.HistogramVec
	lastDataTimestamp *prometheus.GaugeVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	deviceInfo := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_device_info",
		Help: "Device information",
	}, []string{"device_name", "device_mac", "status", "product_name", "product_code", "product_id"})

	lastDataTimestamp := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "device_last_data_timestamp",
		Help: "Last data timestamp from the API (Unix seconds). Use time() - device_last_data_timestamp to calculate staleness.",
	}, []string{"device_mac"})

	syncDuration := promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
		Name:                            "air_monitor_sync_duration_seconds",
		Help:                            "Duration of the sync request",
		Buckets:                         prometheus.DefBuckets,
		NativeHistogramBucketFactor:     1.1,
		NativeHistogramMaxBucketNumber:  200,
		NativeHistogramMinResetDuration: 10 * time.Minute,
	}, []string{"phase"})

	return &metrics{
		deviceInfo:        deviceInfo,
		syncDuration:      syncDuration,
		lastDataTimestamp: lastDataTimestamp,
	}
}

type exporterOpts struct {
	syncInterval       time.Duration
	useFixedTimestamps bool
	bufferWindow       time.Duration
}

var defaultExporterOpts = exporterOpts{
	syncInterval:       5 * time.Minute, // Default to 5 minutes to reduce duplicate samples from API
	useFixedTimestamps: false,
	bufferWindow:       60 * time.Minute, // Default to 60 minutes to account for API delays and outages
}

type Option func(*exporterOpts)

func WithSyncInterval(syncInterval time.Duration) Option {
	return func(o *exporterOpts) {
		o.syncInterval = syncInterval
	}
}

// WithFixedTimestamps enables emitting metrics with their original API timestamps.
// When enabled, all buffered samples are emitted with their original timestamps.
// When disabled (default), only the latest sample per series is emitted without a timestamp.
func WithFixedTimestamps() Option {
	return func(o *exporterOpts) {
		o.useFixedTimestamps = true
	}
}

// WithBufferWindow sets how long to retain samples in the buffer.
// Longer windows help survive API outages but require larger Prometheus out_of_order_time_window.
// Default is 5 minutes.
func WithBufferWindow(d time.Duration) Option {
	return func(o *exporterOpts) {
		o.bufferWindow = d
	}
}

// AirMonitorLite is a Qingping air monitor lite exporter.
// It reads all data from API for the device model (CGDN1).
//
// It implements prometheus.Collector to support both regular gauge mode
// and fixed timestamp mode via the TimestampedCollector.
type AirMonitorLite struct {
	client             *client.Client
	reg                prometheus.Registerer
	m                  *metrics
	tsCollector        *TimestampedCollector
	syncInterval       time.Duration
	useFixedTimestamps bool
	logger             log.Logger
}

// NewAirMonitorLiteExporter creates a new AirMonitorLite exporter.
func NewAirMonitorLiteExporter(client *client.Client, reg prometheus.Registerer, logger log.Logger, opts ...Option) *AirMonitorLite {
	o := defaultExporterOpts
	for _, opt := range opts {
		opt(&o)
	}

	// Create timestamped collector with configured buffer window
	tsCollector := NewTimestampedCollector(o.bufferWindow, reg)

	// Register metric descriptors
	tsCollector.RegisterMetric(metricTemperature, "Temperature in degrees Celsius", []string{"device_mac"})
	tsCollector.RegisterMetric(metricHumidity, "Humidity percentage", []string{"device_mac"})
	tsCollector.RegisterMetric(metricCO2, "CO2 concentration in ppm", []string{"device_mac"})
	tsCollector.RegisterMetric(metricPM25, "PM2.5 concentration in µg/m³", []string{"device_mac"})
	tsCollector.RegisterMetric(metricPM10, "PM10 concentration in µg/m³", []string{"device_mac"})
	tsCollector.RegisterMetric(metricBattery, "Battery level percentage", []string{"device_mac"})

	a := &AirMonitorLite{
		client:             client,
		reg:                reg,
		m:                  newMetrics(reg),
		tsCollector:        tsCollector,
		syncInterval:       o.syncInterval,
		useFixedTimestamps: o.useFixedTimestamps,
		logger:             logger,
	}

	// Register self as a collector (for the timestamped metrics)
	reg.MustRegister(a)

	return a
}

// Describe implements prometheus.Collector.
func (a *AirMonitorLite) Describe(ch chan<- *prometheus.Desc) {
	a.tsCollector.Describe(ch)
}

// Collect implements prometheus.Collector.
func (a *AirMonitorLite) Collect(ch chan<- prometheus.Metric) {
	a.tsCollector.Collect(ch, a.useFixedTimestamps)
}

func (a *AirMonitorLite) Run(ctx context.Context) error {
	return runutil.Repeat(a.syncInterval, ctx.Done(), a.sync)
}

func (a *AirMonitorLite) sync() error {
	level.Info(a.logger).Log("msg", "starting sync loop")
	defer level.Info(a.logger).Log("msg", "sync loop finished")

	timer := prometheus.NewTimer(a.m.syncDuration.WithLabelValues("total"))
	defer timer.ObserveDuration()

	devices, err := a.client.GetDeviceList()
	if err != nil {
		level.Error(a.logger).Log("msg", "failed to get device list", "err", err)
		return err
	}
	endTime := time.Now().UTC()
	// TODO: make this a flag
	startTime := endTime.Add(-2 * time.Hour).UTC()

	for _, device := range devices.Devices {
		if device.Info.Product.Code != DeviceModel {
			continue
		}
		a.updateDeviceInfo(device)
		data, err := a.client.GetDataHistory(device.Info.MAC, startTime, endTime)
		if err != nil {
			level.Error(a.logger).Log("msg", "failed to get data history", "mac", device.Info.MAC, "err", err)
			continue
		}

		if len(data.Data) == 0 {
			level.Warn(a.logger).Log(
				"msg", "no data available",
				"mac", device.Info.MAC,
				"name", device.Info.Name,
				"start_time", startTime,
				"end_time", endTime,
			)
			continue
		}

		// Add all data points to the timestamped collector buffer
		for _, d := range data.Data {
			tsMs := int64(d.Timestamp.Value * 1000) // Convert Unix seconds to milliseconds
			labels := []string{device.Info.MAC}

			a.tsCollector.AddSample(metricTemperature, labels, d.Temperature.Value, tsMs)
			a.tsCollector.AddSample(metricHumidity, labels, d.Humidity.Value, tsMs)
			a.tsCollector.AddSample(metricCO2, labels, d.CO2.Value, tsMs)
			a.tsCollector.AddSample(metricPM25, labels, d.PM25.Value, tsMs)
			a.tsCollector.AddSample(metricPM10, labels, d.PM10.Value, tsMs)
			a.tsCollector.AddSample(metricBattery, labels, d.Battery.Value, tsMs)
		}

		// Update staleness metric with latest timestamp
		latestData := data.Data[len(data.Data)-1]
		a.m.lastDataTimestamp.WithLabelValues(device.Info.MAC).Set(latestData.Timestamp.Value)
	}

	// Prune old samples from the buffer
	a.tsCollector.Prune()

	return nil
}

func (a *AirMonitorLite) updateDeviceInfo(device client.Device) {
	status := "online"
	if device.Info.Status.Offline {
		status = "offline"
	}

	value := 1.0
	if device.Info.Status.Offline {
		value = 0.0
	}

	a.m.deviceInfo.WithLabelValues(
		device.Info.Name,
		device.Info.MAC,
		status,
		device.Info.Product.EnName,
		device.Info.Product.Code,
		strconv.FormatInt(int64(device.Info.Product.ID), 10),
	).Set(value)

	a.m.lastDataTimestamp.WithLabelValues(device.Info.MAC).Set(device.Data.Timestamp.Value)
}
