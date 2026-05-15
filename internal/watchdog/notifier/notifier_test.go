package notifier_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/crypto"
	"github.com/ondrejsindelka/praetor-server/internal/watchdog/notifier"
	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

// --- mock repo ---

type mockWebhookRepo struct {
	mu       sync.Mutex
	webhooks []*storage.Webhook
}

func (m *mockWebhookRepo) Create(_ context.Context, w *storage.Webhook) error { return nil }
func (m *mockWebhookRepo) Get(_ context.Context, id, _ string) (*storage.Webhook, error) {
	return nil, nil
}
func (m *mockWebhookRepo) List(_ context.Context, _ storage.ListOptions) ([]*storage.Webhook, error) {
	return nil, nil
}
func (m *mockWebhookRepo) ListEnabled(_ context.Context, fleetID string) ([]*storage.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*storage.Webhook
	for _, w := range m.webhooks {
		if w.Enabled && w.FleetID == fleetID {
			out = append(out, w)
		}
	}
	return out, nil
}
func (m *mockWebhookRepo) Update(_ context.Context, w *storage.Webhook) error  { return nil }
func (m *mockWebhookRepo) Delete(_ context.Context, id, _ string) error        { return nil }

// --- helpers ---

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(noopWriter{}, nil))
}

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

func validCrypto(t *testing.T) *crypto.Crypto {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	c, err := crypto.NewCrypto(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	return c
}

// runNotifier starts a Notifier in the background and returns a cancel func.
func runNotifier(t *testing.T, n *notifier.Notifier) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go n.Start(ctx)
	return cancel
}

// --- tests ---

// TestDeliveryToMatchingWebhook verifies that an event is POSTed when
// the webhook subscribes to that event.
func TestDeliveryToMatchingWebhook(t *testing.T) {
	var received atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh1", FleetID: "fleet1", URL: srv.URL, Events: []string{notifier.EventRuleFired}, Enabled: true},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet1", notifier.EventRuleFired, map[string]any{"host": "h1"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if received.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("webhook was not called within deadline")
}

// TestNoDeliveryForUnsubscribedEvent verifies that a webhook is NOT called
// when the event does not match its subscription.
func TestNoDeliveryForUnsubscribedEvent(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh2", FleetID: "fleet1", URL: srv.URL, Events: []string{notifier.EventInvestigationStarted}, Enabled: true},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet1", notifier.EventRuleFired, nil)

	// Give the notifier time to process and (incorrectly) call the server.
	time.Sleep(200 * time.Millisecond)
	if called.Load() {
		t.Error("webhook was called for an unsubscribed event")
	}
}

// TestNoDeliveryForDisabledWebhook verifies disabled webhooks are skipped.
func TestNoDeliveryForDisabledWebhook(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh3", FleetID: "fleet1", URL: srv.URL, Events: []string{"*"}, Enabled: false},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet1", notifier.EventRuleFired, nil)

	time.Sleep(200 * time.Millisecond)
	if called.Load() {
		t.Error("disabled webhook was called")
	}
}

// TestRetryOnServerError verifies that a 500 on the first attempt is retried
// and succeeds on the second attempt.
func TestRetryOnServerError(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh4", FleetID: "fleet1", URL: srv.URL, Events: []string{"*"}, Enabled: true},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet1", notifier.EventRuleFired, nil)

	// Retry delay is 2s, so allow up to 5s for two attempts.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("expected at least 2 attempts, got %d", attempts.Load())
}

// TestNonRetryable400 verifies that a 400 response causes no retry.
func TestNonRetryable400(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh5", FleetID: "fleet1", URL: srv.URL, Events: []string{"*"}, Enabled: true},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet1", notifier.EventRuleFired, nil)

	// Give time for potential (incorrect) retries.
	time.Sleep(500 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt for 400, got %d", got)
	}
}

