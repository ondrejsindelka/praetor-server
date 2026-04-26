//go:build integration

package loki_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/storage/loki"
)

func lokiURL(t *testing.T) string {
	t.Helper()
	if u := os.Getenv("TEST_LOKI_URL"); u != "" {
		return u
	}
	return "http://localhost:3100"
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestWriteLogBatch(t *testing.T) {
	w := loki.New(lokiURL(t), testLogger())

	batch := &praetorv1.LogBatch{
		Entries: []*praetorv1.LogEntry{
			{
				Timestamp: timestamppb.New(time.Now()),
				Severity:  praetorv1.LogSeverity_LOG_SEVERITY_INFO,
				Source:    "agent.main",
				Message:   "agent started successfully",
			},
		},
	}

	if err := w.Write(context.Background(), "test-host-001", batch); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestWriteLogBatch_Empty(t *testing.T) {
	w := loki.New(lokiURL(t), testLogger())

	batch := &praetorv1.LogBatch{}
	if err := w.Write(context.Background(), "test-host-001", batch); err != nil {
		t.Fatalf("Write empty batch: %v", err)
	}
}

func TestWriteLogBatch_MultipleSourcesAndSeverities(t *testing.T) {
	w := loki.New(lokiURL(t), testLogger())

	now := time.Now()
	batch := &praetorv1.LogBatch{
		Entries: []*praetorv1.LogEntry{
			{
				Timestamp: timestamppb.New(now),
				Severity:  praetorv1.LogSeverity_LOG_SEVERITY_ERROR,
				Source:    "agent.collector",
				Message:   "failed to collect disk metrics: permission denied",
			},
			{
				Timestamp: timestamppb.New(now.Add(time.Millisecond)),
				Severity:  praetorv1.LogSeverity_LOG_SEVERITY_WARNING,
				Source:    "agent.stream",
				Message:   "reconnecting to server",
			},
			{
				Timestamp: timestamppb.New(now.Add(2 * time.Millisecond)),
				Severity:  praetorv1.LogSeverity_LOG_SEVERITY_DEBUG,
				Source:    "agent.collector",
				Message:   "metric collection cycle completed",
			},
		},
	}

	if err := w.Write(context.Background(), "test-host-002", batch); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestWriteLogBatch_NoSource(t *testing.T) {
	w := loki.New(lokiURL(t), testLogger())

	batch := &praetorv1.LogBatch{
		Entries: []*praetorv1.LogEntry{
			{
				Timestamp: timestamppb.New(time.Now()),
				Severity:  praetorv1.LogSeverity_LOG_SEVERITY_INFO,
				Message:   "entry without source falls back to unknown",
			},
		},
	}

	if err := w.Write(context.Background(), "test-host-003", batch); err != nil {
		t.Fatalf("Write: %v", err)
	}
}
