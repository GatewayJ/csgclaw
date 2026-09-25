package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	channel "csgclaw/internal/channel"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestNativeCOTUsesAppIdentityAndSeparateEndpoints(t *testing.T) {
	var methods, paths []string
	client := lark.NewClient("app", "secret", lark.WithEnableTokenCache(false), lark.WithHttpClient(&singleAttemptHTTPClient{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		methods = append(methods, req.Method)
		paths = append(paths, req.URL.Path)
		if req.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Error("missing tenant identity")
		}
		if len(methods) == 1 {
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["origin_message_id"] != "origin" || body["receive_id"] != "chat" || req.URL.Query().Get("receive_id_type") != "chat_id" {
				t.Fatalf("create=%+v", body)
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"cot_id":"cot","message_id":"message"}}`))}, nil
	})}}))
	outbound := newDirectOutbound(client, tenantTokenSourceFunc(func(context.Context) (string, error) { return "tenant-token", nil }))
	ref, err := outbound.CreateCOT(context.Background(), COTCreateRequest{ChatID: "chat", OriginMessageID: "origin"})
	if err != nil {
		t.Fatal(err)
	}
	if err = outbound.UpdateCOT(context.Background(), COTUpdateRequest{Ref: ref, Events: []channel.COTEvent{{EventType: "RUN_STARTED", Content: `{}`, Timestamp: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err = outbound.CompleteCOT(context.Background(), COTCompleteRequest{Ref: ref, Reason: "done"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST,PUT,POST" || paths[2] != "/open-apis/im/v1/message_cot/complete/cot" {
		t.Fatalf("requests=%v %v", methods, paths)
	}
}
