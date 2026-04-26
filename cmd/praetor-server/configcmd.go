package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ondrejsindelka/praetor-server/internal/config"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

func runConfigCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: praetor-server config <set|get>")
		os.Exit(1)
	}
	switch args[0] {
	case "set":
		runConfigSet(args[1:])
	case "get":
		runConfigGet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// runConfigSet: praetor-server config set <host_id> <key>=<value> --config ...
func runConfigSet(args []string) {
	fs := flag.NewFlagSet("config set", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/praetor/server.yaml", "server config path")
	_ = fs.Parse(args)

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: praetor-server config set <host_id> <key>=<value>")
		os.Exit(1)
	}
	hostID := fs.Arg(0)
	parts := strings.SplitN(fs.Arg(1), "=", 2)
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "error: expected key=value")
		os.Exit(1)
	}
	key, valueStr := parts[0], parts[1]
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: value must be integer: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, dbErr := db.Connect(ctx, cfg.PostgresDSN)
	if dbErr != nil {
		fmt.Fprintf(os.Stderr, "db error: %v\n", dbErr)
		os.Exit(1)
	}
	defer db.Close(pool)

	cs := store.NewConfigStore(pool)
	if err := cs.SetField(ctx, hostID, key, value); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Config updated: host=%s  %s=%d  (will push on next agent heartbeat or connect)\n", hostID, key, value)
}

func runConfigGet(args []string) {
	fs := flag.NewFlagSet("config get", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/praetor/server.yaml", "server config path")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: praetor-server config get <host_id>")
		os.Exit(1)
	}
	hostID := fs.Arg(0)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, dbErr := db.Connect(ctx, cfg.PostgresDSN)
	if dbErr != nil {
		fmt.Fprintf(os.Stderr, "db error: %v\n", dbErr)
		os.Exit(1)
	}
	defer db.Close(pool)

	cs := store.NewConfigStore(pool)
	hcfg, err := cs.Get(ctx, hostID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("host_id:                           %s\n", hcfg.HostID)
	fmt.Printf("config_version:                    %d\n", hcfg.ConfigVersion)
	fmt.Printf("heartbeat_interval_seconds:         %d\n", hcfg.HeartbeatIntervalSeconds)
	fmt.Printf("metric_collection_interval_seconds: %d\n", hcfg.MetricCollectionIntervalSeconds)
	fmt.Printf("log_sources:                       %v\n", hcfg.LogSources)
}
