package trigger

import (
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

// Phase constants for the rule state machine.
const (
	PhaseIdle     = "idle"
	PhasePending  = "pending"
	PhaseFired    = "fired"
	PhaseCooldown = "cooldown"
)

// RuleState holds the in-memory state for a single (rule_id, host_id) pair.
type RuleState struct {
	Phase        string
	PendingSince *time.Time
	LastFiredAt  *time.Time
}

// newIdleState returns a fresh idle state.
func newIdleState() *RuleState {
	return &RuleState{Phase: PhaseIdle}
}

// toStorage converts an in-memory RuleState to the storage representation.
func (s *RuleState) toStorage(ruleID, hostID string) *storage.RuleState {
	return &storage.RuleState{
		RuleID:       ruleID,
		HostID:       hostID,
		Phase:        s.Phase,
		PendingSince: s.PendingSince,
		LastFiredAt:  s.LastFiredAt,
	}
}

// fromStorage converts a storage RuleState to the in-memory representation.
func fromStorage(ss *storage.RuleState) *RuleState {
	return &RuleState{
		Phase:        ss.Phase,
		PendingSince: ss.PendingSince,
		LastFiredAt:  ss.LastFiredAt,
	}
}

// transition applies the state machine logic given the current condition result.
// It returns the new state and a boolean indicating whether an investigation
// should be dispatched (true when the transition is pending→fired).
//
// State machine (per SPEC-005):
//   - idle → pending    : conditionTrue
//   - pending → idle    : !conditionTrue (before durationS elapses)
//   - pending → fired   : conditionTrue AND durationS elapsed
//   - fired → cooldown  : (called when investigation dispatched)
//   - cooldown → idle   : cooldownS elapsed since LastFiredAt
func transition(s *RuleState, conditionTrue bool, durationS, cooldownS int, now time.Time) (next *RuleState, dispatch bool) {
	next = &RuleState{
		Phase:        s.Phase,
		PendingSince: s.PendingSince,
		LastFiredAt:  s.LastFiredAt,
	}

	switch s.Phase {
	case PhaseIdle:
		if conditionTrue {
			t := now
			next.Phase = PhasePending
			next.PendingSince = &t
		}
		// else: stays idle

	case PhasePending:
		if !conditionTrue {
			// drop back to idle
			next.Phase = PhaseIdle
			next.PendingSince = nil
		} else if s.PendingSince != nil && now.Sub(*s.PendingSince) >= time.Duration(durationS)*time.Second {
			// duration elapsed and condition still true → fire
			t := now
			next.Phase = PhaseFired
			next.LastFiredAt = &t
			next.PendingSince = nil
			dispatch = true
		}
		// else: still within window, stays pending

	case PhaseFired:
		// Caller dispatches investigation, then calls transitionToAfterDispatch.
		// This case handles a re-entry where dispatch already happened; move to cooldown.
		next.Phase = PhaseCooldown

	case PhaseCooldown:
		if s.LastFiredAt != nil && now.Sub(*s.LastFiredAt) >= time.Duration(cooldownS)*time.Second {
			next.Phase = PhaseIdle
		}
		// else: still in cooldown

	default:
		// unknown phase — reset to idle
		next.Phase = PhaseIdle
		next.PendingSince = nil
	}

	return next, dispatch
}

// transitionAfterDispatch moves a just-fired state to cooldown.
// Call this immediately after the investigation has been dispatched.
func transitionAfterDispatch(s *RuleState) *RuleState {
	return &RuleState{
		Phase:        PhaseCooldown,
		PendingSince: nil,
		LastFiredAt:  s.LastFiredAt,
	}
}
