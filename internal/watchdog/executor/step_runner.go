package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// tier0AllowedChecks is the set of diagnostic check names permitted at Tier 0.
// Tier 2/3 checks require elevated privilege and are not executed by the executor.
var tier0AllowedChecks = map[string]bool{
	"cpu":        true,
	"memory":     true,
	"disk":       true,
	"network":    true,
	"processes":  true,
	"uptime":     true,
	"load":       true,
	"hostname":   true,
	"os_version": true,
	"kernel":     true,
}

// PlaybookStep is the typed representation of one element in Playbook.Steps.
type PlaybookStep struct {
	ID            string
	Type          string // "diagnostic" | "log_search" | "metric_query" | "metric_trend" | "wait"
	Check         string // for type=diagnostic
	Params        map[string]string
	TimeoutS      int    // default 30
	OnError       string // "continue" | "abort", default "continue"
	ParallelGroup string

	// log_search
	Query   string
	Minutes int
	Limit   int

	// metric_trend / metric_query
	Metric       string
	RangeMinutes int
	StepSeconds  int

	// wait
	DurationS int

	// host_id_override (diagnostic)
	HostIDOverride string
}

// parseSteps converts the raw []map[string]any slice from storage into typed PlaybookStep values.
func parseSteps(raw []map[string]any) ([]PlaybookStep, []error) {
	steps := make([]PlaybookStep, 0, len(raw))
	var errs []error
	for i, m := range raw {
		s, err := parseStep(m)
		if err != nil {
			errs = append(errs, fmt.Errorf("step[%d]: %w", i, err))
			// Append a zero-value step so indices remain aligned.
			steps = append(steps, PlaybookStep{})
			continue
		}
		steps = append(steps, s)
	}
	return steps, errs
}

