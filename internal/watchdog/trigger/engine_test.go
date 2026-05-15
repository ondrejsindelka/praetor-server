package trigger

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

// --- Mock VMClient ---

type mockVMClient struct {
	mu      sync.Mutex
	instant map[string]float64 // query → value; missing key means "not found"
	found   map[string]bool
}

func newMockVM() *mockVMClient {
	return &mockVMClient{
		instant: make(map[string]float64),
		found:   make(map[string]bool),
	}
}

func (m *mockVMClient) set(query string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instant[query] = value
	m.found[query] = true
}

func (m *mockVMClient) QueryInstant(_ context.Context, query string) (float64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.instant[query]
	return v, ok, nil
}

func (m *mockVMClient) QueryRange(_ context.Context, _ string, _, _ time.Time) ([]MetricSample, error) {
	return nil, nil
}

// --- Mock Dispatcher ---

type mockDispatcher struct {
	mu       sync.Mutex
	requests []InvestigationRequest
}

func (d *mockDispatcher) Dispatch(_ context.Context, req InvestigationRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, req)
}

func (d *mockDispatcher) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.requests)
}

// --- Mock RuleRepo ---

type mockRuleRepo struct {
	rules []*storage.Rule
}

func (r *mockRuleRepo) Create(_ context.Context, _ *storage.Rule) error  { return nil }
func (r *mockRuleRepo) Get(_ context.Context, _, _ string) (*storage.Rule, error) {
	return nil, nil
}
func (r *mockRuleRepo) List(_ context.Context, _ storage.ListOptions) ([]*storage.Rule, error) {
	return r.rules, nil
}
func (r *mockRuleRepo) ListEnabled(_ context.Context, _ string) ([]*storage.Rule, error) {
	var out []*storage.Rule
	for _, rule := range r.rules {
		if rule.Enabled {
			out = append(out, rule)
		}
	}
	return out, nil
}
func (r *mockRuleRepo) Update(_ context.Context, _ *storage.Rule) error { return nil }
func (r *mockRuleRepo) Delete(_ context.Context, _, _ string) error     { return nil }

// --- Mock RuleStateRepo ---

type mockRuleStateRepo struct {
	mu     sync.Mutex
	states map[string]*storage.RuleState // key: ruleID+":"+hostID
}

func newMockRuleStateRepo() *mockRuleStateRepo {
	return &mockRuleStateRepo{states: make(map[string]*storage.RuleState)}
}

func stateRepoKey(ruleID, hostID string) string { return ruleID + ":" + hostID }

func (r *mockRuleStateRepo) Upsert(_ context.Context, s *storage.RuleState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.states[stateRepoKey(s.RuleID, s.HostID)] = &cp
	return nil
}
func (r *mockRuleStateRepo) Get(_ context.Context, ruleID, hostID string) (*storage.RuleState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.states[stateRepoKey(ruleID, hostID)]
	if !ok {
		return nil, nil
	}
	return s, nil
}
func (r *mockRuleStateRepo) ListByRule(_ context.Context, ruleID string) ([]*storage.RuleState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*storage.RuleState
	for _, s := range r.states {
		if s.RuleID == ruleID {
			out = append(out, s)
		}
	}
	return out, nil
}
func (r *mockRuleStateRepo) ListAll(_ context.Context) ([]*storage.RuleState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*storage.RuleState, 0, len(r.states))
	for _, s := range r.states {
		out = append(out, s)
	}
	return out, nil
}
func (r *mockRuleStateRepo) BulkUpsert(_ context.Context, states []*storage.RuleState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range states {
		cp := *s
		r.states[stateRepoKey(s.RuleID, s.HostID)] = &cp
	}
	return nil
}

// --- Mock HostResolver ---

type mockHostResolver struct {
	hosts []string
}

func (h *mockHostResolver) Resolve(_ context.Context, _ string, _ map[string]any) ([]string, error) {
	return h.hosts, nil
}

// --- Helper to build a test engine ---

func buildEngine(rules []*storage.Rule, vm *mockVMClient, disp *mockDispatcher) *RuleEngine {
	return New(
		&mockRuleRepo{rules: rules},
		newMockRuleStateRepo(),
		vm,
		disp,
		&mockHostResolver{hosts: []string{"host-1"}},
		newTestLogger(),
	)
}

