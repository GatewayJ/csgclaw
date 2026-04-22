//go:build !csghub
// +build !csghub

// Package boxlite adapts the BoxLite SDK to the generic sandbox interfaces.
package boxlite

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	boxlitesdk "github.com/RussellLuo/boxlite/sdks/go"

	"csgclaw/internal/sandbox"
)

const providerName = "boxlite"

// Provider opens BoxLite-backed sandbox runtimes.
type Provider struct{}

// NewProvider returns a BoxLite sandbox provider.
func NewProvider() Provider {
	return Provider{}
}

// Name returns the provider name.
func (Provider) Name() string {
	return providerName
}

// Open creates a BoxLite runtime rooted at homeDir.
func (Provider) Open(_ context.Context, homeDir string) (sandbox.Runtime, error) {
	rt, err := boxlitesdk.NewRuntime(boxlitesdk.WithHomeDir(homeDir))
	if err != nil {
		return nil, wrapError("open boxlite runtime", err)
	}
	return &Runtime{runtime: rt}, nil
}

// Runtime wraps a BoxLite runtime.
type Runtime struct {
	runtime *boxlitesdk.Runtime
}

var _ sandbox.Provider = Provider{}
var _ sandbox.Runtime = (*Runtime)(nil)

// Create creates and starts a BoxLite box from a generic sandbox create spec.
func (r *Runtime) Create(ctx context.Context, spec sandbox.CreateSpec) (sandbox.Instance, error) {
	if r == nil || r.runtime == nil {
		return nil, fmt.Errorf("invalid boxlite runtime")
	}
	opts, err := boxOptions(spec)
	if err != nil {
		return nil, err
	}
	box, err := r.runtime.Create(ctx, spec.Image, opts...)
	if err != nil {
		return nil, wrapError("create boxlite box", err)
	}
	if err := box.Start(ctx); err != nil {
		_ = box.Close()
		return nil, wrapError("start boxlite box", err)
	}
	return &Instance{box: box}, nil
}

// Get returns a handle for an existing BoxLite box by ID or name.
func (r *Runtime) Get(ctx context.Context, idOrName string) (sandbox.Instance, error) {
	if r == nil || r.runtime == nil {
		return nil, fmt.Errorf("invalid boxlite runtime")
	}
	box, err := r.runtime.Get(ctx, idOrName)
	if err != nil {
		return nil, wrapError("get boxlite box", err)
	}
	return &Instance{box: box}, nil
}

// Remove removes a BoxLite box. Force maps to BoxLite ForceRemove.
func (r *Runtime) Remove(ctx context.Context, idOrName string, opts sandbox.RemoveOptions) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("invalid boxlite runtime")
	}
	var err error
	if opts.Force {
		err = r.runtime.ForceRemove(ctx, idOrName)
	} else {
		err = r.runtime.Remove(ctx, idOrName)
	}
	return wrapError("remove boxlite box", err)
}

// StreamExecute runs a shell command inside the named box and invokes
// emit once per stdout line. Used by agent.Service to tail per-agent
// gateway logs without caring which backend hosts the sandbox.
func (r *Runtime) StreamExecute(ctx context.Context, name, command string, emit func(line string) error) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("invalid boxlite runtime")
	}
	if emit == nil {
		return fmt.Errorf("emit callback is required")
	}
	inst, err := r.Get(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = inst.Close() }()
	bi, ok := inst.(*Instance)
	if !ok || bi.box == nil {
		return fmt.Errorf("invalid boxlite instance")
	}

	pr, pw := io.Pipe()
	cmd := bi.box.Command("sh", "-c", command)
	cmd.Stdout = pw

	errCh := make(chan error, 1)
	go func() {
		runErr := cmd.Run(ctx)
		_ = pw.Close()
		errCh <- runErr
	}()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var emitErr error
	for scanner.Scan() {
		if emitErr = emit(scanner.Text()); emitErr != nil {
			_ = pr.CloseWithError(emitErr)
			break
		}
	}
	if scanErr := scanner.Err(); emitErr == nil && scanErr != nil {
		emitErr = scanErr
	}
	runErr := <-errCh
	if emitErr != nil {
		return emitErr
	}
	if runErr != nil {
		return wrapError("stream boxlite command", runErr)
	}
	if code := cmd.ExitCode(); code != 0 {
		return fmt.Errorf("boxlite command exited with code %d", code)
	}
	return nil
}

// Close releases the BoxLite runtime handle.
func (r *Runtime) Close() error {
	if r == nil || r.runtime == nil {
		return nil
	}
	err := r.runtime.Close()
	r.runtime = nil
	return wrapError("close boxlite runtime", err)
}

// Instance wraps a BoxLite box handle.
type Instance struct {
	box *boxlitesdk.Box
}

