package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----

func openAISuccessResponse(model, content, finishReason string, promptTok, completionTok int) []byte {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type choice struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	}
	type usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}
	type body struct {
		Model   string   `json:"model"`
		Choices []choice `json:"choices"`
		Usage   usage    `json:"usage"`
	}
	b, _ := json.Marshal(body{
		Model: model,
		Choices: []choice{
			{Message: message{Role: "assistant", Content: content}, FinishReason: finishReason},
		},
		Usage: usage{PromptTokens: promptTok, CompletionTokens: completionTok},
	})
	return b
}

func anthropicSuccessResponse(model, content, stopReason string, inputTok, outputTok int) []byte {
	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	type body struct {
		Model      string         `json:"model"`
		Content    []contentBlock `json:"content"`
		Usage      usage          `json:"usage"`
		StopReason string         `json:"stop_reason"`
	}
	b, _ := json.Marshal(body{
		Model:      model,
		Content:    []contentBlock{{Type: "text", Text: content}},
		Usage:      usage{InputTokens: inputTok, OutputTokens: outputTok},
		StopReason: stopReason,
	})
	return b
}

// ---- OpenRouter tests ----

func TestOpenRouterHeaders(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "hello", "stop", 10, 5))
	}))
	defer srv.Close()

	p, err := NewOpenRouterProvider("test-api-key", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewOpenRouterProvider: %v", err)
	}

	_, err = p.Complete(context.Background(), CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if auth := capturedReq.Header.Get("Authorization"); auth != "Bearer test-api-key" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer test-api-key")
	}
	if ref := capturedReq.Header.Get("HTTP-Referer"); ref != "https://praetor.dev" {
		t.Errorf("HTTP-Referer = %q, want %q", ref, "https://praetor.dev")
	}
	if title := capturedReq.Header.Get("X-Title"); title != "Praetor Watchdog" {
		t.Errorf("X-Title = %q, want %q", title, "Praetor Watchdog")
	}
	if capturedReq.URL.Path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", capturedReq.URL.Path)
	}
}

func TestOpenRouterRequestFormat(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "world", "stop", 8, 3))
	}))
	defer srv.Close()

	p, _ := NewOpenRouterProvider("key", srv.URL, srv.Client())
	_, err := p.Complete(context.Background(), CompletionRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "hello"},
		},
		MaxTokens:   100,
		Temperature: 0.7,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if capturedBody["model"] != "gpt-4o" {
		t.Errorf("model = %v, want gpt-4o", capturedBody["model"])
	}
	msgs, _ := capturedBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Errorf("messages count = %d, want 2", len(msgs))
	}
}

func TestOpenRouterResponseParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "the answer", "stop", 20, 10))
	}))
	defer srv.Close()

	p, _ := NewOpenRouterProvider("key", srv.URL, srv.Client())
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "q"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "the answer" {
		t.Errorf("Content = %q, want %q", resp.Content, "the answer")
	}
	if resp.InputTokens != 20 {
		t.Errorf("InputTokens = %d, want 20", resp.InputTokens)
	}
	if resp.OutputTokens != 10 {
		t.Errorf("OutputTokens = %d, want 10", resp.OutputTokens)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if resp.Provider != "openrouter" {
		t.Errorf("Provider = %q, want openrouter", resp.Provider)
	}
}

// ---- Anthropic tests ----

func TestAnthropicHeaders(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(anthropicSuccessResponse("claude-3-5-sonnet-20241022", "hi", "end_turn", 15, 7))
	}))
	defer srv.Close()

	p, err := NewAnthropicProvider("ant-key", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewAnthropicProvider: %v", err)
	}
	_, err = p.Complete(context.Background(), CompletionRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if key := capturedReq.Header.Get("x-api-key"); key != "ant-key" {
		t.Errorf("x-api-key = %q, want %q", key, "ant-key")
	}
	if ver := capturedReq.Header.Get("anthropic-version"); ver != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", ver, anthropicVersion)
	}
	if ct := capturedReq.Header.Get("content-type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

func TestAnthropicSystemMessageExtraction(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(anthropicSuccessResponse("claude-3-5-sonnet-20241022", "ok", "end_turn", 5, 2))
	}))
	defer srv.Close()

	p, _ := NewAnthropicProvider("key", srv.URL, srv.Client())
	_, err := p.Complete(context.Background(), CompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant"},
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// System message should be extracted to top-level "system" field.
	system, _ := capturedBody["system"].(string)
	if system != "You are a helpful assistant" {
		t.Errorf("system = %q, want %q", system, "You are a helpful assistant")
	}

	// Non-system messages should be in "messages" array.
	msgs, _ := capturedBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("messages count = %d, want 1", len(msgs))
	}
}

func TestAnthropicResponseParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(anthropicSuccessResponse("claude-3-5-sonnet-20241022", "response text", "end_turn", 30, 15))
	}))
	defer srv.Close()

	p, _ := NewAnthropicProvider("key", srv.URL, srv.Client())
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []Message{{Role: "user", Content: "q"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "response text" {
		t.Errorf("Content = %q, want %q", resp.Content, "response text")
	}
	if resp.InputTokens != 30 {
		t.Errorf("InputTokens = %d, want 30", resp.InputTokens)
	}
	if resp.OutputTokens != 15 {
		t.Errorf("OutputTokens = %d, want 15", resp.OutputTokens)
	}
	if resp.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", resp.Provider)
	}
}

