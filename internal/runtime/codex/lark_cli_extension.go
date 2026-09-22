package codex

import (
	"context"
	"os/exec"
	"strings"

	agentruntime "csgclaw/internal/runtime"
	larkextension "csgclaw/internal/runtimeextension/larkcli"
)

const (
	larkCLIWorkspaceName  = larkextension.WorkspaceName
	larkCLIConfigFileName = larkextension.ConfigFileName
)

var (
	larkCLILookPath       = exec.LookPath
	larkCLICommandContext = exec.CommandContext
)

var _ agentruntime.ExtensionDriverProvider = (*Runtime)(nil)

func (r *Runtime) RuntimeExtensionDriver(kind string) (agentruntime.ExtensionDriver, bool) {
	if r == nil || strings.TrimSpace(kind) != larkextension.Kind {
		return nil, false
	}
	driver := larkextension.NewDriver(larkextension.DriverOptions{
		ResolveHome:  r.resolveCodexHomeDir,
		Instructions: feishuLarkCLIManagedInstructions,
		LookPath:     func(name string) (string, error) { return larkCLILookPath(name) },
		CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return larkCLICommandContext(ctx, name, args...)
		},
		RuntimeLoaded: func(agentID string, projection agentruntime.ExtensionProjection) bool {
			session, err := r.SessionManager().LiveSession(SessionHandle{RuntimeID: "rt-" + canonicalRuntimeAgentID(agentID)})
			return err == nil && session != nil && session.ProcessID > 0 && session.ExtensionDigests[projection.Name] == projection.Digest
		},
	})
	return driver, true
}

func larkEnvironment(home, dir, agentID string) map[string]string {
	return larkextension.Environment(home, dir, agentID)
}
