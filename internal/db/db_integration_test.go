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
		t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM hosts WHERE id = $1", h.ID) })

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
		t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM enrollment_tokens WHERE id = $1", tok.ID) })

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
	})

	t.Run("identity_lifecycle", func(t *testing.T) {
		hs := store.NewHostStore(pool)
		is := store.NewIdentityStore(pool)

		osVer := "6.1.0"
		h := &store.Host{
			ID:       "01HZ000000000000000000003",
			Hostname: "identity-test-host",
			OS:       "linux",
			OSVersion: &osVer,
			Arch:     "amd64",
			Status:   "pending",
			OrgID:    "default",
		}
		if err := hs.Upsert(ctx, h); err != nil {
			t.Fatalf("Upsert host: %v", err)
		}
		t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM hosts WHERE id = $1", h.ID) })

		ai := &store.AgentIdentity{
			HostID:          h.ID,
			CertPEM:         "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----",
			CertFingerprint: "aa:bb:cc:dd:ee:ff",
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		if err := is.Insert(ctx, ai); err != nil {
			t.Fatalf("Insert identity: %v", err)
		}
		if ai.ID == 0 {
			t.Error("expected ai.ID to be set after Insert")
		}
		if ai.IssuedAt.IsZero() {
			t.Error("expected ai.IssuedAt to be set after Insert")
		}

		byFP, err := is.GetByCertFingerprint(ctx, ai.CertFingerprint)
		if err != nil {
			t.Fatalf("GetByCertFingerprint: %v", err)
		}
		if byFP.HostID != h.ID {
			t.Errorf("GetByCertFingerprint: got HostID %q, want %q", byFP.HostID, h.ID)
		}

		current, err := is.GetCurrentByHostID(ctx, h.ID)
		if err != nil {
			t.Fatalf("GetCurrentByHostID: %v", err)
		}
		if current.CertFingerprint != ai.CertFingerprint {
			t.Errorf("GetCurrentByHostID: got fingerprint %q, want %q", current.CertFingerprint, ai.CertFingerprint)
		}

		if err := is.Revoke(ctx, ai.ID, "test revocation"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}

		if err := is.Revoke(ctx, ai.ID, "double revoke"); err == nil {
			t.Error("expected error on double-revoke, got nil")
		}
	})
}
