# Qingping Prometheus Exporter

This is a simple Prometheus exporter that collects data from Qingping API and exposes it in a format that Prometheus can
scrape.

## Usage

### Getting credentials

Before using the exporter you will have to create a developer account in the Qingping Cloud.

1. Download the Qingping+ App and create an account.
2. Use your account to create a developer account in the [Qingping Cloud](https://developer.qingping.co/).
3. Go to [your credentials page](https://developer.qingping.co/personal/permissionApply) and take note
   of your AppKey and AppSecret.

### Running the exporter

Once you have the credentials you can run the exporter with docker or docker-compose, using our
pre-built images published to GitHub Container Registry.

```bash
docker run -e QINGPING_APP_KEY=your_app_key -e QINGPING_APP_SECRET=your_app_secret ghcr.io/pedro-stanaka/qingping_exporter:latest
```

Or using docker-compose:

```yaml
version: '3'

services:
  qingping_exporter:
    image: ghcr.io/pedro-stanaka/qingping_exporter:latest
    environment:
      QINGPING_APP_KEY: your_app_key
      QINGPING_APP_SECRET: your_app_secret
```

### Configuration

The exporter can be configured using environment variables or command-line flags. Additionally, it supports loading
configuration from a `.env` file in the current working directory.

#### Using a `.env` file

Copy the provided `.env.template` file to `.env` and fill in your credentials:

```bash
cp .env.template .env
# Edit .env with your credentials
```

Then simply run the exporter:

```bash
./qingping_exporter run
```

#### Environment variables

| Variable              | Description                      | Default                                      | Required |
|-----------------------|----------------------------------|----------------------------------------------|----------|
| `QINGPING_APP_KEY`    | Your Qingping API app key        | -                                            | Yes      |
| `QINGPING_APP_SECRET` | Your Qingping API app secret     | -                                            | Yes      |
| `QINGPING_BASE_URL`   | Base URL of the Qingping API     | `https://apis.cleargrass.com`                | No       |
| `QINGPING_OAUTH_URL`  | OAuth URL of the Qingping API    | `https://oauth.cleargrass.com/oauth2/token`  | No       |

#### Command-line flags

```bash
./qingping_exporter run --help
```

| Flag                     | Description                                           | Default        |
|--------------------------|-------------------------------------------------------|----------------|
| `--web.listen-address`   | Address to listen on for web interface and telemetry  | `:10803`       |
| `--use-fixed-timestamps` | Emit metrics with original API timestamps (see below) | `false`        |
| `--buffer-window`        | How long to retain samples in buffer (e.g., `30m`, `60m`) | `60m`        |
| `--base-url`             | Base URL of the Qingping API                          | (see above)    |
| `--oauth-url`            | OAuth URL of the Qingping API                         | (see above)    |
| `--app-key`              | App key of the Qingping API                           | -              |
| `--app-secret`           | App secret of the Qingping API                        | -              |

### Collected metrics

The exporter collects the following metrics:

| **Metric Name**                   | **Type**  | **Labels**                                                                   | **Description**                |
|-----------------------------------|-----------|------------------------------------------------------------------------------|--------------------------------|
| air_monitor_temperature           | Gauge     | device\_mac                                                                  | Temperature in degrees Celsius |
| air_monitor_humidity              | Gauge     | device\_mac                                                                  | Humidity percentage            |
| air_monitor_pm25                  | Gauge     | device\_mac                                                                  | PM2.5 concentration in µg/m³   |
| air_monitor_pm10                  | Gauge     | device\_mac                                                                  | PM10 concentration in µg/m³    |
| air_monitor_co2                   | Gauge     | device\_mac                                                                  | CO2 concentration in ppm       |
| air_monitor_battery               | Gauge     | device\_mac                                                                  | Battery level percentage       |
| air_monitor_device_info           | Gauge     | device\_name, device\_mac, status, product\_name, product\_code, product\_id | Device information             |
| device_last_data_timestamp        | Gauge     | device\_mac                                                                  | Last data timestamp            |
| air_monitor_sync_duration_seconds | Histogram | phase                                                                        | Duration of the sync request   |

### Staleness Alerting

The `device_last_data_timestamp` metric contains the Unix timestamp (in seconds) of the most recent data point from the API. You can use this to alert on stale data:

```promql
# Alert if data is older than 1 hour
time() - device_last_data_timestamp{device_mac="XX:XX:XX:XX:XX:XX"} > 3600
```

### Fixed Timestamps Mode

By default, the exporter emits metrics without explicit timestamps, letting Prometheus assign the scrape time. This is the standard behavior for most exporters.

When `--use-fixed-timestamps` is enabled, the exporter emits **all buffered samples** with their **original timestamps** from when the device recorded the measurement. This provides more precise timing data but requires additional Prometheus configuration.

#### How it works

1. The exporter maintains a rolling buffer of samples (configurable via `--buffer-window`, default 60 minutes)
2. On each sync, new samples are added to the buffer and samples older than the buffer window are pruned
3. On each Prometheus scrape:
   - **Default mode**: Returns only the latest value per metric/device (no timestamp)
   - **Fixed timestamps mode**: Returns all buffered samples with their original timestamps

The buffer window determines how long data is retained. A longer window (e.g., `10m` or `15m`) helps survive API outages or network issues, ensuring data isn't lost if the API is temporarily unavailable.

#### When to use fixed timestamps

- When you need precise measurement timing for analysis
- When correlating air quality data with other time-series data
- When the delay between device measurement and scrape time matters

#### Prometheus configuration

When using fixed timestamps, configure Prometheus to accept out-of-order samples. The `out_of_order_time_window` should be at least as large as your `--buffer-window`:

```yaml
storage:
  tsdb:
    out_of_order_time_window: 60m  # Should match or exceed --buffer-window (default: 60m)
```

#### Internal metrics

The exporter exposes metrics about the timestamped collector:

| Metric | Description |
|--------|-------------|
| `qingping_exporter_samples_buffered` | Current number of samples in the buffer |
| `qingping_exporter_samples_dropped_out_of_order_total` | Samples dropped due to out-of-order timestamps |
| `qingping_exporter_samples_dropped_duplicate_total` | Samples dropped due to duplicate timestamps |

#### Important notes

- Prometheus rejects samples with timestamps older than the last ingested sample for that series (unless `out_of_order_time_window` is configured)
- Duplicate timestamps with different values are rejected by Prometheus (first value wins)
- The exporter handles these cases locally to minimize rejected samples