package trigger

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

const (
	tickInterval      = 15 * time.Second
	statePersistEvery = 30 * time.Second
)

// HostResolver resolves a HostSelector JSONB to a list of host IDs.
type HostResolver interface {
	Resolve(ctx context.Context, fleetID string, selector map[string]any) ([]string, error)
}

// stateKey uniquely identifies a (rule, host) pair.
type stateKey struct {
	RuleID string
	HostID string
}

// RuleEngine evaluates watchdog rules on a periodic tick.
type RuleEngine struct {
	rules      storage.RuleRepo
	ruleState  storage.RuleStateRepo
	vmClient   VMClient
	dispatcher Dispatcher
	resolver   HostResolver
	logger     *slog.Logger

	stateMu sync.Mutex
	states  map[stateKey]*RuleState
}

// New creates a new RuleEngine.
func New(
	rules storage.RuleRepo,
	ruleState storage.RuleStateRepo,
	vmClient VMClient,
	dispatcher Dispatcher,
	resolver HostResolver,
	logger *slog.Logger,
) *RuleEngine {
	return &RuleEngine{
		rules:      rules,
		ruleState:  ruleState,
		vmClient:   vmClient,
		dispatcher: dispatcher,
		resolver:   resolver,
		logger:     logger,
		states:     make(map[stateKey]*RuleState),
	}
}

// Run starts the tick loop and the state-persist loop.
// It blocks until ctx is cancelled.
func (e *RuleEngine) Run(ctx context.Context) {
	if err := e.loadState(ctx); err != nil {
		e.logger.Error("trigger: failed to load initial state", "err", err)
	}

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	persistTicker := time.NewTicker(statePersistEvery)
	defer persistTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Persist state on clean shutdown (best-effort).
			e.persistState(context.Background())
			return
		case <-ticker.C:
			e.tick(ctx)
		case <-persistTicker.C:
			e.persistState(ctx)
		}
	}
}

// tick evaluates all enabled rules for all matching hosts.
func (e *RuleEngine) tick(ctx context.Context) {
	now := time.Now().UTC()

	// We need all fleet IDs. The RuleRepo.List requires a fleetID, so we use
	// a fleetID="" sentinel — for cross-fleet listing we iterate rules stored
	// in-memory. For a real multi-tenant system the engine would be scoped per
	// fleet; here we iterate the full state key space and query per unique rule.

	// Collect all unique rule IDs from in-memory state plus reload enabled rules
	// from DB. We re-load rules every tick to pick up changes.
	//
	// NOTE: RuleRepo.ListEnabled requires a fleetID. Since rules span multiple fleets
	// we collect fleetIDs from existing states and supplement with a full scan via
	// List with an empty FleetID (if the implementation supports it).
	// To avoid coupling to Postgres internals, we use the state keys to infer
	// which rules to process and fetch each rule individually.
	uniqueRuleIDs := e.uniqueRuleIDsFromState()

	// Also fetch all rules from DB via a broad list. We use an empty FleetID
	// with a large limit so all fleets are covered.
	dbRules, err := e.rules.List(ctx, storage.ListOptions{Limit: 10000})
	if err != nil {
		e.logger.Error("trigger: list rules", "err", err)
		// fall back to rules already in state
	}

	// Merge DB rules into a map for O(1) lookup and add new rule IDs.
	ruleMap := make(map[string]*storage.Rule, len(dbRules))
	for _, r := range dbRules {
		if r.Enabled {
			ruleMap[r.ID] = r
			uniqueRuleIDs[r.ID] = struct{}{}
		}
	}

	for ruleID := range uniqueRuleIDs {
		rule, ok := ruleMap[ruleID]
		if !ok {
			// Rule may have been deleted or disabled; skip but preserve state.
			continue
		}
		e.processRule(ctx, rule, now)
	}
}