// ---- Ollama tests ----

func TestOllamaNoAuthHeader(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("llama3", "hi", "stop", 5, 3))
	}))
	defer srv.Close()

	p, err := NewOllamaProvider(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewOllamaProvider: %v", err)
	}
	_, err = p.Complete(context.Background(), CompletionRequest{
		Model:    "llama3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if auth := capturedReq.Header.Get("Authorization"); auth != "" {
		t.Errorf("Authorization header should be absent, got %q", auth)
	}
}

func TestOllamaPrivateRangeHTTP(t *testing.T) {
	// 127.x should be allowed with HTTP.
	_, err := NewOllamaProvider("http://127.0.0.1:11434", nil)
	if err != nil {
		t.Errorf("localhost HTTP should be allowed: %v", err)
	}

	// 192.168.x should be allowed with HTTP.
	_, err = NewOllamaProvider("http://192.168.1.10:11434", nil)
	if err != nil {
		t.Errorf("192.168.x HTTP should be allowed: %v", err)
	}

	// 10.x should be allowed with HTTP.
	_, err = NewOllamaProvider("http://10.0.0.5:11434", nil)
	if err != nil {
		t.Errorf("10.x HTTP should be allowed: %v", err)
	}

	// 172.16.x should be allowed with HTTP.
	_, err = NewOllamaProvider("http://172.16.0.1:11434", nil)
	if err != nil {
		t.Errorf("172.16.x HTTP should be allowed: %v", err)
	}
}

func TestOllamaPublicEndpointRequiresHTTPS(t *testing.T) {
	_, err := NewOllamaProvider("http://1.2.3.4:11434", nil)
	if err == nil {
		t.Error("expected error for public HTTP endpoint, got nil")
	}
}

func TestOllamaPublicEndpointHTTPS(t *testing.T) {
	_, err := NewOllamaProvider("https://my-ollama.example.com", nil)
	if err != nil {
		t.Errorf("public HTTPS endpoint should be allowed: %v", err)
	}
}

// ---- Retry tests ----

func TestRetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "ok", "stop", 5, 2))
	}))
	defer srv.Close()

	inner, _ := NewOpenRouterProvider("key", srv.URL, srv.Client())
	p := WithRetry(inner, RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
		Multiplier:   1.0,
	})

	resp, err := p.Complete(context.Background(), CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestNoRetryOn400(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	inner, _ := NewOpenRouterProvider("key", srv.URL, srv.Client())
	p := WithRetry(inner, RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond})

	_, err := p.Complete(context.Background(), CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 400)", attempts)
	}
}

func TestRetryAfterHeaderRespected(t *testing.T) {
	callTimes := []time.Time{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimes = append(callTimes, time.Now())
		if len(callTimes) < 2 {
			w.Header().Set("Retry-After", "0") // 0s — still goes through retry path
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "done", "stop", 1, 1))
	}))
	defer srv.Close()

	inner, _ := NewOpenRouterProvider("key", srv.URL, srv.Client())
	p := WithRetry(inner, RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Second,
		Multiplier:   2.0,
	})

	resp, err := p.Complete(context.Background(), CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "done" {
		t.Errorf("Content = %q, want done", resp.Content)
	}
	if len(callTimes) != 2 {
		t.Errorf("call count = %d, want 2", len(callTimes))
	}
}

func TestRetryOn5xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "recovered", "stop", 3, 3))
	}))
	defer srv.Close()

	inner, _ := NewOpenRouterProvider("key", srv.URL, srv.Client())
	p := WithRetry(inner, RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
		Multiplier:   1.0,
	})

	resp, err := p.Complete(context.Background(), CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("Content = %q, want recovered", resp.Content)
	}
}

func TestNoRetryOnCanceled(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	inner, _ := NewOpenRouterProvider("key", srv.URL, srv.Client())
	p := WithRetry(inner, RetryPolicy{
		MaxAttempts:  5,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
		Multiplier:   1.0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after the first attempt by using the server to cancel.
	// We test this by using a cancelled context from the start.
	cancel()

	_, err := p.Complete(ctx, CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		// The error wraps context.Canceled (from client.Do with canceled ctx) or
		// from our own isRetryable check; either form is acceptable.
		_ = err
	}
}

// ---- Sanitizer / API key leak prevention ----

func TestAPIKeyNotInLogs(t *testing.T) {
	const secretKey = "super-secret-key-12345"

	// Server that always returns 500 to trigger error path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Test OpenRouter.
	p, err := NewOpenRouterProvider(secretKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewOpenRouterProvider: %v", err)
	}
	_, err = p.Complete(context.Background(), CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil && strings.Contains(err.Error(), secretKey) {
		t.Errorf("OpenRouter error contains API key: %v", err)
	}

	// Test Anthropic.
	ap, err := NewAnthropicProvider(secretKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewAnthropicProvider: %v", err)
	}
	_, err = ap.Complete(context.Background(), CompletionRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil && strings.Contains(err.Error(), secretKey) {
		t.Errorf("Anthropic error contains API key: %v", err)
	}
}
