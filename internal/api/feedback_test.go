package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"csgclaw/internal/auth"
)

func TestFeedbackUsesAuthenticatedSiteAndServerVersion(t *testing.T) {
	var called bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/v1/csgbot/user-feedback" {
			t.Error(r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing user token")
		}
		var payload struct {
			CSGClaw struct {
				SiteURL string `json:"site_url"`
				Version string `json:"version"`
			} `json:"csgclaw"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.CSGClaw.SiteURL == "https://attacker.example" || payload.CSGClaw.Version == "fake" {
			t.Error("trusted caller metadata")
		}
		io.WriteString(w, `{"success":true,"issue_id":5}`)
	}))
	defer upstream.Close()
	previousStore := auth.Default().Store
	t.Cleanup(func() { auth.Default().Store = previousStore })
	auth.Default().Store = auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := auth.Default().Store.Save(auth.Record{Tokens: auth.Tokens{AccessToken: "test-token"}, Account: auth.Account{BaseURL: upstream.URL}}); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"problem_description": "Test", "site_url": "https://attacker.example", "version": "fake"} {
		writer.WriteField(key, value)
	}
	writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	request.Header.Set("Authorization", "Bearer server-token")
	(&Handler{serverAccessToken: "server-token"}).Routes().ServeHTTP(recorder, request)
	if recorder.Code != 200 || !called {
		t.Fatalf("status=%d body=%s called=%v", recorder.Code, recorder.Body, called)
	}
}

func TestFeedbackRequiresLogin(t *testing.T) {
	previousStore := auth.Default().Store
	t.Cleanup(func() { auth.Default().Store = previousStore })
	auth.Default().Store = auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", nil)
	request.Header.Set("Authorization", "Bearer server-token")
	(&Handler{serverAccessToken: "server-token"}).Routes().ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal(response.Code)
	}
}

func TestFeedbackValidatesServerCredential(t *testing.T) {
	previousStore := auth.Default().Store
	t.Cleanup(func() { auth.Default().Store = previousStore })
	auth.Default().Store = auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer account-token" {
			t.Error("upstream must receive the account credential")
		}
		io.WriteString(w, `{"success":true,"issue_id":5}`)
	}))
	defer upstream.Close()
	if err := auth.Default().Store.Save(auth.Record{Tokens: auth.Tokens{AccessToken: "account-token"}, Account: auth.Account{BaseURL: upstream.URL}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		authorization string
		origin        string
		noAuth        bool
		want          int
	}{
		{name: "missing credential", want: http.StatusUnauthorized},
		{name: "invalid credential", authorization: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "account credential is not server credential", authorization: "Bearer account-token", want: http.StatusUnauthorized},
		{name: "cross origin without credential", origin: "https://untrusted.example", want: http.StatusUnauthorized},
		{name: "server credential", authorization: "Bearer server-token", want: http.StatusOK},
		{name: "desktop credential", authorization: "Bearer desktop-token", want: http.StatusOK},
		{name: "authentication disabled", noAuth: true, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			if err := writer.WriteField("problem_description", "Test"); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			request.Header.Set("Authorization", tc.authorization)
			request.Header.Set("Origin", tc.origin)
			response := httptest.NewRecorder()
			before := calls
			handler := &Handler{serverAccessToken: "server-token", desktopSessionToken: "desktop-token", serverNoAuth: tc.noAuth}
			handler.Routes().ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			wantCalls := before
			if tc.want == http.StatusOK {
				wantCalls++
			}
			if calls != wantCalls {
				t.Fatalf("upstream calls=%d, want %d", calls, wantCalls)
			}
		})
	}
}
