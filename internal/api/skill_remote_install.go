package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"csgclaw/internal/config"
	skilllocal "csgclaw/internal/skill/local"
	skillremote "csgclaw/internal/skill/remote"
)

type skillInstallRequest struct {
	RemotePath string `json:"remote_path"`
	Ref        string `json:"ref,omitempty"`
}

func (h *Handler) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req skillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	remotePath, ref, err := skillremote.NormalizeAgenticHubSkillRequest(req.RemotePath, req.Ref)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	root, err := skilllocal.SkillsRoot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg, _, err := h.loadBootstrapConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	baseURL := normalizeRemoteSource(config.UserSettingsFromConfig(cfg).HubOfficialURL)
	if baseURL == "" {
		http.Error(w, "official Hub URL is not configured", http.StatusBadRequest)
		return
	}
	archive, err := skillremote.FetchAgenticHubSkillArchive(r.Context(), baseURL, remotePath, ref)
	if err != nil {
		if skillremote.IsInvalidAgenticHubRequest(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	item, err := skilllocal.InstallArchive(root, skillremote.AgenticHubSkillArchiveName(remotePath), archive)
	if err != nil {
		writeSkillInstallError(w, err)
		return
	}
	item.RemoteSource = baseURL
	item.RemotePath = remotePath
	if err := skilllocal.WriteRemoteMetadata(root, item.Name, skilllocal.RemoteMetadata{
		RemoteSource: baseURL,
		RemotePath:   remotePath,
	}); err != nil {
		_ = skilllocal.Delete(root, item.Name)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func normalizeRemoteSource(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/")
}

func writeSkillInstallError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, skilllocal.ErrSkillAlreadyExists):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, skilllocal.ErrSkillArchiveEmpty),
		errors.Is(err, skilllocal.ErrSkillArchiveUnsafe),
		errors.Is(err, skilllocal.ErrSkillArchiveInvalid),
		errors.Is(err, skilllocal.ErrSKILLMDMissing):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
