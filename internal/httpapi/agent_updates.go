package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/agentupdate"
	"github.com/monitored-io/monitored/internal/store"
)

func (a *API) agentUpdatePackages(w http.ResponseWriter, _ *http.Request) {
	if a.updateCache == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_update_cache_unavailable", "The Agent update cache is unavailable.")
		return
	}
	values, err := a.updateCache.List()
	if err != nil {
		a.internalError(w, "list Agent update packages", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values, "count": len(values)})
}

func (a *API) createAgentUpdatePackage(w http.ResponseWriter, r *http.Request) {
	if a.updateCache == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_update_cache_unavailable", "The Agent update cache is unavailable.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.config.AgentUpdateMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_update_package", "The update package form is invalid or too large.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	value := agentupdate.Package{
		Version:      strings.TrimSpace(r.FormValue("version")),
		OS:           strings.ToLower(strings.TrimSpace(r.FormValue("os"))),
		Architecture: strings.ToLower(strings.TrimSpace(r.FormValue("architecture"))),
		Strategy:     strings.ToLower(strings.TrimSpace(r.FormValue("strategy"))),
		Image:        strings.TrimSpace(r.FormValue("image")),
		ImageDigest:  strings.TrimSpace(r.FormValue("imageDigest")),
		CreatedBy:    actorFrom(r).Email,
	}
	var (
		created agentupdate.Package
		err     error
	)
	switch value.Strategy {
	case "binary":
		file, header, fileErr := r.FormFile("package")
		if fileErr != nil {
			writeError(w, http.StatusBadRequest, "package_required", "A binary Agent package is required.")
			return
		}
		defer file.Close()
		value.FileName = header.Filename
		value.ContentType = header.Header.Get("Content-Type")
		created, err = a.updateCache.PutBinary(r.Context(), value, file)
	case "docker":
		created, err = a.updateCache.PutDocker(value)
	default:
		writeError(w, http.StatusBadRequest, "invalid_update_strategy", "Update strategy must be binary or docker.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_update_package", err.Error())
		return
	}
	_ = a.repository.AppendAudit(r.Context(), actorFrom(r).Email, "agent.update_package_cached", "agent_update_package", created.ID, remoteAddress(r), map[string]any{
		"version": created.Version, "strategy": created.Strategy, "os": created.OS,
		"architecture": created.Architecture, "sha256": created.SHA256, "sizeBytes": created.SizeBytes,
		"image": created.Image, "imageDigest": created.ImageDigest,
	})
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) downloadAgentUpdatePackage(w http.ResponseWriter, r *http.Request) {
	if a.updateCache == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_update_cache_unavailable", "The Agent update cache is unavailable.")
		return
	}
	expiresUnix, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_download_token", "The Agent update download token is invalid.")
		return
	}
	id := r.PathValue("id")
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	if !a.updateCache.VerifyDownload(id, expiresAt, r.URL.Query().Get("token"), time.Now()) {
		writeError(w, http.StatusUnauthorized, "invalid_download_token", "The Agent update download token is invalid or expired.")
		return
	}
	value, file, err := a.updateCache.Open(id)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "update_package_not_found", "The Agent update package does not exist.")
		return
	}
	if err != nil {
		a.internalError(w, "open Agent update package", err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", contentType(value.ContentType))
	w.Header().Set("Content-Length", strconv.FormatInt(value.SizeBytes, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, value.FileName))
	w.Header().Set("X-Checksum-SHA256", value.SHA256)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, value.FileName, value.CreatedAt, file)
}

func (a *API) queueAgentUpdate(w http.ResponseWriter, r *http.Request) {
	if a.updateCache == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_update_cache_unavailable", "The Agent update cache is unavailable.")
		return
	}
	var payload struct {
		PackageID string `json:"packageId"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	update, err := a.updateCache.Get(strings.TrimSpace(payload.PackageID))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "update_package_not_found", "The Agent update package does not exist.")
		return
	}
	if err != nil {
		a.internalError(w, "read Agent update package", err)
		return
	}
	downloadURL := ""
	if update.Strategy == "binary" {
		expiresAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
		downloadURL = signedDownloadURL(a.config.PublicURL, update.ID, expiresAt, a.updateCache.SignDownload(update.ID, expiresAt))
	}
	task, err := a.repository.QueueAgentUpdate(r.Context(), actorFrom(r).Email, r.PathValue("id"), update, downloadURL)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "agent_not_found", "The selected Agent is not registered.")
		return
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "agent_update_conflict", "The package does not match the Agent, the Agent is revoked, or another update is active.")
		return
	case err != nil:
		a.internalError(w, "queue Agent update", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"taskId": task.ID, "agentId": task.AgentID, "state": task.State,
		"packageId": update.ID, "version": update.Version, "strategy": update.Strategy,
	})
}

func signedDownloadURL(publicURL, packageID string, expiresAt time.Time, token string) string {
	base := strings.TrimRight(publicURL, "/") + "/api/v1/agent-update-packages/" + url.PathEscape(packageID) + "/download"
	query := url.Values{}
	query.Set("expires", strconv.FormatInt(expiresAt.Unix(), 10))
	query.Set("token", token)
	return base + "?" + query.Encode()
}

func contentType(value string) string {
	if value == "" {
		return "application/octet-stream"
	}
	return value
}
