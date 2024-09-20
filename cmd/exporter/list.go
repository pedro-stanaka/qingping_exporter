package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/pedro-stanaka/qingping_exporter/pkg/client"
)

func registerListCommand(app *kingpin.Application, cfg *cmdsConfig) {
	cmd := app.Command("devices", "List all devices.")

	cfg.cmdAction[cmd.FullCommand()] = func(reg *prometheus.Registry, logger log.Logger) error {
		// setup client
		// call client.ListDevices()
		c := client.New(apiConfig, client.WithRegistry(reg))

		devices, err := c.GetDeviceList()
		if err != nil {
			level.Error(logger).Log("msg", "failed to get device list", "err", err)
			return err
		}

		fmt.Println("Devices:")
		for _, device := range devices.Devices {
			fmt.Print("\t- ")
			device.PrettyPrint(os.Stdout)
			fmt.Println()
		}
		return nil
	}
}
