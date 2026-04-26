// Package stream manages active agent connections and the Connect RPC handler.
package stream

import (
	"sync"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"google.golang.org/grpc"
)

// AgentStream is the server-side handle to a connected agent's bidi stream.
type AgentStream = grpc.BidiStreamingServer[praetorv1.AgentMessage, praetorv1.ServerMessage]

// Registry tracks active agent streams, keyed by host_id.
// Used by the command broker (M1.4) to push ServerMessages to specific agents.
type Registry struct {
	mu      sync.RWMutex
	streams map[string]AgentStream
}

// NewRegistry creates a new Registry.
func NewRegistry() *Registry {
	return &Registry{streams: make(map[string]AgentStream)}
}

// Add registers an active stream for hostID.
func (r *Registry) Add(hostID string, s AgentStream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streams[hostID] = s
}

// Remove deregisters the stream for hostID.
func (r *Registry) Remove(hostID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.streams, hostID)
}

// Get returns the stream for hostID, or (nil, false) if not connected.
func (r *Registry) Get(hostID string) (AgentStream, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.streams[hostID]
	return s, ok
}

// Count returns the number of currently connected agents.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.streams)
}
