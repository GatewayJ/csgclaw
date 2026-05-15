package api

import "net/http"

func (h *Handler) handleCsgclawRoomByID(w http.ResponseWriter, r *http.Request) {
	id, membersPath := parseCsgclawRoomPath(r.URL.Path)
	if id == "" {
		http.NotFound(w, r)
		return
	}
	h.handleLocalRoomByID(w, r, id, membersPath)
}

func (h *Handler) handleCsgclawUserByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCsgclawUserPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.handleLocalUserByID(w, r, id)
}

func parseCsgclawRoomPath(path string) (string, bool) {
	return parseRoomPath(path, "/api/v1/channels/csgclaw/rooms/")
}

func parseCsgclawUserPath(path string) (string, bool) {
	return parseUserPath(path, "/api/v1/channels/csgclaw/users/")
}
