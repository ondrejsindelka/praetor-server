// Package llm provides LLM provider abstractions for the Watchdog subsystem.
package llm

import (
	"context"
	"encoding/json"
	"time"
)

// Provider sends completion requests to an LLM endpoint.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	HealthCheck(ctx context.Context) error
}

// CompletionRequest describes a chat completion request.
type CompletionRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature float32
	Timeout     time.Duration
}

// Message is a single chat message.
type Message struct {
	Role    string // "system" | "user" | "assistant"
	Content string
}

// CompletionResponse holds the result of a completion call.
type CompletionResponse struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
	FinishReason string
	LatencyMS    int64
	Provider     string
	RawResponse  json.RawMessage
}
