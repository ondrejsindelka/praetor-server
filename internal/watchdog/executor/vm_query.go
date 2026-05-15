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

// VMQueryClient queries a VictoriaMetrics (or Prometheus-compatible) metrics backend.
type VMQueryClient interface {
	QueryRange(ctx context.Context, query string, start, end time.Time) ([]MetricSeries, error)
}

// MetricSeries is one labelled time series returned by VictoriaMetrics.
type MetricSeries struct {
	Labels map[string]string
	Values []ValuePoint
}

// ValuePoint is a single sample in a MetricSeries.
type ValuePoint struct {
	Timestamp time.Time
	Value     float64
}

// HTTPVMQueryClient queries VictoriaMetrics for range data.
type HTTPVMQueryClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPVMQueryClient creates a VictoriaMetrics HTTP client.
func NewHTTPVMQueryClient(baseURL string, timeout time.Duration) *HTTPVMQueryClient {
	return &HTTPVMQueryClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

// vmQueryRangeResponse is the wire format returned by /api/v1/query_range.
type vmQueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"` // [[unixSec, "value"], ...]
		} `json:"result"`
	} `json:"data"`
}

// QueryRange calls GET /api/v1/query_range on VictoriaMetrics.
func (c *HTTPVMQueryClient) QueryRange(ctx context.Context, query string, start, end time.Time) ([]MetricSeries, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))

	reqURL := c.baseURL + "/api/v1/query_range?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("vmquery: build request: %w", err)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vmquery: query_range: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("vmquery: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vmquery: query_range status %d: %s", resp.StatusCode, body)
	}

	var result vmQueryRangeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("vmquery: decode response: %w", err)
	}

	series := make([]MetricSeries, 0, len(result.Data.Result))
	for _, r := range result.Data.Result {
		points := make([]ValuePoint, 0, len(r.Values))
		for _, v := range r.Values {
			if len(v) < 2 {
				continue
			}
			ts, ok := toFloat64(v[0])
			if !ok {
				continue
			}
			valStr, ok := v[1].(string)
			if !ok {
				continue
			}
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				continue
			}
			points = append(points, ValuePoint{
				Timestamp: time.Unix(int64(ts), 0),
				Value:     val,
			})
		}
		series = append(series, MetricSeries{
			Labels: r.Metric,
			Values: points,
		})
	}
	return series, nil
}

// toFloat64 coerces a JSON number (float64) or string to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
