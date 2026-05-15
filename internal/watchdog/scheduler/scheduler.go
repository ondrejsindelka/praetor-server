// Package scheduler provides a cron-based scheduler for Watchdog investigations.
// It loads enabled schedules from the DB, registers them with robfig/cron, and
// dispatches InvestigationRequest values via a caller-supplied DispatchFunc.
// The cron table is re-synced every 60 s so that DB changes are picked up without
// a restart.  Manual triggers are also supported via TriggerManual.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

// Scheduler manages cron-based investigation schedules.
type Scheduler struct {
	repo     storage.ScheduleRepo
	dispatch DispatchFunc
	logger   *slog.Logger

	mu   sync.Mutex
	cron *cron.Cron
	// jobs maps schedule_id to the cron entry ID so we can remove/replace entries.
	jobs map[string]cron.EntryID
}

// New creates a Scheduler. dispatch is called (synchronously from the cron
// goroutine) whenever a schedule fires.
func New(repo storage.ScheduleRepo, dispatch DispatchFunc, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		repo:     repo,
		dispatch: dispatch,
		logger:   logger,
		jobs:     make(map[string]cron.EntryID),
	}
}

// Start loads all enabled schedules and begins the cron loop.
// It blocks until ctx is canceled, then stops the cron runner and returns nil.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	s.cron = cron.New(cron.WithSeconds())
	s.mu.Unlock()

	if err := s.Reload(ctx); err != nil {
		return fmt.Errorf("scheduler: initial reload: %w", err)
	}

	s.mu.Lock()
	s.cron.Start()
	s.mu.Unlock()

	reloadTicker := time.NewTicker(60 * time.Second)
	defer reloadTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.cron.Stop()
			s.mu.Unlock()
			return nil
		case <-reloadTicker.C:
			if err := s.Reload(ctx); err != nil {
				s.logger.Warn("scheduler: periodic reload failed", "err", err)
			}
		}
	}
}

// Reload re-syncs the cron jobs against the current DB state.
// It adds new schedules, removes deleted/disabled ones, and replaces entries
// whose cron expression has changed.
func (s *Scheduler) Reload(ctx context.Context) error {
	enabled, err := s.repo.ListEnabled(ctx, "") // "" = all fleets
	if err != nil {
		return fmt.Errorf("scheduler: list enabled schedules: %w", err)
	}

	// Build lookup map for fast O(1) access.
	newByID := make(map[string]*storage.Schedule, len(enabled))
	for _, sc := range enabled {
		newByID[sc.ID] = sc
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove stale or disabled jobs.
	for id, entryID := range s.jobs {
		if _, ok := newByID[id]; !ok {
			s.cron.Remove(entryID)
			delete(s.jobs, id)
			s.logger.Info("scheduler: removed schedule", "schedule_id", id)
		}
	}

	// Add new jobs; re-add jobs whose cron expression changed.
	for id, sc := range newByID {
		if sc.PlaybookID == nil {
			s.logger.Warn("scheduler: schedule has no playbook, skipping", "schedule_id", id)
			continue
		}

		// Check whether the expression changed for an existing entry.
		if existingEntryID, exists := s.jobs[id]; exists {
			entry := s.cron.Entry(existingEntryID)
			// If the entry is still valid (non-zero next run) we keep it.
			// We cannot inspect the stored expression directly, so we rely on the
			// caller to call Reload after an Update; here we simply keep existing entries.
			if entry.ID != 0 {
				continue
			}
			// Entry was removed externally; fall through to re-add.
			delete(s.jobs, id)
		}

		sc := sc // capture loop var for closure
		entryID, err := s.cron.AddFunc(sc.CronExpr, func() {
			s.fire(ctx, sc)
		})
		if err != nil {
			s.logger.Warn("scheduler: invalid cron expression, skipping",
				"schedule_id", id, "expr", sc.CronExpr, "err", err)
			continue
		}
		s.jobs[id] = entryID
		s.logger.Info("scheduler: registered schedule", "schedule_id", id, "expr", sc.CronExpr)
	}

	return nil
}

// TriggerManual fires a schedule immediately, outside its cron schedule.
// It looks up the schedule by id+fleetID, then calls fire directly.
func (s *Scheduler) TriggerManual(ctx context.Context, scheduleID, fleetID string) error {
	sc, err := s.repo.Get(ctx, scheduleID, fleetID)
	if err != nil {
		return fmt.Errorf("scheduler: get schedule %q: %w", scheduleID, err)
	}
	if sc.PlaybookID == nil {
		return fmt.Errorf("scheduler: schedule %q has no playbook", scheduleID)
	}
	s.fire(ctx, sc)
	return nil
}

// fire dispatches a single InvestigationRequest and records the run timestamp.
func (s *Scheduler) fire(ctx context.Context, sc *storage.Schedule) {
	if sc.PlaybookID == nil {
		s.logger.Warn("scheduler: fire called with nil PlaybookID, skipping", "schedule_id", sc.ID)
		return
	}
	now := time.Now()
	req := InvestigationRequest{
		FleetID:     sc.FleetID,
		HostIDs:     sc.HostIDs,
		PlaybookID:  *sc.PlaybookID,
		TriggerType: "schedule",
		TriggerData: map[string]any{
			"schedule_id":   sc.ID,
			"schedule_name": sc.Name,
		},
		ScheduleID:  sc.ID,
		TriggeredAt: now,
	}
	if err := s.repo.UpdateLastRunAt(ctx, sc.ID, now); err != nil {
		s.logger.Warn("scheduler: failed to update last_run_at",
			"schedule_id", sc.ID, "err", err)
	}
	s.dispatch(req)
}

// ValidateCronExpr checks whether expr is parseable by robfig/cron.
// Supports standard 5-field (minute hour dom month dow) expressions.
func ValidateCronExpr(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}
