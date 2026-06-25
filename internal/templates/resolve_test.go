package templates

import (
	"io/fs"
	"strings"
	"testing"

	"csgclaw/internal/runtime"
)

func TestLookupBuiltin(t *testing.T) {
	tests := []struct {
		id          string
		runtimeKind string
		role        string
		root        string
	}{
		{id: "openclaw-manager", runtimeKind: runtime.KindOpenClawSandbox, role: roleManager, root: OpenClawManagerRoot},
		{id: "openclaw-worker", runtimeKind: runtime.KindOpenClawSandbox, role: roleWorker, root: OpenClawWorkerRoot},
		{id: "picoclaw-manager", runtimeKind: runtime.KindPicoClawSandbox, role: roleManager, root: PicoClawManagerRoot},
		{id: "picoclaw-worker", runtimeKind: runtime.KindPicoClawSandbox, role: roleWorker, root: PicoClawWorkerRoot},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got, ok := LookupBuiltin(tt.id)
			if !ok {
				t.Fatalf("LookupBuiltin(%q) ok = false, want true", tt.id)
			}
			if got.ID != tt.id {
				t.Fatalf("LookupBuiltin(%q).ID = %q, want %q", tt.id, got.ID, tt.id)
			}
			if got.RuntimeKind != tt.runtimeKind {
				t.Fatalf("LookupBuiltin(%q).RuntimeKind = %q, want %q", tt.id, got.RuntimeKind, tt.runtimeKind)
			}
			if got.Role != tt.role {
				t.Fatalf("LookupBuiltin(%q).Role = %q, want %q", tt.id, got.Role, tt.role)
			}
			if got.Root != tt.root {
				t.Fatalf("LookupBuiltin(%q).Root = %q, want %q", tt.id, got.Root, tt.root)
			}
		})
	}
}

func TestBuiltinsReturnsClone(t *testing.T) {
	got := Builtins()
	if len(got) == 0 {
		t.Fatal("Builtins() returned empty slice")
	}
	got[0].ID = "changed"
	again := Builtins()
	if again[0].ID == "changed" {
		t.Fatal("Builtins() should return a cloned slice")
	}
}

func TestOpenClawWorkerLarkCLIScopeUsage(t *testing.T) {
	authStart := readTemplateFile(t, OpenClawWorkerRoot+"/workspace/skills/lark-cli/scripts/lark_cli_auth_start.sh")
	if strings.Contains(authStart, `cmd+=(--scope "$2")`) {
		t.Fatal("lark_cli_auth_start.sh must not forward repeated --scope flags to lark-cli")
	}
	if !strings.Contains(authStart, `cmd+=(--scope "$AUTH_SCOPE_ARG")`) {
		t.Fatal("lark_cli_auth_start.sh should pass one merged --scope value to lark-cli")
	}

	approvalCommon := readTemplateFile(t, OpenClawWorkerRoot+"/workspace/skills/feishu-approval/scripts/approval_common.sh")
	if strings.Contains(approvalCommon, `args+=(--scope "$scope")`) {
		t.Fatal("approval_common.sh must pass the grouped approval OAuth bundle as one --scope value")
	}
	if !strings.Contains(approvalCommon, `args+=(--scope "$scopes")`) {
		t.Fatal("approval_common.sh should pass the grouped approval OAuth bundle as one --scope value")
	}

	authRef := readTemplateFile(t, OpenClawWorkerRoot+"/workspace/skills/lark-cli/references/auth.md")
	if strings.Contains(authRef, "--scope <missing_user_scope_1> \\\n  --scope <missing_user_scope_2>") {
		t.Fatal("auth.md must not document repeated --scope flags for multi-scope recovery")
	}
}

func TestOpenClawWorkerFeishuApprovalReferences(t *testing.T) {
	skill := readTemplateFile(t, OpenClawWorkerRoot+"/workspace/skills/feishu-approval/SKILL.md")
	for _, ref := range []string{
		"references/commands.md",
		"references/oauth-recovery.md",
		"references/minimal-permissions.md",
		"references/submission.md",
	} {
		if !strings.Contains(skill, ref) {
			t.Fatalf("feishu-approval SKILL.md should reference %s", ref)
		}
		readTemplateFile(t, OpenClawWorkerRoot+"/workspace/skills/feishu-approval/"+ref)
	}
}

func readTemplateFile(t *testing.T, path string) string {
	t.Helper()
	data, err := fs.ReadFile(FS(), path)
	if err != nil {
		t.Fatalf("read template file %s: %v", path, err)
	}
	return string(data)
}
