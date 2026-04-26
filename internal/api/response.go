// Package api implements the praetor-server REST API.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

// writeJSON serialises v to JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode failed", "err", err)
	}
}

// writeError writes {"error": msg} with the given HTTP status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// HostResponse is the JSON representation of a host.
type HostResponse struct {
	ID              string            `json:"id"`
	Hostname        string            `json:"hostname"`
	OS              string            `json:"os"`
	OSVersion       string            `json:"os_version"`
	Kernel          string            `json:"kernel"`
	Arch            string            `json:"arch"`
	CPUCores        int               `json:"cpu_cores"`
	MemoryBytes     int64             `json:"memory_bytes"`
	MachineID       *string           `json:"machine_id,omitempty"`
	IPAddresses     []string          `json:"ip_addresses"`
	Labels          map[string]string `json:"labels"`
	FirstSeenAt     time.Time         `json:"first_seen_at"`
	LastHeartbeatAt time.Time         `json:"last_heartbeat_at"`
	Status          string            `json:"status"`
	AgentVersion    string            `json:"agent_version"`
	OrgID           string            `json:"org_id"`
}

// TokenResponse is the JSON representation of an enrollment token (no hash, no plain token).
// Plain token is NEVER included here — only in IssueTokenResponse.
type TokenResponse struct {
	ID           string     `json:"id"`
	Label        *string    `json:"label,omitempty"`
	OrgID        string     `json:"org_id"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	UsedByHostID *string    `json:"used_by_host_id,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// IssueTokenResponse is returned ONLY by POST /v1/tokens.
// The plain token is shown exactly once and never stored in plaintext.
type IssueTokenResponse struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	Label     string    `json:"label"`
	ExpiresAt time.Time `json:"expires_at"`
}

// IssueTokenRequest is the body for POST /v1/tokens.
type IssueTokenRequest struct {
	Label      string `json:"label"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// toHostResponse converts a store.Host to a HostResponse for the API.
// Pointer fields are dereferenced safely; nil pointers become zero values.
func toHostResponse(h *store.Host) HostResponse {
	var osVersion, kernel, agentVersion string
	var cpuCores int
	var memoryBytes int64
	var lastHeartbeat time.Time

	if h.OSVersion != nil {
		osVersion = *h.OSVersion
	}
	if h.Kernel != nil {
		kernel = *h.Kernel
	}
	if h.AgentVersion != nil {
		agentVersion = *h.AgentVersion
	}
	if h.CPUCores != nil {
		cpuCores = int(*h.CPUCores)
	}
	if h.MemoryBytes != nil {
		memoryBytes = *h.MemoryBytes
	}
	if h.LastHeartbeatAt != nil {
		lastHeartbeat = *h.LastHeartbeatAt
	}

	ips := h.IPAddresses
	if ips == nil {
		ips = []string{}
	}
	labels := h.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	return HostResponse{
		ID:              h.ID,
		Hostname:        h.Hostname,
		OS:              h.OS,
		OSVersion:       osVersion,
		Kernel:          kernel,
		Arch:            h.Arch,
		CPUCores:        cpuCores,
		MemoryBytes:     memoryBytes,
		MachineID:       h.MachineID,
		IPAddresses:     ips,
		Labels:          labels,
		FirstSeenAt:     h.FirstSeenAt,
		LastHeartbeatAt: lastHeartbeat,
		Status:          h.Status,
		AgentVersion:    agentVersion,
		OrgID:           h.OrgID,
	}
}

// toTokenResponse converts a store.EnrollmentToken to a TokenResponse.
// token_hash is intentionally excluded — never included in API responses.
func toTokenResponse(t *store.EnrollmentToken) TokenResponse {
	return TokenResponse{
		ID:           t.ID,
		Label:        t.Label,
		OrgID:        t.OrgID,
		CreatedAt:    t.CreatedAt,
		ExpiresAt:    t.ExpiresAt,
		UsedAt:       t.UsedAt,
		UsedByHostID: t.UsedByHostID,
		RevokedAt:    t.RevokedAt,
	}
}

// IssueCommandRequest is the body for POST /v1/commands.
type IssueCommandRequest struct {
	HostID         string                 `json:"host_id"`
	Tier           int                    `json:"tier"`
	Reason         string                 `json:"reason"`
	IssuedBy       string                 `json:"issued_by"`
	TimeoutSeconds int32                  `json:"timeout_seconds"`
	Diagnostic     *DiagnosticCommandJSON `json:"diagnostic,omitempty"`
	Shell          *ShellCommandJSON      `json:"shell,omitempty"`
}

// DiagnosticCommandJSON is the JSON representation of a DiagnosticCommand.
type DiagnosticCommandJSON struct {
	Check  string            `json:"check"`
	Params map[string]string `json:"params,omitempty"`
}

// ShellCommandJSON is the JSON representation of a ShellCommand.
type ShellCommandJSON struct {
	Binary string   `json:"binary"`
	Args   []string `json:"args"`
}

// CommandResponse is returned by GET /v1/commands/{id}.
type CommandResponse struct {
	ID              string     `json:"id"`
	HostID          string     `json:"host_id"`
	Status          string     `json:"status"`
	Tier            int        `json:"tier"`
	Reason          string     `json:"reason"`
	IssuedBy        string     `json:"issued_by"`
	IssuedAt        time.Time  `json:"issued_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	Stdout          *string    `json:"stdout,omitempty"`
	Stderr          *string    `json:"stderr,omitempty"`
	StdoutTruncated bool       `json:"stdout_truncated"`
	StderrTruncated bool       `json:"stderr_truncated"`
	DurationMs      *int64     `json:"duration_ms,omitempty"`
	Error           *string    `json:"error,omitempty"`
}

// diagnosticCheckMap maps check name strings to DiagnosticCheck enum values.
var diagnosticCheckMap = map[string]praetorv1.DiagnosticCheck{
	"DISK_USAGE":          praetorv1.DiagnosticCheck_DIAGNOSTIC_CHECK_DISK_USAGE,
	"TOP_PROCESSES":       praetorv1.DiagnosticCheck_DIAGNOSTIC_CHECK_TOP_PROCESSES,
	"RECENT_AUTH_EVENTS":  praetorv1.DiagnosticCheck_DIAGNOSTIC_CHECK_RECENT_AUTH_EVENTS,
	"JOURNALCTL_FOR_UNIT": praetorv1.DiagnosticCheck_DIAGNOSTIC_CHECK_JOURNALCTL_FOR_UNIT,
	"READ_CONFIG_FILE":    praetorv1.DiagnosticCheck_DIAGNOSTIC_CHECK_READ_CONFIG_FILE,
}

// toCommandResponse converts a store.CommandExecution to a CommandResponse.
func toCommandResponse(c *store.CommandExecution) CommandResponse {
	return CommandResponse{
		ID:              c.ID,
		HostID:          c.HostID,
		Status:          c.Status,
		Tier:            c.Tier,
		Reason:          c.Reason,
		IssuedBy:        c.IssuedBy,
		IssuedAt:        c.IssuedAt,
		CompletedAt:     c.CompletedAt,
		ExitCode:        c.ExitCode,
		Stdout:          c.Stdout,
		Stderr:          c.Stderr,
		StdoutTruncated: c.StdoutTruncated,
		StderrTruncated: c.StderrTruncated,
		DurationMs:      c.DurationMs,
		Error:           c.Error,
	}
}
