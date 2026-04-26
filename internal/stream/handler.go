package stream

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

const pingInterval = 30 * time.Second

// Handler implements the Connect RPC.
type Handler struct {
	praetorv1.UnimplementedAgentServiceServer
	registry *Registry
	hosts    *store.HostStore
	logger   *slog.Logger
}

// NewHandler creates a Connect stream handler.
func NewHandler(registry *Registry, hosts *store.HostStore, logger *slog.Logger) *Handler {
	return &Handler{
		registry: registry,
		hosts:    hosts,
		logger:   logger,
	}
}

// Connect handles the persistent bidirectional stream from an agent.
func (h *Handler) Connect(stream AgentStream) error {
	// Extract host_id from the mTLS client certificate CN.
	hostID, err := hostIDFromStream(stream)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "connect: %v", err)
	}

	h.registry.Add(hostID, stream)
	defer h.registry.Remove(hostID)

	h.logger.Info("agent connected", "host_id", hostID, "active_agents", h.registry.Count())

	defer func() {
		h.logger.Info("agent disconnected", "host_id", hostID)
	}()

	ctx := stream.Context()

	// Ping ticker.
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	// Channel to receive inbound messages.
	msgCh := make(chan *praetorv1.AgentMessage, 16)
	errCh := make(chan error, 1)

	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- msg
		}
	}()

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("agent stream context done", "host_id", hostID)
			return nil

		case err := <-errCh:
			if err == io.EOF {
				return nil
			}
			h.logger.Warn("agent stream recv error", "host_id", hostID, "err", err)
			return status.Errorf(codes.Internal, "recv: %v", err)

		case msg := <-msgCh:
			if err := h.handleMessage(stream, hostID, msg); err != nil {
				h.logger.Warn("handle message error", "host_id", hostID, "err", err)
			}

		case t := <-ticker.C:
			ping := &praetorv1.ServerMessage{
				Payload: &praetorv1.ServerMessage_Ping{
					Ping: &praetorv1.Ping{SentAt: timestamppb.New(t)},
				},
			}
			if err := stream.Send(ping); err != nil {
				h.logger.Warn("ping send failed", "host_id", hostID, "err", err)
				return status.Errorf(codes.Internal, "ping: %v", err)
			}
		}
	}
}

func (h *Handler) handleMessage(stream AgentStream, hostID string, msg *praetorv1.AgentMessage) error {
	switch p := msg.GetPayload().(type) {
	case *praetorv1.AgentMessage_Heartbeat:
		return h.handleHeartbeat(stream.Context(), hostID, p.Heartbeat)
	case *praetorv1.AgentMessage_MetricBatch:
		// M2: forward to VictoriaMetrics.
		h.logger.Debug("metric_batch received (M2 TODO)", "host_id", hostID, "count", len(p.MetricBatch.GetMetrics()))
		return nil
	default:
		h.logger.Warn("unknown agent message type", "host_id", hostID)
		return nil
	}
}

func (h *Handler) handleHeartbeat(ctx context.Context, hostID string, hb *praetorv1.Heartbeat) error {
	t := time.Now().UTC()
	if hb.GetTimestamp() != nil {
		t = hb.GetTimestamp().AsTime()
	}
	if err := h.hosts.UpdateHeartbeat(ctx, hostID, t, "online"); err != nil {
		h.logger.Warn("heartbeat update failed", "host_id", hostID, "err", err)
		return fmt.Errorf("heartbeat: %w", err)
	}
	h.logger.Debug("heartbeat", "host_id", hostID, "agent_version", hb.GetAgentVersion(), "uptime_s", hb.GetUptimeSeconds())
	return nil
}

// hostIDFromStream extracts the host_id from the mTLS peer certificate CN.
// host_id is always sourced from the cert — never from the AgentMessage payload.
func hostIDFromStream(stream AgentStream) (string, error) {
	p, ok := peer.FromContext(stream.Context())
	if !ok {
		return "", fmt.Errorf("no peer info in context")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", fmt.Errorf("no TLS info in peer auth")
	}
	state := tlsInfo.State
	if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return "", fmt.Errorf("no verified client certificate")
	}
	cn := state.VerifiedChains[0][0].Subject.CommonName
	if cn == "" {
		return "", fmt.Errorf("client certificate has empty CN")
	}
	return cn, nil
}
