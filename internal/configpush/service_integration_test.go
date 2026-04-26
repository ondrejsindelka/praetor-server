//go:build integration

package configpush_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/ondrejsindelka/praetor-server/internal/configpush"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"github.com/ondrejsindelka/praetor-server/internal/stream"
)

func testDSN(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("TEST_POSTGRES_DSN"); v != "" {
		return v
	}
	return "postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable"
}

func TestConfigSetAndGet(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Connect(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close(pool)
	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cs := store.NewConfigStore(pool)
	hostID := "01TEST-CONFIG-M25-0001"

	// Ensure host row exists (FK requirement).
	pool.Exec(ctx, `
		INSERT INTO hosts (id, hostname, os, arch, org_id, status)
		VALUES ($1, 'test-config-host', 'linux', 'amd64', 'test', 'online')
		ON CONFLICT (id) DO NOTHING
	`, hostID)
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM hosts WHERE id = $1", hostID)
	})

	// Default (no row)
	cfg, err := cs.Get(ctx, hostID)
	if err != nil {
		t.Fatalf("Get default: %v", err)
	}
	if cfg.HeartbeatIntervalSeconds != 30 {
		t.Errorf("default heartbeat = %d, want 30", cfg.HeartbeatIntervalSeconds)
	}

	// SetField
	if err := cs.SetField(ctx, hostID, "heartbeat_interval_seconds", 60); err != nil {
		t.Fatalf("SetField: %v", err)
	}

	cfg2, err := cs.Get(ctx, hostID)
	if err != nil {
		t.Fatalf("Get after set: %v", err)
	}
	if cfg2.HeartbeatIntervalSeconds != 60 {
		t.Errorf("heartbeat = %d, want 60", cfg2.HeartbeatIntervalSeconds)
	}
	if cfg2.ConfigVersion != 1 {
		t.Errorf("config_version = %d, want 1", cfg2.ConfigVersion)
	}
}

func TestPushToUnconnectedHostIsNoOp(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Connect(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close(pool)
	db.Migrate(ctx, pool, db.Migrations)

	svc := configpush.New(
		store.NewConfigStore(pool),
		stream.NewRegistry(),
		slog.Default(),
	)
	if err := svc.Push(ctx, "nonexistent-host-id"); err != nil {
		t.Errorf("Push to unconnected: %v", err)
	}
}
