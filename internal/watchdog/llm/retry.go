package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// HTTPError is returned when the upstream API responds with an HTTP error status.
// It never includes the API key or request body.
type HTTPError struct {
	StatusCode int
	Provider   string
	RetryAfter time.Duration // set when status is 429 with Retry-After header
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: http %d", e.Provider, e.StatusCode)
}

// parseRetryAfter parses the Retry-After header value (seconds as integer).
func parseRetryAfter(header http.Header) time.Duration {
	val := header.Get("Retry-After")
	if val == "" {
		return 0
	}
	secs, err := strconv.Atoi(val)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// RetryPolicy configures exponential-backoff retry behaviour.
type RetryPolicy struct {
	MaxAttempts  int           // default 3
	InitialDelay time.Duration // default 1s
	MaxDelay     time.Duration // default 30s
	Multiplier   float64       // default 2.0
}

func (rp RetryPolicy) withDefaults() RetryPolicy {
	if rp.MaxAttempts <= 0 {
		rp.MaxAttempts = 3
	}
	if rp.InitialDelay <= 0 {
		rp.InitialDelay = time.Second
	}
	if rp.MaxDelay <= 0 {
		rp.MaxDelay = 30 * time.Second
	}
	if rp.Multiplier <= 0 {
		rp.Multiplier = 2.0
	}
	return rp
}

// retryProvider wraps a Provider with retry logic.
type retryProvider struct {
	inner  Provider
	policy RetryPolicy
}

// WithRetry wraps a Provider with the given RetryPolicy.
func WithRetry(p Provider, policy RetryPolicy) Provider {
	return &retryProvider{inner: p, policy: policy.withDefaults()}
}

// Name delegates to the wrapped provider.
func (r *retryProvider) Name() string { return r.inner.Name() }

// HealthCheck delegates without retrying.
func (r *retryProvider) HealthCheck(ctx context.Context) error {
	return r.inner.HealthCheck(ctx)
}

// Complete calls the wrapped provider, retrying on transient errors.
func (r *retryProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	delay := r.policy.InitialDelay
	var lastErr error

	for attempt := 1; attempt <= r.policy.MaxAttempts; attempt++ {
		resp, err := r.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}

		// Check if we should retry.
		if !r.isRetryable(err) {
			return nil, err
		}

		lastErr = err

		// Don't sleep after the last attempt.
		if attempt == r.policy.MaxAttempts {
			break
		}

		// Determine sleep duration.
		sleep := delay
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
			sleep = httpErr.RetryAfter
		}
		if sleep > r.policy.MaxDelay {
			sleep = r.policy.MaxDelay
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}

		// Increase delay for next iteration.
		delay = time.Duration(float64(delay) * r.policy.Multiplier)
		if delay > r.policy.MaxDelay {
			delay = r.policy.MaxDelay
		}
	}

	return nil, fmt.Errorf("all %d attempts failed: %w", r.policy.MaxAttempts, lastErr)
}

// isRetryable returns true for HTTP 429, 5xx, and network errors.
// Returns false for context.Canceled and 4xx (except 429).
func (r *retryProvider) isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		if httpErr.StatusCode >= 500 {
			return true
		}
		// 4xx (other than 429) are not retryable.
		if httpErr.StatusCode >= 400 {
			return false
		}
	}
	// Network errors and other non-HTTP errors are retryable.
	return true
}
