package agent

import (
	"context"
	"fmt"
	"strings"

	agentruntime "csgclaw/internal/runtime"
)

type LifecycleObserver interface {
	EnsureAgent(context.Context, Agent) error
	StopAgent(string)
}

type BindingActivator interface {
	RefreshAgentChannel(context.Context, Agent, string) error
}

type ExternalBindingActivation string

const (
	ExternalBindingActivationChannelRefreshed ExternalBindingActivation = "channel_refreshed"
	ExternalBindingActivationRuntimeRecreated ExternalBindingActivation = "runtime_recreated"
)

func (s *Service) lifecycleObserver() LifecycleObserver {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lifecycle
}

func (s *Service) bindingActivator() BindingActivator {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bindingActivation
}

func (s *Service) syncLifecycleForAgent(ctx context.Context, a Agent) error {
	observer := s.lifecycleObserver()
	if observer == nil {
		return nil
	}
	if shouldEnsureLifecycle(a) {
		return observer.EnsureAgent(ctx, a)
	}
	observer.StopAgent(a.ID)
	return nil
}

// ApplyExternalBinding activates an updated external binding using the
// lifecycle required by the agent's runtime.
func (s *Service) ApplyExternalBinding(ctx context.Context, id, channel string) (Agent, ExternalBindingActivation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Agent{}, "", fmt.Errorf("agent id is required")
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		return Agent{}, "", fmt.Errorf("channel is required")
	}
	got, ok := s.Agent(id)
	if !ok {
		return Agent{}, "", fmt.Errorf("agent %q not found", id)
	}
	if strings.EqualFold(strings.TrimSpace(got.RuntimeKind), RuntimeKindCodex) {
		if !shouldEnsureLifecycle(got) {
			return Agent{}, "", fmt.Errorf("agent %q must be running with a complete profile to refresh external bindings", got.ID)
		}
		activator := s.bindingActivator()
		if activator == nil {
			return Agent{}, "", fmt.Errorf("agent binding activator is not configured")
		}
		if err := activator.RefreshAgentChannel(ctx, got, channel); err != nil {
			return Agent{}, "", err
		}
		return got, ExternalBindingActivationChannelRefreshed, nil
	}
	recreated, err := s.Recreate(ctx, got.ID)
	return recreated, ExternalBindingActivationRuntimeRecreated, err
}

func (s *Service) stopLifecycleAgent(agentID string) {
	observer := s.lifecycleObserver()
	if observer == nil {
		return
	}
	observer.StopAgent(strings.TrimSpace(agentID))
}

func shouldEnsureLifecycle(a Agent) bool {
	return isAgentProfileComplete(a) &&
		strings.EqualFold(strings.TrimSpace(a.Status), string(agentruntime.StateRunning))
}
