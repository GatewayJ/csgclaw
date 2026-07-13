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

type ExternalBindingActivation string

const (
	ExternalBindingActivationLifecycleReconciled ExternalBindingActivation = "lifecycle_reconciled"
	ExternalBindingActivationRuntimeRecreated    ExternalBindingActivation = "runtime_recreated"
)

func (s *Service) lifecycleObserver() LifecycleObserver {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lifecycle
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

// ReconcileLifecycle reapplies lifecycle integrations for an agent without
// changing its runtime instance. This is used when an external binding, such
// as a channel participant, changes after the runtime is already running.
func (s *Service) ReconcileLifecycle(ctx context.Context, id string) (Agent, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Agent{}, fmt.Errorf("agent id is required")
	}
	got, ok := s.Agent(id)
	if !ok {
		return Agent{}, fmt.Errorf("agent %q not found", id)
	}
	return s.reconcileLifecycle(ctx, got)
}

// ApplyExternalBinding activates an updated external binding using the
// lifecycle required by the agent's runtime.
func (s *Service) ApplyExternalBinding(ctx context.Context, id string) (Agent, ExternalBindingActivation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Agent{}, "", fmt.Errorf("agent id is required")
	}
	got, ok := s.Agent(id)
	if !ok {
		return Agent{}, "", fmt.Errorf("agent %q not found", id)
	}
	if strings.EqualFold(strings.TrimSpace(got.RuntimeKind), RuntimeKindCodex) {
		reconciled, err := s.reconcileLifecycle(ctx, got)
		return reconciled, ExternalBindingActivationLifecycleReconciled, err
	}
	recreated, err := s.Recreate(ctx, got.ID)
	return recreated, ExternalBindingActivationRuntimeRecreated, err
}

func (s *Service) reconcileLifecycle(ctx context.Context, got Agent) (Agent, error) {
	if !strings.EqualFold(strings.TrimSpace(got.RuntimeKind), RuntimeKindCodex) {
		return Agent{}, fmt.Errorf("agent %q runtime %q does not support lifecycle reconciliation", got.ID, got.RuntimeKind)
	}
	if !shouldEnsureLifecycle(got) {
		return Agent{}, fmt.Errorf("agent %q must be running with a complete profile to reconcile external bindings", got.ID)
	}
	observer := s.lifecycleObserver()
	if observer == nil {
		return Agent{}, fmt.Errorf("agent lifecycle observer is not configured")
	}
	if err := observer.EnsureAgent(ctx, got); err != nil {
		return Agent{}, err
	}
	return got, nil
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
