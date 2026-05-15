package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---- mock implementations ----

type mockRepo struct {
	records  map[string]*LLMProviderRecord
	defaults map[string]*LLMProviderRecord
}

func (m *mockRepo) Get(ctx context.Context, id, fleetID string) (*LLMProviderRecord, error) {
	key := fleetID + "/" + id
	rec, ok := m.records[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return rec, nil
}

func (m *mockRepo) GetDefault(ctx context.Context, fleetID string) (*LLMProviderRecord, error) {
	rec, ok := m.defaults[fleetID]
	if !ok {
		return nil, errors.New("no default provider")
	}
	return rec, nil
}

func (m *mockRepo) List(ctx context.Context, fleetID string) ([]*LLMProviderRecord, error) {
	var out []*LLMProviderRecord
	for _, rec := range m.records {
		if rec.FleetID == fleetID {
			out = append(out, rec)
		}
	}
	return out, nil
}

type mockDecrypter struct {
	plaintext string
	err       error
	calls     int
}

func (d *mockDecrypter) Decrypt(ciphertext []byte) ([]byte, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return []byte(d.plaintext), nil
}

// ---- tests ----

func TestRegistryCacheTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "ok", "stop", 1, 1))
	}))
	defer srv.Close()

	decrypter := &mockDecrypter{plaintext: "test-api-key"}
	repo := &mockRepo{
		records: map[string]*LLMProviderRecord{
			"fleet1/provider1": {
				ID:      "provider1",
				FleetID: "fleet1",
				Name:    "test-openrouter",
				Provider: "openrouter",
				Endpoint: srv.URL,
				APIKeyEnc: []byte("encrypted"),
			},
		},
		defaults: map[string]*LLMProviderRecord{},
	}

	reg := NewRegistry(repo, decrypter)

	// First call: should build and cache provider; decrypter called once.
	p1, err := reg.Get(context.Background(), "provider1", "fleet1")
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}
	if decrypter.calls != 1 {
		t.Errorf("decrypter.calls = %d, want 1", decrypter.calls)
	}

	// Second call within TTL: should return cached provider; decrypter NOT called again.
	p2, err := reg.Get(context.Background(), "provider1", "fleet1")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if decrypter.calls != 1 {
		t.Errorf("decrypter.calls = %d after cache hit, want still 1", decrypter.calls)
	}
	if p1 != p2 {
		t.Error("expected same provider instance from cache")
	}
}

func TestRegistryCacheExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "ok", "stop", 1, 1))
	}))
	defer srv.Close()

	decrypter := &mockDecrypter{plaintext: "api-key"}
	repo := &mockRepo{
		records: map[string]*LLMProviderRecord{
			"fleet1/p1": {
				ID: "p1", FleetID: "fleet1", Provider: "openrouter",
				Endpoint: srv.URL, APIKeyEnc: []byte("enc"),
			},
		},
		defaults: map[string]*LLMProviderRecord{},
	}

	reg := NewRegistry(repo, decrypter)

	// Prime the cache but set an expired entry manually.
	reg.mu.Lock()
	reg.cache["fleet1/p1"] = cachedProvider{
		provider: &OpenRouterProvider{apiKey: "old", endpoint: srv.URL, client: srv.Client()},
		cachedAt: time.Now().Add(-cacheTTL - time.Second), // expired
	}
	reg.mu.Unlock()

	// Get should bypass the expired cache and rebuild; decrypter called.
	_, err := reg.Get(context.Background(), "p1", "fleet1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if decrypter.calls != 1 {
		t.Errorf("decrypter.calls = %d, want 1 (rebuild after expiry)", decrypter.calls)
	}
}

func TestRegistryDecryptionFailure(t *testing.T) {
	decrypter := &mockDecrypter{err: errors.New("decryption failed")}
	repo := &mockRepo{
		records: map[string]*LLMProviderRecord{
			"fleet1/p1": {
				ID: "p1", FleetID: "fleet1", Provider: "openrouter",
				Endpoint: "https://openrouter.ai/api/v1", APIKeyEnc: []byte("bad"),
			},
		},
		defaults: map[string]*LLMProviderRecord{},
	}

	reg := NewRegistry(repo, decrypter)
	_, err := reg.Get(context.Background(), "p1", "fleet1")
	if err == nil {
		t.Fatal("expected error on decryption failure, got nil")
	}
	if !errors.Is(err, decrypter.err) {
		t.Errorf("expected wrapped decryption error, got: %v", err)
	}
}

func TestRegistryGetDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("gpt-4o", "default response", "stop", 1, 1))
	}))
	defer srv.Close()

	decrypter := &mockDecrypter{plaintext: "key"}
	repo := &mockRepo{
		records: map[string]*LLMProviderRecord{},
		defaults: map[string]*LLMProviderRecord{
			"fleet1": {
				ID: "default-p", FleetID: "fleet1", Provider: "openrouter",
				Endpoint: srv.URL, APIKeyEnc: []byte("enc"), IsDefault: true,
			},
		},
	}

	reg := NewRegistry(repo, decrypter)
	p, err := reg.GetDefault(context.Background(), "fleet1")
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Errorf("Name = %q, want openrouter", p.Name())
	}
}

func TestRegistryUnknownProvider(t *testing.T) {
	decrypter := &mockDecrypter{plaintext: "key"}
	repo := &mockRepo{
		records: map[string]*LLMProviderRecord{
			"fleet1/p1": {
				ID: "p1", FleetID: "fleet1", Provider: "unknown-llm",
				Endpoint: "https://example.com", APIKeyEnc: []byte("enc"),
			},
		},
		defaults: map[string]*LLMProviderRecord{},
	}

	reg := NewRegistry(repo, decrypter)
	_, err := reg.Get(context.Background(), "p1", "fleet1")
	if err == nil {
		t.Fatal("expected error for unknown provider type")
	}
}

func TestRegistryOllamaNoDecryption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAISuccessResponse("llama3", "hi", "stop", 1, 1))
	}))
	defer srv.Close()

	decrypter := &mockDecrypter{} // calls should remain 0
	repo := &mockRepo{
		records: map[string]*LLMProviderRecord{
			"fleet1/ollama1": {
				ID: "ollama1", FleetID: "fleet1", Provider: "ollama",
				Endpoint:  srv.URL,
				APIKeyEnc: nil, // no key for Ollama
			},
		},
		defaults: map[string]*LLMProviderRecord{},
	}

	reg := NewRegistry(repo, decrypter)
	p, err := reg.Get(context.Background(), "ollama1", "fleet1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Name() != "ollama" {
		t.Errorf("Name = %q, want ollama", p.Name())
	}
	if decrypter.calls != 0 {
		t.Errorf("decrypter.calls = %d, want 0 for Ollama (no API key)", decrypter.calls)
	}
}
