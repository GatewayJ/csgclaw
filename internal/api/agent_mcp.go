package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"csgclaw/internal/agent"
	agentruntime "csgclaw/internal/runtime"
)

type addAgentMCPServersRequest struct {
	Names []string `json:"names"`
}

func (h *Handler) handleAgentMCPByID(w http.ResponseWriter, r *http.Request) {
	h.handleAgentMCP(w, r, pathValue(r, "id"))
}

func (h *Handler) handleAgentMCPServersByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.svc == nil {
		http.Error(w, "agent service is not configured", http.StatusServiceUnavailable)
		return
	}
	if h.hub == nil {
		http.Error(w, "hub service is not configured", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(pathValue(r, "id"))
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var req addAgentMCPServersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	names := normalizeAgentMCPServerNames(req.Names)
	if len(names) == 0 {
		http.Error(w, "names is required", http.StatusBadRequest)
		return
	}

	current, ok := h.svc.Agent(id)
	if !ok {
		http.Error(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}
	hubServers, err := h.hub.ListMCPServers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	currentServers, err := agentruntime.MCPConfigServers(current.MCPConfig)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if currentServers == nil {
		currentServers = map[string]any{}
	}
	for _, name := range names {
		rawServer, ok := hubServers[name]
		if !ok {
			http.Error(w, fmt.Sprintf("hub mcp server %q not found", name), http.StatusNotFound)
			return
		}
		serverConfig, err := runtimeMCPServerConfigFromHub(name, rawServer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		currentServers[name] = serverConfig
	}
	nextConfig := map[string]any{agentruntime.MCPConfigServersKey: currentServers}
	updated, err := h.svc.Update(r.Context(), id, agent.UpdateRequest{
		MCPConfig:    &nextConfig,
		MCPConfigSet: true,
		FieldMask:    []string{"mcp_config"},
	})
	if err != nil {
		writeAgentMCPMutationError(w, err)
		return
	}
	h.publishUpdatedAgentUser(updated)
	view, err := h.svc.MCPConfigView(r.Context(), updated.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, view)
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

func normalizeAgentMCPServerNames(values []string) []string {
	names := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func runtimeMCPServerConfigFromHub(name string, raw any) (map[string]any, error) {
	rawConfig, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hub mcp server %q config must be an object", name)
	}
	normalized, err := agentruntime.NormalizeMCPConfig(map[string]any{
		agentruntime.MCPConfigServersKey: map[string]any{name: rawConfig},
	})
	if err != nil {
		return nil, err
	}
	servers, err := agentruntime.MCPConfigServers(normalized)
	if err != nil {
		return nil, err
	}
	serverConfig, ok := servers[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hub mcp server %q config must be an object", name)
	}
	delete(serverConfig, "description")
	return serverConfig, nil
}

func writeAgentMCPMutationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
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
