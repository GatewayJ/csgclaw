package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"csgclaw/internal/agent"
)

func (h *Handler) handleAgentMCPByID(w http.ResponseWriter, r *http.Request) {
	h.handleAgentMCP(w, r, pathValue(r, "id"))
}

func (h *Handler) handleAgentMCP(w http.ResponseWriter, r *http.Request, id string) {
	if h.svc == nil {
		http.Error(w, "agent service is not configured", http.StatusServiceUnavailable)
		return
	}
	id = strings.TrimSpace(id)
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		view, err := h.svc.MCPConfigView(r.Context(), id)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, http.StatusOK, view)
	case http.MethodPut:
		cfg, set, err := decodeAgentMCPConfigRequest(r)
		if err != nil {
			http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
			return
		}
		if !set {
			http.Error(w, "mcp_config is required", http.StatusBadRequest)
			return
		}
		updated, err := h.svc.Update(r.Context(), id, agent.UpdateRequest{
			MCPConfig:    cfg,
			MCPConfigSet: true,
			FieldMask:    []string{"mcp_config"},
		})
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		h.publishUpdatedAgentUser(updated)
		view, err := h.svc.MCPConfigView(r.Context(), updated.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, view)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func decodeAgentMCPConfigRequest(r *http.Request) (*map[string]any, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, false, err
	}
	payload, ok := raw["mcp_config"]
	if !ok {
		return nil, false, nil
	}
	if string(payload) == "null" {
		return nil, true, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return nil, false, err
	}
	return &cfg, true, nil
}
