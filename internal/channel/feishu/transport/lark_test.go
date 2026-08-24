package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestParseMessageContentPreservesTextAndResources(t *testing.T) {
	text, raw, resources := parseMessageContent("post", `{
		"zh_cn":{"title":"告警","content":[
			[{"tag":"text","text":"磁盘告警"}],
			[{"tag":"a","text":"处理手册","href":"https://example.test/runbook"}],
			[{"tag":"img","image_key":"img_disk"}],
			[{"tag":"media","file_key":"file_log"}]
		]}
	}`)
	if !strings.Contains(text, "**告警**") || !strings.Contains(text, "磁盘告警") ||
		!strings.Contains(text, "[处理手册](https://example.test/runbook)") || len(raw) == 0 {
		t.Fatalf("text=%q raw=%q", text, raw)
	}
	if len(resources) != 2 || resources[0].Kind != "image" || resources[0].ID != "img_disk" ||
		resources[1].Kind != "file" || resources[1].ID != "file_log" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestParseMessageContentPrefersChinesePostLocale(t *testing.T) {
	text, _, resources := parseMessageContent("post", `{
		"en_us":{"title":"Alert","content":[
			[{"tag":"text","text":"English alert"}],
			[{"tag":"img","image_key":"img_en"}]
		]},
		"zh_cn":{"title":"告警","content":[
			[{"tag":"text","text":"中文告警"}],
			[{"tag":"img","image_key":"img_zh"}]
		]}
	}`)
	if !strings.Contains(text, "中文告警") || strings.Contains(text, "English alert") {
		t.Fatalf("text = %q", text)
	}
	if len(resources) != 1 || resources[0].ID != "img_zh" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestIngressRoutingHelpers(t *testing.T) {
	if got := ingressChatMode("group", "thread"); got != ModeTopic {
		t.Fatalf("mode = %q", got)
	}
	if chat, kind := ingressCardChat("", "ou_user"); chat != "ou_user" || kind != ChatP2P {
		t.Fatalf("card chat = %q, %q", chat, kind)
	}
}

func TestHandleMessagePreservesOrdinaryReplyRoot(t *testing.T) {
	var events []Event
	ingress := &oapiIngress{handler: func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	}}
	for index, rootID := range []string{"root-1", "root-2"} {
		messageID := fmt.Sprintf("message-%d", index+1)
		chatID, chatType := "chat-1", "group"
		messageType, content := "text", `{"text":"hello"}`
		if err := ingress.handleMessage(context.Background(), &larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{
				MessageId: &messageID, ChatId: &chatID, ChatType: &chatType,
				RootId: &rootID, MessageType: &messageType, Content: &content,
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 2 || events[0].Message.RootID != "root-1" || events[1].Message.RootID != "root-2" {
		t.Fatalf("root reply events = %#v", events)
	}
}

type fakeIngressConnection struct {
	handler   func(context.Context, Event) error
	identity  Identity
	connected bool
	closed    bool
}

func (f *fakeIngressConnection) Connect(_ context.Context, handler func(context.Context, Event) error) error {
	f.connected = true
	f.handler = handler
	return nil
}
func (f *fakeIngressConnection) Disconnect(context.Context) error {
	f.closed = true
	return nil
}
func (f *fakeIngressConnection) Identity(context.Context) (Identity, error) {
	return f.identity, nil
}

type recordingSink struct {
	events []Event
	err    error
}

func (s *recordingSink) HandleEvent(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return s.err
}

func TestIngressLifecyclePreparesIdentityAndPropagatesSinkError(t *testing.T) {
	connection := &fakeIngressConnection{identity: Identity{OpenID: "ou_bot", Name: "Bot"}}
	sink := &recordingSink{err: errors.New("sink failed")}
	lifecycle := newIngressLifecycle(connection, connection, sink)
	identity, err := lifecycle.PrepareIdentity(context.Background())
	if err != nil || identity.OpenID != "ou_bot" {
		t.Fatalf("PrepareIdentity() = %#v, %v", identity, err)
	}
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := Event{Kind: EventMessage, EventID: "event-1"}
	if err := connection.handler(context.Background(), event); !errors.Is(err, sink.err) {
		t.Fatalf("handler error = %v", err)
	}
	if err := lifecycle.Disconnect(context.Background()); err != nil || !connection.closed {
		t.Fatalf("Disconnect() = %v, closed=%t", err, connection.closed)
	}
}

func TestProductionFactoryBuildsLocalTransport(t *testing.T) {
	created, err := NewFactory().New(Config{AppID: "cli_test", AppSecret: "secret"}, &recordingSink{})
	if err != nil {
		t.Fatal(err)
	}
	concrete, ok := created.(*adapter)
	if !ok || concrete.lifecycle == nil || concrete.oapi == nil || concrete.comments == nil || concrete.messages == nil {
		t.Fatalf("adapter = %#v", created)
	}
	if _, ok := concrete.lifecycle.(*ingressLifecycle); !ok {
		t.Fatalf("lifecycle type = %T", concrete.lifecycle)
	}
	outbound, ok := concrete.oapi.(*directOutbound)
	if !ok {
		t.Fatalf("outbound type = %T", concrete.oapi)
	}
	sdkAPI, ok := outbound.api.(*sdkLarkOpenAPI)
	if !ok {
		t.Fatalf("OpenAPI type = %T", outbound.api)
	}
	comments, ok := concrete.comments.(*oapiCommentAdapter)
	if !ok {
		t.Fatalf("comment adapter type = %T", concrete.comments)
	}
	messages, ok := concrete.messages.(*oapiMessageAdapter)
	if !ok {
		t.Fatalf("message adapter type = %T", concrete.messages)
	}
	if comments.tokens != sdkAPI.tokens || messages.tokens != sdkAPI.tokens {
		t.Fatal("production read adapters do not share the outbound tenant token source")
	}
}

func TestIngressClientAcquiresTenantTokenForBotIdentity(t *testing.T) {
	var tokenCalls, identityCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokenCalls++
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":7200}`))
		case "/open-apis/bot/v3/info":
			identityCalls++
			if request.Header.Get("Authorization") != "Bearer tenant-token" {
				t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","bot":{"open_id":"ou_bot","app_name":"Bot"}}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client := newIngressLarkClient(
		"cli_test",
		"secret",
		&singleAttemptHTTPClient{client: server.Client()},
		lark.WithOpenBaseUrl(server.URL),
	)
	ingress := &oapiIngress{appID: "cli_test", client: client}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	identity, err := ingress.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if identity.OpenID != "ou_bot" || tokenCalls != 1 || identityCalls != 1 {
		t.Fatalf("identity=%#v tokenCalls=%d identityCalls=%d", identity, tokenCalls, identityCalls)
	}
}

func TestOAPIReadAdaptersAttachTenantTokenWhenSDKCacheIsDisabled(t *testing.T) {
	var messageCalls, commentCalls int
	httpClient := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		body := ""
		switch {
		case strings.HasPrefix(request.URL.Path, "/open-apis/im/v1/messages/"):
			messageCalls++
			body = `{"code":0,"msg":"ok","data":{"items":[]}}`
		case request.URL.Path == "/open-apis/wiki/v2/spaces/get_node":
			commentCalls++
			body = `{"code":0,"msg":"ok","data":{"node":{"obj_token":"doc-token","obj_type":"docx"}}}`
		default:
			t.Errorf("unexpected OpenAPI path %q", request.URL.Path)
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: http.NoBody, Request: request}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})

	client := lark.NewClient(
		"cli_test",
		"secret",
		lark.WithEnableTokenCache(false),
		lark.WithOpenBaseUrl("https://open.feishu.test"),
		lark.WithHttpClient(httpClient),
	)
	tokens := tenantTokenSourceFunc(func(context.Context) (string, error) { return "tenant-token", nil })
	if _, found, err := newOAPIMessageAdapter(client, tokens).FetchMessage(context.Background(), "message-1"); err != nil || found {
		t.Fatalf("FetchMessage() found=%t error=%v", found, err)
	}
	target, accessible, err := newOAPICommentAdapter(client, tokens).ResolveCommentTarget(context.Background(), "wiki-token", "docx")
	if err != nil || !accessible || target != (CommentTarget{FileToken: "doc-token", FileType: "docx"}) {
		t.Fatalf("ResolveCommentTarget() = %#v, %t, %v", target, accessible, err)
	}
	if messageCalls != 1 || commentCalls != 1 {
		t.Fatalf("API calls: message=%d comment=%d", messageCalls, commentCalls)
	}
}
