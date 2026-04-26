package stream_test

import (
	"testing"

	"github.com/ondrejsindelka/praetor-server/internal/stream"
)

func TestRegistryAddGetRemove(t *testing.T) {
	r := stream.NewRegistry()
	if r.Count() != 0 {
		t.Fatalf("expected 0 agents, got %d", r.Count())
	}

	// Registry stores interface values; nil is valid for unit testing add/get/remove mechanics.
	r.Add("host-1", nil)
	r.Add("host-2", nil)
	if r.Count() != 2 {
		t.Fatalf("expected 2 agents, got %d", r.Count())
	}

	_, ok := r.Get("host-1")
	if !ok {
		t.Error("expected host-1 to be in registry")
	}

	r.Remove("host-1")
	if r.Count() != 1 {
		t.Fatalf("expected 1 agent after remove, got %d", r.Count())
	}

	_, ok = r.Get("host-1")
	if ok {
		t.Error("host-1 should be gone after Remove")
	}
}

func TestRegistryOverwrite(t *testing.T) {
	r := stream.NewRegistry()
	r.Add("host-1", nil)
	r.Add("host-1", nil) // second Add for same host should overwrite, not double-count
	if r.Count() != 1 {
		t.Fatalf("expected 1 after overwrite, got %d", r.Count())
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := stream.NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for nonexistent host")
	}
}

func TestRegistryRemoveMissing(t *testing.T) {
	r := stream.NewRegistry()
	// Remove on a key that does not exist must not panic.
	r.Remove("does-not-exist")
	if r.Count() != 0 {
		t.Fatalf("expected 0 after remove of nonexistent, got %d", r.Count())
	}
}
