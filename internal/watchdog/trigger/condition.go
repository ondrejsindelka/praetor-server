package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// maxConditionDepth is the maximum nesting depth for compound conditions.
const maxConditionDepth = 3

// Condition describes a trigger condition parsed from the JSONB rule.Condition field.
type Condition struct {
	// Simple form
	Metric    string  `json:"metric"`
	Op        string  `json:"op"`        // >, <, >=, <=, ==, delta_>, delta_pct_>
	Threshold float64 `json:"threshold"`
	DurationS int     `json:"duration_s"`

	// Compound form
	AllOf []Condition `json:"all_of"`
	AnyOf []Condition `json:"any_of"`
}

// parseCondition converts the raw JSONB map to a typed Condition.
func parseCondition(raw map[string]any) (Condition, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return Condition{}, fmt.Errorf("condition: marshal: %w", err)
	}
	var c Condition
	if err := json.Unmarshal(b, &c); err != nil {
		return Condition{}, fmt.Errorf("condition: unmarshal: %w", err)
	}
	return c, nil
}

// validateCondition checks nesting depth and operator validity.
func validateCondition(c Condition, depth int) error {
	if depth > maxConditionDepth {
		return fmt.Errorf("condition: max nesting depth %d exceeded", maxConditionDepth)
	}
	if len(c.AllOf) > 0 {
		for _, sub := range c.AllOf {
			if err := validateCondition(sub, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if len(c.AnyOf) > 0 {
		for _, sub := range c.AnyOf {
			if err := validateCondition(sub, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	// Leaf node: must have metric + op
	if c.Metric == "" {
		return fmt.Errorf("condition: leaf node missing metric")
	}
	switch c.Op {
	case ">", "<", ">=", "<=", "==", "delta_>", "delta_pct_>":
		// valid
	default:
		return fmt.Errorf("condition: unknown op %q", c.Op)
	}
	return nil
}

// evaluator evaluates conditions by querying VM.
type evaluator struct {
	vm     VMClient
	hostID string
	now    time.Time
}

// evaluate returns true if the condition is satisfied.
func (e *evaluator) evaluate(ctx context.Context, c Condition) (bool, error) {
	if len(c.AllOf) > 0 {
		for _, sub := range c.AllOf {
			ok, err := e.evaluate(ctx, sub)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	if len(c.AnyOf) > 0 {
		for _, sub := range c.AnyOf {
			ok, err := e.evaluate(ctx, sub)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	return e.evaluateLeaf(ctx, c)
}

// evaluateLeaf evaluates a simple (non-compound) condition.
func (e *evaluator) evaluateLeaf(ctx context.Context, c Condition) (bool, error) {
	metricQuery := fmt.Sprintf(`%s{host_id=%q}`, c.Metric, e.hostID)

	switch c.Op {
	case ">", "<", ">=", "<=", "==":
		val, found, err := e.vm.QueryInstant(ctx, metricQuery)
		if err != nil {
			return false, fmt.Errorf("condition: query %q: %w", metricQuery, err)
		}
		if !found {
			return false, nil
		}
		return applyOp(c.Op, val, c.Threshold), nil

	case "delta_>":
		// range query, compute last - first
		start := e.now.Add(-time.Duration(c.DurationS) * time.Second)
		samples, err := e.vm.QueryRange(ctx, metricQuery, start, e.now)
		if err != nil {
			return false, fmt.Errorf("condition: range query %q: %w", metricQuery, err)
		}
		delta, ok := computeDelta(samples)
		if !ok {
			return false, nil
		}
		return delta > c.Threshold, nil

	case "delta_pct_>":
		// range query, compute percent change
		start := e.now.Add(-time.Duration(c.DurationS) * time.Second)
		samples, err := e.vm.QueryRange(ctx, metricQuery, start, e.now)
		if err != nil {
			return false, fmt.Errorf("condition: range query %q: %w", metricQuery, err)
		}
		pct, ok := computeDeltaPct(samples)
		if !ok {
			return false, nil
		}
		return pct > c.Threshold, nil

	default:
		return false, fmt.Errorf("condition: unsupported op %q", c.Op)
	}
}

// applyOp compares val with threshold using the given operator string.
func applyOp(op string, val, threshold float64) bool {
	switch op {
	case ">":
		return val > threshold
	case "<":
		return val < threshold
	case ">=":
		return val >= threshold
	case "<=":
		return val <= threshold
	case "==":
		return val == threshold
	}
	return false
}

// computeDelta returns (last - first) from the first MetricSample, if available.
func computeDelta(samples []MetricSample) (float64, bool) {
	if len(samples) == 0 || len(samples[0].Values) < 2 {
		return 0, false
	}
	pts := samples[0].Values
	return pts[len(pts)-1].Value - pts[0].Value, true
}

// computeDeltaPct returns ((last - first) / first * 100) from the first MetricSample.
func computeDeltaPct(samples []MetricSample) (float64, bool) {
	if len(samples) == 0 || len(samples[0].Values) < 2 {
		return 0, false
	}
	pts := samples[0].Values
	first := pts[0].Value
	if first == 0 {
		return 0, false
	}
	last := pts[len(pts)-1].Value
	return (last - first) / first * 100.0, true
}
