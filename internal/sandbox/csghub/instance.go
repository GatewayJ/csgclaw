package csghub

import (
	"context"
	"fmt"
	"strings"

	"csgclaw/internal/sandbox"
	"csgclaw/internal/sandbox/csghubsdk"
)

// Instance implements sandbox.Instance by delegating to CSGHub Sandbox
// HTTP routes keyed on the sandbox name. `last` caches the most recent
// Response observed by the runtime so callers can read the current
// Spec/State without an extra round-trip.
type Instance struct {
	runtime *Runtime
	name    string
	last    *csghubsdk.Response
}

var _ sandbox.Instance = (*Instance)(nil)

// Name returns the Hub-side sandbox name (the primary key).
func (i *Instance) Name() string { return i.name }

// Start transitions the sandbox to Running, honoring the same
// fallback-on-unsupported-endpoint behavior Reconcile uses.
func (i *Instance) Start(ctx context.Context) error {
	if err := i.valid(); err != nil {
		return err
	}
	resp, err := i.runtime.startOrAssumeAutoStart(ctx, i.name, i.last)
	if err != nil {
		return err
	}
	i.last = resp
	return nil
}

// Stop calls the Hub stop endpoint. Force / Timeout are ignored: the
// Hub decides the teardown primitive.
func (i *Instance) Stop(ctx context.Context, _ sandbox.StopOptions) error {
	if err := i.valid(); err != nil {
		return err
	}
	err := i.runtime.client.Stop(ctx, i.name)
	if csghubsdk.IsNotFound(err) {
		return fmt.Errorf("%w: %w", sandbox.ErrNotFound, err)
	}
	return err
}

// Info returns runtime-neutral metadata. It issues a fresh Hub GET so
// the State reflects current reality.
func (i *Instance) Info(ctx context.Context) (sandbox.Info, error) {
	if err := i.valid(); err != nil {
		return sandbox.Info{}, err
	}
	resp, err := i.runtime.client.Get(ctx, i.name)
	if err != nil {
		if csghubsdk.IsNotFound(err) {
			return sandbox.Info{}, fmt.Errorf("%w: %w", sandbox.ErrNotFound, err)
		}
		return sandbox.Info{}, err
	}
	i.last = resp
	return infoFromResponse(resp, i.name), nil
}

// CachedInfo returns the most recent Response observed by Reconcile /
// Start / Get / Info, converted to runtime-neutral sandbox.Info. It
// performs no HTTP round-trip. ok is false when no Response has been
// cached yet (raw handles minted via Runtime.Instance(name) without a
// subsequent call). Callers that need guaranteed-live state must use
// Info(ctx) instead.
//
// The primary consumer is the agent layer right after Runtime.Create,
// where the returned instance already carries the authoritative Response
// Create validated, and an additional GET would be wasted work.
func (i *Instance) CachedInfo() (sandbox.Info, bool) {
	if i == nil || i.last == nil {
		return sandbox.Info{}, false
	}
	return infoFromResponse(i.last, i.name), true
}

// Run executes a command inside the sandbox via the AI gateway
// stream-execute endpoint. Stdout and Stderr (if set on the spec) are
// both written with the raw line from the gateway; the Hub does not
// demultiplex stdout/stderr on this route.
//
// The returned ExitCode is zero on successful stream completion. Since
// StreamExecute does not expose the guest process exit code, callers
// that require it should use sandbox-runtime's /run endpoint directly
// (exposed on *csghubsdk.Client).
func (i *Instance) Run(ctx context.Context, spec sandbox.CommandSpec) (sandbox.CommandResult, error) {
	if err := i.valid(); err != nil {
		return sandbox.CommandResult{}, err
	}
	if strings.TrimSpace(spec.Name) == "" {
		return sandbox.CommandResult{}, fmt.Errorf("invalid sandbox command: name is required")
	}
	command := spec.Name
	if len(spec.Args) > 0 {
		command = command + " " + strings.Join(spec.Args, " ")
	}
	err := i.runtime.client.StreamExecute(ctx, i.name, command, func(line string) error {
		if spec.Stdout != nil {
			if _, werr := spec.Stdout.Write([]byte(line + "\n")); werr != nil {
				return werr
			}
		}
		if spec.Stderr != nil {
			if _, werr := spec.Stderr.Write([]byte(line + "\n")); werr != nil {
				return werr
			}
		}
		return nil
	})
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	return sandbox.CommandResult{ExitCode: 0}, nil
}

// StreamExecute is a thin pass-through to csghubsdk.Client.StreamExecute
// preserving the emit-callback contract used by log tailing.
func (i *Instance) StreamExecute(ctx context.Context, command string, emit func(line string) error) error {
	if err := i.valid(); err != nil {
		return err
	}
	return i.runtime.client.StreamExecute(ctx, i.name, command, emit)
}

// Close releases the Instance handle. No Hub resources to reclaim.
func (i *Instance) Close() error { return nil }

func (i *Instance) valid() error {
	if i == nil || i.runtime == nil || i.runtime.client == nil {
		return fmt.Errorf("invalid csghub instance")
	}
	if strings.TrimSpace(i.name) == "" {
		return fmt.Errorf("csghub sandbox name is required")
	}
	return nil
}

// infoFromResponse converts a Hub Response into the runtime-neutral
// sandbox.Info contract. The ID is the sandbox name because the Hub
// never exposes its internal UUID on the wire.
func infoFromResponse(resp *csghubsdk.Response, fallbackName string) sandbox.Info {
	name := strings.TrimSpace(resp.Spec.SandboxName)
	if name == "" {
		name = fallbackName
	}
	return sandbox.Info{
		ID:        name,
		Name:      name,
		State:     mapState(resp.State.Status),
		CreatedAt: resp.State.CreatedAt.UTC(),
	}
}

func mapState(status string) sandbox.State {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "ready":
		return sandbox.StateRunning
	case "stopped", "terminated", "dead":
		return sandbox.StateStopped
	case "failed", "error", "errored", "crashed":
		return sandbox.StateExited
	case "creating", "pending", "deploying", "starting":
		return sandbox.StateCreated
	case "":
		return sandbox.StateUnknown
	}
	return sandbox.StateUnknown
}
