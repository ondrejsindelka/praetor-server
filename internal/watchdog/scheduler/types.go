package scheduler

import "time"

// InvestigationRequest is a local copy for the scheduler — avoids importing the executor package.
// When wiring in main.go it is adapted to executor.InvestigationRequest.
type InvestigationRequest struct {
	FleetID     string
	HostIDs     []string
	PlaybookID  string
	TriggerType string // "schedule"
	TriggerData map[string]any
	ScheduleID  string
	TriggeredAt time.Time
}

// DispatchFunc is the callback invoked when a schedule fires.
type DispatchFunc func(req InvestigationRequest)
