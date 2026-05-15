package executor

import "time"

// InvestigationRequest is dispatched by the TriggerRule engine or manual trigger.
type InvestigationRequest struct {
	InvestigationID string
	FleetID         string
	HostID          string
	PlaybookID      string
	TriggerType     string // "rule" | "schedule" | "manual"
	TriggerData     map[string]any
	RuleID          string // empty for manual/schedule
	TriggeredAt     time.Time
}

// StepResult records the output of one playbook step.
type StepResult struct {
	ID         string
	Type       string
	Check      string
	Status     string // "ok" | "failed" | "skipped"
	DurationMS int64
	Output     any    // JSON-serializable
	Error      string
}

// Snapshot is assembled from all StepResults + trigger context.
type Snapshot struct {
	InvestigationID string
	Host            map[string]any
	Trigger         map[string]any
	Playbook        map[string]any
	Steps           []StepResult
	TotalDurationMS int64
	Succeeded       int
	Failed          int
}
