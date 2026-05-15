package scheduler_test

import (
	"io"
	"log/slog"
)

// slogDiscard returns a logger that discards all output.
func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
