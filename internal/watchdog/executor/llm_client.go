package executor

import (
	"context"
	"time"
)

// LLMClient is a minimal interface for sending completion requests.
// Satisfied by watchdog/llm.Registry via an adapter — no import needed (duck typing).
type LLMClient interface {
	Complete(ctx context.Context, req LLMRequest) (*LLMResponse, error)
}

// LLMRequest carries the parameters for a single completion call.
type LLMRequest struct {
	Provider    string
	Model       string
	Messages    []LLMMessage
	MaxTokens   int
	Temperature float32
	Timeout     time.Duration
}

// LLMMessage is one turn in a conversation.
type LLMMessage struct {
	Role    string
	Content string
}

// LLMResponse is the result of a completion call.
type LLMResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
	FinishReason string
	LatencyMS    int64
	Provider     string
}
