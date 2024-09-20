package main

import (
	"os"

	"github.com/alecthomas/kingpin"
	"github.com/go-kit/log"
	"github.com/pedro-stanaka/qingping_exporter/pkg/client"
	"github.com/prometheus/client_golang/prometheus"
)

type actionFunc func(*prometheus.Registry, log.Logger) error

type cmdsConfig struct {
	cmdAction map[string]actionFunc
}

var apiConfig = &client.APIConfig{}

func main() {
	app := kingpin.New("qingping_exporter", "A simple CLI application.")
	kingpin.Version("1.0.0")
	kingpin.HelpFlag.Short('h')

	cfg := &cmdsConfig{
		cmdAction: make(map[string]actionFunc),
	}

	apiConfig.BindFlags(app)

	registerListCommand(app, cfg)
	registerRunCommand(app, cfg)

	cmd, err := app.Parse(os.Args[1:])

	if err != nil {
		kingpin.Fatalf("error: %s", err)
	}

	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	reg := prometheus.NewRegistry()

	if err := cfg.cmdAction[cmd](reg, logger); err != nil {
		kingpin.Fatalf("error: %s", err)
	}

	os.Exit(0)
}
