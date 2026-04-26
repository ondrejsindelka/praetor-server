// Package loki writes log batches to Grafana Loki via the push API.
package loki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
)

// Writer forwards LogBatch entries to Loki.
type Writer struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// New creates a new Loki Writer.
func New(baseURL string, logger *slog.Logger) *Writer {
	return &Writer{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
		logger:  logger,
	}
}

// lokiStream is the JSON representation of a Loki stream.
type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // [timestamp_ns, line]
}

type lokiPushRequest struct {
	Streams []lokiStream `json:"streams"`
}

// Write converts a LogBatch to Loki push API format and POSTs it.
// hostID is added as a label to every stream.
func (w *Writer) Write(ctx context.Context, hostID string, batch *praetorv1.LogBatch) error {
	if len(batch.GetEntries()) == 0 {
		return nil
	}

	// Group entries by source for stream labels.
	bySource := make(map[string][]*praetorv1.LogEntry)
	for _, e := range batch.GetEntries() {
		src := e.GetSource()
		if src == "" {
			src = "unknown"
		}
		bySource[src] = append(bySource[src], e)
	}

	streams := make([]lokiStream, 0, len(bySource))
	for source, entries := range bySource {
		values := make([][2]string, 0, len(entries))
		for _, e := range entries {
			var tsNs int64
			if ts := e.GetTimestamp(); ts != nil {
				tsNs = ts.AsTime().UnixNano()
			} else {
				tsNs = time.Now().UnixNano()
			}
			values = append(values, [2]string{
				strconv.FormatInt(tsNs, 10),
				e.GetMessage(),
			})
		}
		streams = append(streams, lokiStream{
			Stream: map[string]string{
				"host_id":  hostID,
				"source":   source,
				"severity": severityLabel(entries[0].GetSeverity()),
			},
			Values: values,
		})
	}

	body, err := json.Marshal(lokiPushRequest{Streams: streams})
	if err != nil {
		return fmt.Errorf("loki: marshal: %w", err)
	}

	url := w.baseURL + "/loki/api/v1/push"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("loki: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("loki: POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Read body once for error reporting, then discard remainder.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		// 400 from Loki typically means entries out of order or too old — read the body
		// for diagnostic context but don't block the caller on it.
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("loki: unexpected status %d: %s", resp.StatusCode, respBody)
	}

	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	w.logger.Debug("logs written to Loki",
		"host_id", hostID,
		"count", len(batch.GetEntries()),
	)
	return nil
}

func severityLabel(s praetorv1.LogSeverity) string {
	switch s {
	case praetorv1.LogSeverity_LOG_SEVERITY_CRITICAL:
		return "critical"
	case praetorv1.LogSeverity_LOG_SEVERITY_ERROR:
		return "error"
	case praetorv1.LogSeverity_LOG_SEVERITY_WARNING:
		return "warning"
	case praetorv1.LogSeverity_LOG_SEVERITY_INFO:
		return "info"
	case praetorv1.LogSeverity_LOG_SEVERITY_DEBUG:
		return "debug"
	case praetorv1.LogSeverity_LOG_SEVERITY_TRACE:
		return "trace"
	default:
		return "unknown"
	}
}
