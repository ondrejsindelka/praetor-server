//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

func testDSNSec(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("TEST_POSTGRES_DSN"); v != "" {
		return v
	}
	if v := os.Getenv("PRAETOR_TEST_DSN"); v != "" {
		return v
	}
	return "postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable"
}

func TestSecurityEventInsertAndList(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Connect(ctx, testDSNSec(t))
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	defer db.Close(pool)

	if err := db.Migrate(ctx, pool, db.Migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	// Insert a test host first (security_events has FK to hosts).
	hostID := "01TESTSEC000000000001"
	_, _ = pool.Exec(ctx, `INSERT INTO hosts (id, hostname, os, os_version, kernel, arch, status, org_id)
		VALUES ($1, 'test-sec-host', 'linux', '', '', 'amd64', 'pending', 'default')
		ON CONFLICT (id) DO NOTHING`, hostID)
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM security_events WHERE host_id = $1", hostID)
		pool.Exec(ctx, "DELETE FROM hosts WHERE id = $1", hostID)
	})

	s := store.NewSecurityEventStore(pool)
	ev := &praetorv1.SecurityEvent{
		HostId:    hostID,
		Timestamp: timestamppb.Now(),
		Type:      praetorv1.SecurityEventType_SECURITY_EVENT_TYPE_SSH_LOGIN_FAILED,
		Source:    "auth_log",
		Data:      map[string]string{"user": "root", "remote_ip": "1.2.3.4"},
		Raw:       "Failed password for root from 1.2.3.4 port 12345 ssh2",
	}

	if err := s.Insert(ctx, ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	events, err := s.List(ctx, hostID, time.Now().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 event")
	}
	if events[0].Type != "SECURITY_EVENT_TYPE_SSH_LOGIN_FAILED" {
		t.Errorf("type=%q", events[0].Type)
	}
	if events[0].HostID != hostID {
		t.Errorf("host_id=%q, want %q", events[0].HostID, hostID)
	}
	if events[0].Source != "auth_log" {
		t.Errorf("source=%q, want %q", events[0].Source, "auth_log")
	}

	// Test ListAll.
	all, err := s.ListAll(ctx, time.Now().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	found := false
	for _, e := range all {
		if e.HostID == hostID {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListAll: expected to find the inserted event")
	}
}
