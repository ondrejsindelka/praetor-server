package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ondrejsindelka/praetor-server/internal/api"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
)

// --- fakes ---

type fakeHostStore struct {
	hosts  []*store.Host
	getErr error
}

func (f *fakeHostStore) List(_ context.Context, _ string) ([]*store.Host, error) {
	return f.hosts, nil
}

func (f *fakeHostStore) GetByID(_ context.Context, id string) (*store.Host, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, h := range f.hosts {
		if h.ID == id {
			return h, nil
		}
	}
	return nil, pgx.ErrNoRows
}

type fakeTokenStore struct {
	tokens    []*store.EnrollmentToken
	insertErr error
	revokeErr error
}

func (f *fakeTokenStore) List(_ context.Context, _ string, _, _ bool) ([]*store.EnrollmentToken, error) {
	return f.tokens, nil
}

func (f *fakeTokenStore) Insert(_ context.Context, t *store.EnrollmentToken) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.tokens = append(f.tokens, t)
	return nil
}

func (f *fakeTokenStore) Revoke(_ context.Context, id string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	return nil
}

// --- helpers ---

const testAPIKey = "test-api-key"

func newTestHandler(hosts *fakeHostStore, tokens *fakeTokenStore) http.Handler {
	h := api.NewHandler(hosts, tokens, testAPIKey, "default", nil)
	return h.Routes()
}

func authHeader() string {
	return "Bearer " + testAPIKey
}

// --- tests ---

func TestHealthz(t *testing.T) {
	handler := newTestHandler(&fakeHostStore{}, &fakeTokenStore{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

func TestHealthzNoAuth(t *testing.T) {
	handler := newTestHandler(&fakeHostStore{}, &fakeTokenStore{})

	// healthz should not require auth
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 without auth on /healthz, got %d", w.Code)
	}
}

func TestBearerAuth(t *testing.T) {
	handler := newTestHandler(&fakeHostStore{}, &fakeTokenStore{})

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"no authorization header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized},
		{"correct token", "Bearer " + testAPIKey, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/hosts", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("expected %d, got %d", tc.wantStatus, w.Code)
			}
		})
	}
}

func TestListHosts_Empty(t *testing.T) {
	handler := newTestHandler(&fakeHostStore{}, &fakeTokenStore{})

	req := httptest.NewRequest(http.MethodGet, "/v1/hosts", nil)
	req.Header.Set("Authorization", authHeader())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	hosts, ok := body["hosts"].([]any)
	if !ok {
		t.Fatalf("expected hosts array, got %T", body["hosts"])
	}
	if len(hosts) != 0 {
		t.Errorf("expected empty hosts, got %d", len(hosts))
	}
	if body["count"] != float64(0) {
		t.Errorf("expected count=0, got %v", body["count"])
	}
	if body["total"] != float64(0) {
		t.Errorf("expected total=0, got %v", body["total"])
	}
}

func TestGetHost_NotFound(t *testing.T) {
	handler := newTestHandler(&fakeHostStore{}, &fakeTokenStore{})

	req := httptest.NewRequest(http.MethodGet, "/v1/hosts/nonexistent-id", nil)
	req.Header.Set("Authorization", authHeader())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected non-empty error message")
	}
}

func TestIssueToken(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		checkToken bool
	}{
		{
			name:       "missing label",
			body:       `{"ttl_seconds":300}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty label",
			body:       `{"label":"","ttl_seconds":300}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid json",
			body:       `not-json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "ttl too large",
			body:       `{"label":"test","ttl_seconds":999999}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "valid request",
			body:       `{"label":"smoke-test","ttl_seconds":300}`,
			wantStatus: http.StatusCreated,
			checkToken: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newTestHandler(&fakeHostStore{}, &fakeTokenStore{})

			req := httptest.NewRequest(http.MethodPost, "/v1/tokens",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", authHeader())
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d; body: %s", tc.wantStatus, w.Code, w.Body.String())
			}

			if tc.checkToken {
				var resp map[string]any
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				tok, _ := resp["token"].(string)
				if !strings.HasPrefix(tok, "PRAETOR-") {
					t.Errorf("expected token starting with PRAETOR-, got %q", tok)
				}
				if resp["id"] == "" {
					t.Error("expected non-empty id")
				}
				if resp["expires_at"] == "" {
					t.Error("expected non-empty expires_at")
				}
				// token_hash must NOT be present
				if _, hasHash := resp["token_hash"]; hasHash {
					t.Error("token_hash must not appear in POST /v1/tokens response")
				}
			}
		})
	}
}

func TestRevokeToken(t *testing.T) {
	handler := newTestHandler(&fakeHostStore{}, &fakeTokenStore{})

	req := httptest.NewRequest(http.MethodDelete, "/v1/tokens/some-token-id", nil)
	req.Header.Set("Authorization", authHeader())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body on 204, got %q", w.Body.String())
	}
}

func TestListTokens(t *testing.T) {
	label := "test-token"
	tokens := &fakeTokenStore{
		tokens: []*store.EnrollmentToken{
			{
				ID:        "tok-1",
				TokenHash: "should-never-appear-in-response",
				Label:     &label,
				OrgID:     "default",
				CreatedAt: time.Now(),
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
	}
	handler := newTestHandler(&fakeHostStore{}, tokens)

	req := httptest.NewRequest(http.MethodGet, "/v1/tokens", nil)
	req.Header.Set("Authorization", authHeader())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify token_hash never appears in response
	rawBody := w.Body.String()
	if strings.Contains(rawBody, "token_hash") {
		t.Error("token_hash must never appear in GET /v1/tokens response")
	}
	if strings.Contains(rawBody, "should-never-appear-in-response") {
		t.Error("token hash value must never appear in GET /v1/tokens response")
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	toks, ok := body["tokens"].([]any)
	if !ok {
		t.Fatalf("expected tokens array, got %T", body["tokens"])
	}
	if len(toks) != 1 {
		t.Errorf("expected 1 token, got %d", len(toks))
	}
}