var _ sandbox.Instance = (*Instance)(nil)

// Start starts the BoxLite box.
func (i *Instance) Start(ctx context.Context) error {
	if i == nil || i.box == nil {
		return fmt.Errorf("invalid boxlite box")
	}
	return wrapError("start boxlite box", i.box.Start(ctx))
}

// Stop stops the BoxLite box. BoxLite currently does not expose force or
// timeout controls on Stop, so unsupported options are rejected explicitly.
func (i *Instance) Stop(ctx context.Context, opts sandbox.StopOptions) error {
	if i == nil || i.box == nil {
		return fmt.Errorf("invalid boxlite box")
	}
	if opts.Force {
		return fmt.Errorf("unsupported sandbox option: force stop")
	}
	if opts.Timeout != 0 {
		return fmt.Errorf("unsupported sandbox option: stop timeout")
	}
	return wrapError("stop boxlite box", i.box.Stop(ctx))
}

// Info returns runtime-neutral BoxLite box metadata.
func (i *Instance) Info(ctx context.Context) (sandbox.Info, error) {
	if i == nil || i.box == nil {
		return sandbox.Info{}, fmt.Errorf("invalid boxlite box")
	}
	info, err := i.box.Info(ctx)
	if err != nil {
		return sandbox.Info{}, wrapError("read boxlite box info", err)
	}
	return boxInfo(*info), nil
}

// Run executes a command inside the BoxLite box.
func (i *Instance) Run(ctx context.Context, spec sandbox.CommandSpec) (sandbox.CommandResult, error) {
	if i == nil || i.box == nil {
		return sandbox.CommandResult{}, fmt.Errorf("invalid boxlite box")
	}
	cmd := i.box.Command(spec.Name, spec.Args...)
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	if err := cmd.Run(ctx); err != nil {
		return sandbox.CommandResult{}, wrapError("run boxlite command", err)
	}
	return sandbox.CommandResult{ExitCode: cmd.ExitCode()}, nil
}

// Close releases the BoxLite box handle without stopping or removing it.
func (i *Instance) Close() error {
	if i == nil || i.box == nil {
		return nil
	}
	err := i.box.Close()
	i.box = nil
	return wrapError("close boxlite box", err)
}

func boxOptions(spec sandbox.CreateSpec) ([]boxlitesdk.BoxOption, error) {
	var opts []boxlitesdk.BoxOption
	if strings.TrimSpace(spec.Name) != "" {
		opts = append(opts, boxlitesdk.WithName(spec.Name))
	}
	opts = append(opts,
		boxlitesdk.WithDetach(spec.Detach),
		boxlitesdk.WithAutoRemove(spec.AutoRemove),
	)
	for key, value := range spec.Env {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid sandbox env: key is required")
		}
		opts = append(opts, boxlitesdk.WithEnv(key, value))
	}
	for _, mount := range spec.Mounts {
		if strings.TrimSpace(mount.HostPath) == "" {
			return nil, fmt.Errorf("invalid sandbox mount: host path is required")
		}
		if strings.TrimSpace(mount.GuestPath) == "" {
			return nil, fmt.Errorf("invalid sandbox mount: guest path is required")
		}
		if mount.ReadOnly {
			opts = append(opts, boxlitesdk.WithVolumeReadOnly(mount.HostPath, mount.GuestPath))
			continue
		}
		opts = append(opts, boxlitesdk.WithVolume(mount.HostPath, mount.GuestPath))
	}
	if len(spec.Entrypoint) > 0 {
		opts = append(opts, boxlitesdk.WithEntrypoint(spec.Entrypoint...))
	}
	if len(spec.Cmd) > 0 {
		opts = append(opts, boxlitesdk.WithCmd(spec.Cmd...))
	}
	return opts, nil
}

func boxInfo(info boxlitesdk.BoxInfo) sandbox.Info {
	return sandbox.Info{
		ID:        info.ID,
		Name:      info.Name,
		State:     boxState(info.State),
		CreatedAt: info.CreatedAt,
	}
}

func boxState(state boxlitesdk.State) sandbox.State {
	switch state {
	case boxlitesdk.StateConfigured:
		return sandbox.StateCreated
	case boxlitesdk.StateRunning:
		return sandbox.StateRunning
	case boxlitesdk.StateStopping:
		return sandbox.StateUnknown
	case boxlitesdk.StateStopped:
		return sandbox.StateStopped
	default:
		return sandbox.StateUnknown
	}
}

func wrapError(op string, err error) error {
	if err == nil {
		return nil
	}
	if boxlitesdk.IsNotFound(err) {
		return fmt.Errorf("%s: %w: %w", op, sandbox.ErrNotFound, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}