// TestQueueFullNonBlocking verifies that Emit never blocks even when the
// internal queue is full.
func TestQueueFullNonBlocking(t *testing.T) {
	// Build a notifier with a tiny queue (we can't override the constant from
	// outside, so we use a blocking handler + many emits to observe drop behaviour).
	// Instead, we simply verify Emit returns quickly by timing it.
	var block sync.Mutex
	block.Lock() // lock so the handler hangs
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		block.Lock() //nolint:staticcheck // intentional hang to fill queue
		block.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer block.Unlock()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh6", FleetID: "fleet1", URL: srv.URL, Events: []string{"*"}, Enabled: true},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	start := time.Now()
	// Emit far more events than the queue can hold (queue=500; send 600).
	for i := 0; i < 600; i++ {
		n.Emit(context.Background(), "fleet1", notifier.EventRuleFired, nil)
	}
	elapsed := time.Since(start)

	// All 600 Emit calls should complete in well under 1 second (non-blocking).
	if elapsed > time.Second {
		t.Errorf("Emit blocked: 600 emits took %v, expected < 1s", elapsed)
	}
}

// TestHMACSignaturePresent verifies that a webhook with a secret receives the
// X-Praetor-Signature header containing a valid HMAC.
func TestHMACSignaturePresent(t *testing.T) {
	c := validCrypto(t)

	secretPlain := []byte("my-webhook-secret")
	secretEnc, err := c.Encrypt(secretPlain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	sigCh := make(chan string, 1)
	bodyCh := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.Header.Get("X-Praetor-Signature")
		var buf [4096]byte
		n, _ := r.Body.Read(buf[:])
		select {
		case sigCh <- sig:
		default:
		}
		select {
		case bodyCh <- buf[:n]:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh7", FleetID: "fleet1", URL: srv.URL, Events: []string{"*"}, SecretEnc: secretEnc, Enabled: true},
		},
	}
	n := notifier.New(repo, c, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet1", notifier.EventRuleFired, map[string]any{"host": "h1"})

	select {
	case sig := <-sigCh:
		body := <-bodyCh
		if sig == "" {
			t.Fatal("X-Praetor-Signature header missing")
		}
		if !notifier.Verify(secretPlain, body, sig) {
			t.Errorf("HMAC verification failed: sig=%q", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not called within deadline")
	}
}

// TestWildcardSubscription verifies that "*" subscription matches any event.
func TestWildcardSubscription(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh8", FleetID: "fleet1", URL: srv.URL, Events: []string{"*"}, Enabled: true},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet1", notifier.EventInvestigationCompleted, nil)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if called.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("webhook with '*' subscription was not called")
}

// TestPayloadFields verifies that the delivered JSON payload contains the
// expected envelope fields.
func TestPayloadFields(t *testing.T) {
	payloadCh := make(chan notifier.WebhookPayload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p notifier.WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err == nil {
			select {
			case payloadCh <- p:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &mockWebhookRepo{
		webhooks: []*storage.Webhook{
			{ID: "wh9", FleetID: "fleet2", URL: srv.URL, Events: []string{"*"}, Enabled: true},
		},
	}
	n := notifier.New(repo, nil, discardLogger())
	cancel := runNotifier(t, n)
	defer cancel()

	n.Emit(context.Background(), "fleet2", notifier.EventInvestigationStarted, map[string]any{"key": "val"})

	select {
	case p := <-payloadCh:
		if p.Event != notifier.EventInvestigationStarted {
			t.Errorf("Event = %q, want %q", p.Event, notifier.EventInvestigationStarted)
		}
		if p.FleetID != "fleet2" {
			t.Errorf("FleetID = %q, want %q", p.FleetID, "fleet2")
		}
		if p.Timestamp.IsZero() {
			t.Error("Timestamp is zero")
		}
		if p.Data["key"] != "val" {
			t.Errorf("Data[key] = %v, want %q", p.Data["key"], "val")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not called within deadline")
	}
}
