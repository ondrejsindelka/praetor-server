package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ondrejsindelka/praetor-server/internal/config"
	"github.com/ondrejsindelka/praetor-server/internal/db"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(os.Args[2:])
		return
	}

	cfgPath := flag.String("config", "/etc/praetor/server.yaml", "path to server config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Info("starting praetor-server", "version", version)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	logger.Info("config loaded", "grpc_listen", cfg.GRPCListen, "http_listen", cfg.HTTPListen)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("failed to connect to postgres", "err", err)
		os.Exit(1)
	}
	defer db.Close(pool)

	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		logger.Error("database migration failed", "err", err)
		os.Exit(1)
	}
	logger.Info("database ready, migrations applied")

	// TODO M1.3: start gRPC server on cfg.GRPCListen (Enroll + Connect handlers)
	// TODO M1.3: start HTTP server on cfg.HTTPListen (REST API)
	// TODO M2: initialize VictoriaMetrics writer
	// TODO M2: initialize Loki writer

	<-ctx.Done()
	logger.Info("shutting down praetor-server")
}
