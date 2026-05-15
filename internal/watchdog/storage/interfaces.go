// Package storage defines types and repository interfaces for the Watchdog subsystem.
package storage

import (
	"context"
	"time"
)

// Rule mirrors the watchdog_rules table.
type Rule struct {
	ID           string
	FleetID      string
	Name         string
	Description  string
	Enabled      bool
	HostSelector map[string]any // JSONB
	Condition    map[string]any // JSONB
	PlaybookID   string
	CooldownS    int
	Priority     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// RuleState mirrors the watchdog_rule_state table.
type RuleState struct {
	RuleID       string
	HostID       string
	Phase        string // idle | pending | fired | cooldown
	PendingSince *time.Time
	LastFiredAt  *time.Time
	UpdatedAt    time.Time
}

// Playbook mirrors the watchdog_playbooks table.
type Playbook struct {
	ID          string
	FleetID     string
	Name        string
	Description string
	Steps       []map[string]any // JSONB array
	LLMPrompt   *string
	LLMConfig   map[string]any // JSONB
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Investigation mirrors the watchdog_investigations table.
type Investigation struct {
	ID          string
	FleetID     string
	RuleID      *string
	PlaybookID  *string
	TriggerType string
	TriggeredAt time.Time
	HostIDs     []string
	TriggerData map[string]any
	Snapshot    map[string]any
	LLMAnalysis *string
	LLMMetadata map[string]any
	Status      string
	Error       *string
	CompletedAt *time.Time
	CreatedAt   time.Time
}

// Schedule mirrors the watchdog_schedules table.
type Schedule struct {
	ID         string
	FleetID    string
	Name       string
	CronExpr   string
	PlaybookID *string
	HostIDs    []string
	Enabled    bool
	LastRunAt  *time.Time
	CreatedAt  time.Time
}

// LLMProvider mirrors the watchdog_llm_providers table.
type LLMProvider struct {
	ID           string
	FleetID      string
	Name         string
	Provider     string
	Endpoint     string
	APIKeyEnc    []byte // encrypted, nil for Ollama
	DefaultModel string
	IsDefault    bool
	CreatedAt    time.Time
}

// Webhook mirrors the watchdog_webhooks table.
type Webhook struct {
	ID        string
	FleetID   string
	Name      string
	URL       string
	Events    []string
	SecretEnc []byte // encrypted, nil if no secret
	Enabled   bool
	CreatedAt time.Time
}

// ListOptions are common pagination/filtering options.
type ListOptions struct {
	FleetID string
	Limit   int
	Offset  int
}

// InvestigationListOptions extends ListOptions with investigation-specific filters.
type InvestigationListOptions struct {
	ListOptions
	HostID string
	Status string
	RuleID string
	Since  *time.Time
	Until  *time.Time
}

// RuleRepo provides CRUD operations for watchdog rules.
type RuleRepo interface {
	Create(ctx context.Context, r *Rule) error
	Get(ctx context.Context, id, fleetID string) (*Rule, error)
	List(ctx context.Context, opts ListOptions) ([]*Rule, error)
	ListEnabled(ctx context.Context, fleetID string) ([]*Rule, error)
	Update(ctx context.Context, r *Rule) error
	Delete(ctx context.Context, id, fleetID string) error
}

// RuleStateRepo provides upsert/query operations for per-host rule state.
type RuleStateRepo interface {
	Upsert(ctx context.Context, s *RuleState) error
	Get(ctx context.Context, ruleID, hostID string) (*RuleState, error)
	ListByRule(ctx context.Context, ruleID string) ([]*RuleState, error)
	ListAll(ctx context.Context) ([]*RuleState, error)
	BulkUpsert(ctx context.Context, states []*RuleState) error
}

// PlaybookRepo provides CRUD operations for watchdog playbooks.
type PlaybookRepo interface {
	Create(ctx context.Context, p *Playbook) error
	Get(ctx context.Context, id, fleetID string) (*Playbook, error)
	List(ctx context.Context, opts ListOptions) ([]*Playbook, error)
	Update(ctx context.Context, p *Playbook) error
	Delete(ctx context.Context, id, fleetID string) error
}

// InvestigationRepo provides CRUD and state-transition operations for investigations.
type InvestigationRepo interface {
	Create(ctx context.Context, inv *Investigation) error
	Get(ctx context.Context, id, fleetID string) (*Investigation, error)
	List(ctx context.Context, opts InvestigationListOptions) ([]*Investigation, error)
	UpdateStatus(ctx context.Context, id, status string, errMsg *string) error
	UpdateSnapshot(ctx context.Context, id string, snapshot map[string]any) error
	UpdateLLMAnalysis(ctx context.Context, id string, analysis string, metadata map[string]any) error
	Complete(ctx context.Context, id string, status string) error
}

// ScheduleRepo provides CRUD operations for watchdog schedules.
type ScheduleRepo interface {
	Create(ctx context.Context, s *Schedule) error
	Get(ctx context.Context, id, fleetID string) (*Schedule, error)
	List(ctx context.Context, opts ListOptions) ([]*Schedule, error)
	ListEnabled(ctx context.Context, fleetID string) ([]*Schedule, error)
	Update(ctx context.Context, s *Schedule) error
	Delete(ctx context.Context, id, fleetID string) error
	UpdateLastRunAt(ctx context.Context, id string, t time.Time) error
}

// LLMProviderRepo provides CRUD operations for LLM provider configurations.
type LLMProviderRepo interface {
	Create(ctx context.Context, p *LLMProvider) error
	Get(ctx context.Context, id, fleetID string) (*LLMProvider, error)
	GetDefault(ctx context.Context, fleetID string) (*LLMProvider, error)
	List(ctx context.Context, opts ListOptions) ([]*LLMProvider, error)
	Update(ctx context.Context, p *LLMProvider) error
	Delete(ctx context.Context, id, fleetID string) error
}

// WebhookRepo provides CRUD operations for outbound webhook configurations.
type WebhookRepo interface {
	Create(ctx context.Context, w *Webhook) error
	Get(ctx context.Context, id, fleetID string) (*Webhook, error)
	List(ctx context.Context, opts ListOptions) ([]*Webhook, error)
	ListEnabled(ctx context.Context, fleetID string) ([]*Webhook, error)
	Update(ctx context.Context, w *Webhook) error
	Delete(ctx context.Context, id, fleetID string) error
}
