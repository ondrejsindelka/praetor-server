// Package notifier dispatches outbound webhook events to registered subscribers.
// Events are queued internally and delivered asynchronously with HMAC-SHA256
// signing and exponential retry.
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/crypto"
	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

const (
	deliveryTimeout = 10 * time.Second
	maxRetries      = 3
	retryDelay      = 2 * time.Second
	eventQueueSize  = 500
)

type event struct {
	fleetID string
	name    string
	payload map[string]any
}

// Notifier dispatches webhook events to registered subscribers.
type Notifier struct {
	repo   storage.WebhookRepo
	crypto *crypto.Crypto
	client *http.Client
	logger *slog.Logger

	eventCh chan event
}

// New creates a Notifier. cryptoHelper may be nil if no webhooks use secrets.
func New(repo storage.WebhookRepo, cryptoHelper *crypto.Crypto, logger *slog.Logger) *Notifier {
	return &Notifier{
		repo:    repo,
		crypto:  cryptoHelper,
		client:  &http.Client{Timeout: deliveryTimeout},
		logger:  logger,
		eventCh: make(chan event, eventQueueSize),
	}
}

// Start begins the dispatch loop. Blocks until ctx is canceled.
func (n *Notifier) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-n.eventCh:
			n.dispatch(ctx, e)
		}
	}
}

// Emit queues an event for delivery. Non-blocking (drops if queue full, logs warning).
func (n *Notifier) Emit(_ context.Context, fleetID, eventName string, data map[string]any) {
	e := event{fleetID: fleetID, name: eventName, payload: data}
	select {
	case n.eventCh <- e:
	default:
		n.logger.Warn("notifier: event queue full, dropping event", "fleet_id", fleetID, "event", eventName)
	}
}

// dispatch delivers one event to all matching webhooks for the fleet.
func (n *Notifier) dispatch(ctx context.Context, e event) {
	webhooks, err := n.repo.ListEnabled(ctx, e.fleetID)
	if err != nil {
		n.logger.Error("notifier: list webhooks", "err", err)
		return
	}

	payload := WebhookPayload{
		Event:     e.name,
		FleetID:   e.fleetID,
		Timestamp: time.Now(),
		Data:      e.payload,
	}

	for _, wh := range webhooks {
		if !eventMatches(wh.Events, e.name) {
			continue
		}
		wh := wh
		go n.deliver(ctx, wh, payload)
	}
}

func eventMatches(subscribed []string, event string) bool {
	for _, s := range subscribed {
		if s == event || s == "*" {
			return true
		}
	}
	return false
}

// deliver posts payload to one webhook URL with retry.
func (n *Notifier) deliver(ctx context.Context, wh *storage.Webhook, payload WebhookPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		n.logger.Error("notifier: marshal payload", "err", err)
		return
	}

	var secret []byte
	if len(wh.SecretEnc) > 0 {
		if n.crypto == nil {
			n.logger.Error("notifier: webhook has secret but crypto helper is nil, skipping delivery", "webhook_id", wh.ID)
			return
		}
		secret, err = n.crypto.Decrypt(wh.SecretEnc)
		if err != nil {
			n.logger.Error("notifier: decrypt webhook secret", "webhook_id", wh.ID, "err", err)
			return
		}
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.URL, bytes.NewReader(body))
		if err != nil {
			n.logger.Error("notifier: build request", "webhook_id", wh.ID, "err", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Praetor-Event", payload.Event)
		if len(secret) > 0 {
			req.Header.Set("X-Praetor-Signature", Sign(secret, body))
		}

		resp, err := n.client.Do(req)
		if err != nil {
			n.logger.Warn("notifier: delivery failed", "webhook_id", wh.ID, "attempt", attempt+1, "err", err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			n.logger.Debug("notifier: delivered", "webhook_id", wh.ID, "event", payload.Event)
			return
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// Client error — don't retry (except 429)
			if resp.StatusCode != 429 {
				n.logger.Warn("notifier: non-retryable error", "webhook_id", wh.ID, "status", resp.StatusCode)
				return
			}
		}
		n.logger.Warn("notifier: retrying", "webhook_id", wh.ID, "status", resp.StatusCode)
	}
	n.logger.Error("notifier: exhausted retries", "webhook_id", wh.ID, "event", payload.Event)
}
