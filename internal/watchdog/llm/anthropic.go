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

const (
	defaultAnthropicEndpoint = "https://api.anthropic.com"
	anthropicVersion         = "2023-06-01"
)

// AnthropicProvider calls the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

// NewAnthropicProvider creates an AnthropicProvider. endpoint may be empty to use the default.
func NewAnthropicProvider(apiKey, endpoint string, client *http.Client) (*AnthropicProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: api key is required")
	}
	ep := endpoint
	if ep == "" {
		ep = defaultAnthropicEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &AnthropicProvider{
		apiKey:   apiKey,
		endpoint: ep,
		client:   client,
	}, nil
}

// Name implements Provider.
func (p *AnthropicProvider) Name() string { return "anthropic" }

// HealthCheck implements Provider by sending a minimal messages request.
func (p *AnthropicProvider) HealthCheck(ctx context.Context) error {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type body struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []msg  `json:"messages"`
	}
	b := body{
		Model:     "claude-3-haiku-20240307",
		MaxTokens: 1,
		Messages:  []msg{{Role: "user", Content: "ping"}},
	}
	bodyBytes, _ := json.Marshal(b)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("anthropic: health check request: %w", err)
	}
	p.setHeaders(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic: health check: network error")
	}
	defer resp.Body.Close()
	// 400 means the API is reachable (e.g. invalid model is ok for ping)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("anthropic: health check: server error %d", resp.StatusCode)
	}
	return nil
}

// Complete implements Provider by calling POST /v1/messages.
func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	start := time.Now()

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	// Extract system message(s) and non-system messages.
	var systemContent string
	type anthropicMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []anthropicMsg
	for _, m := range req.Messages {
		if m.Role == "system" {
			if systemContent != "" {
				systemContent += "\n"
			}
			systemContent += m.Content
		} else {
			msgs = append(msgs, anthropicMsg{Role: m.Role, Content: m.Content})
		}
	}

	type requestBody struct {
		Model       string         `json:"model"`
		System      string         `json:"system,omitempty"`
		Messages    []anthropicMsg `json:"messages"`
		MaxTokens   int            `json:"max_tokens"`
		Temperature float32        `json:"temperature,omitempty"`
	}

	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 1024 // Anthropic requires max_tokens
	}

	body := requestBody{
		Model:       req.Model,
		System:      systemContent,
		Messages:    msgs,
		MaxTokens:   maxTok,
		Temperature: req.Temperature,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: network error")
	}
	defer httpResp.Body.Close()

	rawBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, &HTTPError{StatusCode: httpResp.StatusCode, Provider: "anthropic"}
	}

	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	type responseBody struct {
		Model      string         `json:"model"`
		Content    []contentBlock `json:"content"`
		Usage      usage          `json:"usage"`
		StopReason string         `json:"stop_reason"`
	}

	var respBody responseBody
	if err := json.Unmarshal(rawBytes, &respBody); err != nil {
		return nil, fmt.Errorf("anthropic: parse response: %w", err)
	}

	var content string
	for _, block := range respBody.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return &CompletionResponse{
		Content:      content,
		Model:        respBody.Model,
		InputTokens:  respBody.Usage.InputTokens,
		OutputTokens: respBody.Usage.OutputTokens,
		FinishReason: respBody.StopReason,
		LatencyMS:    time.Since(start).Milliseconds(),
		Provider:     "anthropic",
		RawResponse:  json.RawMessage(rawBytes),
	}, nil
}

func (p *AnthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")
}
