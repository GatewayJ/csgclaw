package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"csgclaw/internal/auth"
	"csgclaw/internal/feedback"
	"csgclaw/internal/upgrade"
	"csgclaw/internal/version"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *Handler) handleFeedback(w http.ResponseWriter, r *http.Request) {
	record, found, err := auth.Default().Store.Load()
	status := record.Status()
	if err != nil || !found || !status.Authenticated {
		writeCodedAPIError(w, http.StatusUnauthorized, "feedback_login_required", "OpenCSG sign-in is required")
		return
	}
	environment := openCSGEnvironmentFromStatus(status)
	client := feedback.Client{BaseURL: environment.CSGHubBaseURL, Token: record.Tokens.AccessToken}
	var result feedback.Result
	if r.Method == http.MethodGet {
		id, parseErr := uuid.Parse(chi.URLParam(r, "submission_id"))
		if parseErr != nil {
			writeCodedAPIError(w, http.StatusBadRequest, "feedback_invalid", "Invalid submission ID")
			return
		}
		result, err = client.Status(r.Context(), id.String())
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, feedback.MaxRequestBytes)
		if parseErr := r.ParseMultipartForm(2 << 20); parseErr != nil {
			writeCodedAPIError(w, http.StatusBadRequest, "feedback_invalid", "Invalid feedback upload")
			return
		}
		defer r.MultipartForm.RemoveAll()
		id, parseErr := uuid.Parse(r.FormValue("submission_id"))
		if parseErr != nil {
			writeCodedAPIError(w, http.StatusBadRequest, "feedback_invalid", "Invalid submission ID")
			return
		}
		description := r.FormValue("problem_description")
		files := r.MultipartForm.File["images"]
		if parseErr := feedback.ValidateContent(description, len(files)); parseErr != nil {
			writeCodedAPIError(w, http.StatusBadRequest, "feedback_invalid", parseErr.Error())
			return
		}
		images := make([]feedback.Image, 0, len(files))
		for _, file := range files {
			source, openErr := file.Open()
			if openErr != nil {
				writeCodedAPIError(w, http.StatusBadRequest, "feedback_invalid", "Unable to read image")
				return
			}
			data, readErr := io.ReadAll(io.LimitReader(source, feedback.MaxImageBytes+1))
			source.Close()
			image, parseErr := feedback.ParseImage(data)
			if readErr != nil || parseErr != nil {
				writeCodedAPIError(w, http.StatusBadRequest, "feedback_invalid", "Invalid image format or size")
				return
			}
			images = append(images, image)
		}
		currentVersion := version.Current()
		result, err = client.Submit(r.Context(), feedback.Submission{ID: id.String(), Description: description,
			SiteURL: environment.OpenCSGBaseURL, Version: currentVersion, Channel: string(upgrade.InferChannelFromVersion(currentVersion)), Images: images})
	}
	if err != nil {
		slog.WarnContext(r.Context(), "feedback request failed", "error", err)
		statusCode := http.StatusBadGateway
		code := "feedback_unavailable"
		var upstream *feedback.HTTPError
		if errors.As(err, &upstream) {
			switch upstream.Status {
			case 401, 403:
				statusCode = http.StatusUnauthorized
				code = "feedback_login_required"
			case 400, 413, 422:
				statusCode = http.StatusBadRequest
				code = "feedback_invalid"
			case 409:
				statusCode = http.StatusConflict
				code = "feedback_conflict"
			}
		}
		writeCodedAPIError(w, statusCode, code, "Unable to complete feedback request")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
