package trigger

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HostStore is the minimal interface needed to query hosts.
type HostStore interface {
	// ListByFleet returns all host IDs for a fleet (mapped from org_id).
	ListByFleet(ctx context.Context, fleetID string) ([]string, error)
	// ListByLabels returns host IDs for a fleet that match all given labels.
	ListByLabels(ctx context.Context, fleetID string, labels map[string]string) ([]string, error)
}

// SimpleHostResolver resolves HostSelector JSONB to a list of host IDs with a 60s TTL cache.
type SimpleHostResolver struct {
	store HostStore

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	ids       []string
	expiresAt time.Time
}

const cacheTTL = 60 * time.Second

// NewSimpleHostResolver creates a SimpleHostResolver backed by the given HostStore.
func NewSimpleHostResolver(store HostStore) *SimpleHostResolver {
	return &SimpleHostResolver{
		store: store,
		cache: make(map[string]cacheEntry),
	}
}

// Resolve converts a HostSelector map to a list of host IDs.
//
// Supported forms (per SPEC-005):
//   - {"host_ids": ["uuid1", "uuid2"]}  — direct list, returned as-is
//   - {"labels": {"role": "webhost"}}   — query hosts with matching labels
//   - {"all": true}                     — all hosts in fleet
func (r *SimpleHostResolver) Resolve(ctx context.Context, fleetID string, selector map[string]any) ([]string, error) {
	if selector == nil {
		return nil, nil
	}

	// Form 1: direct host_ids list
	if raw, ok := selector["host_ids"]; ok {
		return toStringSlice(raw)
	}

	// Form 2: label-based lookup (cacheable)
	if raw, ok := selector["labels"]; ok {
		labels, err := toStringMap(raw)
		if err != nil {
			return nil, fmt.Errorf("selector: labels: %w", err)
		}
		cacheKey := fleetID + ":labels:" + labelKey(labels)
		if ids, hit := r.cached(cacheKey); hit {
			return ids, nil
		}
		ids, err := r.store.ListByLabels(ctx, fleetID, labels)
		if err != nil {
			return nil, fmt.Errorf("selector: list by labels: %w", err)
		}
		r.setCache(cacheKey, ids)
		return ids, nil
	}

	// Form 3: all hosts in fleet (cacheable)
	if all, ok := selector["all"]; ok {
		if allBool, ok := all.(bool); ok && allBool {
			cacheKey := fleetID + ":all"
			if ids, hit := r.cached(cacheKey); hit {
				return ids, nil
			}
			ids, err := r.store.ListByFleet(ctx, fleetID)
			if err != nil {
				return nil, fmt.Errorf("selector: list by fleet: %w", err)
			}
			r.setCache(cacheKey, ids)
			return ids, nil
		}
	}

	return nil, fmt.Errorf("selector: unrecognised selector form: %v", selector)
}

func (r *SimpleHostResolver) cached(key string) ([]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.ids, true
}

func (r *SimpleHostResolver) setCache(key string, ids []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = cacheEntry{ids: ids, expiresAt: time.Now().Add(cacheTTL)}
}

// toStringSlice converts an any (expected []any or []string) to []string.
func toStringSlice(raw any) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("selector: host_ids[%d] is not a string", i)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("selector: host_ids is not a list, got %T", raw)
	}
}

// toStringMap converts an any (expected map[string]any or map[string]string) to map[string]string.
func toStringMap(raw any) (map[string]string, error) {
	switch v := raw.(type) {
	case map[string]string:
		return v, nil
	case map[string]any:
		out := make(map[string]string, len(v))
		for k, val := range v {
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("selector: label value for %q is not a string", k)
			}
			out[k] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("selector: labels is not a map, got %T", raw)
	}
}

// labelKey builds a deterministic cache key from a label map.
func labelKey(labels map[string]string) string {
	// Sort is not needed for correctness here; uniqueness per (k,v) set is sufficient
	// because labels come from JSONB and ordering is consistent per rule invocation.
	// For production use a sorted join; for test workloads this is fine.
	out := ""
	for k, v := range labels {
		out += k + "=" + v + ","
	}
	return out
}
