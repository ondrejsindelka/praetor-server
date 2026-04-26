// Package victoriametrics writes metric batches to VictoriaMetrics
// via the Prometheus text-format import endpoint.
package victoriametrics

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
)

// Writer forwards MetricBatch entries to VictoriaMetrics.
type Writer struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// New creates a new Writer. baseURL is the VictoriaMetrics base URL,
// e.g. "http://localhost:8428".
func New(baseURL string, logger *slog.Logger) *Writer {
	return &Writer{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
		logger:  logger,
	}
}

// Write converts a MetricBatch to Prometheus exposition format and POSTs to VM.
// hostID is added as an extra label to every metric.
func (w *Writer) Write(ctx context.Context, hostID string, batch *praetorv1.MetricBatch) error {
	if len(batch.GetMetrics()) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, m := range batch.GetMetrics() {
		// Build label set
		labels := make([]string, 0, len(m.GetLabels())+1)
		labels = append(labels, fmt.Sprintf(`host_id=%q`, hostID))
		for k, v := range m.GetLabels() {
			labels = append(labels, fmt.Sprintf(`%s=%q`, sanitizeLabelName(k), v))
		}
		labelStr := ""
		if len(labels) > 0 {
			labelStr = "{" + strings.Join(labels, ",") + "}"
		}

		// Timestamp in milliseconds
		var tsMs int64
		if ts := m.GetTimestamp(); ts != nil {
			tsMs = ts.AsTime().UnixMilli()
		} else {
			tsMs = time.Now().UnixMilli()
		}

		fmt.Fprintf(&buf, "%s%s %g %d\n",
			sanitizeMetricName(m.GetName()),
			labelStr,
			m.GetValue(),
			tsMs,
		)
	}

	url := w.baseURL + "/api/v1/import/prometheus"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("vm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("vm: POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vm: unexpected status %d", resp.StatusCode)
	}

	w.logger.Debug("metrics written to VM",
		"host_id", hostID,
		"count", len(batch.GetMetrics()),
	)
	return nil
}

// sanitizeMetricName replaces dots with underscores for valid Prometheus names.
func sanitizeMetricName(name string) string {
	return strings.ReplaceAll(name, ".", "_")
}

// sanitizeLabelName replaces characters not allowed in Prometheus label names.
func sanitizeLabelName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, ".", "_"), "-", "_")
}
