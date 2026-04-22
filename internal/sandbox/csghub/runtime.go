package csghub

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"csgclaw/internal/sandbox"
	"csgclaw/internal/sandbox/csghubsdk"
)

// Runtime implements sandbox.Runtime on top of the CSGHub Sandbox HTTP
// API. It owns the reconcile-and-wait lifecycle (previously embedded in
// internal/agent/service_csghub.go) so that, from the agent layer's
// point of view, Create/Reconcile returns an Instance only after the
// Hub has reported Running.
type Runtime struct {
	client *csghubsdk.Client
	params Params
}

var _ sandbox.Runtime = (*Runtime)(nil)

// Create applies desired-state lifecycle and returns only after sandbox
// reaches Running/Ready (or returns a clear failure).
func (r *Runtime) Create(ctx context.Context, spec sandbox.CreateSpec) (sandbox.Instance, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("invalid csghub runtime")
	}
	req := r.createRequestFromSpec(spec)
	name := req.SandboxName
	begin := time.Now()
	log.Printf(
		"component=csghub_sandbox sandbox=%q phase=reconcile event=start force_recreate=%t image=%q mounts=%d env=%d",
		name,
		false,
		req.Image,
		len(req.Volumes),
		len(req.Environments),
	)
	seed, err := r.ensureSandboxStarted(ctx, req, false)
	if err != nil {
		log.Printf("component=csghub_sandbox sandbox=%q phase=reconcile event=fail step=ensure_start elapsed_ms=%d err=%q", name, time.Since(begin).Milliseconds(), err)
		return nil, err
	}
	resp, err := r.waitForRunning(ctx, name, seed)
	if err != nil {
		log.Printf("component=csghub_sandbox sandbox=%q phase=reconcile event=fail step=wait_running elapsed_ms=%d err=%q", name, time.Since(begin).Milliseconds(), err)
		return nil, err
	}
	if err := validateCreateResponse(resp, name); err != nil {
		log.Printf("component=csghub_sandbox sandbox=%q phase=reconcile event=fail step=validate_response elapsed_ms=%d err=%q", name, time.Since(begin).Milliseconds(), err)
		return nil, err
	}
	log.Printf("component=csghub_sandbox sandbox=%q phase=reconcile event=success elapsed_ms=%d status=%q", name, time.Since(begin).Milliseconds(), strings.TrimSpace(resp.State.Status))
	return &Instance{runtime: r, name: name, last: resp}, nil
}

// Get returns a lightweight Instance handle by sandbox name. It does
// one Hub GET so callers get a live Response snapshot.
func (r *Runtime) Get(ctx context.Context, idOrName string) (sandbox.Instance, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("invalid csghub runtime")
	}
	resp, err := r.client.Get(ctx, idOrName)
	if err != nil {
		if csghubsdk.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %w", sandbox.ErrNotFound, err)
		}
		return nil, err
	}
	name := strings.TrimSpace(resp.Spec.SandboxName)
	if name == "" {
		name = idOrName
	}
	return &Instance{runtime: r, name: name, last: resp}, nil
}

// Remove stops a sandbox. The Hub has no DELETE endpoint; stop is the
// teardown primitive (matching pycsghub semantics). Force is ignored.
func (r *Runtime) Remove(ctx context.Context, idOrName string, _ sandbox.RemoveOptions) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("invalid csghub runtime")
	}
	err := r.client.Stop(ctx, idOrName)
	if csghubsdk.IsNotFound(err) {
		return fmt.Errorf("%w: %w", sandbox.ErrNotFound, err)
	}
	return err
}

// Close releases runtime resources. The HTTP client is goroutine-safe
// and has no explicit close, so this is a no-op today.
func (r *Runtime) Close() error { return nil }

// Instance returns a lightweight Instance by name without issuing any
// HTTP calls. Handy when the caller already knows the sandbox exists
// and just needs a handle (e.g. to call StreamExecute for log tailing).
func (r *Runtime) Instance(name string) *Instance {
	return &Instance{runtime: r, name: strings.TrimSpace(name)}
}

// StreamExecute is a convenience wrapper equivalent to
// runtime.Instance(name).StreamExecute(ctx, cmd, emit). It saves the
// caller one allocation when no Instance handle is needed.
func (r *Runtime) StreamExecute(ctx context.Context, name, command string, emit func(line string) error) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("invalid csghub runtime")
	}
	return r.client.StreamExecute(ctx, name, command, emit)
}

// Params exposes the runtime configuration. Read-only accessor used by
// tests that need to observe the values the provider computed.
func (r *Runtime) Params() Params { return r.params }

// Client exposes the underlying HTTP client. Used by specialized
// callers that need access to endpoints not surfaced on Runtime /
// Instance.
func (r *Runtime) Client() *csghubsdk.Client { return r.client }

