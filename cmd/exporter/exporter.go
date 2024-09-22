package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kingpin"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/thanos-io/thanos/pkg/component"
	"github.com/thanos-io/thanos/pkg/prober"
	"github.com/thanos-io/thanos/pkg/server/http"

	"github.com/pedro-stanaka/qingping_exporter/pkg/client"
	"github.com/pedro-stanaka/qingping_exporter/pkg/exporter"
)

func registerRunCommand(app *kingpin.Application, cfg *cmdsConfig) {
	cmd := app.Command("run", "Run the air monitor lite exporter.")

	listenAddr := cmd.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").
		Default(":10803").String()

	cfg.cmdAction[cmd.FullCommand()] = func(reg *prometheus.Registry, logger log.Logger) error {
		// setup client
		c := client.New(apiConfig, client.WithRegistry(reg))

		// create exporter
		exp := exporter.NewAirMonitorLiteExporter(c, reg, logger)

		g := &run.Group{}

		// setup context with cancel
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// handle termination signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			level.Info(logger).Log("msg", "received termination signal, shutting down")
			cancel()
		}()

		// run exporter
		g.Add(func() error {
			return exp.Run(ctx)
		}, func(_ error) {
			cancel()
		})

		// run prometheus HTTP server
		// with instrumentation
		// and using reg as the registry
		readyProbe := prober.NewHTTP()
		httpSrv := http.New(logger, reg, component.Debug, readyProbe, http.WithListen(*listenAddr))

		g.Add(func() error {
			readyProbe.Ready()
			readyProbe.Healthy()
			return httpSrv.ListenAndServe()
		}, func(err error) {
			httpSrv.Shutdown(err)
		})

		return g.Run()
	}
}
