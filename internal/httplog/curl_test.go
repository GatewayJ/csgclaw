package httplog

import (
	"net/http"
	"strings"
	"testing"
)

func TestCurlCommandIncludesExecutableRequest(t *testing.T) {
	body := []byte(`{"message":"it's ok"}`)
	req, err := http.NewRequest(http.MethodPost, "https://llm.example/v1/chat/completions?stream=false", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom", "value with 'quote")

	got := CurlCommand(req, body)
	want := `curl -X 'POST' -H 'Authorization: Bearer sk-test' -H 'Content-Type: application/json' -H 'X-Custom: value with '\''quote' --data-raw '{"message":"it'\''s ok"}' 'https://llm.example/v1/chat/completions?stream=false'`
	if got != want {
		t.Fatalf("CurlCommand() = %q, want %q", got, want)
	}
}

func TestCurlCommandOmitsBodyForEmptyPayload(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://llm.example/v1/models", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-test")

	got := CurlCommand(req, nil)
	want := `curl -X 'GET' -H 'Authorization: Bearer sk-test' 'https://llm.example/v1/models'`
	if got != want {
		t.Fatalf("CurlCommand() = %q, want %q", got, want)
	}
}
