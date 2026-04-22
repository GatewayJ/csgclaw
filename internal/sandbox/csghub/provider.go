// Package csghub adapts the CSGHub Sandbox HTTP API to the generic
// sandbox.Provider / Runtime / Instance facade used by internal/agent.
//
// The package encapsulates three concerns that used to live in
// internal/agent/service_csghub.go:
//
//  1. The HTTP client wiring (internal/sandbox/csghubsdk.Client).
//  2. The reconcile-and-wait lifecycle (get → create/apply → start →
//     poll for Running) that the Hub's async provisioning requires.
//  3. Runtime-neutral knowledge of Hub-reported sandbox states.
//
// With this package in place both BoxLite and CSGHub backends are wired
// symmetrically through sandbox.Provider, and WithSandboxProvider is
// honored on both builds.
package csghub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"csgclaw/internal/sandbox"
	"csgclaw/internal/sandbox/csghubsdk"
)

const providerName = "csghub"

// Params captures everything the provider needs to talk to CSGHub and
// materialize sandbox specs. It is assembled by the caller (typically
// from env vars in internal/agent) and handed to NewProvider.
type Params struct {
	// Hub endpoints / credentials.
	BaseURL      string
	AIGatewayURL string
	Token        string

	// Default CreateRequest provisioning fields applied to every spec.
	ClusterID  string
	ResourceID int
	Port       int
	Timeout    int

	// Timing knobs for WaitReady / reconcile polling.
	ReadyTimeout time.Duration
	PollInterval time.Duration

	// Logger is optional; forwarded to the underlying HTTP client.
	Logger csghubsdk.Logger

	// Mount path env contract is resolved inside sandbox/csghub so
	// caller layers stay backend-agnostic.
	pvcMountPath string
	subpathRoot  string
}

// Provider implements sandbox.Provider for CSGHub-hosted sandboxes.
type Provider struct {
	params Params
	client *csghubsdk.Client
	agent  AgentDefaults
}

// NewProvider builds a Provider using a fresh csghubsdk.Client configured
// from params. Callers that need to share an HTTP client can construct
// the Provider manually via NewProviderWithClient.
func NewProvider(params Params) Provider {
	params = fillMountPathEnv(params)
	client := csghubsdk.New(csghubsdk.Config{
		BaseURL:      params.BaseURL,
		AIGatewayURL: params.AIGatewayURL,
		Token:        params.Token,
	}, loggerOption(params.Logger)...)
	return Provider{params: params, client: client}
}

// NewProviderWithClient injects a pre-built csghubsdk.Client. Useful for
// tests that install a transport-level fake.
func NewProviderWithClient(params Params, client *csghubsdk.Client) Provider {
	params = fillMountPathEnv(params)
	return Provider{params: params, client: client}
}

// Name returns the provider name.
func (Provider) Name() string { return providerName }

// Open returns a Runtime. The homeDir argument is ignored: CSGHub
// sandboxes live on the Hub side, not on the server's local filesystem.
func (p Provider) Open(_ context.Context, _ string) (sandbox.Runtime, error) {
	return &Runtime{client: p.client, params: p.params}, nil
}

// Params returns a copy of the provider's configuration.
func (p Provider) Params() Params { return p.params }

// Client exposes the underlying HTTP client for callers that need
// direct access (e.g. Hub-only endpoints not surfaced on Runtime).
func (p Provider) Client() *csghubsdk.Client { return p.client }

// SandboxManagerImage returns provider-derived default manager image.
func (p Provider) SandboxManagerImage() string { return p.agent.ManagerImage }

// SandboxDownstreamEnv returns a copy of provider-derived downstream env.
func (p Provider) SandboxDownstreamEnv() map[string]string {
	if len(p.agent.Downstream) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.agent.Downstream))
	for k, v := range p.agent.Downstream {
		out[k] = v
	}
	return out
}

// AgentSandboxName returns the provider-managed sandbox name for an agent id.
func (p Provider) AgentSandboxName(agentID string) string {
	aid := strings.TrimSpace(agentID)
	if p.agent.NameScope == "" {
		return "csgclaw-" + aid
	}
	return "csgclaw-" + p.agent.NameScope + "-" + aid
}

// AgentMountHostPaths exposes host-side mount layout so agent code does
// not need to know csghub-specific path rules.
func (p Provider) AgentMountHostPaths(agentName string) (picoClaw, workspace, projects string, err error) {
	name := strings.TrimSpace(agentName)
	if name == "" {
		return "", "", "", fmt.Errorf("agent name is required")
	}
	root := strings.TrimSpace(p.params.pvcMountPath)
	if root == "" {
		root = defaultPVCMountPath
	}
	agentRoot := filepath.Join(root, "agents", name)
	projects = filepath.Join(root, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		return "", "", "", fmt.Errorf("create projects dir: %w", err)
	}
	return filepath.Join(agentRoot, ".picoclaw"), filepath.Join(agentRoot, "workspace"), projects, nil
}

var _ sandbox.Provider = Provider{}

func loggerOption(l csghubsdk.Logger) []csghubsdk.Option {
	if l == nil {
		return nil
	}
	return []csghubsdk.Option{csghubsdk.WithLogger(l)}
}
