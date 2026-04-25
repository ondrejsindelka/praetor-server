package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ondrejsindelka/praetor-server/internal/config"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/praetor/server.yaml", "path to server config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Info("starting praetor-server", "version", version)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	logger.Info("config loaded",
		"grpc_listen", cfg.GRPCListen,
		"http_listen", cfg.HTTPListen,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// TODO M1: open Postgres connection pool from cfg.PostgresDSN
	// TODO M1: initialize VictoriaMetrics writer from cfg.VictoriaMetricsURL
	// TODO M1: initialize Loki writer from cfg.LokiURL
	// TODO M1: start gRPC server on cfg.GRPCListen (Enroll + Connect handlers)
	// TODO M1: start HTTP server on cfg.HTTPListen (REST API)

	<-ctx.Done()
	logger.Info("shutting down praetor-server")
}