func newTestLogger() *slog.Logger {
	return slog.Default()
}

// --- Tests ---

// TestConditionSatisfiedForDuration: condition true for durationS → investigation dispatched.
func TestConditionSatisfiedForDuration(t *testing.T) {
	vm := newMockVM()
	disp := &mockDispatcher{}

	rule := &storage.Rule{
		ID:           "rule-1",
		FleetID:      "fleet-1",
		Name:         "cpu high",
		Enabled:      true,
		PlaybookID:   "pb-1",
		CooldownS:    60,
		HostSelector: map[string]any{"host_ids": []any{"host-1"}},
		Condition: map[string]any{
			"metric":     "cpu_usage",
			"op":         ">",
			"threshold":  float64(80),
			"duration_s": float64(30),
		},
	}

	// Set VM to return a value that satisfies the condition.
	vm.set(`cpu_usage{host_id="host-1"}`, 95.0)

	eng := buildEngine([]*storage.Rule{rule}, vm, disp)

	now := time.Now().UTC()

	// Manually seed a pending state that has already been pending for 35s (> 30s duration).
	pendingAt := now.Add(-35 * time.Second)
	eng.stateMu.Lock()
	eng.states[stateKey{RuleID: "rule-1", HostID: "host-1"}] = &RuleState{
		Phase:        PhasePending,
		PendingSince: &pendingAt,
	}
	eng.stateMu.Unlock()

	eng.tick(context.Background())

	if disp.count() != 1 {
		t.Errorf("expected 1 dispatch, got %d", disp.count())
	}
}

// TestConditionDropsBeforeDuration: condition drops before durationS → no dispatch.
func TestConditionDropsBeforeDuration(t *testing.T) {
	vm := newMockVM()
	disp := &mockDispatcher{}

	rule := &storage.Rule{
		ID:           "rule-2",
		FleetID:      "fleet-1",
		Name:         "cpu high",
		Enabled:      true,
		PlaybookID:   "pb-1",
		CooldownS:    60,
		HostSelector: map[string]any{"host_ids": []any{"host-1"}},
		Condition: map[string]any{
			"metric":     "cpu_usage",
			"op":         ">",
			"threshold":  float64(80),
			"duration_s": float64(30),
		},
	}

	// Condition does NOT satisfy (value below threshold).
	vm.set(`cpu_usage{host_id="host-1"}`, 50.0)

	eng := buildEngine([]*storage.Rule{rule}, vm, disp)

	now := time.Now().UTC()
	pendingAt := now.Add(-10 * time.Second) // only 10s elapsed

	eng.stateMu.Lock()
	eng.states[stateKey{RuleID: "rule-2", HostID: "host-1"}] = &RuleState{
		Phase:        PhasePending,
		PendingSince: &pendingAt,
	}
	eng.stateMu.Unlock()

	eng.tick(context.Background())

	if disp.count() != 0 {
		t.Errorf("expected 0 dispatches, got %d", disp.count())
	}

	// State should have returned to idle (condition false).
	eng.stateMu.Lock()
	s := eng.states[stateKey{RuleID: "rule-2", HostID: "host-1"}]
	eng.stateMu.Unlock()
	if s == nil || s.Phase != PhaseIdle {
		t.Errorf("expected idle state, got %v", s)
	}
}

// TestCooldownPreventsRedispatch: after firing, cooldown prevents immediate re-dispatch.
func TestCooldownPreventsRedispatch(t *testing.T) {
	vm := newMockVM()
	disp := &mockDispatcher{}

	rule := &storage.Rule{
		ID:           "rule-3",
		FleetID:      "fleet-1",
		Name:         "cpu high",
		Enabled:      true,
		PlaybookID:   "pb-1",
		CooldownS:    120,
		HostSelector: map[string]any{"host_ids": []any{"host-1"}},
		Condition: map[string]any{
			"metric":     "cpu_usage",
			"op":         ">",
			"threshold":  float64(80),
			"duration_s": float64(0), // fire immediately
		},
	}

	// Condition still high.
	vm.set(`cpu_usage{host_id="host-1"}`, 95.0)

	eng := buildEngine([]*storage.Rule{rule}, vm, disp)

	now := time.Now().UTC()
	lastFired := now.Add(-10 * time.Second) // recently fired, cooldown 120s not elapsed

	eng.stateMu.Lock()
	eng.states[stateKey{RuleID: "rule-3", HostID: "host-1"}] = &RuleState{
		Phase:       PhaseCooldown,
		LastFiredAt: &lastFired,
	}
	eng.stateMu.Unlock()

	eng.tick(context.Background())

	if disp.count() != 0 {
		t.Errorf("expected 0 dispatches during cooldown, got %d", disp.count())
	}
}

