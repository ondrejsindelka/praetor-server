//go:build integration

package command_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"

	"github.com/ondrejsindelka/praetor-server/internal/command"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

// emptyRegistry satisfies command.StreamRegistry with no connected agents.
type emptyRegistry struct{}

func (emptyRegistry) Get(_ string) (command.AgentSender, bool) { return nil, false }

func testDSN(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("TEST_POSTGRES_DSN"); v != "" {
		return v
	}
	return "postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable"
}

const testHostID = "01INTEGRATIONTESTHOST0001"

func TestBrokerIssueAndResult(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Connect(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Insert a minimal host row to satisfy the FK constraint.
	_, err = pool.Exec(ctx, `
		INSERT INTO hosts (id, hostname, os, arch, org_id, status)
		VALUES ($1, 'integration-test-host', 'linux', 'amd64', 'default', 'online')
		ON CONFLICT (id) DO NOTHING`, testHostID)
	if err != nil {
		t.Fatalf("insert test host: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM hosts WHERE id = $1", testHostID) //nolint:errcheck

	cs := store.NewCommandStore(pool)
	broker := command.NewBroker(cs, emptyRegistry{}, slog.Default())

	// Issue to disconnected host — should succeed (queued) but return an error note.
	id, err := broker.Issue(ctx, command.IssueRequest{
		HostID:   testHostID,
		Tier:     praetorv1.CommandTier_COMMAND_TIER_0_SAFE,
		Reason:   "integration test",
		IssuedBy: "test",
		Command:  &praetorv1.DiagnosticCommand{Check: praetorv1.DiagnosticCheck_DIAGNOSTIC_CHECK_DISK_USAGE},
	})
	// ID should be set even if host not connected.
	if id == "" {
		t.Error("expected non-empty command ID")
	}
	// err is expected (host not connected) but ID is still returned.
	t.Logf("id=%s err=%v", id, err)

	// Record a result.
	broker.HandleResult(ctx, &praetorv1.CommandResult{
		CommandId:  id,
		ExitCode:   0,
		Stdout:     "Filesystem      Size  Used Avail Use%",
		DurationMs: 42,
	})

	// Verify in DB.
	cmd, err := cs.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cmd.Status != "completed" {
		t.Errorf("status=%q, want completed", cmd.Status)
	}
	if cmd.Stdout == nil || *cmd.Stdout == "" {
		t.Error("stdout should be set")
	}

	// Cleanup.
	pool.Exec(ctx, "DELETE FROM command_executions WHERE id = $1", id) //nolint:errcheck
}

func TestRateLimiter(t *testing.T) {
	r := command.NewRateLimiter()
	const host = "test-host"
	for i := 0; i < 60; i++ {
		if err := r.Allow(host); err != nil {
			t.Fatalf("expected Allow at i=%d, got error: %v", i, err)
		}
	}
	if err := r.Allow(host); err == nil {
		t.Error("expected rate limit error on 61st call")
	}
}
