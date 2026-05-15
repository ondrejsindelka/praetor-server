// Package trigger implements the watchdog TriggerRule engine.
package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MetricSample holds a single metric's label set and time-series values.
type MetricSample struct {
	Labels map[string]string
	Values []ValuePoint
}

// ValuePoint is a (timestamp, value) pair from a metric query result.
type ValuePoint struct {
	Timestamp time.Time
	Value     float64
}

// VMClient abstracts VictoriaMetrics query calls.
type VMClient interface {
	// QueryRange executes a range query and returns the resulting samples.
	QueryRange(ctx context.Context, query string, start, end time.Time) ([]MetricSample, error)
	// QueryInstant executes an instant query.
	// Returns (value, found, error). For vector results, returns the first sample's latest value.
	QueryInstant(ctx context.Context, query string) (float64, bool, error)
}

// HTTPVMClient implements VMClient against VictoriaMetrics HTTP API.
type HTTPVMClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPVMClient creates an HTTPVMClient for the given base URL.
func NewHTTPVMClient(baseURL string) *HTTPVMClient {
	return &HTTPVMClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// vmInstantResponse is the JSON envelope returned by /api/v1/query.
type vmInstantResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"` // [unixTs, "value"]
		} `json:"result"`
	} `json:"data"`
}

// vmRangeResponse is the JSON envelope returned by /api/v1/query_range.
type vmRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"` // [[unixTs, "value"], ...]
		} `json:"result"`
	} `json:"data"`
}

// QueryInstant calls /api/v1/query and returns the scalar or first vector value.
func (c *HTTPVMClient) QueryInstant(ctx context.Context, query string) (float64, bool, error) {
	reqURL := c.baseURL + "/api/v1/query?" + url.Values{
		"query": {query},
		"time":  {fmt.Sprintf("%d", time.Now().Unix())},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, false, fmt.Errorf("vm: build request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("vm: GET %s: %w", reqURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, false, fmt.Errorf("vm: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("vm: unexpected status %d: %s", resp.StatusCode, body)
	}

	var parsed vmInstantResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, false, fmt.Errorf("vm: parse response: %w", err)
	}
	if parsed.Status != "success" {
		return 0, false, fmt.Errorf("vm: query status %q", parsed.Status)
	}

	if len(parsed.Data.Result) == 0 {
		return 0, false, nil
	}

	// scalar or vector: take first result's value
	rawVal, ok := parsed.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, false, fmt.Errorf("vm: unexpected value type %T", parsed.Data.Result[0].Value[1])
	}
	var f float64
	if _, err := fmt.Sscanf(rawVal, "%g", &f); err != nil {
		return 0, false, fmt.Errorf("vm: parse float %q: %w", rawVal, err)
	}
	return f, true, nil
}

// QueryRange calls /api/v1/query_range and returns all metric samples.
func (c *HTTPVMClient) QueryRange(ctx context.Context, query string, start, end time.Time) ([]MetricSample, error) {
	reqURL := c.baseURL + "/api/v1/query_range?" + url.Values{
		"query": {query},
		"start": {fmt.Sprintf("%d", start.Unix())},
		"end":   {fmt.Sprintf("%d", end.Unix())},
		"step":  {"15s"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("vm: build request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vm: GET %s: %w", reqURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("vm: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vm: unexpected status %d: %s", resp.StatusCode, body)
	}

	var parsed vmRangeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("vm: parse response: %w", err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("vm: query status %q", parsed.Status)
	}

	samples := make([]MetricSample, 0, len(parsed.Data.Result))
	for _, r := range parsed.Data.Result {
		ms := MetricSample{
			Labels: r.Metric,
			Values: make([]ValuePoint, 0, len(r.Values)),
		}
		for _, vp := range r.Values {
			tsFloat, ok := vp[0].(float64)
			if !ok {
				continue
			}
			rawVal, ok := vp[1].(string)
			if !ok {
				continue
			}
			var f float64
			if _, err := fmt.Sscanf(rawVal, "%g", &f); err != nil {
				continue
			}
			ms.Values = append(ms.Values, ValuePoint{
				Timestamp: time.Unix(int64(tsFloat), 0),
				Value:     f,
			})
		}
		samples = append(samples, ms)
	}
	return samples, nil
}
