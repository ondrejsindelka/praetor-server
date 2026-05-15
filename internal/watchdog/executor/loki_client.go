package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// LokiClient queries a Loki-compatible log aggregation system.
type LokiClient interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]LogEntry, error)
}

// LogEntry is one log line returned by Loki.
type LogEntry struct {
	Timestamp time.Time
	Labels    map[string]string
	Line      string
}

// HTTPLokiClient queries Loki via HTTP API.
type HTTPLokiClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPLokiClient creates a Loki HTTP client.
func NewHTTPLokiClient(baseURL string, timeout time.Duration) *HTTPLokiClient {
	return &HTTPLokiClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

// lokiQueryRangeResponse is the wire format for Loki query_range responses.
type lokiQueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [[unixNanoString, line], ...]
		} `json:"result"`
	} `json:"data"`
}

// QueryRange calls GET /loki/api/v1/query_range.
// Returns entries sorted by time ascending.
func (c *HTTPLokiClient) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]LogEntry, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("direction", "forward")
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	reqURL := c.baseURL + "/loki/api/v1/query_range?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("loki: build request: %w", err)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("loki: query_range: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("loki: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki: query_range status %d: %s", resp.StatusCode, body)
	}

	var result lokiQueryRangeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("loki: decode response: %w", err)
	}

	var entries []LogEntry
	for _, stream := range result.Data.Result {
		labels := stream.Stream
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			nanos, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			entries = append(entries, LogEntry{
				Timestamp: time.Unix(0, nanos),
				Labels:    labels,
				Line:      v[1],
			})
		}
	}
	return entries, nil
}
