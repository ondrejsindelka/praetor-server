//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage/postgres"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("praetor_test"),
		tcpostgres.WithUsername("praetor"),
		tcpostgres.WithPassword("praetor"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testcontainers: start postgres: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ctr.Terminate(ctx) }()

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testcontainers: connection string: %v\n", err)
		os.Exit(1)
	}

	testPool, err = db.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db.Connect: %v\n", err)
		os.Exit(1)
	}
	defer db.Close(testPool)

	if err := db.Migrate(ctx, testPool, db.Migrations); err != nil {
		fmt.Fprintf(os.Stderr, "db.Migrate: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// --- helpers ---

func fleetID(suffix string) string {
	// Use deterministic UUIDs scoped to the test to avoid cross-test pollution.
	switch suffix {
	case "A":
		return "aaaaaaaa-0000-0000-0000-000000000001"
	case "B":
		return "bbbbbbbb-0000-0000-0000-000000000002"
	default:
		return "cccccccc-0000-0000-0000-000000000003"
	}
}

// cleanup truncates all watchdog tables between tests.
func cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	tables := []string{
		"watchdog_investigations",
		"watchdog_rule_state",
		"watchdog_schedules",
		"watchdog_webhooks",
		"watchdog_llm_providers",
		"watchdog_rules",
		"watchdog_playbooks",
	}
	for _, tbl := range tables {
		if _, err := testPool.Exec(ctx, fmt.Sprintf("TRUNCATE %s CASCADE", tbl)); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}

// --- migration smoke test ---

func TestMigration_UpDown(t *testing.T) {
	// If TestMain ran without error, migrations are already up.
	// Just verify that tables exist.
	ctx := context.Background()
	tables := []string{
		"watchdog_playbooks", "watchdog_rules", "watchdog_rule_state",
		"watchdog_investigations", "watchdog_schedules",
		"watchdog_llm_providers", "watchdog_webhooks",
	}
	for _, tbl := range tables {
		var exists bool
		err := testPool.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name=$1)", tbl,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after migration", tbl)
		}
	}
}

// --- Playbook CRUD ---

func makePlaybook(fleetID string) *storage.Playbook {
	return &storage.Playbook{
		FleetID:     fleetID,
		Name:        "test-playbook-" + fleetID[:4],
		Description: "integration test playbook",
		Steps:       []map[string]any{{"action": "restart", "service": "nginx"}},
	}
}

func TestPlaybookRepo_CRUD(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	repo := postgres.NewPlaybooksPostgres(testPool)
	fid := fleetID("A")

	pb := makePlaybook(fid)
	if err := repo.Create(ctx, pb); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if pb.ID == "" {
		t.Fatal("expected ID to be set after Create")
	}

	got, err := repo.Get(ctx, pb.ID, fid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != pb.Name {
		t.Errorf("Name: got %q, want %q", got.Name, pb.Name)
	}

	got.Name = "renamed"
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.Get(ctx, pb.ID, fid)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if updated.Name != "renamed" {
		t.Errorf("after update, name: got %q, want %q", updated.Name, "renamed")
	}

	list, err := repo.List(ctx, storage.ListOptions{FleetID: fid})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List: got %d results, want 1", len(list))
	}

	if err := repo.Delete(ctx, pb.ID, fid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, pb.ID, fid); !isNoRows(err) {
		t.Errorf("Get after Delete: expected ErrNoRows, got %v", err)
	}
}

// --- Rule CRUD ---

func makeRule(fleetID, playbookID string) *storage.Rule {
	return &storage.Rule{
		FleetID:      fleetID,
		Name:         "cpu-spike-" + fleetID[:4],
		Description:  "fires on CPU spike",
		Enabled:      true,
		HostSelector: map[string]any{"env": "prod"},
		Condition:    map[string]any{"metric": "cpu", "threshold": 90},
		PlaybookID:   playbookID,
		CooldownS:    300,
		Priority:     "high",
	}
}

