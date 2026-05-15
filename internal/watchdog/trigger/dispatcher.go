package trigger

import "context"

// InvestigationRequest carries the trigger context to the playbook dispatcher.
type InvestigationRequest struct {
	RuleID      string
	FleetID     string
	HostID      string
	PlaybookID  string
	TriggerData map[string]any
}

// Dispatcher receives investigation requests and executes the associated playbooks.
type Dispatcher interface {
	Dispatch(ctx context.Context, req InvestigationRequest)
}
