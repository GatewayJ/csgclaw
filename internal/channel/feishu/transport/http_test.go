package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSingleAttemptHTTPClientIsBoundedAndDoesNotFollowRedirect(t *testing.T) {
	t.Parallel()
	targetCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/target" {
			targetCalls++
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, req, "/target", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	client := newSingleAttemptHTTPClient()
	if client.client.Timeout != defaultOpenAPIRequestTimeout || client.client.Timeout <= 0 {
		t.Fatalf("HTTP timeout = %s", client.client.Timeout)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/redirect", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || targetCalls != 0 {
		t.Fatalf("redirect status=%d target calls=%d", resp.StatusCode, targetCalls)
	}
}
