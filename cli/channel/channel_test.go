package channel

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"csgclaw/cli/command"
)

type fakeHTTPClient struct {
	t        *testing.T
	handle   func(*http.Request) (*http.Response, error)
	lastReq  *http.Request
	lastBody string
}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	f.lastReq = req
	if req.Body != nil {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			f.t.Fatalf("ReadAll(body) error = %v", err)
		}
		f.lastBody = string(data)
		req.Body = io.NopCloser(strings.NewReader(f.lastBody))
	}
	return f.handle(req)
}

func TestFeishuSetReadsSecretFromStdinAndMasksOutput(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeHTTPClient{t: t}
	client.handle = func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", req.Method)
		}
		if got, want := req.URL.Path, "/api/v1/channels/feishu/config/u-dev"; got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		if got, want := req.Header.Get("Authorization"), "Bearer token"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if !strings.Contains(client.lastBody, `"app_secret":"stdin-secret"`) {
			t.Fatalf("request body missing secret: %s", client.lastBody)
		}
		return jsonResponse(200, `{"bot_id":"u-dev","configured":true,"app_id":"cli_dev","app_secret":"present","admin_open_id":"ou_admin","reloaded":true}`), nil
	}
	run := &command.Context{Program: "csgclaw", Stdin: strings.NewReader("stdin-secret\n"), Stdout: &stdout, Stderr: io.Discard, HTTPClient: client}
	err := (cmd{}).Run(context.Background(), run, []string{"feishu", "set", "--bot-id", "u-dev", "--app-id", "cli_dev", "--admin-open-id", "ou_admin", "--app-secret-stdin"}, command.GlobalOptions{Endpoint: "http://example.test", Token: "token"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(stdout.String(), "stdin-secret") {
		t.Fatalf("stdout leaked secret: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "present") {
		t.Fatalf("stdout missing masked secret: %s", stdout.String())
	}
}

func TestFeishuSetRejectsMultipleSecretSources(t *testing.T) {
	run := &command.Context{Program: "csgclaw", Stdin: strings.NewReader("secret"), Stdout: io.Discard, Stderr: io.Discard, HTTPClient: &fakeHTTPClient{t: t}}
	err := (cmd{}).Run(context.Background(), run, []string{"feishu", "set", "--bot-id", "u-dev", "--app-id", "cli_dev", "--app-secret-stdin", "--app-secret-env", "FEISHU_SECRET"}, command.GlobalOptions{})
	if err == nil || !strings.Contains(err.Error(), "provide exactly one") {
		t.Fatalf("Run() error = %v, want exactly one secret source", err)
	}
}

func TestFeishuGetUsesMaskedConfigEndpoint(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeHTTPClient{t: t}
	client.handle = func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if got, want := req.URL.Path, "/api/v1/channels/feishu/config/u-dev"; got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		return jsonResponse(200, `{"bot_id":"u-dev","configured":true,"app_id":"cli_dev","app_secret":"present"}`), nil
	}
	run := &command.Context{Program: "csgclaw", Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: io.Discard, HTTPClient: client}
	if err := (cmd{}).Run(context.Background(), run, []string{"feishu", "get", "--bot-id", "u-dev"}, command.GlobalOptions{Endpoint: "http://example.test"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "u-dev") || !strings.Contains(stdout.String(), "present") {
		t.Fatalf("stdout = %s, want bot and masked secret", stdout.String())
	}
}

func TestChannelReload(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeHTTPClient{t: t}
	client.handle = func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if got, want := req.URL.Path, "/api/v1/channels/reload"; got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		return jsonResponse(200, `{"status":"reloaded","feishu_bots":["u-dev"]}`), nil
	}
	run := &command.Context{Program: "csgclaw", Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: io.Discard, HTTPClient: client}
	if err := (cmd{}).Run(context.Background(), run, []string{"reload"}, command.GlobalOptions{Endpoint: "http://example.test"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "reloaded") || !strings.Contains(stdout.String(), "u-dev") {
		t.Fatalf("stdout = %s, want reload result", stdout.String())
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
