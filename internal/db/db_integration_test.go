//go:build integration

package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PRAETOR_TEST_DSN")
	if dsn == "" {
		t.Skip("PRAETOR_TEST_DSN not set — skipping integration test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	t.Cleanup(func() { db.Close(pool) })
	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return pool
}

func TestMigrateAndStores(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("hosts_upsert_and_get", func(t *testing.T) {
		hs := store.NewHostStore(pool)
		osVer := "6.1.0"
		h := &store.Host{
			ID:          "01HZ000000000000000000001",
			Hostname:    "test-host",
			OS:          "linux",
			OSVersion:   &osVer,
			Arch:        "amd64",
			IPAddresses: []string{"192.168.1.1"},
			Labels:      map[string]string{"env": "test"},
			Status:      "pending",
			OrgID:       "default",
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
		if len(got.IPAddresses) != 1 || got.IPAddresses[0] != "192.168.1.1" {
			t.Errorf("ip_addresses: got %v", got.IPAddresses)
		}

		pool.Exec(ctx, "DELETE FROM hosts WHERE id = $1", h.ID)
	})

	t.Run("enrollment_token_lifecycle", func(t *testing.T) {
		ts := store.NewTokenStore(pool)
		label := "test-token"
		tok := &store.EnrollmentToken{
			ID:        "01HZ000000000000000000002",
			TokenHash: "deadbeef1234",
			Label:     &label,
			OrgID:     "default",
			ExpiresAt: time.Now().Add(15 * time.Minute),
		}

		if err := ts.Insert(ctx, tok); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		got, err := ts.GetByID(ctx, tok.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Label == nil || *got.Label != label {
			t.Errorf("label: got %v, want %q", got.Label, label)
		}

		if err := ts.Revoke(ctx, tok.ID); err != nil {
			t.Fatalf("Revoke: %v", err)
		}

		// GetByID still finds it even after revocation
		revoked, err := ts.GetByID(ctx, tok.ID)
		if err != nil {
			t.Fatalf("GetByID after revoke: %v", err)
		}
		if revoked.RevokedAt == nil {
			t.Error("expected revoked_at to be set")
		}

		pool.Exec(ctx, "DELETE FROM enrollment_tokens WHERE id = $1", tok.ID)
	})
}
