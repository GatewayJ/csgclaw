package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"csgclaw/internal/agent"
)

type batchAddAgentMCPServersRequest struct {
	Names []string `json:"names"`
}

func (h *Handler) handleAgentMCPServersByID(w http.ResponseWriter, r *http.Request) {
	h.handleAgentMCPServers(w, r, pathValue(r, "id"))
}

func (h *Handler) handleBatchAddAgentMCPServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.svc == nil {
		http.Error(w, "agent service is not configured", http.StatusServiceUnavailable)
		return
	}
	if h.mcp == nil {
		http.Error(w, "mcp service is not configured", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(pathValue(r, "id"))
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var req batchAddAgentMCPServersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	hasName := false
	for _, name := range req.Names {
		if strings.TrimSpace(name) != "" {
			hasName = true
			break
		}
	}
	if !hasName {
		http.Error(w, "names is required", http.StatusBadRequest)
		return
	}
	servers, err := h.mcp.ListServers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	updated, err := h.svc.AddMCPServersFromHub(r.Context(), id, req.Names, servers)
	if err != nil {
		writeAgentMCPServersMutationError(w, err)
		return
	}
	h.publishUpdatedAgentUser(updated)
	view, err := h.svc.MCPServersView(r.Context(), updated.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleAgentMCPServers(w http.ResponseWriter, r *http.Request, id string) {
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
		view, err := h.svc.MCPServersView(r.Context(), id)
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
		cfg, set, err := decodeAgentMCPServersRequest(r)
		if err != nil {
			http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
			return
		}
		if !set {
			http.Error(w, "mcpServers is required", http.StatusBadRequest)
			return
		}
		updated, err := h.svc.Update(r.Context(), id, agent.UpdateRequest{
			MCPServers:    cfg,
			MCPServersSet: true,
			FieldMask:     []string{"mcpServers"},
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
		view, err := h.svc.MCPServersView(r.Context(), updated.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, view)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeAgentMCPServersMutationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "not found") {
		status = http.StatusNotFound
	} else if strings.Contains(message, "mcp server") && strings.Contains(message, "config") {
		status = http.StatusBadGateway
	}
	http.Error(w, err.Error(), status)
}

func decodeAgentMCPServersRequest(r *http.Request) (*map[string]any, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, false, err
	}
	payload, ok := raw["mcpServers"]
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
