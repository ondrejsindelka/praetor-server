package notifier

import "time"

const (
	EventInvestigationStarted   = "investigation.started"
	EventInvestigationCompleted = "investigation.completed"
	EventInvestigationFailed    = "investigation.failed"
	EventRuleFired              = "rule.fired"
)

// WebhookPayload is the envelope sent to webhook URLs.
type WebhookPayload struct {
	Event     string         `json:"event"`
	FleetID   string         `json:"fleet_id"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data"`
}
