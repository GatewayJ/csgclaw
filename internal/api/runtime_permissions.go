package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	runtimeactivity "csgclaw/internal/runtime/activity"

	"github.com/go-chi/chi/v5"
)

type runtimePermissionDecisionRequest struct {
	OptionID string `json:"option_id"`
}

type RuntimePermissionDecider = runtimeactivity.PermissionDecider

// Deprecated compatibility route for older Codex permission clients.
func (h *Handler) handleCodexPermissionDecision(w http.ResponseWriter, r *http.Request) {
	h.handleRuntimePermissionDecision(w, r)
}

func (h *Handler) handleRuntimePermissionDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.runtimePermissions == nil {
		http.Error(w, "runtime permissions are not configured", http.StatusServiceUnavailable)
		return
	}

	requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
	if requestID == "" {
		http.Error(w, "request id is required", http.StatusBadRequest)
		return
	}

	var req runtimePermissionDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	snapshot, err := h.runtimePermissions.Decide(r.Context(), requestID, strings.TrimSpace(req.OptionID))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, snapshot)
	case errors.Is(err, runtimeactivity.ErrPermissionInvalidOption):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, runtimeactivity.ErrPermissionNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, runtimeactivity.ErrPermissionAlreadyDecided):
		writeJSON(w, http.StatusConflict, snapshot)
	case errors.Is(err, runtimeactivity.ErrPermissionGone):
		writeJSON(w, http.StatusGone, snapshot)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