// processRule evaluates a single rule across all its target hosts.
func (e *RuleEngine) processRule(ctx context.Context, rule *storage.Rule, now time.Time) {
	hostIDs, err := e.resolver.Resolve(ctx, rule.FleetID, rule.HostSelector)
	if err != nil {
		e.logger.Error("trigger: resolve hosts", "rule", rule.ID, "err", err)
		return
	}

	cond, err := parseCondition(rule.Condition)
	if err != nil {
		e.logger.Error("trigger: parse condition", "rule", rule.ID, "err", err)
		return
	}
	if err := validateCondition(cond, 0); err != nil {
		e.logger.Warn("trigger: invalid condition", "rule", rule.ID, "err", err)
		return
	}

	for _, hostID := range hostIDs {
		e.processRuleHost(ctx, rule, cond, hostID, now)
	}
}

// processRuleHost evaluates a rule for a single host and drives the state machine.
func (e *RuleEngine) processRuleHost(ctx context.Context, rule *storage.Rule, cond Condition, hostID string, now time.Time) {
	key := stateKey{RuleID: rule.ID, HostID: hostID}

	e.stateMu.Lock()
	s, exists := e.states[key]
	if !exists {
		s = newIdleState()
		e.states[key] = s
	}
	e.stateMu.Unlock()

	eval := &evaluator{vm: e.vmClient, hostID: hostID, now: now}
	condTrue, err := eval.evaluate(ctx, cond)
	if err != nil {
		e.logger.Warn("trigger: condition evaluation failed",
			"rule", rule.ID, "host", hostID, "err", err)
		return
	}

	next, dispatch := transition(s, condTrue, cond.DurationS, rule.CooldownS, now)

	if dispatch {
		// Move state to cooldown before dispatching.
		next = transitionAfterDispatch(next)

		req := InvestigationRequest{
			RuleID:     rule.ID,
			FleetID:    rule.FleetID,
			HostID:     hostID,
			PlaybookID: rule.PlaybookID,
			TriggerData: map[string]any{
				"rule_name":  rule.Name,
				"host_id":    hostID,
				"fired_at":   now.Format(time.RFC3339),
				"condition":  rule.Condition,
				"metric_val": condTrue,
			},
		}
		e.logger.Info("trigger: dispatching investigation",
			"rule", rule.ID, "host", hostID, "playbook", rule.PlaybookID)
		e.dispatcher.Dispatch(ctx, req)
	}

	if next.Phase != s.Phase {
		e.logger.Debug("trigger: state transition",
			"rule", rule.ID, "host", hostID,
			"from", s.Phase, "to", next.Phase)
	}

	e.stateMu.Lock()
	e.states[key] = next
	e.stateMu.Unlock()
}

// loadState populates the in-memory state map from the DB.
func (e *RuleEngine) loadState(ctx context.Context) error {
	all, err := e.ruleState.ListAll(ctx)
	if err != nil {
		return err
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	for _, ss := range all {
		e.states[stateKey{RuleID: ss.RuleID, HostID: ss.HostID}] = fromStorage(ss)
	}
	e.logger.Info("trigger: state loaded", "count", len(all))
	return nil
}

// persistState snapshots in-memory state and bulk-upserts to DB (best-effort).
func (e *RuleEngine) persistState(ctx context.Context) {
	states := e.snapshotStates()
	if len(states) == 0 {
		return
	}
	if err := e.ruleState.BulkUpsert(ctx, states); err != nil {
		e.logger.Error("trigger: persist state", "err", err, "count", len(states))
	}
}

// snapshotStates returns a copy of the current state map as a slice.
func (e *RuleEngine) snapshotStates() []*storage.RuleState {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	out := make([]*storage.RuleState, 0, len(e.states))
	for k, s := range e.states {
		out = append(out, s.toStorage(k.RuleID, k.HostID))
	}
	return out
}

// uniqueRuleIDsFromState returns a set of rule IDs currently tracked in-memory.
func (e *RuleEngine) uniqueRuleIDsFromState() map[string]struct{} {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	ids := make(map[string]struct{}, len(e.states))
	for k := range e.states {
		ids[k.RuleID] = struct{}{}
	}
	return ids
}
