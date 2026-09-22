package dsh

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"csgclaw/internal/identity"
	agentruntime "csgclaw/internal/runtime"
	"csgclaw/internal/runtime/extensionstate"
	runtimeinstructions "csgclaw/internal/runtime/instructions"
	larkextension "csgclaw/internal/runtimeextension/larkcli"
)

var (
	larkCLILookPath                  = exec.LookPath
	larkCLICommandContext            = exec.CommandContext
	feishuLarkCLIManagedInstructions = larkextension.ManagedInstructions("bash")
)

var (
	_ agentruntime.ExtensionHost           = (*Runtime)(nil)
	_ agentruntime.ExtensionDriverProvider = (*Runtime)(nil)
)

func extensionStore(home string) (*extensionstate.Store, error) {
	return extensionstate.New(filepath.Join(home, "runtime-extensions"))
}

func managedExtensionInstructions(home string) ([]string, error) {
	store, err := extensionStore(home)
	if err != nil {
		return nil, err
	}
	items, err := store.List()
	if err != nil {
		return nil, err
	}
	fragments := make([]string, 0, len(items))
	for _, item := range items {
		if item.Instructions != "" {
			fragments = append(fragments, item.Instructions)
		}
	}
	return fragments, nil
}

func (r *Runtime) resolveDSHHomeDir(agentID string) (string, error) {
	if r == nil || r.deps.AgentHome == nil {
		return "", fmt.Errorf("DSH agent home resolver is required")
	}
	agentHome, err := r.deps.AgentHome(identity.CanonicalAgentID(agentID))
	if err != nil {
		return "", err
	}
	layout, err := r.ensureRuntimeDirs(agentHome)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(layout.WorkspaceRoot), homeDirName), nil
}

func (r *Runtime) RuntimeExtensionDriver(kind string) (agentruntime.ExtensionDriver, bool) {
	if r == nil || strings.TrimSpace(kind) != larkextension.Kind {
		return nil, false
	}
	driver := larkextension.NewDriver(larkextension.DriverOptions{
		ResolveHome:  r.resolveDSHHomeDir,
		Instructions: feishuLarkCLIManagedInstructions,
		LookPath:     func(name string) (string, error) { return larkCLILookPath(name) },
		CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return larkCLICommandContext(ctx, name, args...)
		},
		RuntimeLoaded: func(agentID string, projection agentruntime.ExtensionProjection) bool {
			proc, err := r.process("rt-" + identity.CanonicalAgentID(agentID))
			return err == nil && proc.extensionDigests[projection.Name] == projection.Digest
		},
	})
	return driver, true
}

func (r *Runtime) ExtensionProjections(agentID string) ([]agentruntime.ExtensionProjection, error) {
	home, err := r.resolveDSHHomeDir(agentID)
	if err != nil {
		return nil, err
	}
	store, err := extensionStore(home)
	if err != nil {
		return nil, err
	}
	return store.List()
}

func (r *Runtime) RenderExtensions(ctx context.Context, agentID string, projections []agentruntime.ExtensionProjection) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := r.resolveDSHHomeDir(agentID)
	if err != nil {
		return err
	}
	root := filepath.Dir(home)
	instructionsPath := filepath.Join(root, workspaceDirName, "AGENTS.md")
	current, err := os.ReadFile(instructionsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read DSH AGENTS.md: %w", err)
	}
	instructions := runtimeinstructions.ExtractUserInstructionsFromAgentsDocument(string(current))
	if r.deps.ResolveAgent != nil {
		ref, resolveErr := r.deps.ResolveAgent(agentruntime.Handle{RuntimeID: "rt-" + identity.CanonicalAgentID(agentID)})
		if resolveErr != nil {
			return resolveErr
		}
		instructions = ref.Instructions
	}
	fragments := make([]string, 0, len(projections))
	for _, projection := range projections {
		if projection.Instructions != "" {
			fragments = append(fragments, projection.Instructions)
		}
	}
	block := runtimeinstructions.RenderRuntimeAgentsInstructionsBlockWithOptions(agentID, instructions, runtimeinstructions.RuntimeManagedInstructionsOptions{Extensions: fragments})
	document := mergeDSHInstructionsDocument(stripManagedInstructions(string(current)), block)
	if string(current) == document {
		return nil
	}
	if err := os.WriteFile(instructionsPath, []byte(document), 0o644); err != nil {
		return fmt.Errorf("write DSH AGENTS.md: %w", err)
	}
	return nil
}

func mergeDSHInstructionsDocument(base, block string) string {
	document := strings.TrimSpace(base)
	if document != "" {
		document += "\n\n"
	}
	return document + strings.TrimSpace(block) + "\n"
}

func (r *Runtime) PrepareExtensionDelete(_ context.Context, agentID, name string) (agentruntime.PreparedExtension, error) {
	home, err := r.resolveDSHHomeDir(agentID)
	if err != nil {
		return nil, err
	}
	store, err := extensionStore(home)
	if err != nil {
		return nil, err
	}
	return store.Delete(name)
}
