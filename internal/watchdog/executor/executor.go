// Package executor implements the Watchdog playbook execution engine.
// It receives InvestigationRequests, loads the relevant playbook, runs steps
// (with parallel group support), calls the LLM for analysis, persists the
// investigation record, and emits webhook events.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

const (
	maxSnapshotBytes  = 5 * 1024 * 1024 // 5 MB
	dedupeWindow      = 60 * time.Second
	workerPoolSize    = 5
	requestBufferSize = 100
)

// Status lifecycle constants.
const (
	StatusPending    = "pending"
	StatusCollecting = "collecting"
	StatusAnalyzing  = "analyzing"
	StatusDone       = "done"
	StatusFailed     = "failed"
)

// CommandDispatcher dispatches diagnostic checks to agents.
// Satisfied by the existing command.Broker (via an adapter).
type CommandDispatcher interface {
	Dispatch(ctx context.Context, fleetID, hostID string, check string, params map[string]string) (string, error)
}

// WebhookEmitter sends webhook events.
type WebhookEmitter interface {
	Emit(ctx context.Context, fleetID, event string, payload map[string]any)
}

// PlaybookExecutor runs playbooks and records investigations.
type PlaybookExecutor struct {
	playbookRepo storage.PlaybookRepo
	invRepo      storage.InvestigationRepo
	llm          LLMClient
	cmdDispatch  CommandDispatcher
	lokiClient   LokiClient
	vmClient     VMQueryClient
	webhooks     WebhookEmitter
	logger       *slog.Logger

	requestCh chan InvestigationRequest
	dedupeMu  sync.Mutex
	inFlight  map[dedupeKey]time.Time // (host_id, rule_id) → started_at
}

type dedupeKey struct {
	HostID string
	RuleID string
}

// New creates a PlaybookExecutor and starts the worker pool.
func New(
	playbookRepo storage.PlaybookRepo,
	invRepo storage.InvestigationRepo,
	llm LLMClient,
	cmdDispatch CommandDispatcher,
	lokiClient LokiClient,
	vmClient VMQueryClient,
	webhooks WebhookEmitter,
	logger *slog.Logger,
) *PlaybookExecutor {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &PlaybookExecutor{
		playbookRepo: playbookRepo,
		invRepo:      invRepo,
		llm:          llm,
		cmdDispatch:  cmdDispatch,
		lokiClient:   lokiClient,
		vmClient:     vmClient,
		webhooks:     webhooks,
		logger:       logger,
		requestCh:    make(chan InvestigationRequest, requestBufferSize),
		inFlight:     make(map[dedupeKey]time.Time),
	}
}

// Start launches the worker pool goroutines. Call with a long-running context.
func (e *PlaybookExecutor) Start(ctx context.Context) {
	for i := 0; i < workerPoolSize; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case req, ok := <-e.requestCh:
					if !ok {
						return
					}
					if err := e.execute(ctx, req); err != nil {
						e.logger.Error("investigation failed",
							"investigation_id", req.InvestigationID,
							"host_id", req.HostID,
							"err", err,
						)
					}
				}
			}
		}()
	}
}

// Dispatch enqueues an InvestigationRequest.
// Drops the request if:
//   - the request channel is full (non-blocking), or
//   - the same (host_id, rule_id) pair is already in-flight within dedupeWindow.
func (e *PlaybookExecutor) Dispatch(ctx context.Context, req InvestigationRequest) {
	// Deduplication only applies to rule-triggered investigations.
	if req.RuleID != "" {
		key := dedupeKey{HostID: req.HostID, RuleID: req.RuleID}
		e.dedupeMu.Lock()
		if startedAt, ok := e.inFlight[key]; ok && time.Since(startedAt) < dedupeWindow {
			e.dedupeMu.Unlock()
			e.logger.Debug("investigation deduplicated",
				"host_id", req.HostID,
				"rule_id", req.RuleID,
			)
			return
		}
		e.inFlight[key] = time.Now()
		e.dedupeMu.Unlock()
	}

	select {
	case e.requestCh <- req:
	default:
		e.logger.Warn("investigation queue full, dropping request",
			"investigation_id", req.InvestigationID,
			"host_id", req.HostID,
		)
		// Clean up the in-flight entry we just added since we're not actually running it.
		if req.RuleID != "" {
			key := dedupeKey{HostID: req.HostID, RuleID: req.RuleID}
			e.dedupeMu.Lock()
			delete(e.inFlight, key)
			e.dedupeMu.Unlock()
		}
	}
}

