package apiclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type captureHTTPClient struct {
	req          *http.Request
	statusCode   int
	responseBody string
}

func (c *captureHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.req = req
	status := c.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	body := c.responseBody
	if body == "" {
		body = "{}"
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestBuildRequestURLMergesBaseAndPathQueries(t *testing.T) {
	got, err := buildRequestURL(
		"https://example.test/v1/sandboxes/sb-1?port=18080",
		"/api/v1/messages?room_id=room-123",
	)
	if err != nil {
		t.Fatalf("buildRequestURL() error = %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got, want := parsed.Path, "/v1/sandboxes/sb-1/api/v1/messages"; got != want {
		t.Fatalf("parsed.Path = %q, want %q", got, want)
	}
	query := parsed.Query()
	if got, want := query.Get("room_id"), "room-123"; got != want {
		t.Fatalf("room_id = %q, want %q", got, want)
	}
	if got, want := query.Get("port"), "18080"; got != want {
		t.Fatalf("port = %q, want %q", got, want)
	}
}

func TestDoJSONUsesRequestURLBuilder(t *testing.T) {
	httpClient := &captureHTTPClient{}
	client := New("https://example.test/v1/sandboxes/sb-1?port=18080", "", httpClient)

	var out map[string]any
	if err := client.DoJSON(context.Background(), http.MethodGet, "/api/v1/messages?room_id=room-123", nil, &out); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if httpClient.req == nil {
		t.Fatal("request was not captured")
	}
	if got, want := httpClient.req.URL.Path, "/v1/sandboxes/sb-1/api/v1/messages"; got != want {
		t.Fatalf("req.URL.Path = %q, want %q", got, want)
	}
	if got, want := httpClient.req.URL.Query().Get("room_id"), "room-123"; got != want {
		t.Fatalf("room_id = %q, want %q", got, want)
	}
	if got, want := httpClient.req.URL.Query().Get("port"), "18080"; got != want {
		t.Fatalf("port = %q, want %q", got, want)
	}
}

func TestStreamUsesRequestURLBuilder(t *testing.T) {
	httpClient := &captureHTTPClient{responseBody: "line1\n"}
	client := New("https://example.test/v1/sandboxes/sb-1?port=18080", "", httpClient)

	var buf bytes.Buffer
	values := url.Values{"room_id": {"room-123"}}
	if err := client.Stream(context.Background(), "/api/v1/messages", values, &buf); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if httpClient.req == nil {
		t.Fatal("request was not captured")
	}
	if got, want := httpClient.req.URL.Path, "/v1/sandboxes/sb-1/api/v1/messages"; got != want {
		t.Fatalf("req.URL.Path = %q, want %q", got, want)
	}
	if got, want := httpClient.req.URL.Query().Get("room_id"), "room-123"; got != want {
		t.Fatalf("room_id = %q, want %q", got, want)
	}
	if got, want := httpClient.req.URL.Query().Get("port"), "18080"; got != want {
		t.Fatalf("port = %q, want %q", got, want)
	}
}
