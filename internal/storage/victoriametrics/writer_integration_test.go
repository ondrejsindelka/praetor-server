//go:build integration

package victoriametrics_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/storage/victoriametrics"
)

func vmURL(t *testing.T) string {
	t.Helper()
	if u := os.Getenv("TEST_VM_URL"); u != "" {
		return u
	}
	return "http://localhost:8428"
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestWriteMetricBatch(t *testing.T) {
	w := victoriametrics.New(vmURL(t), testLogger())

	batch := &praetorv1.MetricBatch{
		Metrics: []*praetorv1.Metric{
			{
				Name:      "cpu.usage_percent",
				Timestamp: timestamppb.New(time.Now()),
				Value:     42.5,
				Labels:    map[string]string{"cpu": "total"},
				Type:      praetorv1.MetricType_METRIC_TYPE_GAUGE,
			},
		},
	}

	if err := w.Write(context.Background(), "test-host-001", batch); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestWriteMetricBatch_Empty(t *testing.T) {
	w := victoriametrics.New(vmURL(t), testLogger())

	batch := &praetorv1.MetricBatch{}
	if err := w.Write(context.Background(), "test-host-001", batch); err != nil {
		t.Fatalf("Write empty batch: %v", err)
	}
}

func TestWriteMetricBatch_MultipleMetrics(t *testing.T) {
	w := victoriametrics.New(vmURL(t), testLogger())

	now := time.Now()
	batch := &praetorv1.MetricBatch{
		Metrics: []*praetorv1.Metric{
			{
				Name:      "mem.used_bytes",
				Timestamp: timestamppb.New(now),
				Value:     1073741824,
				Labels:    map[string]string{"type": "rss"},
				Type:      praetorv1.MetricType_METRIC_TYPE_GAUGE,
			},
			{
				Name:      "disk.read_ops",
				Timestamp: timestamppb.New(now),
				Value:     12345,
				Labels:    map[string]string{"device": "sda"},
				Type:      praetorv1.MetricType_METRIC_TYPE_COUNTER,
			},
		},
	}

	if err := w.Write(context.Background(), "test-host-002", batch); err != nil {
		t.Fatalf("Write: %v", err)
	}
}
