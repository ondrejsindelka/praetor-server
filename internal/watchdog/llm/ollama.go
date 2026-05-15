package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OllamaProvider calls an Ollama server via its OpenAI-compatible endpoint.
type OllamaProvider struct {
	endpoint string
	client   *http.Client
}

// NewOllamaProvider creates an OllamaProvider. endpoint must be a valid URL.
// For non-private endpoints HTTPS is required.
func NewOllamaProvider(endpoint string, client *http.Client) (*OllamaProvider, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("ollama: endpoint is required")
	}
	if err := validateOllamaEndpoint(endpoint); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OllamaProvider{
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   client,
	}, nil
}

// validateOllamaEndpoint checks that non-private endpoints use HTTPS.
func validateOllamaEndpoint(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ollama: invalid endpoint URL: %w", err)
	}
	host := u.Hostname()
	if isPrivateHost(host) {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("ollama: non-private endpoint must use HTTPS (got %q)", u.Scheme)
	}
	return nil
}

// isPrivateHost returns true if host is a loopback or RFC-1918 / RFC-4193 address.
func isPrivateHost(host string) bool {
	// Loopback hostnames
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	// IPv4 private ranges: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}
	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// Name implements Provider.
func (p *OllamaProvider) Name() string { return "ollama" }

// HealthCheck implements Provider by calling the /api/tags endpoint.
func (p *OllamaProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("ollama: health check request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: health check: network error")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ollama: health check: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Complete implements Provider by calling the OpenAI-compatible /v1/chat/completions endpoint.
// No Authorization header is sent.
func (p *OllamaProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
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
		Stream      bool            `json:"stream"`
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
		Stream:      false,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Intentionally no Authorization header for Ollama.

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: network error")
	}
	defer httpResp.Body.Close()

	rawBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, &HTTPError{StatusCode: httpResp.StatusCode, Provider: "ollama"}
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
		return nil, fmt.Errorf("ollama: parse response: %w", err)
	}
	if len(respBody.Choices) == 0 {
		return nil, fmt.Errorf("ollama: no choices in response")
	}

	return &CompletionResponse{
		Content:      respBody.Choices[0].Message.Content,
		Model:        respBody.Model,
		InputTokens:  respBody.Usage.PromptTokens,
		OutputTokens: respBody.Usage.CompletionTokens,
		FinishReason: respBody.Choices[0].FinishReason,
		LatencyMS:    time.Since(start).Milliseconds(),
		Provider:     "ollama",
		RawResponse:  json.RawMessage(rawBytes),
	}, nil
}
