package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/scheduler"
	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

// ---------------------------------------------------------------------------
// Mock ScheduleRepo
// ---------------------------------------------------------------------------

type mockRepo struct {
	mu        sync.Mutex
	schedules map[string]*storage.Schedule
	lastRunAt map[string]time.Time
}

func newMockRepo(scheds ...*storage.Schedule) *mockRepo {
	r := &mockRepo{
		schedules: make(map[string]*storage.Schedule),
		lastRunAt: make(map[string]time.Time),
	}
	for _, s := range scheds {
		r.schedules[s.ID] = s
	}
	return r
}

func (r *mockRepo) Create(_ context.Context, s *storage.Schedule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schedules[s.ID] = s
	return nil
}

func (r *mockRepo) Get(_ context.Context, id, _ string) (*storage.Schedule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.schedules[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *s
	return &cp, nil
}

func (r *mockRepo) List(_ context.Context, _ storage.ListOptions) ([]*storage.Schedule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*storage.Schedule, 0, len(r.schedules))
	for _, s := range r.schedules {
		cp := *s
		out = append(out, &cp)
	}
	return out, nil
}

func (r *mockRepo) ListEnabled(_ context.Context, _ string) ([]*storage.Schedule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*storage.Schedule
	for _, s := range r.schedules {
		if s.Enabled {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *mockRepo) Update(_ context.Context, s *storage.Schedule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schedules[s.ID] = s
	return nil
}

func (r *mockRepo) Delete(_ context.Context, id, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.schedules, id)
	return nil
}

func (r *mockRepo) UpdateLastRunAt(_ context.Context, id string, t time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastRunAt[id] = t
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func pbID(s string) *string { return &s }

func mustSchedule(id, fleetID, expr string, pbID *string, hostIDs []string) *storage.Schedule {
	return &storage.Schedule{
		ID:         id,
		FleetID:    fleetID,
		Name:       id,
		CronExpr:   expr,
		PlaybookID: pbID,
		HostIDs:    hostIDs,
		Enabled:    true,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestScheduleFires verifies that a schedule with a very short interval fires
// the dispatch function at least once within a reasonable timeout.
func TestScheduleFires(t *testing.T) {
	pb := pbID("pb-1")
	sched := mustSchedule("sched-1", "fleet-1", "*/1 * * * * *", pb, []string{"host-1"})

	repo := newMockRepo(sched)

	var (
		mu   sync.Mutex
		reqs []scheduler.InvestigationRequest
	)
	dispatch := func(req scheduler.InvestigationRequest) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
	}

	slog := slogDiscard()
	s := scheduler.New(repo, dispatch, slog)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	// Wait up to 4 s for at least one dispatch.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(reqs)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	got := len(reqs)
	mu.Unlock()

	if got < 1 {
		t.Fatalf("expected at least 1 dispatch, got %d", got)
	}

	// Verify request fields.
	mu.Lock()
	req := reqs[0]
	mu.Unlock()
	if req.FleetID != "fleet-1" {
		t.Errorf("FleetID = %q, want fleet-1", req.FleetID)
	}
	if req.PlaybookID != "pb-1" {
		t.Errorf("PlaybookID = %q, want pb-1", req.PlaybookID)
	}
	if req.TriggerType != "schedule" {
		t.Errorf("TriggerType = %q, want schedule", req.TriggerType)
	}
	if req.ScheduleID != "sched-1" {
		t.Errorf("ScheduleID = %q, want sched-1", req.ScheduleID)
	}
}

// TestReloadAddsNewSchedule checks that calling Reload after adding a schedule
// registers the new cron job (it fires within timeout).
func TestReloadAddsNewSchedule(t *testing.T) {
	repo := newMockRepo() // start empty

	var (
		mu   sync.Mutex
		reqs []scheduler.InvestigationRequest
	)
	dispatch := func(req scheduler.InvestigationRequest) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
	}

	slog := slogDiscard()
	s := scheduler.New(repo, dispatch, slog)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	// Wait a moment then add a schedule and trigger reload.
	time.Sleep(200 * time.Millisecond)

	pb := pbID("pb-2")
	newSched := mustSchedule("sched-new", "fleet-2", "*/1 * * * * *", pb, nil)
	_ = repo.Create(ctx, newSched)
	_ = s.Reload(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(reqs)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	got := len(reqs)
	mu.Unlock()
	if got < 1 {
		t.Fatalf("expected at least 1 dispatch after adding schedule, got %d", got)
	}
}

// TestReloadRemovesDisabledSchedule verifies that disabling a schedule (and
// calling Reload) stops future dispatches.
func TestReloadRemovesDisabledSchedule(t *testing.T) {
	pb := pbID("pb-3")
	sched := mustSchedule("sched-disable", "fleet-3", "*/1 * * * * *", pb, nil)
	repo := newMockRepo(sched)

	var (
		mu    sync.Mutex
		reqs  []scheduler.InvestigationRequest
		phase int // 0 = before disable, 1 = after disable
	)
	dispatch := func(req scheduler.InvestigationRequest) {
		mu.Lock()
		if phase == 1 {
			reqs = append(reqs, req)
		}
		mu.Unlock()
	}

	slog := slogDiscard()
	s := scheduler.New(repo, dispatch, slog)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	// Let it fire a couple of times, then disable and reload.
	time.Sleep(1500 * time.Millisecond)

	sched.Enabled = false
	_ = repo.Update(ctx, sched)
	_ = s.Reload(ctx)

	mu.Lock()
	phase = 1 // now count post-disable dispatches
	mu.Unlock()

	time.Sleep(2 * time.Second)

	cancel()
	<-done

	mu.Lock()
	got := len(reqs)
	mu.Unlock()
	if got != 0 {
		t.Errorf("expected 0 dispatches after disable+reload, got %d", got)
	}
}

// TestTriggerManual verifies that TriggerManual fires the dispatch immediately.
func TestTriggerManual(t *testing.T) {
	pb := pbID("pb-manual")
	// Use a far-future cron expression so it won't auto-fire during the test.
	sched := mustSchedule("sched-manual", "fleet-4", "0 0 1 1 *", pb, []string{"h1", "h2"})
	repo := newMockRepo(sched)

	var (
		mu   sync.Mutex
		reqs []scheduler.InvestigationRequest
	)
	dispatch := func(req scheduler.InvestigationRequest) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
	}

	slog := slogDiscard()
	s := scheduler.New(repo, dispatch, slog)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()
	time.Sleep(100 * time.Millisecond) // let Start initialize

	if err := s.TriggerManual(ctx, "sched-manual", "fleet-4"); err != nil {
		t.Fatalf("TriggerManual returned error: %v", err)
	}

	// Give the dispatch a moment to complete.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	got := len(reqs)
	req := func() scheduler.InvestigationRequest {
		if got > 0 {
			return reqs[0]
		}
		return scheduler.InvestigationRequest{}
	}()
	mu.Unlock()

	if got != 1 {
		t.Fatalf("expected 1 dispatch from TriggerManual, got %d", got)
	}
	if req.ScheduleID != "sched-manual" {
		t.Errorf("ScheduleID = %q, want sched-manual", req.ScheduleID)
	}
	if len(req.HostIDs) != 2 {
		t.Errorf("HostIDs len = %d, want 2", len(req.HostIDs))
	}
}

// TestInvalidCronExpressionDoesNotPanic verifies that a bad cron expression is
// logged and silently skipped — no panic, no crash.
func TestInvalidCronExpressionDoesNotPanic(t *testing.T) {
	pb := pbID("pb-bad")
	bad := &storage.Schedule{
		ID:         "sched-bad",
		FleetID:    "fleet-5",
		Name:       "bad-expr",
		CronExpr:   "not-a-cron-expression!!!",
		PlaybookID: pb,
		Enabled:    true,
	}
	repo := newMockRepo(bad)

	dispatch := func(req scheduler.InvestigationRequest) {}
	slog := slogDiscard()
	s := scheduler.New(repo, dispatch, slog)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start should not return an error and should not panic.
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
}

// TestScheduleWithNilPlaybookIDSkipped verifies that schedules without a
// playbook are gracefully skipped (not added to cron, not panicked).
func TestScheduleWithNilPlaybookIDSkipped(t *testing.T) {
	noPlaybook := &storage.Schedule{
		ID:         "sched-nopb",
		FleetID:    "fleet-6",
		Name:       "no-playbook",
		CronExpr:   "*/1 * * * * *",
		PlaybookID: nil, // intentionally nil
		Enabled:    true,
	}
	repo := newMockRepo(noPlaybook)

	var (
		mu   sync.Mutex
		reqs []scheduler.InvestigationRequest
	)
	dispatch := func(req scheduler.InvestigationRequest) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
	}

	slog := slogDiscard()
	s := scheduler.New(repo, dispatch, slog)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	time.Sleep(1500 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	got := len(reqs)
	mu.Unlock()
	if got != 0 {
		t.Errorf("expected 0 dispatches for nil PlaybookID schedule, got %d", got)
	}
}

// TestValidateCronExpr checks the exported helper.
func TestValidateCronExpr(t *testing.T) {
	cases := []struct {
		expr    string
		wantErr bool
	}{
		{"*/5 * * * *", false},
		{"0 0 * * *", false},
		{"0 9 * * 1-5", false},
		{"not valid at all", true},
		{"99 * * * *", true},
	}
	for _, tc := range cases {
		err := scheduler.ValidateCronExpr(tc.expr)
		if tc.wantErr && err == nil {
			t.Errorf("ValidateCronExpr(%q): expected error, got nil", tc.expr)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ValidateCronExpr(%q): unexpected error: %v", tc.expr, err)
		}
	}
}
