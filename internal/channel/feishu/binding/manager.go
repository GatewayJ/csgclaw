package binding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"csgclaw/internal/channel/feishu"
)

const defaultReconcileInterval = 30 * time.Second

// Worker owns one long-lived Feishu transport and its ingress/execution/
// delivery pipeline.
type Worker interface {
	Start(context.Context) error
	Close(context.Context) error
}

// LocalMessageWorker accepts messages that CSGClaw itself sent through a
// Feishu bot. It must feed them into the same Intake used by remote WebSocket
// events so mention validation, freshness checks, and deduplication are shared.
type LocalMessageWorker interface {
	HandleLocalMessage(context.Context, feishu.MessageEvent) error
}

type WorkerFactory interface {
	NewWorker(Resolved) (Worker, error)
}

type ManagerOptions struct {
	Agents            AgentLister
	Provider          feishu.AgentCredentialProvider
	Workers           WorkerFactory
	Messages          *feishu.MessageBus
	ReconcileInterval time.Duration
}

// Manager reconciles stable App Bindings. Agent Stop/Recreate must not
// disconnect Feishu because runtime status is not part of desired state.
type Manager struct {
	resolver *Resolver
	workers  WorkerFactory
	messages *feishu.MessageBus
	interval time.Duration

	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	closed      bool
	events      <-chan feishu.MessageEvent
	unsubscribe func()

	reconcileMu sync.Mutex
	mu          sync.Mutex
	active      map[string]*activeWorker
}

type activeWorker struct {
	resolved    Resolved
	fingerprint string
	worker      Worker
}

func NewManager(options ManagerOptions) (*Manager, error) {
	if options.Agents == nil {
		return nil, fmt.Errorf("feishu binding manager: agent lister is required")
	}
	if options.Provider == nil {
		return nil, fmt.Errorf("feishu binding manager: credential provider is required")
	}
	if options.Workers == nil {
		return nil, fmt.Errorf("feishu binding manager: worker factory is required")
	}
	interval := options.ReconcileInterval
	if interval <= 0 {
		interval = defaultReconcileInterval
	}
	return &Manager{
		resolver: NewResolver(options.Agents, options.Provider),
		workers:  options.Workers,
		messages: options.Messages,
		interval: interval,
		done:     make(chan struct{}),
		active:   make(map[string]*activeWorker),
	}, nil
}

func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("feishu binding manager is closed")
	}
	if m.cancel != nil {
		m.mu.Unlock()
		return nil
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	if m.messages != nil {
		m.events, m.unsubscribe = m.messages.Subscribe()
	}
	m.mu.Unlock()

	go m.run()
	return nil
}

func (m *Manager) run() {
	defer close(m.done)
	if err := m.Reconcile(m.ctx); err != nil {
		slog.Warn("initial Feishu binding reconciliation failed", "error", err)
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	m.mu.Lock()
	events := m.events
	m.mu.Unlock()
	for {
		select {
		case <-m.ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			m.routeLocalMessage(m.ctx, event)
		case <-ticker.C:
			if err := m.Reconcile(m.ctx); err != nil {
				slog.Warn("feishu binding reconciliation failed", "error", err)
			}
		}
	}
}

func (m *Manager) routeLocalMessage(ctx context.Context, event feishu.MessageEvent) {
	if event.Type != feishu.MessageEventTypeMessageCreated || event.Message == nil {
		return
	}
	targetID := strings.TrimSpace(event.MentionBotID)
	if targetID == "" {
		return
	}
	m.mu.Lock()
	var target *activeWorker
	for _, active := range m.active {
		if strings.TrimSpace(active.resolved.Binding.ParticipantID) == targetID {
			target = active
			break
		}
	}
	m.mu.Unlock()
	if target == nil {
		slog.Warn("drop local Feishu handoff because target binding is inactive",
			"participant_id", targetID,
			"message_id", event.Message.ID,
		)
		return
	}
	worker, ok := target.worker.(LocalMessageWorker)
	if !ok {
		slog.Warn("drop local Feishu handoff because binding worker does not support it",
			"binding_id", target.resolved.Binding.ID,
			"participant_id", targetID,
			"message_id", event.Message.ID,
		)
		return
	}
	if err := worker.HandleLocalMessage(ctx, event); err != nil && ctx.Err() == nil {
		slog.Warn("route local Feishu handoff failed",
			"binding_id", target.resolved.Binding.ID,
			"participant_id", targetID,
			"message_id", event.Message.ID,
			"error", err,
		)
	}
}

// Reconcile makes the active worker set match configured bindings. Agent
// status is intentionally absent from the desired state calculation.
func (m *Manager) Reconcile(ctx context.Context) error {
	if m == nil || m.resolver == nil {
		return nil
	}
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil
	}

	resolvedBindings, err := m.resolver.All(ctx)
	if err != nil && !errors.Is(err, ErrAuthoritativeBindingConflict) {
		// Keep the last known-good worker set on transient Agent/credential
		// snapshot failures. Only a successful snapshot can remove one.
		return err
	}
	desired, desiredErr := safeHostedDesired(resolvedBindings)
	if desiredErr != nil && !errors.Is(desiredErr, ErrAuthoritativeBindingConflict) {
		return errors.Join(err, desiredErr)
	}
	snapshotErr := errors.Join(err, desiredErr)

	m.mu.Lock()
	active := make(map[string]*activeWorker, len(m.active))
	for id, worker := range m.active {
		active[id] = worker
	}
	m.mu.Unlock()

	var outErr error
	for id, current := range active {
		want, exists := desired[id]
		if exists && current.fingerprint == want.Fingerprint() && current.resolved.Binding.AgentID == want.Binding.AgentID {
			delete(desired, id)
			continue
		}
		if err := m.stop(ctx, id, current); err != nil {
			outErr = errors.Join(outErr, err)
			delete(desired, id)
			continue
		}
	}
	for id, resolved := range desired {
		if err := m.start(ctx, id, resolved); err != nil {
			outErr = errors.Join(outErr, err)
		}
	}
	return errors.Join(snapshotErr, outErr)
}

