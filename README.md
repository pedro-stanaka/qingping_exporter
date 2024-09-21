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
docker run QINGPING_APP_KEY=your_app_key QINGPING_APP_SECRET=your_app_secret ghcr.io/pedro-stanaka/qingping_exporter:latest
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