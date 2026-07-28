package httpapi

import (
	"crypto/sha256"
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

	actioncore "github.com/monitored-io/monitored/internal/action"
	"github.com/monitored-io/monitored/internal/agentupdate"
	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/security"
	"github.com/monitored-io/monitored/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type API struct {
	repository  *store.Repository
	config      config.Server
	auth        *authenticator
	events      *Broker
	logger      *slog.Logger
	sealer      *security.Sealer
	agentIssuer security.AgentCertificateIssuer
	updateCache *agentupdate.Cache
}

func New(
	repository *store.Repository,
	cfg config.Server,
	logger *slog.Logger,
	agentIssuer security.AgentCertificateIssuer,
) http.Handler {
	sealer, _ := security.NewSealer(cfg.MasterKey)
	updateSigningKey := sha256.Sum256([]byte(cfg.MasterKey + "|agent-update-downloads"))
	updateCache, cacheErr := agentupdate.NewCache(cfg.AgentUpdateCacheDir, updateSigningKey[:], cfg.AgentUpdateMaxBytes)
	if cacheErr != nil {
		logger.Error("initialize Agent update cache", "error", cacheErr)
	}
	api := &API{
		repository:  repository,
		config:      cfg,
		auth:        newAuthenticator(cfg, repository),
		events:      NewBroker(),
		logger:      logger,
		sealer:      sealer,
		agentIssuer: agentIssuer,
		updateCache: updateCache,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("POST /api/v1/auth/login", api.auth.login)
	mux.HandleFunc("POST /api/v1/auth/logout", api.auth.logout)
	mux.Handle("GET /api/v1/auth/me", api.auth.middleware(http.HandlerFunc(api.auth.me)))
	mux.Handle("POST /api/v1/auth/totp/enroll", api.auth.middleware(http.HandlerFunc(api.enrollTOTP)))
	mux.Handle("POST /api/v1/auth/totp/confirm", api.auth.middleware(http.HandlerFunc(api.confirmTOTP)))
	mux.Handle("GET /api/v1/events", api.auth.middleware(api.events))
	mux.Handle("GET /api/v1/overview", api.auth.middleware(http.HandlerFunc(api.overview)))
	mux.Handle("GET /api/v1/assets", api.auth.middleware(http.HandlerFunc(api.assets)))
	mux.Handle("GET /api/v1/sensors", api.auth.middleware(http.HandlerFunc(api.sensors)))
	mux.Handle("GET /api/v1/sensors/{id}/metrics", api.auth.middleware(http.HandlerFunc(api.metrics)))
	mux.Handle("GET /api/v1/alerts", api.auth.middleware(http.HandlerFunc(api.alerts)))
	mux.Handle("GET /api/v1/incidents", api.auth.middleware(http.HandlerFunc(api.incidents)))
	mux.Handle("GET /api/v1/discovery/runs", api.auth.middleware(http.HandlerFunc(api.discoveryRuns)))
	mux.Handle("POST /api/v1/discovery/runs", api.auth.middleware(requireRole("operator", "admin")(http.HandlerFunc(api.createDiscoveryRun))))
	mux.Handle("POST /api/v1/discovery/scans", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.createDiscoveryScan))))
	mux.Handle("GET /api/v1/llm/suggestions", api.auth.middleware(http.HandlerFunc(api.suggestions)))
	mux.Handle("POST /api/v1/llm/suggestions/generate", api.auth.middleware(requireRole("operator", "admin")(http.HandlerFunc(api.createSuggestionJob))))
	mux.Handle("GET /api/v1/llm/suggestion-jobs", api.auth.middleware(http.HandlerFunc(api.suggestionJobs)))
	mux.Handle("POST /api/v1/llm/suggestions/{id}/decision", api.auth.middleware(requireRole("operator", "admin")(http.HandlerFunc(api.reviewSuggestion))))
	mux.Handle("GET /api/v1/actions", api.auth.middleware(http.HandlerFunc(api.actions)))
	mux.Handle("POST /api/v1/actions", api.auth.middleware(requireRole("operator", "admin")(http.HandlerFunc(api.createAction))))
	mux.Handle("POST /api/v1/actions/{id}/decision", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.reviewAction))))
	mux.Handle("GET /api/v1/audit", api.auth.middleware(http.HandlerFunc(api.audit)))
	mux.Handle("GET /api/v1/connections", api.auth.middleware(http.HandlerFunc(api.connections)))
	mux.Handle("POST /api/v1/connections", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.createConnection))))
	mux.Handle("GET /api/v1/rules", api.auth.middleware(http.HandlerFunc(api.rules)))
	mux.Handle("POST /api/v1/rules", api.auth.middleware(requireRole("operator", "admin")(http.HandlerFunc(api.createRule))))
	mux.Handle("GET /api/v1/maintenance-windows", api.auth.middleware(http.HandlerFunc(api.maintenanceWindows)))
	mux.Handle("POST /api/v1/maintenance-windows", api.auth.middleware(requireRole("operator", "admin")(http.HandlerFunc(api.createMaintenanceWindow))))
	mux.Handle("GET /api/v1/notification-channels", api.auth.middleware(http.HandlerFunc(api.notificationChannels)))
	mux.Handle("POST /api/v1/notification-channels", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.createNotificationChannel))))
	mux.Handle("POST /api/v1/notification-channels/{id}/test", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.testNotificationChannel))))
	mux.Handle("GET /api/v1/notification-routes", api.auth.middleware(http.HandlerFunc(api.notificationRoutes)))
	mux.Handle("POST /api/v1/notification-routes", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.createNotificationRoute))))
	mux.Handle("GET /api/v1/diagnoses", api.auth.middleware(http.HandlerFunc(api.diagnoses)))
	mux.Handle("POST /api/v1/diagnoses", api.auth.middleware(requireRole("operator", "admin")(http.HandlerFunc(api.createDiagnosis))))
	mux.Handle("GET /api/v1/agents", api.auth.middleware(http.HandlerFunc(api.agents)))
	mux.Handle("GET /api/v1/agent-update-packages", api.auth.middleware(http.HandlerFunc(api.agentUpdatePackages)))
	mux.Handle("POST /api/v1/agent-update-packages", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.createAgentUpdatePackage))))
	mux.HandleFunc("GET /api/v1/agent-update-packages/{id}/download", api.downloadAgentUpdatePackage)
	mux.Handle("POST /api/v1/agents/{id}/updates", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.queueAgentUpdate))))
	mux.Handle("POST /api/v1/agents/enrollment-tokens", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.createAgentEnrollmentToken))))
	mux.HandleFunc("POST /api/v1/agents/enroll", api.enrollAgent)
	mux.Handle("GET /api/v1/users", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.users))))
	mux.Handle("POST /api/v1/users", api.auth.middleware(requireRole("admin")(http.HandlerFunc(api.createUser))))
	mux.Handle("GET /api/v1/capabilities", api.auth.middleware(http.HandlerFunc(api.capabilities)))
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

