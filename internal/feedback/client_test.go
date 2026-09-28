package feedback

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSubmitAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authentication")
		}
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != Endpoint {
				t.Error(r.URL.Path)
			}
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("site_url") != "https://site.example" || r.FormValue("version") != "v0.8.0" {
				t.Error(r.Form)
			}
			file, _, err := r.FormFile("images")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if string(data) != "image" {
				t.Error(string(data))
			}
		case http.MethodGet:
			if r.URL.Path != Endpoint+"/submission" {
				t.Error(r.URL.Path)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"state":"succeeded","issue_id":42}`)
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, Token: "test-token", HTTPClient: server.Client()}
	result, err := client.Submit(context.Background(), Submission{ID: "submission", Description: "Test", SiteURL: "https://site.example", Version: "v0.8.0", Channel: "release", Images: []Image{{Data: []byte("image"), Extension: "png"}}})
	if err != nil || result.IssueID != 42 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	result, err = client.Status(context.Background(), "submission")
	if err != nil || !result.Success {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClientRejectsRedirect(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	_, err := (&Client{BaseURL: source.URL, Token: "test-token"}).Status(context.Background(), "id")
	if err == nil || redirected {
		t.Fatalf("err=%v redirected=%v", err, redirected)
	}
}
