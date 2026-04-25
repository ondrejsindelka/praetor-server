package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/ondrejsindelka/praetor-server/internal/config"
	"github.com/ondrejsindelka/praetor-server/internal/db"
)

// runMigrate handles the `migrate` subcommand.
// Usage: praetor-server migrate [up|down|status] [--config path]
// Flags and the direction argument may appear in any order.
func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/praetor/server.yaml", "path to server config file")

	// Separate flag args from positional args so that the direction word
	// (e.g. "up") may appear before or after --config.
	var flagArgs, posArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--config" || a == "-config" {
			flagArgs = append(flagArgs, a)
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else if len(a) > 1 && a[0] == '-' {
			flagArgs = append(flagArgs, a)
		} else {
			posArgs = append(posArgs, a)
		}
	}

	if err := fs.Parse(flagArgs); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	direction := "up"
	if len(posArgs) > 0 {
		direction = posArgs[0]
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := db.New(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("failed to connect to postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, direction); err != nil {
		logger.Error("migration failed", "direction", direction, "err", err)
		os.Exit(1)
	}
	logger.Info("migration complete", "direction", direction)
}
