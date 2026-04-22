//go:build csghub
// +build csghub

package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"csgclaw/internal/config"
	"csgclaw/internal/sandbox"
	"csgclaw/internal/sandbox/csghub"
)

type fakeManagedProvider struct {
	runtime sandbox.Runtime
}

func (p fakeManagedProvider) Name() string { return "fake-managed" }

func (p fakeManagedProvider) Open(context.Context, string) (sandbox.Runtime, error) {
	return p.runtime, nil
}

type fakeManagedRuntime struct{}

func (fakeManagedRuntime) Create(context.Context, sandbox.CreateSpec) (sandbox.Instance, error) {
	return fakeManagedInstance{}, nil
}

func (fakeManagedRuntime) Get(context.Context, string) (sandbox.Instance, error) {
	return fakeManagedInstance{}, nil
}

func (fakeManagedRuntime) Remove(context.Context, string, sandbox.RemoveOptions) error {
	return nil
}

func (fakeManagedRuntime) Close() error { return nil }

func (fakeManagedRuntime) StreamExecute(context.Context, string, string, func(string) error) error {
	return nil
}

type fakeManagedInstance struct{}

func (fakeManagedInstance) Start(context.Context) error { return nil }

func (fakeManagedInstance) Stop(context.Context, sandbox.StopOptions) error { return nil }

func (fakeManagedInstance) Info(context.Context) (sandbox.Info, error) { return sandbox.Info{}, nil }

func (fakeManagedInstance) Run(context.Context, sandbox.CommandSpec) (sandbox.CommandResult, error) {
	return sandbox.CommandResult{}, nil
}

func (fakeManagedInstance) Close() error { return nil }

func TestNewServiceWithSandboxProviderAcceptsRuntime(t *testing.T) {
	t.Setenv(config.EnvHubAPIBase, "https://hub.example.test")
	t.Setenv(config.EnvHubUserToken, "token")
	t.Setenv(config.EnvSandboxImage, "sandbox-image")
	t.Setenv(config.EnvTenantID, "tenant-1")
	t.Setenv(config.EnvPVCMountPath, t.TempDir())

	rt := &fakeManagedRuntime{}
	svc, err := NewServiceWithLLM(
		config.SingleProfileLLM(config.ModelConfig{Provider: "openai", ModelID: "gpt-4o-mini"}),
		config.ServerConfig{},
		"",
		filepath.Join(t.TempDir(), "state.json"),
		WithSandboxProvider(fakeManagedProvider{runtime: rt}),
	)
	if err != nil {
		t.Fatalf("NewServiceWithLLM() error = %v", err)
	}
	defer func() {
		_ = svc.Close()
	}()

	gotRuntime, err := svc.runtimeOrOpen()
	if err != nil {
		t.Fatalf("runtimeOrOpen() error = %v", err)
	}
	if svc.runtime != rt {
		t.Fatalf("runtime = %#v, want injected runtime %#v", svc.runtime, rt)
	}
	if gotRuntime != rt {
		t.Fatalf("runtimeOrOpen() = %#v, want %#v", gotRuntime, rt)
	}
}

func TestRuntimeOpenRequiresConfiguredSandboxProvider(t *testing.T) {
	t.Setenv(config.EnvHubAPIBase, "https://hub.example.test")
	t.Setenv(config.EnvHubUserToken, "token")
	t.Setenv(config.EnvSandboxImage, "sandbox-image")
	t.Setenv(config.EnvTenantID, "tenant-1")
	t.Setenv(config.EnvPVCMountPath, t.TempDir())

	svc, err := NewServiceWithLLM(
		config.SingleProfileLLM(config.ModelConfig{Provider: "openai", ModelID: "gpt-4o-mini"}),
		config.ServerConfig{},
		"",
		filepath.Join(t.TempDir(), "state.json"),
	)
	if err == nil {
		_, openErr := svc.runtimeOrOpen()
		if openErr == nil {
			t.Fatal("runtimeOrOpen() error = nil, want unconfigured sandbox provider error")
		}
		return
	}
	t.Fatalf("NewServiceWithLLM() error = %v, want nil", err)
}

type fakePlainProvider struct{}

func (fakePlainProvider) Name() string { return "fake-plain" }

func (fakePlainProvider) Open(context.Context, string) (sandbox.Runtime, error) {
	return fakePlainRuntime{}, nil
}

type fakePlainRuntime struct{}

func (fakePlainRuntime) Create(context.Context, sandbox.CreateSpec) (sandbox.Instance, error) {
	return fakeManagedInstance{}, nil
}

func (fakePlainRuntime) Get(context.Context, string) (sandbox.Instance, error) {
	return fakeManagedInstance{}, nil
}

func (fakePlainRuntime) Remove(context.Context, string, sandbox.RemoveOptions) error {
	return nil
}

func (fakePlainRuntime) Close() error { return nil }

var (
	_ sandbox.Runtime  = (*fakeManagedRuntime)(nil)
	_ sandbox.Instance = fakeManagedInstance{}
	_ sandbox.Runtime  = fakePlainRuntime{}
)

func TestGatewayMountsUseLocalPVCPaths(t *testing.T) {
	pvcRoot := t.TempDir()
	t.Setenv(config.EnvHubAPIBase, "https://hub.example.test")
	t.Setenv(config.EnvHubUserToken, "token")
	t.Setenv(config.EnvSandboxImage, "sandbox-image")
	t.Setenv(config.EnvTenantID, "tenant-1")
	t.Setenv(config.EnvPVCMountPath, pvcRoot)

	provider, err := csghub.NewProviderFromEnv()
	if err != nil {
		t.Fatalf("NewProviderFromEnv() error = %v", err)
	}
	svc, err := NewServiceWithLLM(
		config.SingleProfileLLM(config.ModelConfig{Provider: "openai", ModelID: "gpt-4o-mini"}),
		config.ServerConfig{},
		"",
		filepath.Join(t.TempDir(), "state.json"),
		WithSandboxProvider(provider),
	)
	if err != nil {
		t.Fatalf("NewServiceWithLLM() error = %v", err)
	}

	_, mounts, err := svc.gatewayMounts("alice")
	if err != nil {
		t.Fatalf("svc.gatewayMounts() error = %v", err)
	}
	if len(mounts) != 3 {
		t.Fatalf("gatewayMounts() len = %d, want 3", len(mounts))
	}
	for i, m := range mounts {
		if !strings.HasPrefix(m.HostPath, pvcRoot) {
			t.Fatalf("mount[%d].HostPath = %q, want prefix %q", i, m.HostPath, pvcRoot)
		}
	}
}
