package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"csgclaw/internal/activity"

	"github.com/go-chi/chi/v5"
)

type botActionDecisionRequest struct {
	OptionID string `json:"option_id"`
}

type BotActionDecider = activity.ActionDecider

func (h *Handler) handleBotActionDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.botActions == nil {
		http.Error(w, "bot actions are not configured", http.StatusServiceUnavailable)
		return
	}

	botID := strings.TrimSpace(chi.URLParam(r, "bot_id"))
	if botID == "" {
		http.Error(w, "bot id is required", http.StatusBadRequest)
		return
	}
	actionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if actionID == "" {
		http.Error(w, "action id is required", http.StatusBadRequest)
		return
	}

	var req botActionDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	snapshot, err := h.botActions.Decide(r.Context(), botID, actionID, strings.TrimSpace(req.OptionID))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, snapshot)
	case errors.Is(err, activity.ErrActionInvalidOption):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, activity.ErrActionNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, activity.ErrActionAlreadyDecided):
		writeJSON(w, http.StatusConflict, snapshot)
	case errors.Is(err, activity.ErrActionGone):
		writeJSON(w, http.StatusGone, snapshot)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