func parseStep(m map[string]any) (PlaybookStep, error) {
	s := PlaybookStep{
		TimeoutS: 30,
		OnError:  "continue",
	}

	if v, ok := stringField(m, "id"); ok {
		s.ID = v
	}
	if v, ok := stringField(m, "type"); ok {
		s.Type = v
	}
	if v, ok := stringField(m, "check"); ok {
		s.Check = v
	}
	if v, ok := stringField(m, "on_error"); ok {
		s.OnError = v
	}
	if v, ok := stringField(m, "parallel_group"); ok {
		s.ParallelGroup = v
	}
	if v, ok := stringField(m, "query"); ok {
		s.Query = v
	}
	if v, ok := stringField(m, "metric"); ok {
		s.Metric = v
	}
	if v, ok := stringField(m, "host_id_override"); ok {
		s.HostIDOverride = v
	}
	if v, ok := intField(m, "timeout_s"); ok {
		s.TimeoutS = v
	}
	if v, ok := intField(m, "minutes"); ok {
		s.Minutes = v
	}
	if v, ok := intField(m, "limit"); ok {
		s.Limit = v
	}
	if v, ok := intField(m, "range_minutes"); ok {
		s.RangeMinutes = v
	}
	if v, ok := intField(m, "step_seconds"); ok {
		s.StepSeconds = v
	}
	if v, ok := intField(m, "duration_s"); ok {
		s.DurationS = v
	}

	// params: map[string]string or map[string]any
	if raw, ok := m["params"]; ok {
		switch p := raw.(type) {
		case map[string]string:
			s.Params = p
		case map[string]any:
			s.Params = make(map[string]string, len(p))
			for k, v := range p {
				s.Params[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	return s, nil
}

func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func intField(m map[string]any, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// runStep dispatches a single step to the appropriate handler.
func (e *PlaybookExecutor) runStep(ctx context.Context, step PlaybookStep, req InvestigationRequest, prevResults map[string]StepResult) StepResult {
	// Per-step timeout.
	timeoutS := step.TimeoutS
	if timeoutS <= 0 {
		timeoutS = 30
	}
	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()

	start := time.Now()
	var result StepResult

	switch step.Type {
	case "diagnostic":
		result = e.runDiagnosticStep(stepCtx, step, req)
	case "log_search":
		result = e.runLogSearchStep(stepCtx, step, req, prevResults)
	case "metric_trend", "metric_query":
		result = e.runMetricTrendStep(stepCtx, step, req, prevResults)
	case "wait":
		result = runWaitStep(stepCtx, step)
	default:
		result = StepResult{
			Status: "failed",
			Error:  fmt.Sprintf("unknown step type %q", step.Type),
		}
	}

	result.ID = step.ID
	result.Type = step.Type
	result.Check = step.Check
	result.DurationMS = time.Since(start).Milliseconds()
	return result
}

// runDiagnosticStep dispatches a diagnostic check command to the target agent.
func (e *PlaybookExecutor) runDiagnosticStep(ctx context.Context, step PlaybookStep, req InvestigationRequest) StepResult {
	if !tier0AllowedChecks[step.Check] {
		return StepResult{
			Status: "failed",
			Error:  fmt.Sprintf("check %q is not in tier-0 allowlist", step.Check),
		}
	}

	hostID := req.HostID
	if step.HostIDOverride != "" {
		hostID = step.HostIDOverride
	}

	output, err := e.cmdDispatch.Dispatch(ctx, req.FleetID, hostID, step.Check, step.Params)
	if err != nil {
		return StepResult{
			Status: "failed",
			Error:  err.Error(),
		}
	}

	// Try to parse output as JSON; fall back to raw string.
	var parsed any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		parsed = output
	}
	return StepResult{
		Status: "ok",
		Output: parsed,
	}
}

// runLogSearchStep executes a Loki log_search step.
func (e *PlaybookExecutor) runLogSearchStep(ctx context.Context, step PlaybookStep, req InvestigationRequest, prevResults map[string]StepResult) StepResult {
	if e.lokiClient == nil {
		return StepResult{Status: "failed", Error: "loki client not configured"}
	}

	minutes := step.Minutes
	if minutes <= 0 {
		minutes = 15
	}
	limit := step.Limit
	if limit <= 0 {
		limit = 100
	}

	end := time.Now()
	start := end.Add(-time.Duration(minutes) * time.Minute)

	// Perform variable substitution on the query.
	playbookName := req.PlaybookID
	vars := buildTemplateVars(req, playbookName, prevResults)
	query := Substitute(step.Query, vars)

	entries, err := e.lokiClient.QueryRange(ctx, query, start, end, limit)
	if err != nil {
		return StepResult{
			Status: "failed",
			Error:  err.Error(),
		}
	}

	lines := make([]map[string]any, 0, len(entries))
	for _, en := range entries {
		lines = append(lines, map[string]any{
			"ts":     en.Timestamp.UTC().Format(time.RFC3339Nano),
			"labels": en.Labels,
			"line":   en.Line,
		})
	}
	return StepResult{
		Status: "ok",
		Output: map[string]any{
			"count":   len(lines),
			"entries": lines,
		},
	}
}

// runMetricTrendStep executes a metric_trend or metric_query step.
func (e *PlaybookExecutor) runMetricTrendStep(ctx context.Context, step PlaybookStep, req InvestigationRequest, prevResults map[string]StepResult) StepResult {
	if e.vmClient == nil {
		return StepResult{Status: "failed", Error: "vm query client not configured"}
	}

	rangeMinutes := step.RangeMinutes
	if rangeMinutes <= 0 {
		rangeMinutes = 30
	}

	end := time.Now()
	start := end.Add(-time.Duration(rangeMinutes) * time.Minute)

	vars := buildTemplateVars(req, req.PlaybookID, prevResults)
	query := Substitute(step.Metric, vars)

	series, err := e.vmClient.QueryRange(ctx, query, start, end)
	if err != nil {
		return StepResult{
			Status: "failed",
			Error:  err.Error(),
		}
	}

	return StepResult{
		Status: "ok",
		Output: metricTrend(series),
	}
}

// runWaitStep pauses for the configured duration or until the context is cancelled.
func runWaitStep(ctx context.Context, step PlaybookStep) StepResult {
	dur := step.DurationS
	if dur <= 0 {
		dur = 1
	}
	timer := time.NewTimer(time.Duration(dur) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return StepResult{Status: "failed", Error: "context canceled"}
	case <-timer.C:
		return StepResult{Status: "ok", Output: map[string]any{"waited_s": dur}}
	}
}

// metricTrend assembles min/avg/max/sparkline statistics from a set of MetricSeries.
func metricTrend(series []MetricSeries) map[string]any {
	if len(series) == 0 {
		return map[string]any{"series_count": 0}
	}

	result := make([]map[string]any, 0, len(series))
	for _, s := range series {
		if len(s.Values) == 0 {
			result = append(result, map[string]any{
				"labels": s.Labels,
				"count":  0,
			})
			continue
		}

		min, max := s.Values[0].Value, s.Values[0].Value
		sum := 0.0
		sparkline := make([]float64, 0, len(s.Values))
		for _, v := range s.Values {
			if v.Value < min {
				min = v.Value
			}
			if v.Value > max {
				max = v.Value
			}
			sum += v.Value
			sparkline = append(sparkline, v.Value)
		}
		avg := sum / float64(len(s.Values))

		result = append(result, map[string]any{
			"labels":    s.Labels,
			"count":     len(s.Values),
			"min":       min,
			"max":       max,
			"avg":       avg,
			"sparkline": sparkline,
		})
	}
	return map[string]any{
		"series_count": len(series),
		"series":       result,
	}
}