// execute runs one investigation synchronously.
func (e *PlaybookExecutor) execute(ctx context.Context, req InvestigationRequest) error {
	start := time.Now()

	// Remove the in-flight dedup entry when done.
	if req.RuleID != "" {
		defer func() {
			key := dedupeKey{HostID: req.HostID, RuleID: req.RuleID}
			e.dedupeMu.Lock()
			delete(e.inFlight, key)
			e.dedupeMu.Unlock()
		}()
	}

	log := e.logger.With(
		"investigation_id", req.InvestigationID,
		"host_id", req.HostID,
		"playbook_id", req.PlaybookID,
	)

	// Create the investigation record.
	invID := req.InvestigationID
	ruleID := &req.RuleID
	if req.RuleID == "" {
		ruleID = nil
	}
	playbookID := &req.PlaybookID
	if req.PlaybookID == "" {
		playbookID = nil
	}

	inv := &storage.Investigation{
		ID:          invID,
		FleetID:     req.FleetID,
		RuleID:      ruleID,
		PlaybookID:  playbookID,
		TriggerType: req.TriggerType,
		TriggeredAt: req.TriggeredAt,
		HostIDs:     []string{req.HostID},
		TriggerData: req.TriggerData,
		Status:      StatusPending,
	}
	if err := e.invRepo.Create(ctx, inv); err != nil {
		return fmt.Errorf("executor: create investigation: %w", err)
	}

	// Load the playbook.
	playbook, err := e.playbookRepo.Get(ctx, req.PlaybookID, req.FleetID)
	if err != nil {
		errMsg := fmt.Sprintf("load playbook: %v", err)
		_ = e.invRepo.UpdateStatus(ctx, invID, StatusFailed, &errMsg)
		return fmt.Errorf("executor: %s", errMsg)
	}

	// Transition to collecting.
	if err := e.invRepo.UpdateStatus(ctx, invID, StatusCollecting, nil); err != nil {
		log.Warn("failed to update status to collecting", "err", err)
	}

	// Parse steps.
	steps, parseErrs := parseSteps(playbook.Steps)
	if len(parseErrs) > 0 {
		log.Warn("some steps failed to parse", "count", len(parseErrs))
	}

	// Execute steps in groups.
	allResults, aborted := e.runSteps(ctx, steps, req)

	totalDuration := time.Since(start)

	// Tally results.
	succeeded, failed := 0, 0
	for _, r := range allResults {
		if r.Status == "ok" {
			succeeded++
		} else if r.Status == "failed" {
			failed++
		}
	}

	// Assemble snapshot.
	snap := &Snapshot{
		InvestigationID: invID,
		Host: map[string]any{
			"id": req.HostID,
		},
		Trigger: map[string]any{
			"type":         req.TriggerType,
			"rule_id":      req.RuleID,
			"triggered_at": req.TriggeredAt.UTC().Format(time.RFC3339),
			"data":         req.TriggerData,
		},
		Playbook: map[string]any{
			"id":   playbook.ID,
			"name": playbook.Name,
		},
		Steps:           allResults,
		TotalDurationMS: totalDuration.Milliseconds(),
		Succeeded:       succeeded,
		Failed:          failed,
	}

	capSnapshot(snap)

	snapMap, err := snapshotToMap(snap)
	if err != nil {
		log.Warn("failed to marshal snapshot", "err", err)
		snapMap = map[string]any{"error": "snapshot marshal failed"}
	}

	if err := e.invRepo.UpdateSnapshot(ctx, invID, snapMap); err != nil {
		log.Warn("failed to update snapshot", "err", err)
	}

	// If aborted early, mark as failed.
	if aborted {
		errMsg := "investigation aborted: step failure with on_error=abort"
		_ = e.invRepo.UpdateStatus(ctx, invID, StatusFailed, &errMsg)
		if e.webhooks != nil {
			e.webhooks.Emit(ctx, req.FleetID, "investigation.failed", map[string]any{
				"investigation_id": invID,
				"host_id":          req.HostID,
				"reason":           errMsg,
			})
		}
		return fmt.Errorf("executor: %s", errMsg)
	}

	// Run LLM analysis if configured.
	if e.llm != nil && playbook.LLMPrompt != nil && *playbook.LLMPrompt != "" {
		if err := e.invRepo.UpdateStatus(ctx, invID, StatusAnalyzing, nil); err != nil {
			log.Warn("failed to update status to analyzing", "err", err)
		}

		llmResp, llmErr := e.runLLMAnalysis(ctx, playbook, snap)
		if llmErr != nil {
			log.Warn("LLM analysis failed", "err", llmErr)
		} else {
			metadata := map[string]any{
				"provider":       llmResp.Provider,
				"input_tokens":   llmResp.InputTokens,
				"output_tokens":  llmResp.OutputTokens,
				"finish_reason":  llmResp.FinishReason,
				"latency_ms":     llmResp.LatencyMS,
			}
			if err := e.invRepo.UpdateLLMAnalysis(ctx, invID, llmResp.Content, metadata); err != nil {
				log.Warn("failed to update LLM analysis", "err", err)
			}
		}
	}

	// Mark done.
	if err := e.invRepo.Complete(ctx, invID, StatusDone); err != nil {
		log.Warn("failed to complete investigation", "err", err)
	}

	// Emit webhook.
	if e.webhooks != nil {
		e.webhooks.Emit(ctx, req.FleetID, "investigation.completed", map[string]any{
			"investigation_id": invID,
			"host_id":          req.HostID,
			"succeeded":        succeeded,
			"failed":           failed,
			"duration_ms":      totalDuration.Milliseconds(),
		})
	}

	log.Info("investigation completed",
		"succeeded", succeeded,
		"failed", failed,
		"duration_ms", totalDuration.Milliseconds(),
	)
	return nil
}