// safeHostedDesired defensively enforces the AppID ownership invariant at the
// Manager boundary too. Resolver normally removes these conflicts, but Manager
// must never activate duplicate transports if a future resolver regresses.
func safeHostedDesired(resolvedBindings []Resolved) (map[string]Resolved, error) {
	byApp := make(map[string][]Resolved)
	byBinding := make(map[string][]Resolved)
	for _, resolved := range resolvedBindings {
		appID := strings.TrimSpace(resolved.App.AppID)
		bindingID := strings.TrimSpace(resolved.Binding.ID)
		byApp[appID] = append(byApp[appID], resolved)
		byBinding[bindingID] = append(byBinding[bindingID], resolved)
	}

	conflictedApps := make(map[string]struct{})
	var conflictErr error
	for appID, bindings := range byApp {
		owners := uniqueResolvedOwners(bindings)
		if appID == "" || len(owners) <= 1 {
			continue
		}
		conflictedApps[appID] = struct{}{}
		conflictErr = errors.Join(conflictErr, &AppOwnershipConflictError{AppID: appID, Owners: owners})
	}

	conflictedBindings := make(map[string]struct{})
	for bindingID, bindings := range byBinding {
		owners := uniqueResolvedOwners(bindings)
		if bindingID == "" || len(owners) <= 1 {
			continue
		}
		conflictedBindings[bindingID] = struct{}{}
		conflictErr = errors.Join(conflictErr, fmt.Errorf("%w: binding_id=%q has multiple owners",
			ErrAuthoritativeBindingConflict, bindingID))
	}

	desired := make(map[string]Resolved, len(resolvedBindings))
	for _, resolved := range resolvedBindings {
		if _, conflicted := conflictedApps[strings.TrimSpace(resolved.App.AppID)]; conflicted {
			continue
		}
		if _, conflicted := conflictedBindings[strings.TrimSpace(resolved.Binding.ID)]; conflicted {
			continue
		}
		desired[resolved.Binding.ID] = resolved
	}
	return desired, conflictErr
}

func uniqueResolvedOwners(bindings []Resolved) []AppOwner {
	unique := make(map[string]AppOwner)
	for _, resolved := range bindings {
		owner := AppOwner{
			AgentID:       strings.TrimSpace(resolved.Binding.AgentID),
			ParticipantID: strings.TrimSpace(resolved.Binding.ParticipantID),
		}
		unique[appOwnerKey(owner)] = owner
	}
	owners := make([]AppOwner, 0, len(unique))
	for _, owner := range unique {
		owners = append(owners, owner)
	}
	return owners
}

func (m *Manager) start(ctx context.Context, id string, resolved Resolved) error {
	worker, err := m.workers.NewWorker(resolved)
	if err != nil {
		return fmt.Errorf("create feishu binding %q: %w", id, err)
	}
	workerCtx := m.ctx
	if workerCtx == nil {
		workerCtx = ctx
	}
	if err := worker.Start(workerCtx); err != nil {
		_ = worker.Close(context.Background())
		return fmt.Errorf("start feishu binding %q: %w", id, err)
	}
	entry := &activeWorker{resolved: resolved, fingerprint: resolved.Fingerprint(), worker: worker}
	m.mu.Lock()
	if previous := m.active[id]; previous != nil {
		m.mu.Unlock()
		_ = worker.Close(context.Background())
		return fmt.Errorf("start feishu binding %q: binding became active concurrently", id)
	}
	m.active[id] = entry
	m.mu.Unlock()
	slog.Info("feishu binding started", "binding_id", id, "agent_id", resolved.Binding.AgentID, "participant_id", resolved.Binding.ParticipantID)
	return nil
}

func (m *Manager) stop(ctx context.Context, id string, expected *activeWorker) error {
	m.mu.Lock()
	current := m.active[id]
	if current == nil || (expected != nil && current != expected) {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if err := current.worker.Close(ctx); err != nil {
		return fmt.Errorf("stop feishu binding %q: %w", id, err)
	}
	m.mu.Lock()
	if m.active[id] == current {
		delete(m.active, id)
	}
	m.mu.Unlock()
	slog.Info("feishu binding stopped", "binding_id", id, "agent_id", current.resolved.Binding.AgentID, "participant_id", current.resolved.Binding.ParticipantID)
	return nil
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	// Cancel first so an in-flight transport Start observes shutdown. Then
	// serialize with the full Reconcile pass before capturing m.active, so a
	// worker cannot be installed from an old desired-state snapshot afterward.
	m.mu.Lock()
	cancel := m.cancel
	unsubscribe := m.unsubscribe
	started := cancel != nil
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	if cancel != nil {
		cancel()
		m.cancel = nil
	}
	m.events = nil
	m.unsubscribe = nil
	m.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}

	m.reconcileMu.Lock()
	m.mu.Lock()
	active := make(map[string]*activeWorker, len(m.active))
	for id, worker := range m.active {
		active[id] = worker
	}
	m.active = make(map[string]*activeWorker)
	m.mu.Unlock()
	m.reconcileMu.Unlock()
	if started {
		<-m.done
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	for id, worker := range active {
		if err := worker.worker.Close(closeCtx); err != nil {
			slog.Warn("close feishu binding failed", "binding_id", id, "error", err)
		}
	}
}
