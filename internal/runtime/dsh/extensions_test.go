package dsh

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentruntime "csgclaw/internal/runtime"
	larkextension "csgclaw/internal/runtimeextension/larkcli"
)

func TestLarkCLIExtensionProjectsIntoDSHRuntime(t *testing.T) {
	root := t.TempDir()
	agentHome := filepath.Join(root, "agent-dev")
	ref := AgentRef{ID: "agent-dev", RuntimeID: "rt-agent-dev", Instructions: "Stay concise."}
	rt := New(Dependencies{
		AgentHome:    func(string) (string, error) { return agentHome, nil },
		ResolveAgent: func(agentruntime.Handle) (AgentRef, error) { return ref, nil },
	})
	if err := rt.Provision(context.Background(), agentruntime.ProvisionRequest{RuntimeID: ref.RuntimeID, AgentID: ref.ID, Instructions: ref.Instructions}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}

	restore := installFakeDSHLarkCLICommand(t)
	defer restore()
	payload, err := larkextension.Encode(larkextension.Payload{
		AgentID: "agent-dev", ParticipantID: "pt-dev", AppID: "cli_dev",
		BaseURL: "http://csgclaw.test", AccessToken: "source-token", HelperPath: "/opt/csgclaw/bin/csgclaw",
	})
	if err != nil {
		t.Fatal(err)
	}
	desired := agentruntime.ExtensionDesired{Name: larkextension.Name, Kind: larkextension.Kind, Generation: 1, SourceRevision: "rev-1", Payload: payload}
	driver, ok := rt.RuntimeExtensionDriver(larkextension.Kind)
	if !ok {
		t.Fatal("DSH lark-cli extension driver is unavailable")
	}
	change, result, err := driver.PrepareExtension(context.Background(), ref.ID, desired)
	if err != nil || result.State != agentruntime.ExtensionStateConfigured {
		t.Fatalf("PrepareExtension() = %+v, %v", result, err)
	}
	if err := change.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	projections, err := rt.ExtensionProjections(ref.ID)
	if err != nil || len(projections) != 1 {
		t.Fatalf("ExtensionProjections() = %+v, %v", projections, err)
	}
	if err := rt.RenderExtensions(context.Background(), ref.ID, projections); err != nil {
		t.Fatal(err)
	}
	if err := change.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}

	instructionsPath := filepath.Join(agentHome, hostStateDirName, workspaceDirName, "AGENTS.md")
	instructions, err := os.ReadFile(instructionsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Feishu lark-cli Access", "through `bash`", "Stay concise."} {
		if !strings.Contains(string(instructions), want) {
			t.Fatalf("DSH AGENTS.md is missing %q", want)
		}
	}

	home := filepath.Join(agentHome, hostStateDirName, homeDirName)
	environment, digests, err := buildEnvironmentWithExtensions(agentruntime.Profile{}, home, projections)
	if err != nil {
		t.Fatal(err)
	}
	env := environmentMap(environment)
	if env["LARK_CHANNEL_PROFILE"] != "agent-dev" || env["LARK_CHANNEL"] != "1" || digests[larkextension.Name] != projections[0].Digest {
		t.Fatalf("DSH extension environment = %#v, digests = %#v", env, digests)
	}
	observed, err := driver.ObserveExtension(context.Background(), ref.ID, desired)
	if err != nil || observed.RuntimeLoaded {
		t.Fatalf("ObserveExtension() before process load = %+v, %v", observed, err)
	}
	proc := &process{done: make(chan struct{}), extensionDigests: map[string]string{larkextension.Name: projections[0].Digest}}
	rt.processes[ref.RuntimeID] = proc
	observed, err = driver.ObserveExtension(context.Background(), ref.ID, desired)
	if err != nil || !observed.RuntimeLoaded {
		t.Fatalf("ObserveExtension() after process load = %+v, %v", observed, err)
	}
	delete(rt.processes, ref.RuntimeID)
}

func installFakeDSHLarkCLICommand(t *testing.T) func() {
	t.Helper()
	originalLookPath := larkCLILookPath
	originalCommand := larkCLICommandContext
	t.Setenv("CSGCLAW_DSH_LARK_EXTENSION_HELPER", "1")
	larkCLILookPath = func(name string) (string, error) {
		if name != "lark-cli" {
			return "", os.ErrNotExist
		}
		return "/opt/lark-cli", nil
	}
	larkCLICommandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=^TestDSHLarkCLIExtensionHelper$", "--"}, args...)
		return exec.CommandContext(ctx, os.Args[0], helperArgs...)
	}
	return func() {
		larkCLILookPath = originalLookPath
		larkCLICommandContext = originalCommand
	}
}

func TestDSHLarkCLIExtensionHelper(t *testing.T) {
	if os.Getenv("CSGCLAW_DSH_LARK_EXTENSION_HELPER") != "1" {
		return
	}
	separator := -1
	for index, value := range os.Args {
		if value == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	args := os.Args[separator+1:]
	if len(args) == 1 && args[0] == "-v" {
		os.Exit(0)
	}
	if strings.Join(args, " ") != "config bind --source lark-channel --identity bot-only --force --lang zh" {
		os.Exit(3)
	}
	sourceRaw, err := os.ReadFile(os.Getenv("LARK_CHANNEL_CONFIG"))
	if err != nil {
		os.Exit(4)
	}
	var source struct {
		Accounts struct {
			App struct {
				ID string `json:"id"`
			} `json:"app"`
		} `json:"accounts"`
	}
	if json.Unmarshal(sourceRaw, &source) != nil || source.Accounts.App.ID == "" {
		os.Exit(5)
	}
	path := filepath.Join(os.Getenv("LARKSUITE_CLI_CONFIG_DIR"), larkextension.WorkspaceName, larkextension.ConfigFileName)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		os.Exit(6)
	}
	data, _ := json.Marshal(map[string]any{"apps": []map[string]string{{"appId": source.Accounts.App.ID}}})
	if os.WriteFile(path, data, 0o600) != nil {
		os.Exit(7)
	}
	os.Exit(0)
}
