//go:build integration

package staleness_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/staleness"
)

func integTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	if dsn := os.Getenv("PRAETOR_TEST_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable"
}

func TestRunOnce_StaleHostMarkedOffline(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Connect(ctx, integTestDSN(t))
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	defer func() {
		pool.Exec(ctx, "DELETE FROM hosts WHERE id IN ('staleness-test-stale-01','staleness-test-fresh-01')")
		db.Close(pool)
	}()

	// Step 1: Insert a stale host — last heartbeat 3 minutes ago.
	staleID := "staleness-test-stale-01"
	staleAt := time.Now().UTC().Add(-3 * time.Minute)
	_, err = pool.Exec(ctx, `
		INSERT INTO hosts (id, hostname, os, arch, status, last_heartbeat_at, org_id)
		VALUES ($1, 'staleness-test-stale', 'linux', 'amd64', 'online', $2, 'default')
		ON CONFLICT (id) DO UPDATE SET status='online', last_heartbeat_at=$2
	`, staleID, staleAt)
	if err != nil {
		t.Fatalf("insert stale host: %v", err)
	}

	// Step 2: Run the staleness sweep.
	if err := staleness.RunOnce(ctx, pool, slog.Default(), ""); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Step 3: Assert the stale host is now offline.
	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM hosts WHERE id=$1", staleID).Scan(&status); err != nil {
		t.Fatalf("query stale host: %v", err)
	}
	if status != "offline" {
		t.Errorf("stale host: expected status='offline', got %q", status)
	}
}

func TestRunOnce_FreshHostRemainsOnline(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Connect(ctx, integTestDSN(t))
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	defer func() {
		pool.Exec(ctx, "DELETE FROM hosts WHERE id='staleness-test-fresh-01'")
		db.Close(pool)
	}()

	// Step 4: Insert a fresh host — last heartbeat 30 seconds ago (within timeout).
	freshID := "staleness-test-fresh-01"
	freshAt := time.Now().UTC().Add(-30 * time.Second)
	_, err = pool.Exec(ctx, `
		INSERT INTO hosts (id, hostname, os, arch, status, last_heartbeat_at, org_id)
		VALUES ($1, 'staleness-test-fresh', 'linux', 'amd64', 'online', $2, 'default')
		ON CONFLICT (id) DO UPDATE SET status='online', last_heartbeat_at=$2
	`, freshID, freshAt)
	if err != nil {
		t.Fatalf("insert fresh host: %v", err)
	}

	// Step 5: Run the staleness sweep.
	if err := staleness.RunOnce(ctx, pool, slog.Default(), ""); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Step 6: Assert the fresh host is still online.
	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM hosts WHERE id=$1", freshID).Scan(&status); err != nil {
		t.Fatalf("query fresh host: %v", err)
	}
	if status != "online" {
		t.Errorf("fresh host: expected status='online', got %q", status)
	}
}
