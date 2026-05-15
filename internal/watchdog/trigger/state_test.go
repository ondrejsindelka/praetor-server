package trigger

import (
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// TestTransitionTable tests all edges of the state machine.
func TestTransitionTable(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	durationS := 30
	cooldownS := 60

	pendingSince := now.Add(-10 * time.Second)    // 10s ago — not yet elapsed
	pendingSinceOld := now.Add(-35 * time.Second) // 35s ago — duration elapsed

	cases := []struct {
		name          string
		state         *RuleState
		conditionTrue bool
		wantPhase     string
		wantDispatch  bool
	}{
		// idle → pending (condition true)
		{
			name:          "idle to pending when condition true",
			state:         &RuleState{Phase: PhaseIdle},
			conditionTrue: true,
			wantPhase:     PhasePending,
			wantDispatch:  false,
		},
		// idle → idle (condition false)
		{
			name:          "idle stays idle when condition false",
			state:         &RuleState{Phase: PhaseIdle},
			conditionTrue: false,
			wantPhase:     PhaseIdle,
			wantDispatch:  false,
		},
		// pending → idle (condition drops before duration)
		{
			name: "pending to idle when condition drops before duration",
			state: &RuleState{
				Phase:        PhasePending,
				PendingSince: &pendingSince, // only 10s — not yet elapsed
			},
			conditionTrue: false,
			wantPhase:     PhaseIdle,
			wantDispatch:  false,
		},
		// pending → pending (condition still true, duration not elapsed)
		{
			name: "pending stays pending when condition true but duration not elapsed",
			state: &RuleState{
				Phase:        PhasePending,
				PendingSince: &pendingSince, // 10s < durationS 30s
			},
			conditionTrue: true,
			wantPhase:     PhasePending,
			wantDispatch:  false,
		},
		// pending → fired (condition true, duration elapsed)
		{
			name: "pending to fired when duration elapsed",
			state: &RuleState{
				Phase:        PhasePending,
				PendingSince: &pendingSinceOld, // 35s > durationS 30s
			},
			conditionTrue: true,
			wantPhase:     PhaseFired,
			wantDispatch:  true,
		},
		// fired → cooldown (via transition, then transitionAfterDispatch)
		{
			name:          "fired to cooldown",
			state:         &RuleState{Phase: PhaseFired, LastFiredAt: &now},
			conditionTrue: true,
			wantPhase:     PhaseCooldown,
			wantDispatch:  false,
		},
		// cooldown → idle (cooldown elapsed)
		{
			name: "cooldown to idle when cooldown elapsed",
			state: &RuleState{
				Phase:       PhaseCooldown,
				LastFiredAt: ptr(now.Add(-70 * time.Second)), // 70s > cooldownS 60s
			},
			conditionTrue: false,
			wantPhase:     PhaseIdle,
			wantDispatch:  false,
		},
		// cooldown → cooldown (still within cooldown)
		{
			name: "cooldown stays in cooldown when not elapsed",
			state: &RuleState{
				Phase:       PhaseCooldown,
				LastFiredAt: ptr(now.Add(-30 * time.Second)), // 30s < cooldownS 60s
			},
			conditionTrue: false,
			wantPhase:     PhaseCooldown,
			wantDispatch:  false,
		},
		// unknown phase → idle reset
		{
			name:          "unknown phase resets to idle",
			state:         &RuleState{Phase: "unknown_garbage"},
			conditionTrue: true,
			wantPhase:     PhaseIdle,
			wantDispatch:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, dispatch := transition(tc.state, tc.conditionTrue, durationS, cooldownS, now)
			if next.Phase != tc.wantPhase {
				t.Errorf("phase: got %q, want %q", next.Phase, tc.wantPhase)
			}
			if dispatch != tc.wantDispatch {
				t.Errorf("dispatch: got %v, want %v", dispatch, tc.wantDispatch)
			}
		})
	}
}

// TestTransitionAfterDispatch verifies the post-dispatch state is cooldown.
func TestTransitionAfterDispatch(t *testing.T) {
	now := time.Now()
	fired := &RuleState{Phase: PhaseFired, LastFiredAt: &now}
	after := transitionAfterDispatch(fired)
	if after.Phase != PhaseCooldown {
		t.Errorf("expected cooldown, got %q", after.Phase)
	}
	if after.LastFiredAt == nil || !after.LastFiredAt.Equal(now) {
		t.Error("LastFiredAt should be preserved")
	}
	if after.PendingSince != nil {
		t.Error("PendingSince should be nil after dispatch")
	}
}

// TestCooldownExpiry verifies that exactly at cooldown boundary, state returns idle.
func TestCooldownExpiry(t *testing.T) {
	cooldownS := 60
	firedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// exactly at cooldown boundary
	nowAtBoundary := firedAt.Add(time.Duration(cooldownS) * time.Second)
	s := &RuleState{Phase: PhaseCooldown, LastFiredAt: &firedAt}
	next, _ := transition(s, false, 0, cooldownS, nowAtBoundary)
	if next.Phase != PhaseIdle {
		t.Errorf("at boundary: expected idle, got %q", next.Phase)
	}

	// one second before boundary
	nowBeforeBoundary := firedAt.Add(time.Duration(cooldownS-1) * time.Second)
	next2, _ := transition(s, false, 0, cooldownS, nowBeforeBoundary)
	if next2.Phase != PhaseCooldown {
		t.Errorf("before boundary: expected cooldown, got %q", next2.Phase)
	}
}

// TestPendingDurationExact verifies that exactly at duration boundary, it fires.
func TestPendingDurationExact(t *testing.T) {
	durationS := 30
	pendingFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// exactly at duration boundary
	nowAtBoundary := pendingFrom.Add(time.Duration(durationS) * time.Second)
	s := &RuleState{Phase: PhasePending, PendingSince: &pendingFrom}
	next, dispatch := transition(s, true, durationS, 60, nowAtBoundary)
	if next.Phase != PhaseFired {
		t.Errorf("at boundary: expected fired, got %q", next.Phase)
	}
	if !dispatch {
		t.Error("expected dispatch=true at boundary")
	}

	// one second before boundary
	nowBeforeBoundary := pendingFrom.Add(time.Duration(durationS-1) * time.Second)
	next2, dispatch2 := transition(s, true, durationS, 60, nowBeforeBoundary)
	if next2.Phase != PhasePending {
		t.Errorf("before boundary: expected pending, got %q", next2.Phase)
	}
	if dispatch2 {
		t.Error("expected dispatch=false before boundary")
	}
}
