package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"csgclaw/internal/template"
)

type hubMCPServerRequest struct {
	Config map[string]any `json:"config"`
	Name   string         `json:"name"`
}

func (h *Handler) handleHubMCPServers(w http.ResponseWriter, r *http.Request) {
	if h.hub == nil {
		http.Error(w, "hub service is not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		servers, err := h.hub.ListMCPServers(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mcpServers": servers})
	case http.MethodPost:
		req, err := decodeHubMCPServerRequest(r)
		if err != nil {
			http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
			return
		}
		state, err := h.hub.CreateMCPServer(r.Context(), req.Name, req.Config)
		if err != nil {
			writeHubMCPError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, state)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleHubMCPServerByName(w http.ResponseWriter, r *http.Request) {
	if h.hub == nil {
		http.Error(w, "hub service is not configured", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimSpace(pathValue(r, "name"))
	if name == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		req, err := decodeHubMCPServerRequest(r)
		if err != nil {
			http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
			return
		}
		nextName := strings.TrimSpace(req.Name)
		if nextName == "" {
			nextName = name
		}
		state, err := h.hub.UpdateMCPServer(r.Context(), name, nextName, req.Config)
		if err != nil {
			writeHubMCPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	case http.MethodDelete:
		state, err := h.hub.DeleteMCPServer(r.Context(), name)
		if err != nil {
			writeHubMCPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func decodeHubMCPServerRequest(r *http.Request) (hubMCPServerRequest, error) {
	var req hubMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Config == nil {
		return req, fmt.Errorf("config is required")
	}
	return req, nil
}

func writeHubMCPError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, template.ErrHubMCPServerExists) {
		status = http.StatusConflict
	}
	if errors.Is(err, template.ErrHubMCPServerNotFound) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
