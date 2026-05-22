package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"csgclaw/internal/activity"
)

type fakeActionDecider struct {
	snapshot activity.ActionRequestSnapshot
	err      error
	gotBot   string
	gotID    string
	gotOpt   string
}

func (d *fakeActionDecider) Decide(_ context.Context, botID string, actionID string, optionID string) (activity.ActionRequestSnapshot, error) {
	d.gotBot = botID
	d.gotID = actionID
	d.gotOpt = optionID
	return d.snapshot, d.err
}

func TestBotActionDecisionEndpoint(t *testing.T) {
	t.Parallel()

	decider := &fakeActionDecider{
		snapshot: activity.ActionRequestSnapshot{
			ID:     "perm-1",
			Status: activity.ActionStatusAllowed,
			Decision: &activity.ActionDecisionSnapshot{
				OptionID: "once",
				Kind:     "allow_once",
			},
		},
	}
	h := &Handler{}
	h.SetBotActionDecider(decider)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/u-codex/actions/perm-1/decision", strings.NewReader(`{"option_id":"once"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decider.gotBot != "u-codex" || decider.gotID != "perm-1" || decider.gotOpt != "once" {
		t.Fatalf("decider got bot=%q id=%q option=%q", decider.gotBot, decider.gotID, decider.gotOpt)
	}
	var got activity.ActionRequestSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != activity.ActionStatusAllowed {
		t.Fatalf("status = %s, want allowed", got.Status)
	}
}

func TestBotActionDecisionEndpointConflictReturnsSnapshot(t *testing.T) {
	t.Parallel()

	h := &Handler{}
	h.SetBotActionDecider(&fakeActionDecider{
		snapshot: activity.ActionRequestSnapshot{ID: "perm-1", Status: activity.ActionStatusRejected},
		err:      activity.ErrActionAlreadyDecided,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/u-codex/actions/perm-1/decision", strings.NewReader(`{"option_id":"reject"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"rejected"`) {
		t.Fatalf("body = %s, want snapshot", rec.Body.String())
	}
}

func TestBotActionDecisionEndpointErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid option", err: activity.ErrActionInvalidOption, want: http.StatusBadRequest},
		{name: "missing", err: activity.ErrActionNotFound, want: http.StatusNotFound},
		{name: "gone", err: activity.ErrActionGone, want: http.StatusGone},
		{name: "unexpected", err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := &Handler{}
			h.SetBotActionDecider(&fakeActionDecider{
				snapshot: activity.ActionRequestSnapshot{ID: "perm-1", Status: activity.ActionStatusExpired},
				err:      tc.err,
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/u-codex/actions/perm-1/decision", strings.NewReader(`{"option_id":"once"}`))
			h.Routes().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
