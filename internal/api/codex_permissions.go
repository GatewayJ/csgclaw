package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	runtimecodex "csgclaw/internal/runtime/codex"

	"github.com/go-chi/chi/v5"
)

type codexPermissionDecisionRequest struct {
	OptionID string `json:"option_id"`
}

type CodexPermissionDecider interface {
	Decide(ctx context.Context, requestID string, optionID string) (runtimecodex.PermissionSnapshot, error)
}

func (h *Handler) handleCodexPermissionDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.codexPermissions == nil {
		http.Error(w, "codex permissions are not configured", http.StatusServiceUnavailable)
		return
	}

	requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
	if requestID == "" {
		http.Error(w, "request id is required", http.StatusBadRequest)
		return
	}

	var req codexPermissionDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	snapshot, err := h.codexPermissions.Decide(r.Context(), requestID, strings.TrimSpace(req.OptionID))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, snapshot)
	case errors.Is(err, runtimecodex.ErrPermissionInvalidOption):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, runtimecodex.ErrPermissionNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, runtimecodex.ErrPermissionAlreadyDecided):
		writeJSON(w, http.StatusConflict, snapshot)
	case errors.Is(err, runtimecodex.ErrPermissionGone):
		writeJSON(w, http.StatusGone, snapshot)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
