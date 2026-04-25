//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable"
	}
	return dsn
}

func TestMigrateAndStores(t *testing.T) {
	ctx := context.Background()

	pool, err := db.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer pool.Close()

	// Apply migrations
	if err := db.Migrate(ctx, pool, "up"); err != nil {
		t.Fatalf("Migrate up: %v", err)
	}

	t.Run("hosts_upsert_and_get", func(t *testing.T) {
		hs := store.NewHostStore(pool)
		h := &store.Host{
			ID:              "01HZ000000000000000000001",
			Hostname:        "test-host",
			OS:              "linux",
			OSVersion:       "6.1.0",
			Kernel:          "6.1.0-amd64",
			Arch:            "amd64",
			CPUCores:        4,
			MemoryBytes:     8 * 1024 * 1024 * 1024,
			IPAddresses:     json.RawMessage(`["192.168.1.1"]`),
			Labels:          json.RawMessage(`{"env":"test"}`),
			FirstSeenAt:     time.Now().UTC().Truncate(time.Microsecond),
			LastHeartbeatAt: time.Now().UTC().Truncate(time.Microsecond),
			Status:          "pending",
			AgentVersion:    "0.0.1",
			OrgID:           "default",
		}

		if err := hs.Upsert(ctx, h); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		got, err := hs.GetByID(ctx, h.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Hostname != h.Hostname {
			t.Errorf("hostname: got %q, want %q", got.Hostname, h.Hostname)
		}

		// Cleanup
		pool.Exec(ctx, "DELETE FROM hosts WHERE id = $1", h.ID)
	})

	t.Run("enrollment_token_lifecycle", func(t *testing.T) {
		ts := store.NewTokenStore(pool)
		tok := &store.EnrollmentToken{
			ID:        "01HZ000000000000000000002",
			TokenHash: "deadbeef1234",
			Label:     "test-token",
			OrgID:     "default",
			CreatedBy: "test",
			CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		}

		if err := ts.Insert(ctx, tok); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		got, err := ts.GetActiveByID(ctx, tok.ID)
		if err != nil {
			t.Fatalf("GetActiveByID: %v", err)
		}
		if got.Label != tok.Label {
			t.Errorf("label: got %q, want %q", got.Label, tok.Label)
		}

		// Revoke
		if err := ts.Revoke(ctx, tok.ID); err != nil {
			t.Fatalf("Revoke: %v", err)
		}

		// Should no longer be found as active
		_, err = ts.GetActiveByID(ctx, tok.ID)
		if err == nil {
			t.Error("expected error after revocation, got nil")
		}

		// Cleanup
		pool.Exec(ctx, "DELETE FROM enrollment_tokens WHERE id = $1", tok.ID)
	})
}
