// Package agent composes the full AgentServiceServer implementation.
package agent

import (
	"context"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/enrollment"
	"github.com/ondrejsindelka/praetor-server/internal/stream"
)

// Service implements the full praetorv1.AgentServiceServer by composing
// the enrollment and stream handlers.
type Service struct {
	praetorv1.UnimplementedAgentServiceServer
	enroll  *enrollment.Service
	connect *stream.Handler
}

// New creates a composed AgentService.
func New(enroll *enrollment.Service, connect *stream.Handler) *Service {
	return &Service{enroll: enroll, connect: connect}
}

// Enroll delegates to the enrollment service.
func (s *Service) Enroll(ctx context.Context, req *praetorv1.EnrollRequest) (*praetorv1.EnrollResponse, error) {
	return s.enroll.Enroll(ctx, req)
}

// Connect delegates to the stream handler.
// AgentService_ConnectServer is an alias for grpc.BidiStreamingServer[AgentMessage, ServerMessage].
func (s *Service) Connect(st praetorv1.AgentService_ConnectServer) error {
	return s.connect.Connect(st)
}