// runSteps executes all playbook steps respecting parallel groups and on_error semantics.
// Returns all collected results and whether execution was aborted.
func (e *PlaybookExecutor) runSteps(ctx context.Context, steps []PlaybookStep, req InvestigationRequest) ([]StepResult, bool) {
	// Group steps in order: sequential steps each form their own singleton group;
	// steps with the same non-empty parallel_group are batched together.
	// We preserve definition order for the groups.
	type group struct {
		key   string // "" means sequential (each step its own group)
		steps []PlaybookStep
	}

	var groups []group
	groupIdx := map[string]int{}

	for _, step := range steps {
		if step.ParallelGroup == "" {
			// Each sequential step is its own singleton group.
			groups = append(groups, group{key: "", steps: []PlaybookStep{step}})
		} else {
			if idx, ok := groupIdx[step.ParallelGroup]; ok {
				groups[idx].steps = append(groups[idx].steps, step)
			} else {
				groupIdx[step.ParallelGroup] = len(groups)
				groups = append(groups, group{key: step.ParallelGroup, steps: []PlaybookStep{step}})
			}
		}
	}

	allResults := make([]StepResult, 0, len(steps))
	prevResults := make(map[string]StepResult)

	for _, g := range groups {
		var groupResults []StepResult
		if len(g.steps) == 1 {
			// Run single step directly.
			r := e.runStep(ctx, g.steps[0], req, prevResults)
			groupResults = []StepResult{r}
		} else {
			groupResults = e.runGroup(ctx, g.steps, req, prevResults)
		}

		aborted := false
		for _, r := range groupResults {
			allResults = append(allResults, r)
			prevResults[r.ID] = r
			// Check abort condition: only for steps explicitly set on_error=abort.
			if r.Status == "failed" {
				// Find the original step to check on_error.
				for _, s := range g.steps {
					if s.ID == r.ID && s.OnError == "abort" {
						aborted = true
					}
				}
			}
		}
		if aborted {
			// Mark remaining steps as skipped.
			remaining := collectRemaining(steps, allResults)
			for _, s := range remaining {
				allResults = append(allResults, StepResult{
					ID:     s.ID,
					Type:   s.Type,
					Check:  s.Check,
					Status: "skipped",
					Error:  "aborted by previous step failure",
				})
			}
			return allResults, true
		}
	}

	return allResults, false
}

