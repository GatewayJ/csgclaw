package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"csgclaw/internal/larkcli"
	projection "csgclaw/internal/runtimeextension/larkcli"
)

func (s *Service) isCLI(agentID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findLocked(agentID, id)
	return err == nil && e.record.Kind == KindCLI
}

func (s *Service) EnsureFeishuCLI(context.Context) (Installation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.record.AgentID == "" && e.record.Kind == KindCLI && e.record.AppID == "feishu" {
			return s.viewLocked(e), nil
		}
	}
	id, err := newInstallationID()
	if err != nil {
		return Installation{}, err
	}
	now := time.Now().UTC()
	r := record{Installation: Installation{Kind: KindCLI, InstallationID: id, AppID: "feishu", Name: "Feishu", Enabled: true, ResourceEnabled: true, Revision: 1, Status: "needs_configuration", CreatedAt: now, UpdatedAt: now}, CLI: &cliRecord{ConfigDir: filepath.Join(s.dataRoot, id, "lark-cli"), Completed: map[string]CLIOperation{}}}
	if err = s.persistLocked(id, &r); err != nil {
		return Installation{}, err
	}
	e := &entry{record: r}
	s.entries[id] = e
	return s.viewLocked(e), nil
}

func (s *Service) bindCLI(ctx context.Context, agentID, id string) (Installation, error) {
	if strings.TrimSpace(agentID) == "" {
		return Installation{}, ErrInvalid
	}
	if s.options.CLI == nil || s.options.CLI.Runtime == nil {
		return Installation{}, ErrInvalid
	}
	if _, err := s.options.CLI.Runtime.State(ctx, agentID); err != nil {
		return Installation{}, err
	}
	s.mu.Lock()
	resource, err := s.findLocked("", id)
	if err != nil {
		s.mu.Unlock()
		return Installation{}, err
	}
	for _, e := range s.entries {
		if e.record.AgentID == agentID && e.record.ResourceID == id {
			bid := e.record.InstallationID
			s.mu.Unlock()
			return s.SetCLIEnabled(ctx, agentID, bid, true)
		}
	}
	bid, err := newInstallationID()
	if err != nil {
		s.mu.Unlock()
		return Installation{}, err
	}
	now := time.Now().UTC()
	r := record{Installation: Installation{InstallationID: bid, AgentID: agentID, ResourceID: id, Enabled: true, Status: "applying", CreatedAt: now, UpdatedAt: now}, CLIApply: &cliBinding{Pending: true}}
	hydrateBinding(&r, resource.record)
	if err = s.persistLocked(bid, &r); err != nil {
		s.mu.Unlock()
		return Installation{}, err
	}
	e := &entry{record: r}
	s.entries[bid] = e
	out := s.viewLocked(e)
	s.mu.Unlock()
	s.scheduleCLI()
	return out, nil
}
func (s *Service) SetCLIEnabled(_ context.Context, agentID, id string, enabled bool) (Installation, error) {
	s.mu.Lock()
	e, err := s.findLocked(agentID, id)
	if err != nil {
		s.mu.Unlock()
		return Installation{}, err
	}
	if agentID == "" || e.record.Kind != KindCLI {
		s.mu.Unlock()
		return Installation{}, ErrInvalid
	}
	next := copyJSON(e.record)
	if next.CLIApply != nil && next.CLIApply.Pending && next.Enabled != enabled && next.LastErrorCode == "" {
		s.mu.Unlock()
		return Installation{}, ErrConflict
	}
	if next.Enabled == enabled && next.CLIApply != nil && !next.CLIApply.Pending && next.LastErrorCode == "" {
		out := s.viewLocked(e)
		s.mu.Unlock()
		return out, nil
	}
	next.Enabled = enabled
	next.Status = "applying"
	next.LastErrorCode = ""
	next.LastError = ""
	if next.CLIApply == nil {
		next.CLIApply = &cliBinding{}
	}
	next.CLIApply.Pending = true
	next.CLIApply.Remove = false
	next.UpdatedAt = time.Now().UTC()
	if err = s.persistLocked(id, &next); err != nil {
		s.mu.Unlock()
		return Installation{}, err
	}
	e.record = next
	if m := s.cliMigration; m != nil && m.Status == "waiting_for_legacy_callers" && m.Workers[agentID] != nil {
		migration := copyJSON(m)
		migration.Status = "pending"
		if err := s.saveMigrationLocked(migration); err != nil {
			s.mu.Unlock()
			return Installation{}, err
		}
		s.cliMigration = migration
	}
	out := s.viewLocked(e)
	s.mu.Unlock()
	s.scheduleCLI()
	return out, nil
}
func (s *Service) deleteCLIBinding(ctx context.Context, agentID, id string) error {
	if agentID == "" {
		return fmt.Errorf("%w: disconnect the global Feishu connector", ErrInvalid)
	}
	if _, err := s.SetCLIEnabled(ctx, agentID, id, false); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findLocked(agentID, id)
	if err != nil {
		return err
	}
	next := copyJSON(e.record)
	next.CLIApply.Pending = true
	next.CLIApply.Remove = true
	if err := s.persistLocked(id, &next); err != nil {
		return err
	}
	e.record = next
	s.scheduleCLI()
	return nil
}

