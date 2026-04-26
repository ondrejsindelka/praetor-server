package command

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"
	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"

	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

// AgentSender is the minimal interface the Broker needs to push a ServerMessage to an agent.
// Satisfied by grpc.BidiStreamingServer[AgentMessage, ServerMessage].
type AgentSender interface {
	Send(*praetorv1.ServerMessage) error
}

// StreamRegistry is the subset of the stream registry that the Broker needs.
// Decoupled from the stream package to avoid an import cycle.
type StreamRegistry interface {
	Get(hostID string) (AgentSender, bool)
}

// Broker issues commands to agents and records results.
type Broker struct {
	commands *store.CommandStore
	registry StreamRegistry
	limiter  *RateLimiter
	logger   *slog.Logger
}

func NewBroker(commands *store.CommandStore, registry StreamRegistry, logger *slog.Logger) *Broker {
	return &Broker{
		commands: commands,
		registry: registry,
		limiter:  NewRateLimiter(),
		logger:   logger,
	}
}

// IssueRequest is the input for issuing a command.
type IssueRequest struct {
	HostID   string
	Tier     praetorv1.CommandTier
	Reason   string
	IssuedBy string
	Timeout  int32
	Command  interface{} // *praetorv1.DiagnosticCommand or *praetorv1.ShellCommand
}

// Issue validates, stores, and pushes a CommandRequest to the target agent.
// Returns the command ID. Returns error if agent is not connected, rate limited, or DB fails.
func (b *Broker) Issue(ctx context.Context, req IssueRequest) (string, error) {
	// Rate limit
	if err := b.limiter.Allow(req.HostID); err != nil {
		return "", fmt.Errorf("command broker: %w", err)
	}

	// Build CommandRequest
	id := ulid.MustNew(ulid.Timestamp(time.Now()), ulid.DefaultEntropy()).String()
	cr := &praetorv1.CommandRequest{
		Id:             id,
		Tier:           req.Tier,
		Reason:         req.Reason,
		IssuedBy:       req.IssuedBy,
		TimeoutSeconds: req.Timeout,
	}
	switch c := req.Command.(type) {
	case *praetorv1.DiagnosticCommand:
		cr.Command = &praetorv1.CommandRequest_Diagnostic{Diagnostic: c}
	case *praetorv1.ShellCommand:
		cr.Command = &praetorv1.CommandRequest_Shell{Shell: c}
	default:
		return "", fmt.Errorf("command broker: unknown command type %T", req.Command)
	}

	// Serialize for audit log
	cmdJSON, err := json.Marshal(cr)
	if err != nil {
		return "", fmt.Errorf("command broker: marshal: %w", err)
	}

	// Store pending record
	if err := b.commands.Insert(ctx, &store.CommandExecution{
		ID:          id,
		HostID:      req.HostID,
		Tier:        int(req.Tier),
		CommandJSON: cmdJSON,
		Reason:      req.Reason,
		IssuedBy:    req.IssuedBy,
	}); err != nil {
		return "", fmt.Errorf("command broker: store: %w", err)
	}

	// Push to connected agent
	agentStream, ok := b.registry.Get(req.HostID)
	if !ok {
		return id, fmt.Errorf("command broker: host %s not connected (command queued with id %s)", req.HostID, id)
	}

	if err := agentStream.Send(&praetorv1.ServerMessage{
		Payload: &praetorv1.ServerMessage_CommandRequest{CommandRequest: cr},
	}); err != nil {
		return id, fmt.Errorf("command broker: send to %s: %w", req.HostID, err)
	}

	b.logger.Info("command issued",
		"id", id, "host_id", req.HostID,
		"tier", req.Tier, "reason", req.Reason,
	)
	return id, nil
}

// HandleResult processes a CommandResult received from an agent.
func (b *Broker) HandleResult(ctx context.Context, result *praetorv1.CommandResult) {
	err := b.commands.Complete(ctx,
		result.GetCommandId(),
		int(result.GetExitCode()),
		result.GetStdout(),
		result.GetStderr(),
		result.GetStdoutTruncated(),
		result.GetStderrTruncated(),
		result.GetDurationMs(),
		result.GetError(),
	)
	if err != nil {
		b.logger.Warn("command broker: failed to record result",
			"command_id", result.GetCommandId(), "err", err)
		return
	}
	b.logger.Info("command result received",
		"command_id", result.GetCommandId(),
		"exit_code", result.GetExitCode(),
		"duration_ms", result.GetDurationMs(),
		"error", result.GetError(),
	)
}