func (a *API) enrollTOTP(w http.ResponseWriter, r *http.Request) {
	if a.config.AuthDisabled {
		writeError(w, http.StatusConflict, "authentication_disabled", "Enable authentication before enrolling TOTP.")
		return
	}
	if a.sealer == nil {
		writeError(w, http.StatusServiceUnavailable, "master_key_invalid", "A strong Master key is required before TOTP can be enrolled.")
		return
	}
	email := strings.ToLower(actorFrom(r).Email)
	secret, err := security.NewTOTPSecret()
	if err != nil {
		a.internalError(w, "create TOTP secret", err)
		return
	}
	encrypted, err := a.sealer.Seal([]byte(secret), "totp:"+email)
	if err != nil {
		a.internalError(w, "encrypt TOTP secret", err)
		return
	}
	if err := a.repository.BeginTOTPEnrollment(r.Context(), email, encrypted); err != nil {
		a.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"secret":           secret,
		"uri":              security.TOTPURI("Monitored", email, secret),
		"expiresInSeconds": 600,
	})
}

func (a *API) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	email := strings.ToLower(actorFrom(r).Email)
	encrypted, err := a.repository.PendingTOTPSecret(r.Context(), email)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "enrollment_not_found", "No active TOTP enrollment was found.")
		return
	}
	if err != nil {
		a.internalError(w, "read TOTP enrollment", err)
		return
	}
	if a.sealer == nil {
		writeError(w, http.StatusServiceUnavailable, "master_key_invalid", "The TOTP secret cannot be opened.")
		return
	}
	secret, err := a.sealer.Open(encrypted, "totp:"+email)
	if err != nil {
		a.internalError(w, "open TOTP enrollment", err)
		return
	}
	if !security.ValidateTOTP(string(secret), payload.Code, time.Now()) {
		writeError(w, http.StatusUnauthorized, "invalid_totp", "The authenticator code is invalid.")
		return
	}
	if err := a.repository.CompleteTOTPEnrollment(r.Context(), email, encrypted); err != nil {
		a.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
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
		AgentID    string   `json:"agentId"`
		Kind       string   `json:"kind"`
		Collectors []string `json:"collectors"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(payload.AgentID) == "" {
		writeError(w, http.StatusBadRequest, "agent_required", "A registered Agent ID is required for an explicit discovery run.")
		return
	}
	run, err := a.repository.CreateDiscoveryRun(
		r.Context(), actorFrom(r).Email, payload.AgentID, payload.Kind, payload.Collectors,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "agent_not_found", "The selected Agent is not registered.")
		return
	}
	if err != nil {
		a.internalError(w, "start discovery", err)
		return
	}
	a.events.Publish(Event{Type: "discovery.progress", Data: map[string]any{
		"id": run.ID, "state": run.State, "progress": run.Progress,
	}})
	writeJSON(w, http.StatusAccepted, run)
}

func (a *API) createDiscoveryScan(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		AgentID       string              `json:"agentId"`
		CIDR          string              `json:"cidr"`
		Protocols     []string            `json:"protocols"`
		Ports         map[string][]uint16 `json:"ports"`
		ConfirmLarge  bool                `json:"confirmLarge"`
		RatePerSecond int                 `json:"ratePerSecond"`
		Concurrency   int                 `json:"concurrency"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(payload.AgentID) == "" || len(payload.Protocols) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_scan", "agentId, cidr, and at least one protocol are required.")
		return
	}
	_, network, err := net.ParseCIDR(payload.CIDR)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_scan", "cidr must be a valid IPv4 or IPv6 network.")
		return
	}
	ones, bits := network.Mask.Size()
	if bits-ones > 16 {
		writeError(w, http.StatusBadRequest, "scan_too_large", "A scan may contain at most 65,536 addresses.")
		return
	}
	if bits-ones > 8 && !payload.ConfirmLarge {
		writeError(w, http.StatusBadRequest, "confirmation_required", "Networks above 256 addresses require confirmLarge=true.")
		return
	}
	for _, protocol := range payload.Protocols {
		if !oneOf(protocol, "redfish", "ipmi", "snmp", "nut") {
			writeError(w, http.StatusBadRequest, "invalid_scan", "Only redfish, ipmi, snmp, and nut scans are supported.")
			return
		}
		if (protocol == "ipmi" || protocol == "snmp") && len(payload.Ports[protocol]) == 0 {
			writeError(w, http.StatusBadRequest, "explicit_port_required", "SNMP and IPMI scans require explicit ports because their native transports are UDP.")
			return
		}
	}
	if payload.RatePerSecond == 0 {
		payload.RatePerSecond = 32
	}
	if payload.Concurrency == 0 {
		payload.Concurrency = 16
	}
	if payload.RatePerSecond < 1 || payload.RatePerSecond > 256 ||
		payload.Concurrency < 1 || payload.Concurrency > 64 {
		writeError(w, http.StatusBadRequest, "invalid_scan_limits", "ratePerSecond must be 1..256 and concurrency must be 1..64.")
		return
	}
	run, err := a.repository.CreateDiscoveryScan(
		r.Context(), actorFrom(r).Email, payload.AgentID, payload.CIDR,
		payload.Protocols, payload.Ports, payload.ConfirmLarge,
		payload.RatePerSecond, payload.Concurrency,
	)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "agent_not_found", "The selected Agent is not registered.")
		return
	}
	if err != nil {
		a.internalError(w, "queue discovery scan", err)
		return
	}
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

