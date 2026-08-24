package transport

import (
	"context"
	"fmt"
	"strings"
	"sync"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

const larkTransportSource = "csgclaw"

type productionFactory struct{}

// NewFactory returns the CSGClaw-owned Feishu protocol implementation.
func NewFactory() Factory { return productionFactory{} }

func (productionFactory) New(config Config, sink Sink) (Adapter, error) {
	appID := strings.TrimSpace(config.AppID)
	appSecret := strings.TrimSpace(config.AppSecret)
	if appID == "" {
		return nil, fmt.Errorf("%w: app id is required", ErrInvalidConfig)
	}
	if appSecret == "" {
		return nil, fmt.Errorf("%w: app secret is required", ErrInvalidConfig)
	}
	if sink == nil {
		return nil, ErrNilSink
	}

	httpClient := newSingleAttemptHTTPClient()
	apiClient := lark.NewClient(
		appID,
		appSecret,
		lark.WithSource(larkTransportSource),
		lark.WithEnableTokenCache(false),
		lark.WithHttpClient(httpClient),
	)
	ingressClient := newIngressLarkClient(appID, appSecret, httpClient)
	ingress, err := newOAPIIngress(appID, appSecret, ingressClient)
	if err != nil {
		return nil, fmt.Errorf("create feishu message ingress: %w", err)
	}
	tokens := newCachedTenantTokenSource(apiClient, appID, appSecret)
	comments := newOAPICommentAdapter(apiClient, tokens)
	return newAdapterWithDependencies(
		newIngressLifecycle(ingress, ingress, sink),
		newDirectOutbound(apiClient, tokens),
		newBoundedResourceDownloader(tokens, httpClient),
		comments,
		newOAPIMessageAdapter(apiClient, tokens),
	), nil
}

func newIngressLarkClient(appID, appSecret string, httpClient larkcore.HttpClient, options ...lark.ClientOptionFunc) *lark.Client {
	clientOptions := []lark.ClientOptionFunc{
		lark.WithSource(larkTransportSource),
		lark.WithHttpClient(httpClient),
	}
	clientOptions = append(clientOptions, options...)
	return lark.NewClient(appID, appSecret, clientOptions...)
}

type ingressConnection interface {
	Connect(context.Context, func(context.Context, Event) error) error
	Disconnect(context.Context) error
}

type ingressIdentitySource interface {
	Identity(context.Context) (Identity, error)
}

type ingressLifecycle struct {
	mu sync.RWMutex

	ingress  ingressConnection
	identity ingressIdentitySource
	sink     Sink
	bot      Identity
}

func newIngressLifecycle(ingress ingressConnection, identity ingressIdentitySource, sink Sink) *ingressLifecycle {
	return &ingressLifecycle{ingress: ingress, identity: identity, sink: sink}
}

func (l *ingressLifecycle) PrepareIdentity(ctx context.Context) (Identity, error) {
	if l == nil || l.identity == nil {
		return Identity{}, ErrInvalidConfig
	}
	l.mu.RLock()
	bot := l.bot
	l.mu.RUnlock()
	if strings.TrimSpace(bot.OpenID) != "" {
		return bot, nil
	}
	identity, err := l.identity.Identity(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("load feishu bot identity: %w", err)
	}
	l.mu.Lock()
	l.bot = identity
	l.mu.Unlock()
	return identity, nil
}

func (l *ingressLifecycle) Start(ctx context.Context) error {
	if l == nil || l.ingress == nil || l.sink == nil {
		return ErrInvalidConfig
	}
	if _, err := l.PrepareIdentity(ctx); err != nil {
		return err
	}
	if err := l.ingress.Connect(ctx, l.sink.HandleEvent); err != nil {
		return fmt.Errorf("connect feishu message ingress: %w", err)
	}
	return nil
}

func (l *ingressLifecycle) Disconnect(ctx context.Context) error {
	if l == nil || l.ingress == nil {
		return nil
	}
	return l.ingress.Disconnect(ctx)
}

func (l *ingressLifecycle) BotIdentity() Identity {
	if l == nil {
		return Identity{}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.bot
}

var _ Factory = productionFactory{}
var _ larkLifecycle = (*ingressLifecycle)(nil)
