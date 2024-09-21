package exporter

import (
	"context"
	"strconv"
	"time"

	"github.com/efficientgo/core/runutil"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pedro-stanaka/qingping_exporter/pkg/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const DeviceModel = "CGDN1"

type metrics struct {
	temperature *prometheus.GaugeVec
	humidity    *prometheus.GaugeVec
	pm25        *prometheus.GaugeVec
	pm10        *prometheus.GaugeVec
	battery     *prometheus.GaugeVec
	deviceInfo  *prometheus.GaugeVec
	co2         *prometheus.GaugeVec

	syncDuration      *prometheus.HistogramVec
	lastDataTimestamp *prometheus.GaugeVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	temperature := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_temperature",
		Help: "Temperature in degrees Celsius",
	}, []string{"device_mac"})

	humidity := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_humidity",
		Help: "Humidity percentage",
	}, []string{"device_mac"})

	pm25 := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_pm25",
		Help: "PM2.5 concentration in µg/m³",
	}, []string{"device_mac"})

	pm10 := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_pm10",
		Help: "PM10 concentration in µg/m³",
	}, []string{"device_mac"})

	co2 := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_co2",
		Help: "CO2 concentration in ppm",
	}, []string{"device_mac"})

	battery := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_battery",
		Help: "Battery level percentage",
	}, []string{"device_mac"})

	deviceInfo := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "air_monitor_device_info",
		Help: "Device information",
	}, []string{"device_name", "device_mac", "status", "product_name", "product_code", "product_id"})

	lastDataTimestamp := promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "device_last_data_timestamp",
		Help: "Last data timestamp",
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
		temperature: temperature,
		humidity:    humidity,
		pm25:        pm25,
		pm10:        pm10,
		battery:     battery,
		deviceInfo:  deviceInfo,
		co2:         co2,

		syncDuration:      syncDuration,
		lastDataTimestamp: lastDataTimestamp,
	}
}

type exporterOpts struct {
	syncInterval time.Duration
}

var defaultExporterOpts = exporterOpts{
	syncInterval: 30 * time.Second,
}

type Option func(*exporterOpts)

func WithSyncInterval(syncInterval time.Duration) func(*exporterOpts) {
	return func(o *exporterOpts) {
		o.syncInterval = syncInterval
	}
}

// AirMonitorLite is a Qingping air monitor lite exporter.
// It reads all data from API for the device model (CGDN1).
type AirMonitorLite struct {
	client       *client.Client
	reg          prometheus.Registerer
	m            *metrics
	syncInterval time.Duration
	logger       log.Logger
}

func NewAirMonitorLiteExporter(client *client.Client, reg prometheus.Registerer, logger log.Logger, opts ...Option) *AirMonitorLite {
	o := defaultExporterOpts
	for _, opt := range opts {
		opt(&o)
	}

	return &AirMonitorLite{
		client:       client,
		reg:          reg,
		m:            newMetrics(reg),
		syncInterval: o.syncInterval,
		logger:       logger,
	}
}

func (a *AirMonitorLite) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.syncInterval)
	defer ticker.Stop()
	return runutil.Repeat(a.syncInterval, ctx.Done(), a.sync)

}

func (a *AirMonitorLite) sync() error {
	level.Info(a.logger).Log("msg", "starting sync loop")
	defer level.Info(a.logger).Log("msg", "sync loop finished")

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
			a.m.lastDataTimestamp.WithLabelValues(device.Info.MAC).Set(0)
			continue
		}

		latestData := data.Data[len(data.Data)-1]
		a.m.lastDataTimestamp.WithLabelValues(device.Info.MAC).Set(latestData.Timestamp.Value)
		a.m.temperature.WithLabelValues(device.Info.MAC).Set(latestData.Temperature.Value)
		a.m.humidity.WithLabelValues(device.Info.MAC).Set(latestData.Humidity.Value)
		a.m.co2.WithLabelValues(device.Info.MAC).Set(latestData.CO2.Value)
		a.m.pm25.WithLabelValues(device.Info.MAC).Set(latestData.PM25.Value)
		a.m.pm10.WithLabelValues(device.Info.MAC).Set(latestData.PM10.Value)
		a.m.battery.WithLabelValues(device.Info.MAC).Set(latestData.Battery.Value)
	}

	return nil
}

func (a *AirMonitorLite) updateDeviceInfo(device client.Device) {
	status := "online"
	if device.Info.Status.Offline {
		status = "offline"
	}
	a.m.deviceInfo.WithLabelValues(
		device.Info.Name,
		device.Info.MAC,
		status,
		device.Info.Product.EnName,
		device.Info.Product.Code,
		strconv.FormatInt(int64(device.Info.Product.ID), 10),
	).Set(1)
}
