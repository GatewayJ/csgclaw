package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agent "csgclaw/internal/agentengine/agents"
	"csgclaw/internal/config"
	"csgclaw/internal/modelpreset"
)

func TestChatCompletionsPreservesTokenLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   string
		effort  string
		options string
	}{
		{"small qwen", "Qwen3-0.6B", "auto", `"max_tokens":8192`},
		{"qwen reasoning disabled", "Qwen3-0.6B", "none", `"max_tokens":8192,"thinking":{"type":"disabled"}`},
		{"glm", "glm-5.1", "auto", `"max_tokens":16384`},
		{"unknown model 16384", "custom-model", "auto", `"max_tokens":16384`},
		{"unknown model 32768", "custom-model", "auto", `"max_tokens":32768`},
		{"large explicit limit", "custom-model", "auto", `"max_tokens":256000`},
		{"completion limit", "Qwen3-0.6B", "auto", `"max_completion_tokens":8192`},
		{"explicit thinking budget", "custom-model", "auto", `"max_tokens":8192,"thinking_budget":32768`},
		{"provider default", "Qwen3-0.6B", "auto", `"temperature":0.5`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]json.RawMessage
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"cmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer upstream.Close()
			profile := agent.AgentProfile{Provider: agent.ProviderAPI, BaseURL: upstream.URL + "/v1", APIKey: "test-key", ModelID: tc.model, APIProtocol: modelpreset.ChatCompletions, ReasoningEffort: tc.effort}
			svc := NewService(config.ModelConfig{}, protocolProfile{profile})
			body := []byte(`{"messages":[{"role":"user","content":"hello"}],` + tc.options + `}`)
			_, status, _, err := svc.ChatCompletions(context.Background(), "agent", body)
			if err != nil || status != http.StatusOK {
				t.Fatalf("status=%d error=%v", status, err)
			}
			var want map[string]json.RawMessage
			if err := json.Unmarshal(body, &want); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"max_tokens", "max_completion_tokens", "thinking_budget", "thinking"} {
				if string(got[key]) != string(want[key]) {
					t.Errorf("%s = %s, want %s", key, got[key], want[key])
				}
			}
		})
	}
}

func TestTokenLimitsAcrossRequestPaths(t *testing.T) {
	for _, tc := range []struct {
		name        string
		protocol    string
		responses   bool
		fallback    bool
		outputField string
	}{
		{"chat direct", modelpreset.ChatCompletions, false, false, "max_tokens"},
		{"chat automatic protocol", "", false, false, "max_tokens"},
		{"chat to responses", modelpreset.Responses, false, false, "max_output_tokens"},
		{"responses direct", modelpreset.Responses, true, false, "max_output_tokens"},
		{"responses to chat", modelpreset.ChatCompletions, true, false, "max_tokens"},
		{"responses fallback to chat", "", true, true, "max_tokens"},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, streaming), func(t *testing.T) {
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if tc.fallback && r.URL.Path == "/v1/responses" {
						http.NotFound(w, r)
						return
					}
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					// A small model can accept this request only if the bridge preserves
					// its 8192-token output limit inside the 32768-token context window.
					var limit int
					if err := json.Unmarshal(payload[tc.outputField], &limit); err != nil {
						t.Error(err)
					}
					if limit != 8192 {
						t.Errorf("%s = %d, want 8192", tc.outputField, limit)
						w.WriteHeader(http.StatusBadRequest)
						io.WriteString(w, `{"error":{"code":"invalid_request_error","message":"output limit exceeds model capacity"}}`)
						return
					}
					for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
						if key != tc.outputField && payload[key] != nil {
							t.Errorf("unexpected output limit field %s=%s", key, payload[key])
						}
					}
					if tc.outputField == "max_output_tokens" {
						terminal := `{"id":"resp-test","object":"response","status":"completed","model":"Qwen3-0.6B","output":[{"id":"msg-test","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", terminal)
						} else {
							w.Header().Set("Content-Type", "application/json")
							io.WriteString(w, terminal)
						}
						return
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: "+`{"id":"cmpl-test","object":"chat.completion.chunk","model":"Qwen3-0.6B","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`+"\n\n")
						fmt.Fprint(w, "data: "+`{"id":"cmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`+"\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, `{"id":"cmpl-test","object":"chat.completion","model":"Qwen3-0.6B","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
					}
				}))
				defer upstream.Close()
				svc := NewService(config.ModelConfig{}, protocolProfile{agent.AgentProfile{Provider: agent.ProviderAPI, BaseURL: upstream.URL + "/v1", APIKey: "test-key", ModelID: "Qwen3-0.6B", APIProtocol: tc.protocol}})
				var response *UpstreamResponse
				var err error
				if tc.responses {
					response, err = svc.Responses(context.Background(), "agent", []byte(fmt.Sprintf(`{"input":"hello","max_output_tokens":8192,"stream":%v}`, streaming)))
				} else {
					response, err = svc.ChatCompletionsStream(context.Background(), "agent", []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":8192,"stream":%v}`, streaming)))
				}
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "ok") {
					t.Fatalf("status=%d body=%s error=%v", response.StatusCode, body, err)
				}
				wantCalls := 1
				if tc.fallback {
					wantCalls = 2
				}
				if calls != wantCalls {
					t.Errorf("upstream calls=%d, want %d", calls, wantCalls)
				}
			})
		}
	}
}