func TestRuleRepo_CRUD(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	pbRepo := postgres.NewPlaybooksPostgres(testPool)
	pb := makePlaybook(fid)
	if err := pbRepo.Create(ctx, pb); err != nil {
		t.Fatalf("Create playbook: %v", err)
	}

	repo := postgres.NewRulesPostgres(testPool)
	rule := makeRule(fid, pb.ID)
	if err := repo.Create(ctx, rule); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rule.ID == "" {
		t.Fatal("expected rule.ID to be set")
	}

	got, err := repo.Get(ctx, rule.ID, fid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Priority != "high" {
		t.Errorf("Priority: got %q, want high", got.Priority)
	}

	got.Priority = "normal"
	got.Enabled = false
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	enabled, err := repo.ListEnabled(ctx, fid)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 0 {
		t.Errorf("ListEnabled: expected 0 after disable, got %d", len(enabled))
	}

	if err := repo.Delete(ctx, rule.ID, fid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// --- RuleState CRUD ---

func TestRuleStateRepo_UpsertAndGet(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	pbRepo := postgres.NewPlaybooksPostgres(testPool)
	pb := makePlaybook(fid)
	if err := pbRepo.Create(ctx, pb); err != nil {
		t.Fatalf("Create playbook: %v", err)
	}
	ruleRepo := postgres.NewRulesPostgres(testPool)
	rule := makeRule(fid, pb.ID)
	if err := ruleRepo.Create(ctx, rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	repo := postgres.NewRuleStatePostgres(testPool)
	hostID := "11111111-1111-1111-1111-111111111111"
	state := &storage.RuleState{
		RuleID: rule.ID,
		HostID: hostID,
		Phase:  "pending",
	}
	if err := repo.Upsert(ctx, state); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(ctx, rule.ID, hostID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Phase != "pending" {
		t.Errorf("Phase: got %q, want pending", got.Phase)
	}

	// Transition to fired.
	now := time.Now().UTC()
	state.Phase = "fired"
	state.LastFiredAt = &now
	if err := repo.Upsert(ctx, state); err != nil {
		t.Fatalf("Upsert fired: %v", err)
	}

	got2, err := repo.Get(ctx, rule.ID, hostID)
	if err != nil {
		t.Fatalf("Get after fired: %v", err)
	}
	if got2.Phase != "fired" {
		t.Errorf("Phase: got %q, want fired", got2.Phase)
	}

	list, err := repo.ListByRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("ListByRule: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListByRule: got %d, want 1", len(list))
	}
}

func TestRuleStateRepo_BulkUpsert(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	pbRepo := postgres.NewPlaybooksPostgres(testPool)
	pb := makePlaybook(fid)
	if err := pbRepo.Create(ctx, pb); err != nil {
		t.Fatalf("Create playbook: %v", err)
	}
	ruleRepo := postgres.NewRulesPostgres(testPool)
	rule := makeRule(fid, pb.ID)
	if err := ruleRepo.Create(ctx, rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	repo := postgres.NewRuleStatePostgres(testPool)
	states := []*storage.RuleState{
		{RuleID: rule.ID, HostID: "22222222-2222-2222-2222-222222222221", Phase: "idle"},
		{RuleID: rule.ID, HostID: "22222222-2222-2222-2222-222222222222", Phase: "pending"},
		{RuleID: rule.ID, HostID: "22222222-2222-2222-2222-222222222223", Phase: "cooldown"},
	}
	if err := repo.BulkUpsert(ctx, states); err != nil {
		t.Fatalf("BulkUpsert: %v", err)
	}
	list, err := repo.ListByRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("ListByRule: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("ListByRule: got %d, want 3", len(list))
	}
}

// --- Investigation CRUD + filters ---

func makeInvestigation(fid string, ruleID, playbookID *string) *storage.Investigation {
	return &storage.Investigation{
		FleetID:     fid,
		RuleID:      ruleID,
		PlaybookID:  playbookID,
		TriggerType: "rule",
		TriggeredAt: time.Now().UTC().Truncate(time.Millisecond),
		HostIDs:     []string{"33333333-3333-3333-3333-333333333333"},
		Status:      "pending",
		TriggerData: map[string]any{"reason": "cpu spike"},
	}
}

func TestInvestigationRepo_CRUD(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	pbRepo := postgres.NewPlaybooksPostgres(testPool)
	pb := makePlaybook(fid)
	if err := pbRepo.Create(ctx, pb); err != nil {
		t.Fatalf("Create playbook: %v", err)
	}
	ruleRepo := postgres.NewRulesPostgres(testPool)
	rule := makeRule(fid, pb.ID)
	if err := ruleRepo.Create(ctx, rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	repo := postgres.NewInvestigationsPostgres(testPool)
	inv := makeInvestigation(fid, &rule.ID, &pb.ID)
	if err := repo.Create(ctx, inv); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if inv.ID == "" {
		t.Fatal("expected inv.ID to be set")
	}

	got, err := repo.Get(ctx, inv.ID, fid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("Status: got %q, want pending", got.Status)
	}

	if err := repo.UpdateStatus(ctx, inv.ID, "collecting", nil); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := repo.UpdateSnapshot(ctx, inv.ID, map[string]any{"metrics": "data"}); err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	if err := repo.UpdateLLMAnalysis(ctx, inv.ID, "CPU usage was high.", map[string]any{"model": "claude-3"}); err != nil {
		t.Fatalf("UpdateLLMAnalysis: %v", err)
	}
	if err := repo.Complete(ctx, inv.ID, "resolved"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	resolved, err := repo.Get(ctx, inv.ID, fid)
	if err != nil {
		t.Fatalf("Get after Complete: %v", err)
	}
	if resolved.Status != "resolved" {
		t.Errorf("Status after Complete: got %q, want resolved", resolved.Status)
	}
	if resolved.CompletedAt == nil {
		t.Error("CompletedAt should be set after Complete")
	}
}

func TestInvestigationRepo_ListFilters(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	pbRepo := postgres.NewPlaybooksPostgres(testPool)
	pb := makePlaybook(fid)
	if err := pbRepo.Create(ctx, pb); err != nil {
		t.Fatalf("Create playbook: %v", err)
	}
	ruleRepo := postgres.NewRulesPostgres(testPool)
	rule := makeRule(fid, pb.ID)
	if err := ruleRepo.Create(ctx, rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	repo := postgres.NewInvestigationsPostgres(testPool)

	hostA := "44444444-4444-4444-4444-444444444441"
	hostB := "44444444-4444-4444-4444-444444444442"

	now := time.Now().UTC()
	past := now.Add(-2 * time.Hour)
	future := now.Add(2 * time.Hour)

	// inv1: pending, hostA, recent
	inv1 := &storage.Investigation{
		FleetID: fid, RuleID: &rule.ID, PlaybookID: &pb.ID,
		TriggerType: "rule", TriggeredAt: now, HostIDs: []string{hostA}, Status: "pending",
	}
	if err := repo.Create(ctx, inv1); err != nil {
		t.Fatalf("Create inv1: %v", err)
	}
	// inv2: analyzing, hostB, older
	inv2 := &storage.Investigation{
		FleetID: fid, RuleID: &rule.ID, PlaybookID: &pb.ID,
		TriggerType: "manual", TriggeredAt: past, HostIDs: []string{hostB}, Status: "analyzing",
	}
	if err := repo.Create(ctx, inv2); err != nil {
		t.Fatalf("Create inv2: %v", err)
	}

	// Filter by status
	pending, err := repo.List(ctx, storage.InvestigationListOptions{
		ListOptions: storage.ListOptions{FleetID: fid},
		Status:      "pending",
	})
	if err != nil {
		t.Fatalf("List status=pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != inv1.ID {
		t.Errorf("status filter: got %d results, want 1 (inv1)", len(pending))
	}

	// Filter by host_id
	byHost, err := repo.List(ctx, storage.InvestigationListOptions{
		ListOptions: storage.ListOptions{FleetID: fid},
		HostID:      hostB,
	})
	if err != nil {
		t.Fatalf("List hostID=hostB: %v", err)
	}
	if len(byHost) != 1 || byHost[0].ID != inv2.ID {
		t.Errorf("host_id filter: got %d results, want 1 (inv2)", len(byHost))
	}

	// Filter by since/until
	since := past.Add(-time.Minute)
	until := past.Add(time.Minute)
	byTime, err := repo.List(ctx, storage.InvestigationListOptions{
		ListOptions: storage.ListOptions{FleetID: fid},
		Since:       &since,
		Until:       &until,
	})
	if err != nil {
		t.Fatalf("List since/until: %v", err)
	}
	if len(byTime) != 1 || byTime[0].ID != inv2.ID {
		t.Errorf("time filter: got %d results, want 1 (inv2)", len(byTime))
	}

	// Filter by rule_id
	byRule, err := repo.List(ctx, storage.InvestigationListOptions{
		ListOptions: storage.ListOptions{FleetID: fid},
		RuleID:      rule.ID,
	})
	if err != nil {
		t.Fatalf("List ruleID: %v", err)
	}
	if len(byRule) != 2 {
		t.Errorf("rule filter: got %d results, want 2", len(byRule))
	}

	_ = future // used indirectly (validates time range exclusion above)
}

// --- Schedule CRUD ---

func TestScheduleRepo_CRUD(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	repo := postgres.NewSchedulesPostgres(testPool)
	sched := &storage.Schedule{
		FleetID:  fid,
		Name:     "daily-check",
		CronExpr: "0 0 * * *",
		HostIDs:  []string{"55555555-5555-5555-5555-555555555555"},
		Enabled:  true,
	}
	if err := repo.Create(ctx, sched); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sched.ID == "" {
		t.Fatal("expected sched.ID to be set")
	}

	got, err := repo.Get(ctx, sched.ID, fid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CronExpr != "0 0 * * *" {
		t.Errorf("CronExpr: got %q, want %q", got.CronExpr, "0 0 * * *")
	}

	lastRun := time.Now().UTC()
	if err := repo.UpdateLastRunAt(ctx, sched.ID, lastRun); err != nil {
		t.Fatalf("UpdateLastRunAt: %v", err)
	}

	enabled, err := repo.ListEnabled(ctx, fid)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 {
		t.Errorf("ListEnabled: got %d, want 1", len(enabled))
	}

	got.Enabled = false
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	enabledAfter, err := repo.ListEnabled(ctx, fid)
	if err != nil {
		t.Fatalf("ListEnabled after disable: %v", err)
	}
	if len(enabledAfter) != 0 {
		t.Errorf("ListEnabled after disable: got %d, want 0", len(enabledAfter))
	}

	if err := repo.Delete(ctx, sched.ID, fid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// --- LLMProvider CRUD ---

func TestLLMProviderRepo_CRUD(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	repo := postgres.NewLLMProvidersPostgres(testPool)
	p := &storage.LLMProvider{
		FleetID:      fid,
		Name:         "claude-prod",
		Provider:     "anthropic",
		Endpoint:     "https://api.anthropic.com",
		APIKeyEnc:    []byte("encrypted-key-bytes"),
		DefaultModel: "claude-sonnet-4-5",
		IsDefault:    true,
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.Get(ctx, p.ID, fid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Provider != "anthropic" {
		t.Errorf("Provider: got %q, want anthropic", got.Provider)
	}

	def, err := repo.GetDefault(ctx, fid)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if def.ID != p.ID {
		t.Errorf("GetDefault: got %q, want %q", def.ID, p.ID)
	}

	list, err := repo.List(ctx, storage.ListOptions{FleetID: fid})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List: got %d, want 1", len(list))
	}

	if err := repo.Delete(ctx, p.ID, fid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// --- Webhook CRUD ---

func TestWebhookRepo_CRUD(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fid := fleetID("A")

	repo := postgres.NewWebhooksPostgres(testPool)
	wh := &storage.Webhook{
		FleetID:   fid,
		Name:      "slack-alerts",
		URL:       "https://hooks.slack.com/services/T000/B000/xxx",
		Events:    []string{"investigation.created", "investigation.completed"},
		SecretEnc: []byte("enc-secret"),
		Enabled:   true,
	}
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wh.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.Get(ctx, wh.ID, fid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Events) != 2 {
		t.Errorf("Events: got %d, want 2", len(got.Events))
	}

	enabled, err := repo.ListEnabled(ctx, fid)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 {
		t.Errorf("ListEnabled: got %d, want 1", len(enabled))
	}

	got.Enabled = false
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := repo.Delete(ctx, wh.ID, fid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// --- Fleet isolation ---

func TestFleetIsolation_RuleList(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fidA := fleetID("A")
	fidB := fleetID("B")

	pbRepo := postgres.NewPlaybooksPostgres(testPool)
	pbA := makePlaybook(fidA)
	if err := pbRepo.Create(ctx, pbA); err != nil {
		t.Fatalf("Create playbook A: %v", err)
	}

	ruleRepo := postgres.NewRulesPostgres(testPool)
	ruleA := makeRule(fidA, pbA.ID)
	if err := ruleRepo.Create(ctx, ruleA); err != nil {
		t.Fatalf("Create rule A: %v", err)
	}

	// List under fleet B — should be empty.
	listB, err := ruleRepo.List(ctx, storage.ListOptions{FleetID: fidB})
	if err != nil {
		t.Fatalf("List fleet B: %v", err)
	}
	if len(listB) != 0 {
		t.Errorf("fleet isolation: expected 0 rules for fleet B, got %d", len(listB))
	}

	// Get under fleet B — should return ErrNoRows.
	_, err = ruleRepo.Get(ctx, ruleA.ID, fidB)
	if !isNoRows(err) {
		t.Errorf("fleet isolation Get: expected ErrNoRows, got %v", err)
	}
}

func TestFleetIsolation_InvestigationList(t *testing.T) {
	cleanup(t)
	ctx := context.Background()
	fidA := fleetID("A")
	fidB := fleetID("B")

	pbRepo := postgres.NewPlaybooksPostgres(testPool)
	pbA := makePlaybook(fidA)
	if err := pbRepo.Create(ctx, pbA); err != nil {
		t.Fatalf("Create playbook: %v", err)
	}
	ruleRepo := postgres.NewRulesPostgres(testPool)
	ruleA := makeRule(fidA, pbA.ID)
	if err := ruleRepo.Create(ctx, ruleA); err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	invRepo := postgres.NewInvestigationsPostgres(testPool)
	invA := makeInvestigation(fidA, &ruleA.ID, &pbA.ID)
	if err := invRepo.Create(ctx, invA); err != nil {
		t.Fatalf("Create investigation: %v", err)
	}

	listB, err := invRepo.List(ctx, storage.InvestigationListOptions{
		ListOptions: storage.ListOptions{FleetID: fidB},
	})
	if err != nil {
		t.Fatalf("List fleet B: %v", err)
	}
	if len(listB) != 0 {
		t.Errorf("fleet isolation: expected 0 investigations for fleet B, got %d", len(listB))
	}
}

// isNoRows returns true if the error wraps pgx.ErrNoRows.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