// runGroup runs a set of steps concurrently using errgroup.
func (e *PlaybookExecutor) runGroup(ctx context.Context, steps []PlaybookStep, req InvestigationRequest, prevResults map[string]StepResult) []StepResult {
	g, gctx := errgroup.WithContext(ctx)
	results := make([]StepResult, len(steps))
	for i, step := range steps {
		i, step := i, step
		g.Go(func() error {
			results[i] = e.runStep(gctx, step, req, prevResults)
			return nil // step errors are stored in StepResult, not propagated
		})
	}
	_ = g.Wait()
	return results
}

// runLLMAnalysis constructs the LLM prompt and calls the LLM client.
func (e *PlaybookExecutor) runLLMAnalysis(ctx context.Context, playbook *storage.Playbook, snap *Snapshot) (*LLMResponse, error) {
	// Build system prompt from playbook LLM config, fall back to default.
	provider := ""
	model := ""
	maxTokens := 1024
	var temperature float32 = 0.3

	if playbook.LLMConfig != nil {
		if v, ok := playbook.LLMConfig["provider"].(string); ok {
			provider = v
		}
		if v, ok := playbook.LLMConfig["model"].(string); ok {
			model = v
		}
		if v, ok := playbook.LLMConfig["max_tokens"].(float64); ok {
			maxTokens = int(v)
		}
		if v, ok := playbook.LLMConfig["temperature"].(float64); ok {
			temperature = float32(v)
		}
	}

	snapJSON, _ := json.MarshalIndent(snap, "", "  ")

	llmReq := LLMRequest{
		Provider:    provider,
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		Messages: []LLMMessage{
			{Role: "system", Content: *playbook.LLMPrompt},
			{Role: "user", Content: string(snapJSON)},
		},
	}

	return e.llm.Complete(ctx, llmReq)
}

// capSnapshot truncates step outputs to keep the snapshot under maxSnapshotBytes.
func capSnapshot(s *Snapshot) {
	raw, _ := json.Marshal(s)
	if len(raw) <= maxSnapshotBytes {
		return
	}

	// Sort steps by output size descending and truncate the largest first.
	type indexedSize struct {
		idx  int
		size int
	}
	sizes := make([]indexedSize, len(s.Steps))
	for i, step := range s.Steps {
		b, _ := json.Marshal(step.Output)
		sizes[i] = indexedSize{idx: i, size: len(b)}
	}
	sort.Slice(sizes, func(a, b int) bool {
		return sizes[a].size > sizes[b].size
	})

	for _, is := range sizes {
		s.Steps[is.idx].Output = "[truncated: snapshot size limit exceeded]"
		raw, _ = json.Marshal(s)
		if len(raw) <= maxSnapshotBytes {
			return
		}
	}
	// If still too large, clear all outputs.
	for i := range s.Steps {
		s.Steps[i].Output = nil
	}
}

// snapshotToMap converts a Snapshot to map[string]any for storage.
func snapshotToMap(s *Snapshot) (map[string]any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// collectRemaining returns steps that have not yet produced a result.
func collectRemaining(steps []PlaybookStep, done []StepResult) []PlaybookStep {
	doneIDs := make(map[string]bool, len(done))
	for _, r := range done {
		doneIDs[r.ID] = true
	}
	var rem []PlaybookStep
	for _, s := range steps {
		if !doneIDs[s.ID] {
			rem = append(rem, s)
		}
	}
	return rem
}