// TestCompoundAllOfCondition: all_of evaluates AND logic correctly.
func TestCompoundAllOfCondition(t *testing.T) {
	vm := newMockVM()
	disp := &mockDispatcher{}

	rule := &storage.Rule{
		ID:           "rule-4",
		FleetID:      "fleet-1",
		Name:         "compound",
		Enabled:      true,
		PlaybookID:   "pb-1",
		CooldownS:    60,
		HostSelector: map[string]any{"host_ids": []any{"host-1"}},
		Condition: map[string]any{
			"all_of": []any{
				map[string]any{
					"metric":     "cpu_usage",
					"op":         ">",
					"threshold":  float64(80),
					"duration_s": float64(0),
				},
				map[string]any{
					"metric":     "mem_usage",
					"op":         ">",
					"threshold":  float64(70),
					"duration_s": float64(0),
				},
			},
		},
	}

	// Both conditions satisfied.
	vm.set(`cpu_usage{host_id="host-1"}`, 90.0)
	vm.set(`mem_usage{host_id="host-1"}`, 75.0)

	eng := buildEngine([]*storage.Rule{rule}, vm, disp)

	now := time.Now().UTC()
	pendingAt := now.Add(-5 * time.Second)

	eng.stateMu.Lock()
	eng.states[stateKey{RuleID: "rule-4", HostID: "host-1"}] = &RuleState{
		Phase:        PhasePending,
		PendingSince: &pendingAt,
	}
	eng.stateMu.Unlock()

	eng.tick(context.Background())

	// durationS is 0 in each leaf but all_of compound doesn't have durationS
	// at the top level — the pending state from 5s ago with durationS=0 should fire.
	if disp.count() != 1 {
		t.Errorf("expected 1 dispatch for compound all_of, got %d", disp.count())
	}
}

// TestCompoundAllOfPartialFail: if one condition fails in all_of, no dispatch.
func TestCompoundAllOfPartialFail(t *testing.T) {
	vm := newMockVM()
	disp := &mockDispatcher{}

	rule := &storage.Rule{
		ID:           "rule-5",
		FleetID:      "fleet-1",
		Name:         "compound-partial",
		Enabled:      true,
		PlaybookID:   "pb-1",
		CooldownS:    60,
		HostSelector: map[string]any{"host_ids": []any{"host-1"}},
		Condition: map[string]any{
			"all_of": []any{
				map[string]any{
					"metric":    "cpu_usage",
					"op":        ">",
					"threshold": float64(80),
				},
				map[string]any{
					"metric":    "mem_usage",
					"op":        ">",
					"threshold": float64(70),
				},
			},
		},
	}

	// CPU satisfied, memory not.
	vm.set(`cpu_usage{host_id="host-1"}`, 90.0)
	vm.set(`mem_usage{host_id="host-1"}`, 50.0)

	eng := buildEngine([]*storage.Rule{rule}, vm, disp)

	now := time.Now().UTC()
	pendingAt := now.Add(-5 * time.Second)

	eng.stateMu.Lock()
	eng.states[stateKey{RuleID: "rule-5", HostID: "host-1"}] = &RuleState{
		Phase:        PhasePending,
		PendingSince: &pendingAt,
	}
	eng.stateMu.Unlock()

	eng.tick(context.Background())

	// mem_usage below threshold → all_of is false → drops to idle.
	if disp.count() != 0 {
		t.Errorf("expected 0 dispatches when one all_of condition fails, got %d", disp.count())
	}
}