// createRequestFromSpec folds the generic CreateSpec with the provider
// Params to produce a Hub CreateRequest. Provider-managed fields
// (ClusterID / ResourceID / Port / Timeout) always come from Params.
func (r *Runtime) createRequestFromSpec(spec sandbox.CreateSpec) csghubsdk.CreateRequest {
	volumes := make([]csghubsdk.VolumeSpec, 0, len(spec.Mounts))
	for _, m := range spec.Mounts {
		volumes = append(volumes, csghubsdk.VolumeSpec{
			SandboxMountSubpath: r.params.resolveMountHostPath(m.HostPath),
			SandboxMountPath:    m.GuestPath,
			ReadOnly:            m.ReadOnly,
		})
	}
	return csghubsdk.CreateRequest{
		Image:        strings.TrimSpace(spec.Image),
		ClusterID:    r.params.ClusterID,
		ResourceID:   r.params.ResourceID,
		SandboxName:  strings.TrimSpace(spec.Name),
		Environments: spec.Env,
		Volumes:      volumes,
		Port:         r.params.Port,
		Timeout:      r.params.Timeout,
	}
}

// ensureSandboxStarted performs the create / start handshake against
// the Hub but does NOT wait for the Running state. It returns the
// latest Response observed in this turn (authoritative for spec,
// non-authoritative for state).
func (r *Runtime) ensureSandboxStarted(ctx context.Context, req csghubsdk.CreateRequest, forceRecreate bool) (*csghubsdk.Response, error) {
	name := req.SandboxName
	probeBegin := time.Now()
	existing, err := r.client.Get(ctx, name)
	switch {
	case err == nil:
		status := strings.ToLower(strings.TrimSpace(existing.State.Status))
		log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=probe_hit elapsed_ms=%d status=%q", name, time.Since(probeBegin).Milliseconds(), status)
		if forceRecreate {
			log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=force_recreate status=%q", name, status)
			stopBegin := time.Now()
			if err := r.client.Stop(ctx, name); err != nil && !csghubsdk.IsNotFound(err) {
				return nil, fmt.Errorf("stop sandbox %q: %w", name, err)
			}
			log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=stop_done elapsed_ms=%d", name, time.Since(stopBegin).Milliseconds())
			applyBegin := time.Now()
			if _, err := r.client.Apply(ctx, req); err != nil {
				return nil, fmt.Errorf("apply sandbox %q: %w", name, err)
			}
			log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=apply_done elapsed_ms=%d", name, time.Since(applyBegin).Milliseconds())
			return r.startOrAssumeAutoStart(ctx, name, existing)
		}
		if isSandboxUpOrComingUp(status) {
			log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=start_skipped reason=already_up status=%q", name, status)
			return existing, nil
		}
		return r.startOrAssumeAutoStart(ctx, name, existing)
	case csghubsdk.IsNotFound(err):
		log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=probe_miss_not_found elapsed_ms=%d", name, time.Since(probeBegin).Milliseconds())
		createBegin := time.Now()
		created, err := r.client.Create(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("create sandbox %q: %w", name, err)
		}
		log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=create_done elapsed_ms=%d", name, time.Since(createBegin).Milliseconds())
		status := strings.ToLower(strings.TrimSpace(created.State.Status))
		if isSandboxUpOrComingUp(status) {
			log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=start_skipped reason=post_create_up status=%q", name, status)
			return created, nil
		}
		return r.startOrAssumeAutoStart(ctx, name, created)
	default:
		log.Printf("component=csghub_sandbox sandbox=%q phase=ensure_start event=probe_fail elapsed_ms=%d err=%q", name, time.Since(probeBegin).Milliseconds(), err)
		return nil, fmt.Errorf("probe sandbox %q: %w", name, err)
	}
}

// startOrAssumeAutoStart issues PUT /status/start, falling back to
// `fallback` on Hub deployments that omit the endpoint (404/405/501).
func (r *Runtime) startOrAssumeAutoStart(ctx context.Context, name string, fallback *csghubsdk.Response) (*csghubsdk.Response, error) {
	startBegin := time.Now()
	log.Printf("component=csghub_sandbox sandbox=%q phase=start event=request", name)
	started, err := r.client.Start(ctx, name)
	if err != nil {
		if isStartUnsupported(err) {
			log.Printf("component=csghub_sandbox sandbox=%q phase=start event=unsupported_assume_autostart elapsed_ms=%d", name, time.Since(startBegin).Milliseconds())
			return fallback, nil
		}
		log.Printf("component=csghub_sandbox sandbox=%q phase=start event=fail elapsed_ms=%d err=%q", name, time.Since(startBegin).Milliseconds(), err)
		return nil, fmt.Errorf("start sandbox %q: %w", name, err)
	}
	log.Printf("component=csghub_sandbox sandbox=%q phase=start event=success elapsed_ms=%d status=%q", name, time.Since(startBegin).Milliseconds(), strings.TrimSpace(started.State.Status))
	return started, nil
}

