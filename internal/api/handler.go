package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"
	"github.com/ondrejsindelka/praetor-server/internal/command"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"github.com/ondrejsindelka/praetor-server/internal/token"
)

const (
	defaultTokenTTL = 15 * time.Minute
	maxTokenTTL     = 24 * time.Hour
)

// hostLister is the interface the Handler uses for host operations.
type hostLister interface {
	List(ctx context.Context, orgID string) ([]*store.Host, error)
	GetByID(ctx context.Context, id string) (*store.Host, error)
}

// tokenManager is the interface the Handler uses for token operations.
type tokenManager interface {
	List(ctx context.Context, orgID string, includeExpired, includeRevoked bool) ([]*store.EnrollmentToken, error)
	Insert(ctx context.Context, t *store.EnrollmentToken) error
	Revoke(ctx context.Context, id string) error
}

// commandIssuer is the interface the Handler uses to issue commands.
type commandIssuer interface {
	Issue(ctx context.Context, req command.IssueRequest) (string, error)
}

// commandGetter is the interface the Handler uses to retrieve command executions.
type commandGetter interface {
	Get(ctx context.Context, id string) (*store.CommandExecution, error)
}

// Handler holds dependencies for all REST handlers.
type Handler struct {
	hosts    hostLister
	tokens   tokenManager
	broker   commandIssuer
	commands commandGetter
	apiKey   string
	orgID    string
	logger   *slog.Logger
}

// NewHandler creates a Handler.
// If logger is nil, slog.Default() is used.
func NewHandler(hosts hostLister, tokens tokenManager, broker commandIssuer, commands commandGetter, apiKey, orgID string, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{hosts: hosts, tokens: tokens, broker: broker, commands: commands, apiKey: apiKey, orgID: orgID, logger: logger}
}

// Routes returns an http.Handler with all routes registered.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// No auth, no logging for healthz
	mux.HandleFunc("GET /healthz", h.handleHealthz)

	// All /v1/ routes: request logger → bearer auth → handler
	v1 := http.NewServeMux()
	v1.HandleFunc("GET /v1/hosts", h.handleListHosts)
	v1.HandleFunc("GET /v1/hosts/{id}", h.handleGetHost)
	v1.HandleFunc("GET /v1/tokens", h.handleListTokens)
	v1.HandleFunc("POST /v1/tokens", h.handleIssueToken)
	v1.HandleFunc("DELETE /v1/tokens/{id}", h.handleRevokeToken)
	v1.HandleFunc("POST /v1/commands", h.handleIssueCommand)
	v1.HandleFunc("GET /v1/commands/{id}", h.handleGetCommand)

	// CORS: deny by default — no Access-Control-Allow-Origin header set.
	// To enable CORS for a specific origin in future, add an allowOrigins config
	// field and set the header here before passing to auth middleware.

	mux.Handle("/v1/", requestLogger(h.logger, bearerAuth(h.apiKey, v1)))

	return mux
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := h.hosts.List(r.Context(), h.orgID)
	if err != nil {
		h.logger.Error("list hosts", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	resp := make([]HostResponse, len(hosts))
	for i, host := range hosts {
		resp[i] = toHostResponse(host)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hosts": resp,
		"count": len(resp),
		"total": len(resp),
	})
}

func (h *Handler) handleGetHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, err := h.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("host %q not found", id))
			return
		}
		h.logger.Error("get host", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toHostResponse(host))
}

func (h *Handler) handleListTokens(w http.ResponseWriter, r *http.Request) {
	// Parse ?include= query param: comma-separated "expired", "revoked"
	include := r.URL.Query().Get("include")
	includeExpired := contains(include, "expired")
	includeRevoked := contains(include, "revoked")

	tokens, err := h.tokens.List(r.Context(), h.orgID, includeExpired, includeRevoked)
	if err != nil {
		h.logger.Error("list tokens", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	resp := make([]TokenResponse, len(tokens))
	for i, t := range tokens {
		resp[i] = toTokenResponse(t)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tokens": resp,
		"count":  len(resp),
		"total":  len(resp),
	})
}

func (h *Handler) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	var req IssueTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}
	ttl := defaultTokenTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if ttl > maxTokenTTL {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("ttl_seconds exceeds maximum (%d)", int(maxTokenTTL.Seconds())))
		return
	}

	tok, err := token.Generate()
	if err != nil {
		h.logger.Error("generate token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	expiresAt := time.Now().Add(ttl)
	t := &store.EnrollmentToken{
		ID:        tok.ID,
		TokenHash: fmt.Sprintf("%x", tok.Hash),
		Label:     &req.Label,
		OrgID:     h.orgID,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}
	if err := h.tokens.Insert(r.Context(), t); err != nil {
		h.logger.Error("insert token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Plain token returned ONLY here — never persisted, never returned again.
	writeJSON(w, http.StatusCreated, IssueTokenResponse{
		ID:        tok.ID,
		Token:     tok.Plain,
		Label:     req.Label,
		ExpiresAt: expiresAt,
	})
}

func (h *Handler) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.tokens.Revoke(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("token %q not found", id))
			return
		}
		h.logger.Error("revoke token", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleIssueCommand(w http.ResponseWriter, r *http.Request) {
	var req IssueCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.HostID == "" {
		writeError(w, http.StatusBadRequest, "host_id is required")
		return
	}
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}

	// Map tier integer to CommandTier enum.
	var tier praetorv1.CommandTier
	switch req.Tier {
	case 0:
		tier = praetorv1.CommandTier_COMMAND_TIER_0_SAFE
	case 1:
		tier = praetorv1.CommandTier_COMMAND_TIER_1_VALIDATED
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported tier %d", req.Tier))
		return
	}

	// Build the typed command payload.
	var cmd interface{}
	switch {
	case req.Diagnostic != nil:
		check, ok := diagnosticCheckMap[req.Diagnostic.Check]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown diagnostic check %q", req.Diagnostic.Check))
			return
		}
		dc := &praetorv1.DiagnosticCommand{Check: check}
		if len(req.Diagnostic.Params) > 0 {
			dc.Params = req.Diagnostic.Params
		}
		cmd = dc
	case req.Shell != nil:
		if req.Shell.Binary == "" {
			writeError(w, http.StatusBadRequest, "shell.binary is required")
			return
		}
		cmd = &praetorv1.ShellCommand{Binary: req.Shell.Binary, Args: req.Shell.Args}
	default:
		writeError(w, http.StatusBadRequest, "one of diagnostic or shell must be specified")
		return
	}

	id, err := h.broker.Issue(r.Context(), command.IssueRequest{
		HostID:   req.HostID,
		Tier:     tier,
		Reason:   req.Reason,
		IssuedBy: req.IssuedBy,
		Timeout:  req.TimeoutSeconds,
		Command:  cmd,
	})
	if err != nil {
		// If the ID is set, the command was stored but the host isn't connected — still 202.
		if id != "" {
			h.logger.Warn("command issued but host not connected", "id", id, "err", err)
			writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "status": "pending"})
			return
		}
		h.logger.Error("issue command", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "status": "pending"})
}

func (h *Handler) handleGetCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cmd, err := h.commands.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("command %q not found", id))
			return
		}
		h.logger.Error("get command", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toCommandResponse(cmd))
}

// contains checks if csv (comma-separated values) contains target.
func contains(csv, target string) bool {
	for _, v := range strings.Split(csv, ",") {
		if strings.TrimSpace(v) == target {
			return true
		}
	}
	return false
}
