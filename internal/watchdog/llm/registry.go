package llm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const cacheTTL = 5 * time.Minute

// LLMProviderRepo is the minimal repository interface required by the Registry.
// It is intentionally narrow to avoid importing the storage package directly.
type LLMProviderRepo interface {
	Get(ctx context.Context, id, fleetID string) (*LLMProviderRecord, error)
	GetDefault(ctx context.Context, fleetID string) (*LLMProviderRecord, error)
	List(ctx context.Context, fleetID string) ([]*LLMProviderRecord, error)
}

// CryptoDecrypter decrypts ciphertext produced by the crypto package.
type CryptoDecrypter interface {
	Decrypt(ciphertext []byte) ([]byte, error)
}

// LLMProviderRecord mirrors storage.LLMProvider for use within this package.
type LLMProviderRecord struct {
	ID           string
	FleetID      string
	Name         string
	Provider     string // "openrouter" | "anthropic" | "ollama"
	Endpoint     string
	APIKeyEnc    []byte // encrypted, nil for Ollama
	DefaultModel string
	IsDefault    bool
}

// cachedProvider holds a built Provider with the time it was cached.
type cachedProvider struct {
	provider Provider
	cachedAt time.Time
}

// Registry loads LLM provider configurations from a repo, decrypts API keys,
// builds the appropriate Provider, and caches the result.
type Registry struct {
	repo   LLMProviderRepo
	crypto CryptoDecrypter
	mu     sync.RWMutex
	cache  map[string]cachedProvider // keyed by "<fleetID>/<providerID>"
}

// NewRegistry creates a Registry with the given repo and decrypter.
func NewRegistry(repo LLMProviderRepo, crypto CryptoDecrypter) *Registry {
	return &Registry{
		repo:   repo,
		crypto: crypto,
		cache:  make(map[string]cachedProvider),
	}
}

// Get returns the Provider for the given provider name and fleet, building and
// caching it if not already cached or if the cache entry has expired.
func (r *Registry) Get(ctx context.Context, name, fleetID string) (Provider, error) {
	rec, err := r.repo.Get(ctx, name, fleetID)
	if err != nil {
		return nil, fmt.Errorf("registry: get provider %q (fleet %q): %w", name, fleetID, err)
	}
	return r.providerFromRecord(rec)
}

// GetDefault returns the default Provider for the given fleet.
func (r *Registry) GetDefault(ctx context.Context, fleetID string) (Provider, error) {
	rec, err := r.repo.GetDefault(ctx, fleetID)
	if err != nil {
		return nil, fmt.Errorf("registry: get default provider (fleet %q): %w", fleetID, err)
	}
	return r.providerFromRecord(rec)
}

// providerFromRecord returns a cached or freshly-built Provider for the record.
func (r *Registry) providerFromRecord(rec *LLMProviderRecord) (Provider, error) {
	key := rec.FleetID + "/" + rec.ID

	// Fast path: check cache under read lock.
	r.mu.RLock()
	if entry, ok := r.cache[key]; ok && time.Since(entry.cachedAt) < cacheTTL {
		r.mu.RUnlock()
		return entry.provider, nil
	}
	r.mu.RUnlock()

	// Slow path: build provider under write lock.
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock.
	if entry, ok := r.cache[key]; ok && time.Since(entry.cachedAt) < cacheTTL {
		return entry.provider, nil
	}

	p, err := r.buildProvider(rec)
	if err != nil {
		return nil, err
	}

	r.cache[key] = cachedProvider{provider: p, cachedAt: time.Now()}
	return p, nil
}

// buildProvider constructs a Provider from a record, decrypting the API key as needed.
func (r *Registry) buildProvider(rec *LLMProviderRecord) (Provider, error) {
	var apiKey string
	if len(rec.APIKeyEnc) > 0 {
		plaintext, err := r.crypto.Decrypt(rec.APIKeyEnc)
		if err != nil {
			return nil, fmt.Errorf("registry: decrypt api key for provider %q: %w", rec.ID, err)
		}
		apiKey = string(plaintext)
	}

	switch rec.Provider {
	case "openrouter":
		return NewOpenRouterProvider(apiKey, rec.Endpoint, nil)
	case "anthropic":
		return NewAnthropicProvider(apiKey, rec.Endpoint, nil)
	case "ollama":
		return NewOllamaProvider(rec.Endpoint, nil)
	default:
		return nil, fmt.Errorf("registry: unknown provider type %q", rec.Provider)
	}
}

// Invalidate removes a cached provider entry, forcing a rebuild on next access.
func (r *Registry) Invalidate(fleetID, id string) {
	key := fleetID + "/" + id
	r.mu.Lock()
	delete(r.cache, key)
	r.mu.Unlock()
}
