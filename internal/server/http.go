package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"csgclaw/internal/agent"
	"csgclaw/internal/agenttask"
	"csgclaw/internal/api"
	"csgclaw/internal/channel/feishu"
	"csgclaw/internal/im"
	"csgclaw/internal/llm"
	"csgclaw/internal/mcp"
	"csgclaw/internal/participant"
	"csgclaw/internal/runtimecatalog"
	"csgclaw/internal/scheduledtask"
	"csgclaw/internal/team"
	hub "csgclaw/internal/template"
	"csgclaw/internal/upgrade"
	"csgclaw/internal/worklease"
)

type Options struct {
	ListenAddr string
	// Service is borrowed for request handling. The caller must close it after
	// stopping components such as channel bridges that retain runtime handles.
	Service            *agent.Service
	Hub                *hub.Service
	MCP                *mcp.Service
	Participant        *participant.Service
	IM                 *im.Service
	IMBus              *im.Bus
	WorkReporter       worklease.ParticipantWorkReporter
	WorkBus            *worklease.Bus
	ParticipantBridge  *im.ParticipantBridge
	Feishu             *feishu.Service
	LLM                *llm.Service
	Team               *team.Service
	AgentTask          *agenttask.Service
	ScheduledTask      *scheduledtask.Service
	AgentRuntimes      *runtimecatalog.Service
	TeamAdapters       *team.AdapterRegistry
	Upgrade            *upgrade.Manager
	ActivityDecider    api.ActivityDecider
	UserInputResponder api.UserInputResponder
	ConfigPath         string
	AccessToken        string
	NoAuth             bool
	Context            context.Context
	OnReady            func(h *api.Handler, router chi.Router)
}

func newHandler(opts Options) *api.Handler {
	handler := api.NewHandlerWithAuth(opts.Service, opts.IM, opts.IMBus, opts.ParticipantBridge, opts.Feishu, opts.LLM, opts.AccessToken, opts.NoAuth)
	handler.SetParticipantService(opts.Participant)
	handler.SetParticipantWorkService(opts.WorkReporter, opts.WorkBus)
	handler.SetHubService(opts.Hub)
	handler.SetMCPService(opts.MCP)
	handler.SetTeamService(opts.Team)
	handler.SetAgentTaskService(opts.AgentTask)
	handler.SetScheduledTaskService(opts.ScheduledTask)
	handler.SetAgentRuntimeService(opts.AgentRuntimes)
	if opts.TeamAdapters != nil {
		handler.SetTeamAdapterRegistry(opts.TeamAdapters)
	}
	handler.SetUpgradeManager(opts.Upgrade)
	handler.SetActivityDecider(opts.ActivityDecider)
	handler.SetUserInputResponder(opts.UserInputResponder)
	handler.SetUpgradeConfigPath(opts.ConfigPath)
	handler.SetConfigPath(opts.ConfigPath)
	return handler
}

func Run(opts Options) error {
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	handler := newHandler(opts)
	router := handler.Routes()
	router.Handle("/*", uiFallbackHandler())

	httpServer := &http.Server{
		Addr:              opts.ListenAddr,
		Handler:           accessLog(slog.Default(), router),
		ReadHeaderTimeout: 5 * time.Second,
	}

	if opts.IMBus != nil && opts.ParticipantBridge != nil {
		events, cancel := opts.IMBus.Subscribe()
		defer cancel()

		go func() {
			for {
				select {
				case <-opts.Context.Done():
					return
				case evt, ok := <-events:
					if !ok {
						return
					}
					handler.PublishParticipantEvent(evt)
				}
			}
		}()
	}

	if opts.Upgrade != nil {
		go opts.Upgrade.Start(opts.Context)
	}
	if opts.ScheduledTask != nil {
		go opts.ScheduledTask.Start(opts.Context)
	}

	listener, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return err
	}
	stopShutdownWatch := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-opts.Context.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
		case <-stopShutdownWatch:
		}
	}()
	if opts.OnReady != nil {
		opts.OnReady(handler, router)
	}

	serveErr := httpServer.Serve(listener)
	if opts.Context.Err() != nil {
		<-shutdownDone
	} else {
		close(stopShutdownWatch)
		<-shutdownDone
	}
	if serveErr != nil && serveErr != http.ErrServerClosed {
		return serveErr
	}
	return nil
}