func activeCLI(op *cliOperation) bool {
	return op != nil && (op.Status == "running" || op.Status == "pending")
}

// StartCLI records intent before the background task acquires any Agent lifecycle lock.
func (s *Service) StartCLI(_ context.Context, id string, in CLIRequest) (CLIOperation, error) {
	switch in.Action {
	case "connect", "login", "logout", "disconnect", "repair", "check":
	default:
		return CLIOperation{}, ErrInvalid
	}
	if in.OperationID == "" || len(in.OperationID) > 128 {
		return CLIOperation{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findLocked("", id)
	if err != nil {
		return CLIOperation{}, err
	}
	if e.record.CLI == nil {
		return CLIOperation{}, ErrInvalid
	}
	state := e.record.CLI
	if op := state.Queued; op != nil {
		if op.ID == in.OperationID && op.Action == in.Action && op.Revision == in.Revision {
			return op.CLIOperation, nil
		}
		return CLIOperation{}, ErrConflict
	}
	if old, ok := state.Completed[in.OperationID]; ok {
		if old.Action != in.Action || old.Revision != in.Revision {
			return CLIOperation{}, ErrConflict
		}
		return old, nil
	}
	if op := state.Operation; op != nil && op.ID == in.OperationID {
		if op.Action != in.Action || op.Revision != in.Revision {
			return CLIOperation{}, ErrConflict
		}
		return op.CLIOperation, nil
	}
	if in.Revision != e.record.Revision {
		return CLIOperation{}, ErrChanged
	}
	if activeCLI(state.Operation) {
		if state.Operation.Action == in.Action {
			return state.Operation.CLIOperation, nil
		}
		if in.Action != "disconnect" {
			return CLIOperation{}, ErrConflict
		}
		session, err := newInstallationID()
		if err != nil {
			return CLIOperation{}, err
		}
		next := copyJSON(e.record)
		next.CLI.Queued = &cliOperation{CLIOperation: CLIOperation{ID: in.OperationID, SessionID: session, ResourceID: id, Revision: in.Revision, Action: in.Action, Stage: "pending", Status: "pending", UpdatedAt: time.Now().UTC()}}
		next.CLI.Operation.Cancelled = true
		if err := s.persistLocked(id, &next); err != nil {
			return CLIOperation{}, err
		}
		e.record = next
		if s.cliCancel != nil {
			s.cliCancel()
		}
		s.scheduleCLI()
		return next.CLI.Queued.CLIOperation, nil
	}
	session, err := newInstallationID()
	if err != nil {
		return CLIOperation{}, err
	}
	op := &cliOperation{CLIOperation: CLIOperation{ID: in.OperationID, SessionID: session, ResourceID: id, Revision: in.Revision, Action: in.Action, Stage: "pending", Status: "pending", UpdatedAt: time.Now().UTC()}, Workers: map[string]bool{}}
	next := copyJSON(e.record)
	// Failed tasks retain original running intent until a subsequent operation succeeds.
	if old := next.CLI.Operation; old != nil && !old.Settled {
		for worker, running := range old.Workers {
			op.Workers[worker] = running
		}
	}
	if old := next.CLI.Operation; old != nil && !old.Settled && next.CLI.Recovery == nil {
		next.CLI.Recovery = copyJSON(old)
	}
	next.CLI.Operation = op
	if err = s.persistLocked(id, &next); err != nil {
		return CLIOperation{}, err
	}
	e.record = next
	if (in.Action == "connect" || in.Action == "repair") && s.cliMigration != nil && s.cliMigration.ResourceID == id && s.cliMigration.Status != "completed" {
		migration := copyJSON(s.cliMigration)
		migration.Status = "pending"
		if err := s.saveMigrationLocked(migration); err != nil {
			return CLIOperation{}, err
		}
		s.cliMigration = migration
	}
	s.scheduleCLI()
	return op.CLIOperation, nil
}
func (s *Service) CLISession(_ context.Context, id, session string) (CLIOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findLocked("", id)
	if err != nil {
		return CLIOperation{}, err
	}
	if e.record.CLI == nil {
		return CLIOperation{}, ErrInvalid
	}
	if op := e.record.CLI.Queued; op != nil && op.SessionID == session {
		return op.CLIOperation, nil
	}
	if op := e.record.CLI.Operation; op != nil && op.SessionID == session {
		return op.CLIOperation, nil
	}
	for _, op := range e.record.CLI.Completed {
		if op.SessionID == session {
			return op, nil
		}
	}
	return CLIOperation{}, ErrNotFound
}
func (s *Service) CancelCLI(_ context.Context, id, session string) (CLIOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findLocked("", id)
	if err != nil {
		return CLIOperation{}, err
	}
	if e.record.CLI == nil || e.record.CLI.Operation == nil || e.record.CLI.Operation.SessionID != session {
		return CLIOperation{}, ErrNotFound
	}
	op := e.record.CLI.Operation
	if !activeCLI(op) {
		return op.CLIOperation, nil
	}
	next := copyJSON(e.record)
	next.CLI.Operation.Cancelled = true
	if err = s.persistLocked(id, &next); err != nil {
		return CLIOperation{}, err
	}
	e.record = next
	if s.cliCancel != nil {
		s.cliCancel()
	}
	s.scheduleCLI()
	return next.CLI.Operation.CLIOperation, nil
}

// cliBindingNeedsConfiguration includes unfinished removal of a previous configuration.
func cliBindingNeedsConfiguration(r record) bool {
	return r.Enabled || (r.CLIApply != nil && r.CLIApply.Pending)
}

func (s *Service) CheckCLIReady(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.cliMigration; m != nil && m.Status != "completed" && m.Workers[agentID] != nil {
		if m.Status == "waiting_for_legacy_callers" && m.Workers[agentID].Phase == "original" && m.Workers[agentID].Recovered {
			return nil
		}
		return errors.New("Feishu CLI migration is pending")
	}
	for _, e := range s.entries {
		if e.record.AgentID != agentID || e.record.Kind != KindCLI || !cliBindingNeedsConfiguration(e.record) {
			continue
		}
		if e.record.CLIApply != nil && e.record.CLIApply.Pending {
			return errors.New("Feishu connector configuration is pending")
		}
		resource := s.entries[e.record.ResourceID]
		if resource != nil && resource.record.CLI != nil {
			op := resource.record.CLI.Operation
			if op != nil && !op.Settled {
				return errors.New("Feishu connector operation is pending")
			}
		}
	}
	return nil
}
func (s *Service) CLIProjection(agentID string) (*projection.Payload, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.record.AgentID != agentID || e.record.Kind != KindCLI || !e.record.Enabled {
			continue
		}
		resource := s.entries[e.record.ResourceID]
		if resource == nil || resource.record.CLI == nil {
			return nil, 0, ErrNotFound
		}
		r := resource.record
		if r.Disconnected || !cliHasIdentity(r.AuthStatus) || r.AuthStatus.AppID == "" {
			return nil, r.Revision, nil
		}
		return &projection.Payload{AgentID: agentID, ResourceID: r.InstallationID, AppID: r.AuthStatus.AppID, ConfigDir: r.CLI.ConfigDir, Executable: r.CLI.Executable}, r.Revision, nil
	}
	return nil, 0, nil
}
func (s *Service) scheduleCLI() {
	select {
	case s.cliWake <- struct{}{}:
	default:
	}
}
func (s *Service) runCLI() {
	defer s.cliWG.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.cliWake:
		case <-ticker.C:
			s.cliWorkMu.Lock()
			_ = s.cleanupMigration(s.ctx)
			s.cliWorkMu.Unlock()
			continue
		}
		s.cliWorkMu.Lock()
		s.mu.Lock()
		var queued []string
		for id, e := range s.entries {
			if e.record.CLI != nil && e.record.CLI.Queued != nil {
				queued = append(queued, id)
			}
		}
		s.mu.Unlock()
		for _, id := range queued {
			s.promoteQueuedCLI(id)
		}
		if m := s.migrationSnapshot(); m != nil && (m.Status == "pending" || m.Status == "running") {
			_ = s.executeMigration(s.ctx)
		}
		s.mu.Lock()
		var operations, bindings []string
		for id, e := range s.entries {
			if e.record.CLI != nil && activeCLI(e.record.CLI.Operation) {
				operations = append(operations, id)
			}
			if e.record.CLIApply != nil && e.record.CLIApply.Pending && e.record.LastErrorCode == "" && s.cliResourceSettledLocked(e.record.ResourceID) {
				bindings = append(bindings, id)
			}
		}
		s.mu.Unlock()
		sort.Strings(operations)
		sort.Strings(bindings)
		for _, id := range operations {
			s.executeCLI(id)
		}
		for _, id := range bindings {
			if s.ctx.Err() == nil {
				_ = s.applyCLIBinding(s.ctx, id)
			}
		}
		s.cliWorkMu.Unlock()
	}
}
func (s *Service) editCLI(id string, fn func(*record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findLocked("", id)
	if err != nil {
		return err
	}
	next := copyJSON(e.record)
	if err = fn(&next); err != nil {
		return err
	}
	next.UpdatedAt = time.Now().UTC()
	if next.CLI.Operation != nil {
		next.CLI.Operation.UpdatedAt = next.UpdatedAt
	}
	if err = s.persistLocked(id, &next); err != nil {
		return err
	}
	e.record = next
	for _, b := range s.entries {
		if b.record.ResourceID == id {
			hydrateBinding(&b.record, next)
		}
	}
	return nil
}
func (s *Service) cliSnapshot(id string) (record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findLocked("", id)
	if err != nil {
		return record{}, err
	}
	return copyJSON(e.record), nil
}
func (s *Service) executeCLI(id string) {
	defer s.promoteQueuedCLI(id)
	r, err := s.cliSnapshot(id)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	s.cliCancel = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.cliCancel = nil; s.mu.Unlock() }()
	if r.CLI.Operation.Cancelled {
		cancel()
	}
	err = s.performCLI(ctx, id)
	if s.ctx.Err() != nil {
		return
	} // Saved stage and process identity are recovered on startup.
	if err != nil {
		latest, _ := s.cliSnapshot(id)
		cancelled := ctx.Err() != nil
		settled := false
		migration := s.migrationSnapshot()
		migrationReady := migration == nil || migration.ResourceID != id || migration.Status == "completed"
		if migrationReady && latest.CLI != nil && latest.CLI.Recovery == nil && !latest.CLI.Operation.Committed {
			recovery, stop := context.WithTimeout(s.ctx, 30*time.Second)
			restoreErr := s.restoreCLIConfig(id)
			if restoreErr == nil {
				restoreErr = s.recoverCLIStatus(recovery, id)
			}
			if restoreErr == nil {
				restoreErr = s.applyCLIWorkers(recovery, id)
			}
			stop()
			settled = restoreErr == nil
		}
		_ = s.editCLI(id, func(r *record) error {
			op := r.CLI.Operation
			op.Settled = settled
			op.Status = "failed"
			op.ErrorCode = "cli_operation_failed"
			var commandErr *larkcli.OperationError
			if errors.As(err, &commandErr) {
				op.ErrorCode = commandErr.Code
				if settled && commandErr.Code == "authorization_expired" {
					op.Status = "expired"
				}
				if settled && commandErr.Code == "authorization_denied" {
					op.Status = "denied"
				}
			}
			if cancelled && settled {
				op.Status = "cancelled"
				op.ErrorCode = ""
			}
			if !settled {
				op.ErrorCode = "cleanup_pending"
			}
			op.URL = ""
			op.DeviceCode = ""
			r.LastErrorCode = op.ErrorCode
			r.LastError = "Feishu connector operation could not be completed; retry the operation."
			if r.CLI.Completed == nil {
				r.CLI.Completed = map[string]CLIOperation{}
			}
			r.CLI.Completed[op.ID] = op.CLIOperation
			return nil
		})
		return
	}

	_ = s.editCLI(id, func(r *record) error {
		op := r.CLI.Operation
		op.Settled = true
		op.Status = "completed"
		op.Stage = "completed"
		op.URL = ""
		op.DeviceCode = ""
		op.ErrorCode = ""
		r.LastErrorCode = ""
		r.LastError = ""
		if r.CLI.Completed == nil {
			r.CLI.Completed = map[string]CLIOperation{}
		}
		r.CLI.Completed[op.ID] = op.CLIOperation
		return nil
	})
}
func (s *Service) performCLI(ctx context.Context, id string) error {
	r, err := s.cliSnapshot(id)
	if err != nil {
		return err
	}
	if m := s.migrationSnapshot(); m != nil && m.ResourceID == id && m.Status != "completed" {
		return errors.New("waiting_for_legacy_callers")
	}
	op := r.CLI.Operation
	if err = larkcli.RecoverProcess(op.Process); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.editCLI(id, func(r *record) error {
		r.CLI.Operation.Status = "running"
		r.CLI.Operation.Process = larkcli.ProcessRef{}
		return nil
	}); err != nil {
		return err
	}
	if err = s.suspendCLIWorkers(ctx, id); err != nil {
		return err
	}
	if r.CLI.Recovery != nil {
		if err = larkcli.RecoverProcess(r.CLI.Recovery.Process); err != nil {
			return err
		}
		if err = s.restoreCLIFile(id, r, r.CLI.Recovery); err != nil {
			return err
		}
		if err = s.editCLI(id, func(r *record) error { r.CLI.Recovery = nil; return nil }); err != nil {
			return err
		}
	}
	if op.Committed {
		return s.applyCLIWorkers(ctx, id)
	}
	if err = s.backupCLIConfig(id); err != nil {
		return err
	}
	if op.Action != "disconnect" {
		install := func(ctx context.Context, root string, progress func(string)) (larkcli.Package, error) {
			return (larkcli.Installer{Root: root}).Install(ctx, progress)
		}
		if s.options.CLI != nil && s.options.CLI.Install != nil {
			install = s.options.CLI.Install
		}
		pkg, err := install(ctx, filepath.Join(s.dataRoot, id, "package"), func(stage string) {
			_ = s.editCLI(id, func(r *record) error { r.CLI.Operation.Stage = stage; return nil })
		})
		if err != nil {
			return err
		}
		if err = s.editCLI(id, func(r *record) error {
			r.CLI.Executable = pkg.Executable
			r.CLI.SkillsDir = pkg.SkillsDir
			r.PackageVersion = pkg.Version
			r.SkillVersion = larkcli.SkillsCommit
			return nil
		}); err != nil {
			return err
		}
	}
	r, err = s.cliSnapshot(id)
	if err != nil {
		return err
	}
	client := larkcli.Client{Executable: r.CLI.Executable, ConfigDir: r.CLI.ConfigDir, OperationID: op.ID, Process: func(ref larkcli.ProcessRef) error {
		return s.editCLI(id, func(r *record) error { r.CLI.Operation.Process = ref; return nil })
	}, URL: func(url string) { _ = s.editCLI(id, func(r *record) error { r.CLI.Operation.URL = url; return nil }) }}
	if s.options.CLI != nil {
		client.Runner = s.options.CLI.Run
	}
	var status *larkcli.Status
	if op.Action == "disconnect" {
		if _, configErr := os.Stat(client.ConfigPath()); configErr == nil {
			if err = client.Logout(ctx); err != nil {
				return err
			}
		} else if !errors.Is(configErr, os.ErrNotExist) {
			return configErr
		}
		if err = client.Disconnect(); err != nil {
			return err
		}
	} else {
		current, statusErr := client.Status(ctx, true)
		switch op.Action {
		case "connect", "repair":
			if statusErr != nil {
				if _, configErr := larkcli.ReadConfigIdentity(client.ConfigPath()); configErr == nil {
					return statusErr
				}
			}
			if statusErr != nil || !current.Identities.Bot.Available {
				if err = s.editCLI(id, func(r *record) error { r.CLI.Operation.Stage = "app_authorization"; return nil }); err != nil {
					return err
				}
				if err = client.Init(ctx); err != nil {
					return err
				}
				current, statusErr = client.Status(ctx, true)
				if statusErr == nil {
					if err := s.keepCLIApplication(id, current); err != nil {
						return err
					}
				}
			}
			if statusErr != nil {
				return statusErr
			}
			fallthrough
		case "login":
			if statusErr != nil {
				return statusErr
			}
			if !current.Identities.User.Available || current.Identities.User.Verified == nil || !*current.Identities.User.Verified {
				if err = s.editCLI(id, func(r *record) error {
					r.CLI.Operation.Stage = "user_authorization"
					r.CLI.Operation.URL = ""
					return nil
				}); err != nil {
					return err
				}
				device := larkcli.DeviceAuthorization{DeviceCode: op.DeviceCode, URL: op.URL, ExpiresIn: int64(time.Until(op.ExpiresAt).Seconds()), RequestedScope: r.RequestedScopes}
				if device.DeviceCode == "" || device.ExpiresIn <= 0 || !larkcli.CurrentUserAuthorizationScopes(device.RequestedScope) {
					device, err = client.Login(ctx)
					if err != nil {
						return err
					}
				}
				if err = s.editCLI(id, func(r *record) error {
					r.RequestedScopes = device.RequestedScope
					r.CLI.Operation.URL = device.URL
					r.CLI.Operation.DeviceCode = device.DeviceCode
					r.CLI.Operation.ExpiresAt = time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
					return nil
				}); err != nil {
					return err
				}
				authCtx, cancel := context.WithTimeout(ctx, time.Duration(device.ExpiresIn)*time.Second)
				err = client.WaitLogin(authCtx, device.DeviceCode)
				cancel()
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
						return &larkcli.OperationError{Code: "authorization_expired"}
					}
					return err
				}
			}
			current, err = client.Status(ctx, true)
			if err != nil {
				return err
			}
			if !current.Identities.User.Available || current.Identities.User.OpenID == "" || current.Identities.User.Verified == nil || !*current.Identities.User.Verified {
				return errors.New("personal authorization is unavailable")
			}
			if err = client.EnablePersonalIdentity(ctx); err != nil {
				return err
			}
		case "logout":
			if err = client.Logout(ctx); err != nil {
				return err
			}
		}
		current, err = client.Status(ctx, true)
		if err != nil {
			return err
		}
		status = &current
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.editCLI(id, func(r *record) error {
		if r.CLI.Operation.Cancelled {
			return context.Canceled
		}
		r.AuthStatus = status
		r.CheckedAt = time.Now().UTC()
		r.Disconnected = op.Action == "disconnect"
		if r.Disconnected {
			r.RequestedScopes = ""
		}
		r.Revision++
		r.CLI.Operation.Committed = true
		r.CLI.Operation.Stage = "applying"
		r.CLI.Operation.URL = ""
		r.CLI.Operation.DeviceCode = ""
		return nil
	}); err != nil {
		return err
	}
	return s.applyCLIWorkers(ctx, id)
}
func (s *Service) suspendCLIWorkers(ctx context.Context, id string) error {
	s.mu.Lock()
	var workers []string
	for _, e := range s.entries {
		if e.record.ResourceID == id && cliBindingNeedsConfiguration(e.record) {
			workers = append(workers, e.record.AgentID)
		}
	}
	s.mu.Unlock()
	sort.Strings(workers)
	if len(workers) == 0 {
		return nil
	}
	if s.options.CLI == nil || s.options.CLI.Runtime == nil {
		return errors.New("CLI runtime support is unavailable")
	}
	rt := s.options.CLI.Runtime
	for _, worker := range workers {
		r, err := s.cliSnapshot(id)
		if err != nil {
			return err
		}
		if _, ok := r.CLI.Operation.Workers[worker]; !ok {
			running, err := rt.State(ctx, worker)
			s.mu.Lock()
			for _, b := range s.entries {
				if b.record.AgentID == worker && b.record.ResourceID == id && b.record.CLIApply != nil && b.record.CLIApply.ResumeRecorded {
					running = running || b.record.CLIApply.Resume
				}
			}
			s.mu.Unlock()
			if err != nil {
				if !s.hasCLIBinding(id, worker) {
					continue
				}
				return err
			}
			if err = s.editCLI(id, func(r *record) error {
				if r.CLI.Operation.Workers == nil {
					r.CLI.Operation.Workers = map[string]bool{}
				}
				r.CLI.Operation.Workers[worker] = running
				return nil
			}); err != nil {
				return err
			}
		}
		if err = rt.Update(ctx, worker, false, nil); err != nil {
			if !s.hasCLIBinding(id, worker) {
				continue
			}
			return err
		}
	}
	return nil
}
func (s *Service) backupCLIConfig(id string) error {
	r, err := s.cliSnapshot(id)
	if err != nil {
		return err
	}
	if r.CLI.Operation.BackupTaken {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(r.CLI.ConfigDir, "config.json"))
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists {
		if err = larkcli.AtomicWrite(filepath.Join(s.dataRoot, id, "operations", r.CLI.Operation.SessionID, "config.json"), data); err != nil {
			return err
		}
	}
	return s.editCLI(id, func(r *record) error {
		r.CLI.Operation.BackupTaken = true
		r.CLI.Operation.ConfigExisted = exists
		return nil
	})
}
func (s *Service) restoreCLIConfig(id string) error {
	r, err := s.cliSnapshot(id)
	if err != nil {
		return err
	}
	return s.restoreCLIFile(id, r, r.CLI.Operation)
}
func (s *Service) restoreCLIFile(id string, r record, op *cliOperation) error {
	var err error
	if !op.BackupTaken || op.Committed {
		return nil
	}
	if err = larkcli.RecoverProcess(op.Process); err != nil {
		return err
	}
	target := filepath.Join(r.CLI.ConfigDir, "config.json")
	if !op.ConfigExisted {
		return (larkcli.Client{ConfigDir: r.CLI.ConfigDir}).Disconnect()
	}
	data, err := os.ReadFile(filepath.Join(s.dataRoot, id, "operations", op.SessionID, "config.json"))
	if err != nil {
		return err
	}
	return larkcli.AtomicWrite(target, data)
}
func (s *Service) applyCLIWorkers(ctx context.Context, id string) error {
	s.mu.Lock()
	var bindings []string
	for bid, e := range s.entries {
		if e.record.ResourceID != id || !cliBindingNeedsConfiguration(e.record) {
			continue
		}
		next := copyJSON(e.record)
		next.LastErrorCode = ""
		next.LastError = ""
		if next.CLIApply == nil {
			next.CLIApply = &cliBinding{}
		}
		next.CLIApply.Pending = true
		resource := s.entries[id]
		if resource != nil && resource.record.CLI.Operation != nil {
			if running, ok := resource.record.CLI.Operation.Workers[next.AgentID]; ok {
				next.CLIApply.Resume = running
				next.CLIApply.ResumeRecorded = true
			}
		}
		if err := s.persistLocked(bid, &next); err != nil {
			s.mu.Unlock()
			return err
		}
		e.record = next
		bindings = append(bindings, bid)
	}
	s.mu.Unlock()
	sort.Strings(bindings)
	var errs []error
	for _, bid := range bindings {
		if err := s.applyCLIBinding(ctx, bid); err != nil {
			s.mu.Lock()
			exists := s.entries[bid] != nil
			s.mu.Unlock()
			if exists {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
func (s *Service) applyCLIBinding(ctx context.Context, id string) (result error) {
	s.mu.Lock()
	e := s.entries[id]
	if e == nil || e.record.CLIApply == nil || !e.record.CLIApply.Pending {
		s.mu.Unlock()
		return nil
	}
	r := copyJSON(e.record)
	resource := copyJSON(s.entries[r.ResourceID].record)
	s.mu.Unlock()
	defer func() {
		if result == nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if e := s.entries[id]; e != nil {
			e.record.LastErrorCode = "configuration_failed"
			e.record.LastError = "Feishu connector configuration needs repair."
			e.record.Status = "error"
			_ = s.persistLocked(id, &e.record)
		}
	}()
	if s.options.CLI == nil || s.options.CLI.Runtime == nil {
		return errors.New("CLI runtime support is unavailable")
	}
	rt := s.options.CLI.Runtime
	if !r.CLIApply.ResumeRecorded {
		running, err := rt.State(ctx, r.AgentID)
		if err != nil {
			return err
		}
		r.CLIApply.Resume = running
		r.CLIApply.ResumeRecorded = true
		s.mu.Lock()
		e = s.entries[id]
		if e == nil {
			s.mu.Unlock()
			return ErrNotFound
		}
		e.record.CLIApply = r.CLIApply
		err = s.persistLocked(id, &e.record)
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	configured := r.Enabled && !resource.Disconnected && cliHasIdentity(resource.AuthStatus)
	err := rt.Update(ctx, r.AgentID, r.CLIApply.Resume, func(ctx context.Context) error {
		previous := r.Skills
		if r.CLIApply.Plan != nil && (r.CLIApply.PlannedEnabled != configured || r.CLIApply.PlannedRevision != resource.Revision) {
			if err := rt.ApplySkills(ctx, r.AgentID, *r.CLIApply.Plan); err != nil {
				return err
			}
			previous = r.CLIApply.Plan.After
			r.CLIApply.Plan = nil
		}
		if r.CLIApply.Plan == nil {
			plan, err := rt.PlanSkills(r.AgentID, resource.CLI.SkillsDir, resource.SkillVersion, previous, configured)
			if err != nil {
				return err
			}
			r.CLIApply.Plan = &plan
			r.CLIApply.PlannedEnabled = configured
			r.CLIApply.PlannedRevision = resource.Revision
			s.mu.Lock()
			e = s.entries[id]
			if e == nil {
				s.mu.Unlock()
				return ErrNotFound
			}
			e.record.CLIApply = r.CLIApply
			err = s.persistLocked(id, &e.record)
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
		if m := s.migrationSnapshot(); m != nil && m.Workers[r.AgentID] != nil && !m.Workers[r.AgentID].Committed {
			if err := s.editMigration(func(m *cliMigration) error {
				if w := m.Workers[r.AgentID]; w != nil {
					w.Plan = copyJSON(r.CLIApply.Plan)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if err := rt.ApplySkills(ctx, r.AgentID, *r.CLIApply.Plan); err != nil {
			return err
		}
		var p *projection.Payload
		if configured {
			p = &projection.Payload{AgentID: r.AgentID, ResourceID: r.ResourceID, AppID: resource.AuthStatus.AppID, ConfigDir: resource.CLI.ConfigDir, Executable: resource.CLI.Executable}
		}
		return rt.Configure(ctx, r.AgentID, p)
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e = s.entries[id]
	if e == nil {
		return ErrNotFound
	}
	next := copyJSON(e.record)
	if next.CLIApply.Remove && !configured {
		if err := s.persistLocked(id, nil); err != nil {
			return err
		}
		delete(s.entries, id)
		return nil
	}
	next.AppliedRevision = resource.Revision
	next.Skills = r.CLIApply.Plan.After
	next.CLIApply = &cliBinding{}
	next.LastErrorCode = ""
	next.LastError = ""
	next.Status = "needs_configuration"
	if configured {
		next.Status = "connected"
	}
	next.UpdatedAt = time.Now().UTC()
	if err = s.persistLocked(id, &next); err != nil {
		return err
	}
	e.record = next
	return nil
}

func (s *Service) cliViewLocked(e *entry) Installation {
	item := view(e)
	resource := e
	if item.AgentID != "" {
		resource = s.entries[item.ResourceID]
	}
	if resource == nil {
		return item
	}
	r := resource.record
	item.AuthStatus = copyJSON(r.AuthStatus)
	item.Revision = r.Revision
	if r.AuthStatus != nil {
		item.FeishuAppID = r.AuthStatus.AppID
	}
	if r.CLI != nil && r.CLI.Operation != nil {
		op := r.CLI.Operation.CLIOperation
		item.Operation = &op
	}
	if item.AgentID != "" {
		if e.record.CLIApply != nil && e.record.CLIApply.Pending {
			item.Status = "applying"
			if item.LastErrorCode != "" {
				item.Status = "error"
			}
		} else if !item.Enabled {
			item.Status = "disabled"
		} else if !cliHasIdentity(r.AuthStatus) || r.Disconnected {
			item.Status = "needs_configuration"
		} else if item.AppliedRevision == r.Revision {
			item.Status = "connected"
		}
		return item
	}
	item.Migration = s.migrationViewLocked(item.InstallationID)
	if item.Migration != nil && item.Migration.Status != "completed" {
		item.LastErrorCode = "migration_failed"
	}
	item.ResourceID = item.InstallationID
	item.ResourceEnabled = true
	item.Status = "needs_configuration"
	if cliHasIdentity(r.AuthStatus) && !r.Disconnected {
		item.Status = "connected"
	}
	if r.CLI != nil && activeCLI(r.CLI.Operation) {
		item.Status = "connecting"
	} else if item.LastErrorCode != "" {
		item.Status = "error"
	}
	item.Bindings = []BindingSummary{}
	for _, bound := range s.entries {
		if bound.record.ResourceID == item.InstallationID {
			b := s.cliViewLocked(bound)
			item.Bindings = append(item.Bindings, BindingSummary{InstallationID: b.InstallationID, AgentID: b.AgentID, Enabled: b.Enabled, Status: b.Status})
		}
	}
	sort.Slice(item.Bindings, func(i, j int) bool { return item.Bindings[i].AgentID < item.Bindings[j].AgentID })
	return item
}

func (s *Service) cliResourceSettledLocked(id string) bool {
	if s.cliMigration != nil && s.cliMigration.ResourceID == id && s.cliMigration.Status != "completed" {
		return false
	}
	e := s.entries[id]
	return e != nil && e.record.CLI != nil && (e.record.CLI.Operation == nil || e.record.CLI.Operation.Settled)
}

func (s *Service) promoteQueuedCLI(id string) {
	if s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[id]
	if e == nil || e.record.CLI == nil || e.record.CLI.Queued == nil || activeCLI(e.record.CLI.Operation) {
		return
	}
	next := copyJSON(e.record)
	previous := next.CLI.Operation
	op := next.CLI.Queued
	op.Workers = map[string]bool{}
	if previous != nil {
		for id, running := range previous.Workers {
			op.Workers[id] = running
		}
		if !previous.Settled && next.CLI.Recovery == nil {
			next.CLI.Recovery = previous
		}
	}
	next.CLI.Operation = op
	next.CLI.Queued = nil
	if err := s.persistLocked(id, &next); err != nil {
		return
	}
	e.record = next
	s.scheduleCLI()
}

func (s *Service) keepCLIApplication(id string, status larkcli.Status) error {
	r, err := s.cliSnapshot(id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(r.CLI.ConfigDir, "config.json"))
	if err != nil {
		return err
	}
	if err := larkcli.AtomicWrite(filepath.Join(s.dataRoot, id, "operations", r.CLI.Operation.SessionID, "config.json"), data); err != nil {
		return err
	}
	return s.editCLI(id, func(r *record) error {
		r.CLI.Operation.BackupTaken = true
		r.CLI.Operation.ConfigExisted = true
		r.AuthStatus = &status
		r.Disconnected = false
		r.Revision++
		return nil
	})
}

func (s *Service) recoverCLIStatus(ctx context.Context, id string) error {
	r, err := s.cliSnapshot(id)
	if err != nil {
		return err
	}
	if !r.CLI.Operation.BackupTaken {
		return nil
	}
	var status *larkcli.Status
	if _, err := os.Stat(filepath.Join(r.CLI.ConfigDir, "config.json")); err == nil {
		client := larkcli.Client{Executable: r.CLI.Executable, ConfigDir: r.CLI.ConfigDir, OperationID: r.CLI.Operation.ID, Process: func(ref larkcli.ProcessRef) error {
			return s.editCLI(id, func(r *record) error { r.CLI.Operation.Process = ref; return nil })
		}}
		if s.options.CLI != nil {
			client.Runner = s.options.CLI.Run
		}
		current, err := client.Status(ctx, false)
		if err != nil {
			return err
		}
		if r.AuthStatus != nil && r.AuthStatus.AppID == current.AppID {
			if current.Identities.Bot.Verified == nil {
				current.Identities.Bot.Verified = r.AuthStatus.Identities.Bot.Verified
			}
			if current.Identities.User.Verified == nil && current.Identities.User.OpenID != "" && current.Identities.User.OpenID == r.AuthStatus.Identities.User.OpenID {
				current.Identities.User.Verified = r.AuthStatus.Identities.User.Verified
			}
		}
		status = &current
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.editCLI(id, func(r *record) error { r.AuthStatus = status; r.CheckedAt = time.Now().UTC(); return nil })
}

func (s *Service) hasCLIBinding(resourceID, agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.record.ResourceID == resourceID && e.record.AgentID == agentID {
			return true
		}
	}
	return false
}

func cliHasIdentity(status *larkcli.Status) bool {
	if status == nil {
		return false
	}
	available := func(identity larkcli.Identity) bool {
		return identity.Available && (identity.Verified == nil || *identity.Verified)
	}
	return available(status.Identities.Bot) || available(status.Identities.User)
}
