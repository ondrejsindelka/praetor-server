// Package command implements the command broker and rate limiting.
package command

import (
	"fmt"
	"sync"
	"time"
)

const (
	maxCommandsPerWindow = 60
	windowDuration       = 5 * time.Minute
)

type hostBucket struct {
	count int
	reset time.Time
}

// RateLimiter enforces per-host command rate limits.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*hostBucket
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: make(map[string]*hostBucket)}
}

// Allow returns nil if the command is within rate limits, or an error if not.
func (r *RateLimiter) Allow(hostID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	b, ok := r.buckets[hostID]
	if !ok || now.After(b.reset) {
		r.buckets[hostID] = &hostBucket{count: 1, reset: now.Add(windowDuration)}
		return nil
	}
	if b.count >= maxCommandsPerWindow {
		return fmt.Errorf("rate limit exceeded: max %d commands per %s per host", maxCommandsPerWindow, windowDuration)
	}
	b.count++
	return nil
}
