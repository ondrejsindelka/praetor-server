package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultOpenRouterEndpoint = "https://openrouter.ai/api/v1"

// OpenRouterProvider calls the OpenRouter chat completions API (OpenAI-compatible).
type OpenRouterProvider struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

// NewOpenRouterProvider creates an OpenRouterProvider. endpoint may be empty to use the default.
func NewOpenRouterProvider(apiKey, endpoint string, client *http.Client) (*OpenRouterProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter: api key is required")
	}
	ep := endpoint
	if ep == "" {
		ep = defaultOpenRouterEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OpenRouterProvider{
		apiKey:   apiKey,
		endpoint: ep,
		client:   client,
	}, nil
}

// Name implements Provider.
func (p *OpenRouterProvider) Name() string { return "openrouter" }

// HealthCheck implements Provider by listing models.
func (p *OpenRouterProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+"/models", nil)
	if err != nil {
		return fmt.Errorf("openrouter: health check request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("openrouter: health check: network error")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("openrouter: health check: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Complete implements Provider by calling POST /chat/completions.
func (p *OpenRouterProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	start := time.Now()

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	type openAIMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type requestBody struct {
		Model       string          `json:"model"`
		Messages    []openAIMessage `json:"messages"`
		MaxTokens   int             `json:"max_tokens,omitempty"`
		Temperature float32         `json:"temperature,omitempty"`
	}

	msgs := make([]openAIMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = openAIMessage{Role: m.Role, Content: m.Content}
	}
	body := requestBody{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openrouter: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("openrouter: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://praetor.dev")
	httpReq.Header.Set("X-Title", "Praetor Watchdog")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openrouter: network error")
	}
	defer httpResp.Body.Close()

	rawBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("openrouter: read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, &HTTPError{StatusCode: httpResp.StatusCode, Provider: "openrouter"}
	}

	type choice struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	}
	type usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}
	type responseBody struct {
		Model   string   `json:"model"`
		Choices []choice `json:"choices"`
		Usage   usage    `json:"usage"`
	}

	var respBody responseBody
	if err := json.Unmarshal(rawBytes, &respBody); err != nil {
		return nil, fmt.Errorf("openrouter: parse response: %w", err)
	}
	if len(respBody.Choices) == 0 {
		return nil, fmt.Errorf("openrouter: no choices in response")
	}

	return &CompletionResponse{
		Content:      respBody.Choices[0].Message.Content,
		Model:        respBody.Model,
		InputTokens:  respBody.Usage.PromptTokens,
		OutputTokens: respBody.Usage.CompletionTokens,
		FinishReason: respBody.Choices[0].FinishReason,
		LatencyMS:    time.Since(start).Milliseconds(),
		Provider:     "openrouter",
		RawResponse:  json.RawMessage(rawBytes),
	}, nil
}
