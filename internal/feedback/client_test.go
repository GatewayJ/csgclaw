package feedback

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientUploadsToPortalAndSubmitsJSON(t *testing.T) {
	uploaded := false
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != UploadEndpoint {
			t.Error(r.URL)
		}
		cookie, err := r.Cookie("user_token")
		if err != nil || cookie.Value != "test-token" {
			t.Error("missing storage proxy credential")
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if string(data) != "image" {
			t.Error(string(data))
		}
		uploaded = true
		io.WriteString(w, `{"url":"https://images.example/test.png"}`)
	}))
	defer portal.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !uploaded || r.URL.Path != Endpoint || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect submission sequence or format")
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authentication")
		}
		var payload struct {
			ScreenshotURLs []string `json:"screenshot_urls"`
			UserName       string   `json:"user_name"`
			CSGClaw        struct {
				SiteURL string `json:"site_url"`
				Version string `json:"version"`
			} `json:"csgclaw"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.ScreenshotURLs) != 1 || payload.ScreenshotURLs[0] != "https://images.example/test.png" || payload.CSGClaw.SiteURL != portal.URL || payload.CSGClaw.Version != "v0.8.0" || payload.UserName != "alice" {
			t.Errorf("payload=%+v", payload)
		}
		io.WriteString(w, `{"success":true,"issue_id":42}`)
	}))
	defer upstream.Close()
	client := Client{BaseURL: upstream.URL, Token: "test-token"}
	result, err := client.Submit(context.Background(), Submission{Description: "Test", SiteURL: portal.URL, Version: "v0.8.0", Channel: "release", UserName: "alice", Images: []Image{{Data: []byte("image"), Extension: "png"}}})
	if err != nil || result.IssueID != 42 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClientUploadFailurePreventsIssueCreation(t *testing.T) {
	for _, body := range []string{`{"url":""}`, `{"url":"javascript:alert(1)"}`, `invalid JSON`} {
		t.Run(body, func(t *testing.T) {
			portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			defer portal.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("created issue after failed upload") }))
			defer upstream.Close()
			_, err := (&Client{BaseURL: upstream.URL}).Submit(context.Background(), Submission{SiteURL: portal.URL, Images: []Image{{Data: []byte("image"), Extension: "png"}}})
			if err == nil {
				t.Fatal("expected upload error")
			}
		})
	}
}

func TestClientRejectsRedirect(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	for _, images := range [][]Image{nil, {{Data: []byte("image"), Extension: "png"}}} {
		_, err := (&Client{BaseURL: source.URL, Token: "test-token"}).Submit(context.Background(), Submission{SiteURL: source.URL, Images: images})
		if err == nil || redirected {
			t.Fatalf("err=%v redirected=%v", err, redirected)
		}
	}
}
