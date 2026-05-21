package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runtimecodex "csgclaw/internal/runtime/codex"
)

type fakePermissionDecider struct {
	snapshot runtimecodex.PermissionSnapshot
	err      error
	gotID    string
	gotOpt   string
}

func (d *fakePermissionDecider) Decide(_ context.Context, requestID string, optionID string) (runtimecodex.PermissionSnapshot, error) {
	d.gotID = requestID
	d.gotOpt = optionID
	return d.snapshot, d.err
}

func TestCodexPermissionDecisionEndpoint(t *testing.T) {
	t.Parallel()

	decider := &fakePermissionDecider{
		snapshot: runtimecodex.PermissionSnapshot{
			ID:     "perm-1",
			Status: runtimecodex.PermissionStatusAllowed,
			Decision: &runtimecodex.PermissionDecisionSnapshot{
				OptionID: "once",
				Kind:     "allow_once",
			},
		},
	}
	h := &Handler{}
	h.SetCodexPermissionDecider(decider)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/codex/permissions/perm-1/decision", strings.NewReader(`{"option_id":"once"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decider.gotID != "perm-1" || decider.gotOpt != "once" {
		t.Fatalf("decider got id=%q option=%q", decider.gotID, decider.gotOpt)
	}
	var got runtimecodex.PermissionSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != runtimecodex.PermissionStatusAllowed {
		t.Fatalf("status = %s, want allowed", got.Status)
	}
}

func TestCodexPermissionDecisionEndpointConflictReturnsSnapshot(t *testing.T) {
	t.Parallel()

	h := &Handler{}
	h.SetCodexPermissionDecider(&fakePermissionDecider{
		snapshot: runtimecodex.PermissionSnapshot{ID: "perm-1", Status: runtimecodex.PermissionStatusRejected},
		err:      runtimecodex.ErrPermissionAlreadyDecided,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/codex/permissions/perm-1/decision", strings.NewReader(`{"option_id":"reject"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"rejected"`) {
		t.Fatalf("body = %s, want snapshot", rec.Body.String())
	}
}

func TestCodexPermissionDecisionEndpointErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid option", err: runtimecodex.ErrPermissionInvalidOption, want: http.StatusBadRequest},
		{name: "missing", err: runtimecodex.ErrPermissionNotFound, want: http.StatusNotFound},
		{name: "gone", err: runtimecodex.ErrPermissionGone, want: http.StatusGone},
		{name: "unexpected", err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := &Handler{}
			h.SetCodexPermissionDecider(&fakePermissionDecider{
				snapshot: runtimecodex.PermissionSnapshot{ID: "perm-1", Status: runtimecodex.PermissionStatusExpired},
				err:      tc.err,
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/codex/permissions/perm-1/decision", strings.NewReader(`{"option_id":"once"}`))
			h.Routes().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