func (a *API) suggestionJobs(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListSuggestionJobs(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		a.internalError(w, "list LLM suggestion jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createSuggestionJob(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SensorID string         `json:"sensorId"`
		Provider string         `json:"provider"`
		Model    string         `json:"model"`
		Context  map[string]any `json:"context"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(payload.SensorID) == "" ||
		!oneOf(payload.Provider, "openai-compatible", "ollama") ||
		strings.TrimSpace(payload.Model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_suggestion_job", "sensorId, supported provider, and model are required.")
		return
	}
	value, err := a.repository.CreateSuggestionJob(r.Context(), actorFrom(r).Email, domain.LLMSuggestionJob{
		SensorID: payload.SensorID,
		Provider: payload.Provider,
		Model:    payload.Model,
		Context:  payload.Context,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Sensor was not found.")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "suggestion_exists", "This sensor already has a queued, reviewed, or rejected suggestion.")
		return
	}
	if err != nil {
		a.internalError(w, "queue LLM suggestion", err)
		return
	}
	writeJSON(w, http.StatusAccepted, value)
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

func (a *API) createAction(w http.ResponseWriter, r *http.Request) {
	var payload domain.ActionRequest
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if payload.AgentID == "" || payload.AssetID == "" || payload.Title == "" ||
		!oneOf(payload.Risk, "low", "medium", "high", "root") {
		writeError(w, http.StatusBadRequest, "invalid_action", "Agent, asset, title, and risk are required.")
		return
	}
	executor := actioncore.Default()
	executor.AllowRoot = true
	if (payload.Identity == "root") != (payload.Risk == "root") {
		writeError(w, http.StatusBadRequest, "invalid_root_action", "Root identity and root risk must be selected together.")
		return
	}
	canonical, err := executor.Canonicalize(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsafe_action", err.Error())
		return
	}
	hash, err := executor.Hash(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_action", err.Error())
		return
	}
	payload.ImmutableHash = hash
	raw, _ := json.Marshal(canonical)
	var canonicalMap map[string]any
	_ = json.Unmarshal(raw, &canonicalMap)
	value, err := a.repository.CreateAction(r.Context(), actorFrom(r).Email, payload, canonicalMap)
	if err != nil {
		a.internalError(w, "create action request", err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) reviewAction(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Decision string `json:"decision"`
		Password string `json:"password"`
		TOTPCode string `json:"totpCode"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if payload.Decision != "approve" && payload.Decision != "reject" {
		writeError(w, http.StatusBadRequest, "invalid_decision", "Decision must be approve or reject.")
		return
	}
	if payload.Decision == "approve" {
		risk, err := a.repository.ActionRisk(r.Context(), r.PathValue("id"))
		if err != nil {
			a.writeStoreError(w, err)
			return
		}
		if risk == "root" {
			if a.config.AuthDisabled || !a.auth.reauthenticate(
				r.Context(), actorFrom(r).Email, payload.Password, payload.TOTPCode,
			) {
				writeError(w, http.StatusUnauthorized, "root_reauthentication_required", "Root approval requires the Admin password and a valid enrolled TOTP code.")
				return
			}
		}
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

func (a *API) connections(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListConnections(r.Context(), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list connections", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createConnection(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name     string         `json:"name"`
		Kind     string         `json:"kind"`
		Endpoint string         `json:"endpoint"`
		AgentID  *string        `json:"agentId"`
		TLSMode  string         `json:"tlsMode"`
		Enabled  *bool          `json:"enabled"`
		Config   map[string]any `json:"config"`
		Secret   map[string]any `json:"secret"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Endpoint) == "" ||
		!oneOf(payload.Kind, "truenas", "redfish", "ipmi", "nut", "snmp", "ssh", "http", "tcp") {
		writeError(w, http.StatusBadRequest, "invalid_connection", "Name, endpoint, and a supported connection kind are required.")
		return
	}
	if payload.TLSMode == "" {
		payload.TLSMode = "verify"
	}
	if !oneOf(payload.TLSMode, "verify", "custom_ca", "insecure") {
		writeError(w, http.StatusBadRequest, "invalid_tls_mode", "Unsupported TLS verification mode.")
		return
	}
	var encrypted []byte
	if len(payload.Secret) > 0 {
		if a.sealer == nil {
			writeError(w, http.StatusServiceUnavailable, "master_key_invalid", "A strong Master key is required before credentials can be stored.")
			return
		}
		raw, _ := json.Marshal(payload.Secret)
		var err error
		encrypted, err = a.sealer.Seal(raw, "connection:"+payload.Kind+":"+payload.Endpoint)
		if err != nil {
			a.internalError(w, "encrypt connection credentials", err)
			return
		}
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item, err := a.repository.CreateConnection(r.Context(), actorFrom(r).Email, domain.Connection{
		Name: payload.Name, Kind: payload.Kind, Endpoint: payload.Endpoint,
		AgentID: payload.AgentID, Enabled: enabled, TLSMode: payload.TLSMode,
	}, payload.Config, encrypted)
	if err != nil {
		a.internalError(w, "create connection", err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *API) rules(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListRules(r.Context(), queryInt(r, "limit", 500))
	if err != nil {
		a.internalError(w, "list rules", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createRule(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		domain.Rule
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rule := payload.Rule
	if rule.Name == "" || !oneOf(rule.RuleType, "threshold", "rate", "counter_delta", "state", "state_change", "missing") ||
		!oneOf(rule.Severity, "info", "warning", "critical") {
		writeError(w, http.StatusBadRequest, "invalid_rule", "Name, supported rule type, and severity are required.")
		return
	}
	if rule.MissingDataMode == "" {
		rule.MissingDataMode = "ignore"
	}
	if !oneOf(rule.MissingDataMode, "ignore", "alert", "resolve") {
		writeError(w, http.StatusBadRequest, "invalid_missing_data_mode", "Unsupported missing-data mode.")
		return
	}
	rule.Enabled = true
	if payload.Enabled != nil {
		rule.Enabled = *payload.Enabled
	}
	value, err := a.repository.CreateRule(r.Context(), actorFrom(r).Email, rule)
	if err != nil {
		a.internalError(w, "create rule", err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) maintenanceWindows(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListMaintenanceWindows(r.Context(), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list maintenance windows", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	var payload domain.MaintenanceWindow
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if payload.Name == "" || payload.StartsAt.IsZero() || !payload.EndsAt.After(payload.StartsAt) {
		writeError(w, http.StatusBadRequest, "invalid_maintenance_window", "Name and a valid start/end range are required.")
		return
	}
	value, err := a.repository.CreateMaintenanceWindow(r.Context(), actorFrom(r).Email, payload)
	if err != nil {
		a.internalError(w, "create maintenance window", err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) notificationChannels(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListNotificationChannels(r.Context(), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list notification channels", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name    string         `json:"name"`
		Kind    string         `json:"kind"`
		Enabled *bool          `json:"enabled"`
		Config  map[string]any `json:"config"`
		Secret  map[string]any `json:"secret"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if payload.Name == "" || !oneOf(payload.Kind, "telegram", "feishu", "webhook", "smtp", "bark") {
		writeError(w, http.StatusBadRequest, "invalid_notification_channel", "Name and a supported channel kind are required.")
		return
	}
	var encrypted []byte
	if len(payload.Secret) > 0 {
		if a.sealer == nil {
			writeError(w, http.StatusServiceUnavailable, "master_key_invalid", "A strong Master key is required before credentials can be stored.")
			return
		}
		raw, _ := json.Marshal(payload.Secret)
		var err error
		encrypted, err = a.sealer.Seal(raw, "notification:"+payload.Kind+":"+payload.Name)
		if err != nil {
			a.internalError(w, "encrypt notification credentials", err)
			return
		}
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item, err := a.repository.CreateNotificationChannel(
		r.Context(), actorFrom(r).Email,
		domain.NotificationChannel{Name: payload.Name, Kind: payload.Kind, Enabled: enabled, Config: payload.Config},
		encrypted,
	)
	if err != nil {
		a.internalError(w, "create notification channel", err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *API) testNotificationChannel(w http.ResponseWriter, r *http.Request) {
	if err := a.repository.EnqueueNotificationTest(r.Context(), actorFrom(r).Email, r.PathValue("id")); err != nil {
		a.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued"})
}

func (a *API) notificationRoutes(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListNotificationRoutes(r.Context(), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list notification routes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createNotificationRoute(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		domain.NotificationRoute
		Enabled      *bool `json:"enabled"`
		SendRecovery *bool `json:"sendRecovery"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	route := payload.NotificationRoute
	if route.ChannelID == "" || !oneOf(route.MinSeverity, "info", "warning", "critical") {
		writeError(w, http.StatusBadRequest, "invalid_notification_route", "Channel and minimum severity are required.")
		return
	}
	if route.Locale == "" {
		route.Locale = "zh-CN"
	}
	if !oneOf(route.Locale, "zh-CN", "en-US") {
		writeError(w, http.StatusBadRequest, "invalid_locale", "Locale must be zh-CN or en-US.")
		return
	}
	if route.GroupWindowSeconds == 0 {
		route.GroupWindowSeconds = 120
	}
	if route.RepeatIntervalSeconds == 0 {
		route.RepeatIntervalSeconds = 3600
	}
	if route.GroupWindowSeconds < 0 || route.GroupWindowSeconds > 3600 ||
		route.RepeatIntervalSeconds < 0 || route.RepeatIntervalSeconds > 604800 {
		writeError(w, http.StatusBadRequest, "invalid_notification_route", "Grouping and repeat intervals are outside the allowed range.")
		return
	}
	route.Enabled = true
	if payload.Enabled != nil {
		route.Enabled = *payload.Enabled
	}
	route.SendRecovery = true
	if payload.SendRecovery != nil {
		route.SendRecovery = *payload.SendRecovery
	}
	value, err := a.repository.CreateNotificationRoute(r.Context(), actorFrom(r).Email, route)
	if err != nil {
		a.internalError(w, "create notification route", err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) diagnoses(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListDiagnoses(r.Context(), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list diagnoses", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createDiagnosis(w http.ResponseWriter, r *http.Request) {
	var payload domain.Diagnosis
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !oneOf(payload.Provider, "openai-compatible", "ollama") ||
		strings.TrimSpace(payload.Model) == "" ||
		(payload.IncidentID == nil && payload.AssetID == nil && len(payload.Context) == 0) {
		writeError(w, http.StatusBadRequest, "invalid_diagnosis", "Provider, model, and incident, asset, or context evidence are required.")
		return
	}
	value, err := a.repository.CreateDiagnosis(r.Context(), actorFrom(r).Email, payload)
	if err != nil {
		a.internalError(w, "queue diagnosis", err)
		return
	}
	writeJSON(w, http.StatusAccepted, value)
}

func (a *API) agents(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListAgents(r.Context(), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list agents", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) users(w http.ResponseWriter, r *http.Request) {
	value, err := a.repository.ListUsers(r.Context(), queryInt(r, "limit", 250))
	if err != nil {
		a.internalError(w, "list users", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": value, "count": len(value)})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !strings.Contains(payload.Email, "@") || len(payload.Password) < 12 || !oneOf(payload.Role, "viewer", "operator", "admin") {
		writeError(w, http.StatusBadRequest, "invalid_user", "A valid email, 12+ character password, and supported role are required.")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), 12)
	if err != nil {
		a.internalError(w, "hash user password", err)
		return
	}
	value, err := a.repository.CreateUser(r.Context(), actorFrom(r).Email, strings.ToLower(payload.Email), payload.Role, string(hash))
	if err != nil {
		a.internalError(w, "create user", err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) capabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": []domain.CapabilityStatus{
		{ID: "agent-grpc", Label: "Agent mTLS/gRPC", State: "available", Detail: "Bidirectional registration, telemetry, heartbeat, gap and tasks"},
		{ID: "agent-update", Label: "Agent self-update", State: "available", Detail: "Master-cached signed binary downloads and immutable Docker digest pulls", Enhancement: "Activation remains constrained by each Agent's local policy"},
		{ID: "linux-hwmon", Label: "Linux hwmon/sysfs", State: "available", Detail: "Raw hwmon, PCI, block, network, thermal, power supply and RDMA"},
		{ID: "smart", Label: "SMART/NVMe/SCSI", State: "available", Detail: "Standby-safe scan-aware smartctl JSON, HBA types, temperatures, attributes and self-tests"},
		{ID: "zfs", Label: "OpenZFS", State: "available", Detail: "Pool health, capacity and fragmentation"},
		{ID: "truenas", Label: "TrueNAS SCALE", State: "available", Detail: "Optional Agent JSON-RPC system/pool/disk/temperature collector", Enhancement: "Full SMART uses the local Agent; CORE uses a documented legacy path"},
		{ID: "nvidia", Label: "NVIDIA GPU", State: "available", Detail: "NVML-backed nvidia-smi capability probing", Enhancement: "DCGM when detected"},
		{ID: "mellanox", Label: "Mellanox/RDMA", State: "available", Detail: "PCI/sysfs/RDMA baseline", Enhancement: "Read-only MFT when installed"},
		{ID: "redfish", Label: "Redfish BMC", State: "available", Detail: "Hypermedia chassis, thermal and power"},
		{ID: "nut", Label: "NUT UPS", State: "available", Detail: "Read-only upsd LIST protocol"},
		{ID: "snmp", Label: "SNMP", State: "available", Detail: "v2c/v3 authPriv and constrained YAML profiles"},
	}})
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

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
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
