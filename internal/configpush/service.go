// Package configpush manages pushing runtime config to connected agents.
package configpush

import (
	"context"
	"fmt"
	"log/slog"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"google.golang.org/grpc"
)

// AgentStream is the minimal interface needed to send a ServerMessage to an agent.
// Satisfied by stream.AgentStream (grpc.BidiStreamingServer).
type AgentStream interface {
	Send(*praetorv1.ServerMessage) error
}

// Registry is the minimal interface needed to look up a connected agent stream.
// Satisfied by *stream.Registry.
type Registry interface {
	Get(hostID string) (grpc.BidiStreamingServer[praetorv1.AgentMessage, praetorv1.ServerMessage], bool)
}

// Service pushes ConfigUpdate to connected agents.
type Service struct {
	configs  *store.ConfigStore
	registry Registry
	logger   *slog.Logger
}

func New(configs *store.ConfigStore, registry Registry, logger *slog.Logger) *Service {
	return &Service{configs: configs, registry: registry, logger: logger}
}

// Push sends the current config for hostID to the agent if connected.
// No-op (not an error) if agent is not currently connected.
func (s *Service) Push(ctx context.Context, hostID string) error {
	agentStream, ok := s.registry.Get(hostID)
	if !ok {
		s.logger.Info("config push: agent not connected, will sync on next connect",
			"host_id", hostID)
		return nil
	}
	cfg, err := s.configs.Get(ctx, hostID)
	if err != nil {
		return fmt.Errorf("configpush: get config: %w", err)
	}
	return s.send(agentStream, cfg)
}

// SyncOnConnect sends config to a newly connected agent if its version is stale.
func (s *Service) SyncOnConnect(ctx context.Context, hostID string, agentConfigVersion int64) error {
	cfg, err := s.configs.Get(ctx, hostID)
	if err != nil {
		return fmt.Errorf("configpush: sync on connect: %w", err)
	}
	if cfg.ConfigVersion <= agentConfigVersion {
		return nil // agent is up to date
	}
	agentStream, ok := s.registry.Get(hostID)
	if !ok {
		return nil
	}
	return s.send(agentStream, cfg)
}

func (s *Service) send(agentStream AgentStream, cfg *store.HostConfig) error {
	msg := &praetorv1.ServerMessage{
		Payload: &praetorv1.ServerMessage_ConfigUpdate{
			ConfigUpdate: &praetorv1.ConfigUpdate{
				ConfigVersion: cfg.ConfigVersion,
				Config: &praetorv1.AgentConfig{
					HeartbeatIntervalSeconds:        int32(cfg.HeartbeatIntervalSeconds),
					MetricCollectionIntervalSeconds: int32(cfg.MetricCollectionIntervalSeconds),
					LogSources:                      cfg.LogSources,
				},
			},
		},
	}
	if err := agentStream.Send(msg); err != nil {
		return fmt.Errorf("configpush: send: %w", err)
	}
	s.logger.Info("config pushed",
		"host_id", cfg.HostID,
		"config_version", cfg.ConfigVersion,
	)
	return nil
}

// HandleAck logs the result of a ConfigAck from an agent.
func (s *Service) HandleAck(hostID string, ack *praetorv1.ConfigAck) {
	if ack.GetApplied() {
		s.logger.Info("config ack: applied",
			"host_id", hostID,
			"config_version", ack.GetConfigVersion(),
		)
	} else {
		s.logger.Warn("config ack: rejected",
			"host_id", hostID,
			"config_version", ack.GetConfigVersion(),
			"error", ack.GetError(),
		)
	}
}