// waitForRunning blocks until the sandbox reports Running/Ready,
// returns an error on terminal failure states, or gives up after the
// configured timeout. `seed` is the Response observed by the caller
// right before calling us — if it already shows Running/Ready we
// return immediately without a wasted GET.
func (r *Runtime) waitForRunning(ctx context.Context, name string, seed *csghubsdk.Response) (*csghubsdk.Response, error) {
	if seed != nil && isSandboxRunning(seed.State.Status) {
		return seed, nil
	}

	timeout := r.readyTimeout()
	interval := r.pollInterval()
	start := time.Now()
	deadline := start.Add(timeout)

	lastStatus := ""
	if seed != nil {
		lastStatus = strings.TrimSpace(seed.State.Status)
		log.Printf("component=csghub_sandbox sandbox=%q phase=wait_running event=start seed_status=%q timeout_ms=%d", name, lastStatus, timeout.Milliseconds())
	}

	last := seed
	for {
		pollBegin := time.Now()
		resp, err := r.client.Get(ctx, name)
		if err != nil {
			log.Printf("component=csghub_sandbox sandbox=%q phase=wait_running event=poll_fail elapsed_ms=%d err=%q", name, time.Since(pollBegin).Milliseconds(), err)
			return nil, fmt.Errorf("poll sandbox %q: %w", name, err)
		}
		last = resp
		status := strings.TrimSpace(resp.State.Status)
		if status != lastStatus {
			log.Printf("component=csghub_sandbox sandbox=%q phase=wait_running event=status_change elapsed_ms=%d from=%q to=%q", name, time.Since(start).Milliseconds(), lastStatus, status)
			lastStatus = status
		}
		switch {
		case isSandboxRunning(status):
			log.Printf("component=csghub_sandbox sandbox=%q phase=wait_running event=success elapsed_ms=%d status=%q", name, time.Since(start).Milliseconds(), status)
			return resp, nil
		case isSandboxTerminalFailure(status):
			log.Printf("component=csghub_sandbox sandbox=%q phase=wait_running event=terminal_failure elapsed_ms=%d status=%q", name, time.Since(start).Milliseconds(), status)
			return nil, fmt.Errorf("sandbox %q entered terminal state %q before reaching Running", name, status)
		}

		if time.Now().After(deadline) {
			log.Printf("component=csghub_sandbox sandbox=%q phase=wait_running event=timeout elapsed_ms=%d timeout_ms=%d last_status=%q", name, time.Since(start).Milliseconds(), timeout.Milliseconds(), status)
			return last, fmt.Errorf("sandbox %q did not reach Running within %s (last status=%q)", name, timeout, status)
		}
		select {
		case <-ctx.Done():
			log.Printf("component=csghub_sandbox sandbox=%q phase=wait_running event=canceled elapsed_ms=%d err=%q", name, time.Since(start).Milliseconds(), ctx.Err())
			return last, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Default timing bounds used when Params leaves the corresponding
// field zero. Values mirror the pre-refactor defaults in
// internal/agent/env_csghub.go so behavior is bit-for-bit identical.
const (
	defaultReadyTimeout = 5 * time.Minute
	defaultPollInterval = 3 * time.Second
	minReadyTimeout     = 5 * time.Second
	minPollInterval     = 500 * time.Millisecond
	maxPollInterval     = 30 * time.Second
)

func (r *Runtime) readyTimeout() time.Duration {
	d := r.params.ReadyTimeout
	if d <= 0 {
		d = defaultReadyTimeout
	}
	if d < minReadyTimeout {
		return minReadyTimeout
	}
	return d
}

func (r *Runtime) pollInterval() time.Duration {
	d := r.params.PollInterval
	if d <= 0 {
		d = defaultPollInterval
	}
	switch {
	case d < minPollInterval:
		return minPollInterval
	case d > maxPollInterval:
		return maxPollInterval
	}
	return d
}

// validateCreateResponse ensures the Hub API returned the fields we
// care about before we persist them. When the Hub echoes only a
// partial spec we fall back to the expected name so the caller can
// still index into the response.
func validateCreateResponse(resp *csghubsdk.Response, expectedName string) error {
	if resp == nil {
		return fmt.Errorf("sandbox API returned empty response")
	}
	if strings.TrimSpace(resp.Spec.SandboxName) == "" {
		resp.Spec.SandboxName = expectedName
	}
	return nil
}
