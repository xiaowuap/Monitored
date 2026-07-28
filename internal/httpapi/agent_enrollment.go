package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/security"
	"github.com/monitored-io/monitored/internal/store"
)

func (a *API) createAgentEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	if a.agentIssuer == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_ca_unavailable", "Automatic Agent certificate issuance is not configured.")
		return
	}
	var payload struct {
		AgentID string `json:"agentId"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	payload.AgentID = strings.TrimSpace(payload.AgentID)
	if !security.ValidAgentID(payload.AgentID) {
		writeError(w, http.StatusBadRequest, "invalid_agent_id", "Agent ID must be 1-128 letters, digits, dots, underscores, or hyphens.")
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		a.internalError(w, "generate Agent enrollment token", err)
		return
	}
	token := "menr_" + base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	expiresAt := time.Now().UTC().Add(a.config.AgentEnrollmentTTL)
	if err := a.repository.CreateAgentEnrollmentToken(
		r.Context(),
		actorFrom(r).Email,
		payload.AgentID,
		tokenHash[:],
		expiresAt,
	); err != nil {
		a.internalError(w, "store Agent enrollment token", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"agentId":   payload.AgentID,
		"token":     token,
		"expiresAt": expiresAt,
		"environment": map[string]string{
			"MONITORED_AGENT_PROTOCOL":         "grpc",
			"MONITORED_SERVER_URL":             a.config.PublicURL,
			"MONITORED_GRPC_TARGET":            a.config.GRPCPublicTarget,
			"MONITORED_AGENT_NAME":             payload.AgentID,
			"MONITORED_AGENT_ENROLLMENT_TOKEN": token,
		},
	})
}

func (a *API) enrollAgent(w http.ResponseWriter, r *http.Request) {
	if a.agentIssuer == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_ca_unavailable", "Automatic Agent certificate issuance is not configured.")
		return
	}
	if !a.secureEnrollmentEndpoint() {
		writeError(w, http.StatusServiceUnavailable, "https_required", "Agent enrollment requires an HTTPS public URL outside local development.")
		return
	}
	var payload struct {
		AgentID string `json:"agentId"`
		Token   string `json:"token"`
		CSRPEM  string `json:"csrPem"`
	}
	if err := decodeJSONWithLimit(r, &payload, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	payload.AgentID = strings.TrimSpace(payload.AgentID)
	if !security.ValidAgentID(payload.AgentID) || !strings.HasPrefix(payload.Token, "menr_") {
		writeError(w, http.StatusUnauthorized, "invalid_enrollment", "The enrollment token or Agent identity is invalid.")
		return
	}
	tokenHash := sha256.Sum256([]byte(payload.Token))
	if err := a.repository.ValidateAgentEnrollmentToken(r.Context(), payload.AgentID, tokenHash[:]); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid_enrollment", "The enrollment token is invalid, expired, or already used.")
			return
		}
		a.internalError(w, "validate Agent enrollment token", err)
		return
	}
	certificate, err := a.agentIssuer.IssueAgentCertificate(payload.AgentID, []byte(payload.CSRPEM), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_csr", err.Error())
		return
	}
	if err := a.repository.ConsumeAgentEnrollmentToken(
		r.Context(),
		payload.AgentID,
		tokenHash[:],
		remoteAddress(r),
	); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid_enrollment", "The enrollment token is invalid, expired, or already used.")
			return
		}
		a.internalError(w, "consume Agent enrollment token", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"agentId":        payload.AgentID,
		"certificatePem": string(certificate.CertificatePEM),
		"caPem":          string(certificate.CAPEM),
		"serial":         certificate.Serial,
		"notAfter":       certificate.NotAfter,
	})
}

func (a *API) secureEnrollmentEndpoint() bool {
	publicURL, err := url.Parse(a.config.PublicURL)
	if err != nil {
		return false
	}
	return publicURL.Scheme == "https" || isLoopbackHost(publicURL.Hostname())
}
