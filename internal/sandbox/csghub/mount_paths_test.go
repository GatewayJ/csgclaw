package csghub

import "testing"

func TestResolveMountHostPathFromEnvLayout(t *testing.T) {
	t.Setenv("CSGCLAW_PVC_MOUNT_PATH", "/opt/csgclaw")
	t.Setenv("CSGCLAW_SANDBOX_SUBPATH_ROOT", "tenant-a")

	p := fillMountPathEnv(Params{})
	got := p.resolveMountHostPath("/opt/csgclaw/agents/alice/workspace")
	want := "tenant-a/agents/alice/workspace"
	if got != want {
		t.Fatalf("resolveMountHostPath() = %q, want %q", got, want)
	}
}

func TestResolveMountHostPathPassThroughOutsidePVCRoot(t *testing.T) {
	t.Setenv("CSGCLAW_PVC_MOUNT_PATH", "/opt/csgclaw")
	t.Setenv("CSGCLAW_SANDBOX_SUBPATH_ROOT", "tenant-a")

	p := fillMountPathEnv(Params{})
	got := p.resolveMountHostPath("/tmp/not-under-pvc")
	want := "/tmp/not-under-pvc"
	if got != want {
		t.Fatalf("resolveMountHostPath() = %q, want %q", got, want)
	}
}
