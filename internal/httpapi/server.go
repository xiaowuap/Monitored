package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/store"
)

type API struct {
	repository *store.Repository
	config     config.Server
	auth       *authenticator
	events     *Broker
	logger     *slog.Logger
}

func New(repository *store.Repository, cfg config.Server, logger *slog.Logger) http.Handler {
	api := &API{
		repository: repository,
		config:     cfg,
		auth:       newAuthenticator(cfg),
		events:     NewBroker(),
		logger:     logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("POST /api/v1/auth/login", api.auth.login)
	mux.HandleFunc("POST /api/v1/auth/logout", api.auth.logout)
	mux.Handle("GET /api/v1/auth/me", api.auth.middleware(http.HandlerFunc(api.auth.me)))
	mux.Handle("GET /api/v1/events", api.auth.middleware(api.events))
	mux.Handle("GET /api/v1/overview", api.auth.middleware(http.HandlerFunc(api.overview)))
	mux.Handle("GET /api/v1/assets", api.auth.middleware(http.HandlerFunc(api.assets)))
	mux.Handle("GET /api/v1/sensors", api.auth.middleware(http.HandlerFunc(api.sensors)))
	mux.Handle("GET /api/v1/sensors/{id}/metrics", api.auth.middleware(http.HandlerFunc(api.metrics)))
	mux.Handle("GET /api/v1/alerts", api.auth.middleware(http.HandlerFunc(api.alerts)))
	mux.Handle("GET /api/v1/incidents", api.auth.middleware(http.HandlerFunc(api.incidents)))
	mux.Handle("GET /api/v1/discovery/runs", api.auth.middleware(http.HandlerFunc(api.discoveryRuns)))
	mux.Handle("POST /api/v1/discovery/runs", api.auth.middleware(http.HandlerFunc(api.createDiscoveryRun)))
	mux.Handle("GET /api/v1/llm/suggestions", api.auth.middleware(http.HandlerFunc(api.suggestions)))
	mux.Handle("POST /api/v1/llm/suggestions/{id}/decision", api.auth.middleware(http.HandlerFunc(api.reviewSuggestion)))
	mux.Handle("GET /api/v1/actions", api.auth.middleware(http.HandlerFunc(api.actions)))
	mux.Handle("POST /api/v1/actions/{id}/decision", api.auth.middleware(http.HandlerFunc(api.reviewAction)))
	mux.Handle("GET /api/v1/audit", api.auth.middleware(http.HandlerFunc(api.audit)))
	mux.HandleFunc("POST /api/v1/ingest", api.ingest)

	return api.recover(api.requestLog(api.cors(mux)))
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := timeboxedContext(r, 2*time.Second)
	defer cancel()
	if err := a.repository.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":   "degraded",
			"database": "unavailable",
			"error":    err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "healthy",
		"database": "healthy",
		"redis":    "optional",
		"time":     time.Now().UTC(),
	})
}

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.Overview(r.Context())
	if err != nil {
		a.internalError(w, "load overview", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) assets(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListAssets(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		a.internalError(w, "list assets", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) sensors(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListSensors(r.Context(), r.URL.Query().Get("status"), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list sensors", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	hours := queryInt(r, "hours", 24)
	if hours < 1 || hours > 24*90 {
		hours = 24
	}
	value, err := a.repository.Metrics(r.Context(), r.PathValue("id"), time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		a.internalError(w, "load metrics", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": value,
		"range": map[string]any{"hours": hours},
	})
}

func (a *API) alerts(w http.ResponseWriter, r *http.Request) {
	active := r.URL.Query().Get("active") != "false"
	value, err := a.repository.ListAlerts(r.Context(), active, queryInt(r, "limit", 100))
	if err != nil {
		a.internalError(w, "list alerts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) incidents(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListIncidents(r.Context(), queryInt(r, "limit", 50))
	if err != nil {
		a.internalError(w, "list incidents", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) discoveryRuns(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListDiscoveryRuns(r.Context(), queryInt(r, "limit", 50))
	if err != nil {
		a.internalError(w, "list discovery runs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createDiscoveryRun(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		AgentID string `json:"agentId"`
		Kind    string `json:"kind"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	run, err := a.repository.CreateDiscoveryRun(r.Context(), actorFrom(r).Email, payload.AgentID, payload.Kind)
	if err != nil {
		a.internalError(w, "start discovery", err)
		return
	}
	a.events.Publish(Event{Type: "discovery.progress", Data: map[string]any{
		"id": run.ID, "state": run.State, "progress": run.Progress,
	}})
	go func() {
		time.Sleep(1600 * time.Millisecond)
		ctx, cancel := detachedContext(10 * time.Second)
		defer cancel()
		if err := a.repository.CompleteDiscoveryRun(ctx, run.ID); err != nil {
			a.logger.Error("complete simulated discovery run", "id", run.ID, "error", err)
			return
		}
		a.events.Publish(Event{Type: "discovery.completed", Data: map[string]any{
			"id": run.ID, "state": "succeeded", "progress": 100,
		}})
	}()
	writeJSON(w, http.StatusAccepted, run)
}

func (a *API) suggestions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	value, err := a.repository.ListSuggestions(r.Context(), status, queryInt(r, "limit", 100))
	if err != nil {
		a.internalError(w, "list suggestions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) reviewSuggestion(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Decision string `json:"decision"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if payload.Decision != "Accepted" && payload.Decision != "Rejected" {
		writeError(w, http.StatusBadRequest, "invalid_decision", "Decision must be Accepted or Rejected.")
		return
	}
	if err := a.repository.ReviewSuggestion(r.Context(), r.PathValue("id"), payload.Decision, actorFrom(r).Email); err != nil {
		a.writeStoreError(w, err)
		return
	}
	a.events.Publish(Event{Type: "suggestion.reviewed", Data: map[string]any{
		"id": r.PathValue("id"), "decision": payload.Decision,
	}})
	writeJSON(w, http.StatusOK, map[string]any{"status": payload.Decision})
}

func (a *API) actions(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListActions(r.Context(), r.URL.Query().Get("state"), queryInt(r, "limit", 100))
	if err != nil {
		a.internalError(w, "list actions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) reviewAction(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Decision string `json:"decision"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if payload.Decision != "approve" && payload.Decision != "reject" {
		writeError(w, http.StatusBadRequest, "invalid_decision", "Decision must be approve or reject.")
		return
	}
	if actorFrom(r).Role != "admin" {
		writeError(w, http.StatusForbidden, "admin_required", "Administrator approval is required.")
		return
	}
	if err := a.repository.ReviewAction(
		r.Context(),
		r.PathValue("id"),
		payload.Decision,
		actorFrom(r).Email,
		remoteAddress(r),
	); err != nil {
		a.writeStoreError(w, err)
		return
	}
	a.events.Publish(Event{Type: "action.reviewed", Data: map[string]any{
		"id": r.PathValue("id"), "decision": payload.Decision,
	}})
	writeJSON(w, http.StatusOK, map[string]any{"state": payload.Decision})
}

func (a *API) audit(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListAudit(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		a.internalError(w, "list audit entries", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) ingest(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" || subtleTokenMismatch(token, a.config.AgentToken) {
		writeError(w, http.StatusUnauthorized, "invalid_agent_token", "Agent authentication failed.")
		return
	}
	var batch domain.IngestBatch
	if err := decodeJSONWithLimit(r, &batch, 8<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_batch", err.Error())
		return
	}
	if err := a.repository.Ingest(r.Context(), batch); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "ingest_failed", err.Error())
		return
	}
	a.events.Publish(Event{Type: "telemetry.ingested", Data: map[string]any{
		"agentId": batch.AgentID,
		"samples": len(batch.Samples),
	}})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": len(batch.Samples),
		"at":       time.Now().UTC(),
	})
}

func (a *API) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "The requested resource does not exist.")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "state_conflict", "The resource has already been reviewed.")
	default:
		a.internalError(w, "repository operation", err)
	}
}

func (a *API) internalError(w http.ResponseWriter, operation string, err error) {
	a.logger.Error(operation, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed.")
}

func (a *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && a.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) originAllowed(origin string) bool {
	if origin == a.config.PublicURL {
		return true
	}
	publicURL, publicErr := url.Parse(a.config.PublicURL)
	originURL, originErr := url.Parse(origin)
	if publicErr != nil || originErr != nil || !isLoopbackHost(publicURL.Hostname()) {
		return false
	}
	return isLoopbackHost(originURL.Hostname()) &&
		(originURL.Scheme == "http" || originURL.Scheme == "https")
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *API) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "panic", recovered, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal_error", "The request failed unexpectedly.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *API) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		a.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func decodeJSON(r *http.Request, target any) error {
	return decodeJSONWithLimit(r, target, 1<<20)
}

func decodeJSONWithLimit(r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("decode JSON body: exactly one JSON value is required")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func queryInt(r *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return fallback
	}
	return value
}

func remoteAddress(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
