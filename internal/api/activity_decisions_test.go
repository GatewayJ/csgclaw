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

type fakeActivityDecider struct {
	snapshot activity.ActivitySnapshot
	err      error
	gotChan  string
	gotID    string
	gotOpt   string
}

func (d *fakeActivityDecider) Decide(_ context.Context, req activity.ActivityDecisionRequest) (activity.ActivitySnapshot, error) {
	d.gotChan = req.Channel
	d.gotID = req.ActivityID
	d.gotOpt = req.OptionID
	return d.snapshot, d.err
}

func TestChannelActivityDecisionEndpoint(t *testing.T) {
	t.Parallel()

	decider := &fakeActivityDecider{
		snapshot: activity.ActivitySnapshot{
			ID:     "perm-1",
			Status: activity.ActionStatusAllowed,
			Decision: &activity.ActionDecisionSnapshot{
				OptionID: "once",
				Kind:     "allow_once",
			},
		},
	}
	h := &Handler{}
	h.SetActivityDecider(decider)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/csgclaw/activities/perm-1:decide", strings.NewReader(`{"option_id":"once"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decider.gotChan != "csgclaw" || decider.gotID != "perm-1" || decider.gotOpt != "once" {
		t.Fatalf("decider got channel=%q id=%q option=%q", decider.gotChan, decider.gotID, decider.gotOpt)
	}
	var got activity.ActivitySnapshot
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != activity.ActionStatusAllowed {
		t.Fatalf("status = %s, want allowed", got.Status)
	}
}

func TestChannelActivityDecisionEndpointConflictReturnsSnapshot(t *testing.T) {
	t.Parallel()

	h := &Handler{}
	h.SetActivityDecider(&fakeActivityDecider{
		snapshot: activity.ActivitySnapshot{ID: "perm-1", Status: activity.ActionStatusRejected},
		err:      activity.ErrActionAlreadyDecided,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/csgclaw/activities/perm-1:decide", strings.NewReader(`{"option_id":"reject"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"rejected"`) {
		t.Fatalf("body = %s, want snapshot", rec.Body.String())
	}
}

func TestChannelActivityDecisionEndpointErrorMapping(t *testing.T) {
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
			h.SetActivityDecider(&fakeActivityDecider{
				snapshot: activity.ActivitySnapshot{ID: "perm-1", Status: activity.ActionStatusExpired},
				err:      tc.err,
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/csgclaw/activities/perm-1:decide", strings.NewReader(`{"option_id":"once"}`))
			h.Routes().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
