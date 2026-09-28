package api

import (
	"bytes"
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
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("site_url") == "https://attacker.example" || r.FormValue("version") == "fake" {
			t.Error("trusted caller metadata")
		}
		io.WriteString(w, `{"success":true,"state":"succeeded","issue_id":5}`)
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
	for key, value := range map[string]string{"submission_id": "5316f614-ab57-4e44-a4b6-1cccbcb1a0af", "problem_description": "Test", "site_url": "https://attacker.example", "version": "fake"} {
		writer.WriteField(key, value)
	}
	writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	(&Handler{}).handleFeedback(recorder, request)
	if recorder.Code != 200 || !called {
		t.Fatalf("status=%d body=%s called=%v", recorder.Code, recorder.Body, called)
	}
}

func TestFeedbackRequiresLogin(t *testing.T) {
	previousStore := auth.Default().Store
	t.Cleanup(func() { auth.Default().Store = previousStore })
	auth.Default().Store = auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	response := httptest.NewRecorder()
	(&Handler{}).handleFeedback(response, httptest.NewRequest(http.MethodPost, "/api/v1/feedback", nil))
	if response.Code != 401 {
		t.Fatal(response.Code)
	}
}
