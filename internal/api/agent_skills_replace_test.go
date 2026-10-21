package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"csgclaw/internal/agentengine"
	agent "csgclaw/internal/agentengine/agents"
	"csgclaw/internal/auth"
	skillinstall "csgclaw/internal/skill/installstate"
	skilllocal "csgclaw/internal/skill/local"
	skill "csgclaw/internal/skill/state"
)

func TestHandleAgentSkillsBatchReplaceInstallsAndEnables(t *testing.T) {
	item := completeWorkerAgent("agent-replace", "Replace")
	item.RuntimeKind, item.RuntimeName = agent.RuntimeKindCodex, agent.RuntimeNameCodex
	item.Status, item.DesiredState = "stopped", "stopped"
	item.SkillStates = map[string]skill.State{"selected": {Enabled: false}, "local-only": {Enabled: false}}
	rt := &resourceEnablementRuntime{snapshotMCPServersRuntime: snapshotMCPServersRuntime{fakeCompatRuntime: fakeCompatRuntime{kind: agent.RuntimeKindCodex}}}
	controller := mustNewSeededServiceWithOptions(t, []agent.Agent{item}, agent.WithRuntime(rt))
	t.Cleanup(func() { _ = controller.Close() })
	layout, err := controller.AgentLayout(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	globalRoot, err := skilllocal.SkillsRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{globalRoot, layout.SkillsRoot} {
		if err := os.MkdirAll(filepath.Join(root, "selected"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "selected", "SKILL.md"), []byte("# Previous\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "selected", "stale.txt"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(layout.SkillsRoot, "local-only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.SkillsRoot, "local-only", "SKILL.md"), []byte("# Local edits\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := skillinstall.Update(layout.SkillsRoot, "local-only", &skillinstall.Record{Source: skillinstall.SourceLocal}, nil); err != nil {
		t.Fatal(err)
	}
	archiveContent := "# First replacement\n"
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/skills/owner/selected/download_archive/refs/main" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(mustZipBytes(t, map[string]string{"SKILL.md": archiveContent}))
	}))
	t.Cleanup(hub.Close)
	t.Setenv("CSGHUB_API_BASE_URL", hub.URL)
	previousToken := remoteSkillsHubAccessToken
	remoteSkillsHubAccessToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { remoteSkillsHubAccessToken = previousToken })
	t.Cleanup(stubAuthStatus(func(*http.Request) (auth.Status, error) {
		return auth.Status{Authenticated: true, OpenCSGBaseURL: hub.URL, BaseURL: hub.URL}, nil
	}))
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[[hub.registries]]\nname = \"official\"\nkind = \"remote\"\nurl = \""+hub.URL+"\"\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{svc: controller, agentEngine: agentengine.New(controller), workspace: controller.Workspace(), agentRuntime: controller}
	h.SetConfigPath(configPath)
	routes := h.Routes()
	send := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return response
	}
	for _, content := range []string{"# First replacement\n", "# Second replacement\n"} {
		archiveContent = content
		before, err := controller.Get(context.Background(), item.ID, agentengine.AgentGetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		installed := send("/api/v1/skills:install", `{"remote_path":"owner/selected","replace":true}`)
		if installed.Code != http.StatusCreated {
			t.Fatalf("global replacement = %d: %s", installed.Code, installed.Body.String())
		}
		var summary struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(installed.Body.Bytes(), &summary); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(batchAddAgentSkillsRequest{Names: []string{summary.Name}, Replace: true})
		if err != nil {
			t.Fatal(err)
		}
		replaced := send("/api/v1/agents/"+item.ID+"/skills:batchAdd", string(body))
		if replaced.Code != http.StatusNoContent {
			t.Fatalf("worker replacement = %d: %s", replaced.Code, replaced.Body.String())
		}
		if err := controller.Reload(); err != nil {
			t.Fatal(err)
		}
		after, err := controller.Get(context.Background(), item.ID, agentengine.AgentGetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if before.ResourceVersion == after.ResourceVersion || !skill.Enabled(after.Spec.SkillStates, "selected") || skill.Enabled(after.Spec.SkillStates, "local-only") {
			t.Fatalf("replacement version/enablement = %+v", after)
		}
		for name, want := range map[string]string{"selected": content, "local-only": "# Local edits\n"} {
			data, err := os.ReadFile(filepath.Join(layout.SkillsRoot, name, "SKILL.md"))
			if err != nil || string(data) != want {
				t.Fatalf("worker %s content = %q, error = %v", name, data, err)
			}
		}
		if _, err := os.Stat(filepath.Join(layout.SkillsRoot, "selected", "stale.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale worker file retained: %v", err)
		}
		origins, err := skillinstall.Read(layout.SkillsRoot)
		if err != nil || origins["selected"].Source != skillinstall.SourceMarket || origins["selected"].SourceURL != hub.URL+"/skills/owner/selected" || origins["local-only"].Source != skillinstall.SourceLocal {
			t.Fatalf("worker origins = %+v, error = %v", origins, err)
		}
	}
}

func TestHandleAgentSkillsBatchReplaceAddsMissingSkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h, controller, item := newAgentSkillManagementTestServer(t)
	t.Cleanup(func() { _ = controller.Close() })
	globalRoot, err := skilllocal.SkillsRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(globalRoot, "selected"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalRoot, "selected", "SKILL.md"), []byte("# Selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+item.ID+"/skills:batchAdd", strings.NewReader(`{"names":["selected"],"replace":true}`)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("replacement of missing worker skill = %d: %s", response.Code, response.Body.String())
	}
	layout, err := controller.AgentLayout(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(layout.SkillsRoot, "selected", "SKILL.md"))
	if err != nil || string(data) != "# Selected\n" {
		t.Fatalf("installed content = %q, error = %v", data, err)
	}
	updated, err := controller.Get(context.Background(), item.ID, agentengine.AgentGetOptions{})
	if err != nil || !skill.Enabled(updated.Spec.SkillStates, "selected") {
		t.Fatalf("installed skill enablement = %+v, error = %v", updated.Spec.SkillStates, err)
	}
}
