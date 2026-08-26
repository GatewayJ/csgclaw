package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"csgclaw/internal/agent"
	"csgclaw/internal/apitypes"
	"csgclaw/internal/participant"
	agentruntime "csgclaw/internal/runtime"
)

func TestGetAgentFeishuAppInfoReturnsStoredSecret(t *testing.T) {
	svc := mustNewSeededServiceWithOptions(t, []agent.Agent{{
		ID:              "u-dev",
		Name:            "dev",
		Role:            agent.RoleWorker,
		RuntimeKind:     agent.RuntimeKindCodex,
		ProfileComplete: true,
		CreatedAt:       time.Now().UTC(),
	}}, agent.WithRuntime(fakeCompatRuntime{kind: agent.RuntimeKindCodex}))
	participantSvc := participant.NewService(participant.NewMemoryStore([]apitypes.Participant{{
		ID:              "pt-dev",
		Channel:         participant.ChannelFeishu,
		Type:            participant.TypeAgent,
		Name:            "dev",
		ChannelUserKind: participant.ChannelUserKindAppID,
		ChannelAppConfig: map[string]any{
			"app_id":     "cli_dev",
			"app_secret": "dev-secret",
		},
		AgentID:         "agent-dev",
		LifecycleStatus: participant.LifecycleStatusActive,
		Mentionable:     true,
	}}), participant.WithAgentService(svc))
	srv := &Handler{svc: svc, participant: participantSvc, serverAccessToken: "server-secret"}
	token, err := srv.larkCLISourceAccessToken("agent-dev")
	if err != nil {
		t.Fatalf("larkCLISourceAccessToken() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/u-dev/feishu/app-info", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got apitypes.FeishuBotAppInfo
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.AgentID != "agent-dev" || got.ParticipantID != "pt-dev" || got.AppID != "cli_dev" || got.AppSecret != "dev-secret" {
		t.Fatalf("app info = %#v", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestGetAgentFeishuAppInfoRejectsGlobalAndOtherAgentSourceToken(t *testing.T) {
	svc := mustNewSeededServiceWithOptions(t, []agent.Agent{
		{
			ID:              "u-dev",
			Name:            "dev",
			Role:            agent.RoleWorker,
			RuntimeKind:     agent.RuntimeKindCodex,
			ProfileComplete: true,
			CreatedAt:       time.Now().UTC(),
		},
		{
			ID:              "u-qa",
			Name:            "qa",
			Role:            agent.RoleWorker,
			RuntimeKind:     agent.RuntimeKindCodex,
			ProfileComplete: true,
			CreatedAt:       time.Now().UTC(),
		},
	}, agent.WithRuntime(fakeCompatRuntime{kind: agent.RuntimeKindCodex}))
	participantSvc := participant.NewService(participant.NewMemoryStore([]apitypes.Participant{
		{
			ID:              "pt-dev",
			Channel:         participant.ChannelFeishu,
			Type:            participant.TypeAgent,
			Name:            "dev",
			ChannelUserKind: participant.ChannelUserKindAppID,
			ChannelAppConfig: map[string]any{
				"app_id":     "cli_dev",
				"app_secret": "dev-secret",
			},
			AgentID:         "agent-dev",
			LifecycleStatus: participant.LifecycleStatusActive,
			Mentionable:     true,
		},
		{
			ID:              "pt-qa",
			Channel:         participant.ChannelFeishu,
			Type:            participant.TypeAgent,
			Name:            "qa",
			ChannelUserKind: participant.ChannelUserKindAppID,
			ChannelAppConfig: map[string]any{
				"app_id":     "cli_qa",
				"app_secret": "qa-secret",
			},
			AgentID:         "agent-qa",
			LifecycleStatus: participant.LifecycleStatusActive,
			Mentionable:     true,
		},
	}), participant.WithAgentService(svc))
	srv := &Handler{svc: svc, participant: participantSvc, serverAccessToken: "server-secret"}
	qaToken, err := srv.larkCLISourceAccessToken("agent-qa")
	if err != nil {
		t.Fatalf("larkCLISourceAccessToken() error = %v", err)
	}

	for _, auth := range []string{"Bearer server-secret", "Bearer " + qaToken} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/u-dev/feishu/app-info", nil)
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()

		srv.Routes().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q status = %d, want %d; body=%s", auth, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "dev-secret") {
			t.Fatalf("unauthorized response leaked secret: %s", rec.Body.String())
		}
	}
}

func TestInitAgentLarkCLIReturnsConflictWhenFeishuBotMissing(t *testing.T) {
	svc := mustNewSeededServiceWithOptions(t, []agent.Agent{{
		ID:              "u-dev",
		Name:            "dev",
		Role:            agent.RoleWorker,
		RuntimeKind:     agent.RuntimeKindCodex,
		RuntimeID:       "rt-dev",
		BoxID:           "codex-session-dev",
		Status:          string(agentruntime.StateRunning),
		ProfileComplete: true,
		CreatedAt:       time.Now().UTC(),
	}}, agent.WithRuntime(fakeCompatRuntime{
		kind: agent.RuntimeKindCodex,
		stop: func(context.Context, agentruntime.Handle) (agentruntime.State, error) {
			return agentruntime.StateStopped, nil
		},
		start: func(context.Context, agentruntime.Handle) (agentruntime.State, error) {
			return agentruntime.StateRunning, nil
		},
	}))
	srv := &Handler{
		svc:               svc,
		participant:       participant.NewService(participant.NewMemoryStore(nil), participant.WithAgentService(svc)),
		serverAccessToken: "server-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/u-dev/lark-cli:init", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	assertAPIErrorCode(t, rec, http.StatusConflict, feishuBotNotConfiguredCode)
}

func TestInitAgentLarkCLIRejectsSharedFeishuAppID(t *testing.T) {
	svc := mustNewSeededServiceWithOptions(t, []agent.Agent{
		{
			ID:              "u-dev",
			Name:            "dev",
			Role:            agent.RoleWorker,
			RuntimeKind:     agent.RuntimeKindCodex,
			ProfileComplete: true,
			CreatedAt:       time.Now().UTC(),
		},
		{
			ID:              "u-qa",
			Name:            "qa",
			Role:            agent.RoleWorker,
			RuntimeKind:     agent.RuntimeKindCodex,
			ProfileComplete: true,
			CreatedAt:       time.Now().UTC(),
		},
	}, agent.WithRuntime(fakeCompatRuntime{kind: agent.RuntimeKindCodex}))
	participantSvc := participant.NewService(participant.NewMemoryStore([]apitypes.Participant{
		{
			ID:              "pt-dev",
			Channel:         participant.ChannelFeishu,
			Type:            participant.TypeAgent,
			Name:            "dev",
			ChannelUserKind: participant.ChannelUserKindAppID,
			ChannelAppConfig: map[string]any{
				"app_id":     "cli_shared",
				"app_secret": "dev-secret",
			},
			AgentID:         "agent-dev",
			LifecycleStatus: participant.LifecycleStatusActive,
			Mentionable:     true,
		},
		{
			ID:              "pt-qa",
			Channel:         participant.ChannelFeishu,
			Type:            participant.TypeAgent,
			Name:            "qa",
			ChannelUserKind: participant.ChannelUserKindAppID,
			ChannelAppConfig: map[string]any{
				"app_id":     "cli_shared",
				"app_secret": "qa-secret",
			},
			AgentID:         "agent-qa",
			LifecycleStatus: participant.LifecycleStatusActive,
			Mentionable:     true,
		},
	}), participant.WithAgentService(svc))
	srv := &Handler{svc: svc, participant: participantSvc, serverAccessToken: "server-secret"}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/u-dev/lark-cli:init", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	assertAPIErrorCode(t, rec, http.StatusConflict, feishuBotAppIDConflictCode)
}

func TestInitAgentLarkCLIConfiguresWorkerScopedSource(t *testing.T) {
	originalLookPath := larkCLILookPath
	originalCommandContext := larkCLICommandContext
	originalCurrentExe := larkCLICurrentExe
	t.Cleanup(func() {
		larkCLILookPath = originalLookPath
		larkCLICommandContext = originalCommandContext
		larkCLICurrentExe = originalCurrentExe
	})

	recordPath := filepath.Join(t.TempDir(), "bind.json")
	t.Setenv("CSGCLAW_FAKE_LARK_CLI_COMMAND", "1")
	t.Setenv("CSGCLAW_FAKE_LARK_RECORD_PATH", recordPath)
	larkCLILookPath = func(name string) (string, error) {
		if name == "lark-cli" {
			return "/opt/lark/bin/lark-cli", nil
		}
		return "", os.ErrNotExist
	}
	larkCLICommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=^TestLarkCLIFakeCommand$", "--"}, args...)
		return exec.CommandContext(ctx, os.Args[0], helperArgs...)
	}
	larkCLICurrentExe = func() (string, error) {
		return "/opt/csgclaw/bin/csgclaw", nil
	}

	svc := mustNewSeededServiceWithOptions(t, []agent.Agent{{
		ID:              "u-dev",
		Name:            "dev",
		Role:            agent.RoleWorker,
		RuntimeKind:     agent.RuntimeKindCodex,
		RuntimeID:       "rt-dev",
		BoxID:           "codex-session-dev",
		Status:          string(agentruntime.StateRunning),
		ProfileComplete: true,
		CreatedAt:       time.Now().UTC(),
	}}, agent.WithRuntime(fakeCompatRuntime{
		kind: agent.RuntimeKindCodex,
		stop: func(context.Context, agentruntime.Handle) (agentruntime.State, error) {
			return agentruntime.StateStopped, nil
		},
		start: func(context.Context, agentruntime.Handle) (agentruntime.State, error) {
			return agentruntime.StateRunning, nil
		},
	}))
	participantSvc := participant.NewService(participant.NewMemoryStore([]apitypes.Participant{{
		ID:              "pt-dev",
		Channel:         participant.ChannelFeishu,
		Type:            participant.TypeAgent,
		Name:            "dev",
		ChannelUserKind: participant.ChannelUserKindAppID,
		ChannelAppConfig: map[string]any{
			"app_id":     "cli_dev",
			"app_secret": "dev-secret",
		},
		AgentID:         "agent-dev",
		LifecycleStatus: participant.LifecycleStatusActive,
		Mentionable:     true,
	}}), participant.WithAgentService(svc))
	srv := &Handler{
		svc:               svc,
		participant:       participantSvc,
		serverAccessToken: "server-secret",
		advertiseBaseURL:  "http://csgclaw.test",
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/u-dev/lark-cli:init", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got apitypes.AgentLarkCLIInitResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	layout, err := svc.AgentLayout("agent-dev")
	if err != nil {
		t.Fatalf("AgentLayout() error = %v", err)
	}
	codexHomeDir := codexHomeDirFromLayout(layout)
	wantConfigDir := filepath.Join(codexHomeDir, larkCLIConfigDirName)
	wantSourcePath := filepath.Join(codexHomeDir, larkCLISourceDirName, larkCLISourceConfigFileName)
	if got.AgentID != "agent-dev" || got.AppID != "cli_dev" || got.Installed || got.RestartStatus != "runtime_restarted" {
		t.Fatalf("init response = %#v", got)
	}
	if got.LarkCLIPath != "/opt/lark/bin/lark-cli" || got.ConfigDir != wantConfigDir || got.SourceConfigPath != wantSourcePath {
		t.Fatalf("init paths = %#v, want config %q source %q", got, wantConfigDir, wantSourcePath)
	}
	if info, err := os.Stat(wantConfigDir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("config dir stat = %#v, %v", info, err)
	}
	if info, err := os.Stat(wantSourcePath); err != nil || info.IsDir() || info.Mode().Perm() != 0o600 {
		t.Fatalf("source config stat = %#v, %v", info, err)
	}
	sourceRaw, err := os.ReadFile(wantSourcePath)
	if err != nil {
		t.Fatalf("read source config: %v", err)
	}
	sourceText := string(sourceRaw)
	for _, want := range []string{
		`"id": "cli_dev"`,
		`"command": "/opt/csgclaw/bin/csgclaw"`,
		`"CSGCLAW_BASE_URL": "http://csgclaw.test"`,
		`"CSGCLAW_ACCESS_TOKEN": "larkcli-src-v1.`,
		`"pt"`,
		`"app-info"`,
		`"--exec-provider"`,
	} {
		if !strings.Contains(sourceText, want) {
			t.Fatalf("source config missing %q in %s", want, sourceText)
		}
	}
	if strings.Contains(sourceText, "server-secret") {
		t.Fatalf("source config persisted global server token: %s", sourceText)
	}
	markerPath := filepath.Join(codexHomeDir, larkCLISourceDirName, larkCLIBindMarkerFileName)
	if info, err := os.Stat(markerPath); err != nil || info.IsDir() || info.Mode().Perm() != 0o600 {
		t.Fatalf("bind marker stat = %#v, %v", info, err)
	}

	var bind struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
	}
	recordRaw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read fake bind record: %v", err)
	}
	if err := json.Unmarshal(recordRaw, &bind); err != nil {
		t.Fatalf("decode fake bind record: %v", err)
	}
	if got, want := strings.Join(bind.Args, " "), "config bind --source lark-channel --identity bot-only --force --lang zh"; got != want {
		t.Fatalf("bind args = %q, want %q", got, want)
	}
	if bind.Env["LARKSUITE_CLI_CONFIG_DIR"] != wantConfigDir ||
		bind.Env["LARK_CHANNEL"] != "1" ||
		bind.Env["LARK_CHANNEL_HOME"] != codexHomeDir ||
		bind.Env["LARK_CHANNEL_PROFILE"] != "agent-dev" ||
		bind.Env["LARK_CHANNEL_CONFIG"] != wantSourcePath {
		t.Fatalf("bind env = %#v, want worker-scoped lark-cli env", bind.Env)
	}
}

func TestInitAgentLarkCLIRemovesSourceWhenBindFails(t *testing.T) {
	originalLookPath := larkCLILookPath
	originalCommandContext := larkCLICommandContext
	originalCurrentExe := larkCLICurrentExe
	t.Cleanup(func() {
		larkCLILookPath = originalLookPath
		larkCLICommandContext = originalCommandContext
		larkCLICurrentExe = originalCurrentExe
	})

	recordPath := filepath.Join(t.TempDir(), "bind.json")
	t.Setenv("CSGCLAW_FAKE_LARK_CLI_COMMAND", "1")
	t.Setenv("CSGCLAW_FAKE_LARK_RECORD_PATH", recordPath)
	t.Setenv("CSGCLAW_FAKE_LARK_EXIT_CODE", "1")
	larkCLILookPath = func(name string) (string, error) {
		if name == "lark-cli" {
			return "/opt/lark/bin/lark-cli", nil
		}
		return "", os.ErrNotExist
	}
	larkCLICommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=^TestLarkCLIFakeCommand$", "--"}, args...)
		return exec.CommandContext(ctx, os.Args[0], helperArgs...)
	}
	larkCLICurrentExe = func() (string, error) {
		return "/opt/csgclaw/bin/csgclaw", nil
	}

	svc := mustNewSeededServiceWithOptions(t, []agent.Agent{{
		ID:              "u-dev",
		Name:            "dev",
		Role:            agent.RoleWorker,
		RuntimeKind:     agent.RuntimeKindCodex,
		ProfileComplete: true,
		CreatedAt:       time.Now().UTC(),
	}}, agent.WithRuntime(fakeCompatRuntime{kind: agent.RuntimeKindCodex}))
	participantSvc := participant.NewService(participant.NewMemoryStore([]apitypes.Participant{{
		ID:              "pt-dev",
		Channel:         participant.ChannelFeishu,
		Type:            participant.TypeAgent,
		Name:            "dev",
		ChannelUserKind: participant.ChannelUserKindAppID,
		ChannelAppConfig: map[string]any{
			"app_id":     "cli_dev",
			"app_secret": "dev-secret",
		},
		AgentID:         "agent-dev",
		LifecycleStatus: participant.LifecycleStatusActive,
		Mentionable:     true,
	}}), participant.WithAgentService(svc))
	srv := &Handler{
		svc:               svc,
		participant:       participantSvc,
		serverAccessToken: "server-secret",
		advertiseBaseURL:  "http://csgclaw.test",
	}
	layout, err := svc.AgentLayout("agent-dev")
	if err != nil {
		t.Fatalf("AgentLayout() error = %v", err)
	}
	codexHomeDir := codexHomeDirFromLayout(layout)
	sourcePath := filepath.Join(codexHomeDir, larkCLISourceDirName, larkCLISourceConfigFileName)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("stale source-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/u-dev/lark-cli:init", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	assertAPIErrorCode(t, rec, http.StatusBadGateway, "lark_cli_bind_failed")
	for _, path := range []string{
		sourcePath,
		filepath.Join(codexHomeDir, larkCLISourceDirName, larkCLIBindMarkerFileName),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stat %s = %v, want not exist after failed bind", path, err)
		}
	}
}

func TestWriteLarkChannelSourceConfigUsesExecProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.json")

	if err := writeLarkChannelSourceConfig(path, larkChannelSourceConfig{
		AppID:       "cli_dev",
		BaseURL:     "http://127.0.0.1:8080",
		AccessToken: "server-secret",
		HelperPath:  "/usr/local/bin/csgclaw",
		AgentID:     "agent-dev",
	}); err != nil {
		t.Fatalf("writeLarkChannelSourceConfig() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat source config: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("source config mode = %04o, want 0600", mode)
	}

	var got struct {
		Accounts struct {
			App struct {
				ID     string `json:"id"`
				Secret struct {
					Source   string `json:"source"`
					Provider string `json:"provider"`
					ID       string `json:"id"`
				} `json:"secret"`
				Tenant string `json:"tenant"`
			} `json:"app"`
		} `json:"accounts"`
		Secrets struct {
			Providers map[string]struct {
				Source              string            `json:"source"`
				Command             string            `json:"command"`
				Args                []string          `json:"args"`
				Env                 map[string]string `json:"env"`
				TrustedDirs         []string          `json:"trustedDirs"`
				AllowInsecurePath   bool              `json:"allowInsecurePath"`
				AllowSymlinkCommand bool              `json:"allowSymlinkCommand"`
			} `json:"providers"`
		} `json:"secrets"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source config: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode source config: %v", err)
	}
	if got.Accounts.App.ID != "cli_dev" || got.Accounts.App.Tenant != "feishu" {
		t.Fatalf("accounts.app = %#v", got.Accounts.App)
	}
	if got.Accounts.App.Secret.Source != "exec" ||
		got.Accounts.App.Secret.Provider != larkCLISourceProviderName ||
		got.Accounts.App.Secret.ID != larkCLIAppSecretExecID {
		t.Fatalf("accounts.app.secret = %#v", got.Accounts.App.Secret)
	}
	provider := got.Secrets.Providers[larkCLISourceProviderName]
	if provider.Source != "exec" || provider.Command != "/usr/local/bin/csgclaw" {
		t.Fatalf("provider = %#v", provider)
	}
	if strings.Join(provider.Args, " ") != "pt app-info --channel feishu --agent-id agent-dev --exec-provider" {
		t.Fatalf("provider args = %#v", provider.Args)
	}
	if provider.Env["CSGCLAW_BASE_URL"] != "http://127.0.0.1:8080" || provider.Env["CSGCLAW_ACCESS_TOKEN"] != "server-secret" {
		t.Fatalf("provider env = %#v", provider.Env)
	}
	if len(provider.TrustedDirs) != 1 || provider.TrustedDirs[0] != "/usr/local/bin" {
		t.Fatalf("trustedDirs = %#v", provider.TrustedDirs)
	}
	if !provider.AllowInsecurePath || !provider.AllowSymlinkCommand {
		t.Fatalf("provider path allowances = insecure:%t symlink:%t", provider.AllowInsecurePath, provider.AllowSymlinkCommand)
	}
}

func TestLarkCLIFakeCommand(t *testing.T) {
	if os.Getenv("CSGCLAW_FAKE_LARK_CLI_COMMAND") != "1" {
		return
	}
	recordPath := strings.TrimSpace(os.Getenv("CSGCLAW_FAKE_LARK_RECORD_PATH"))
	if recordPath == "" {
		t.Fatal("CSGCLAW_FAKE_LARK_RECORD_PATH is required")
	}
	var args []string
	for idx, arg := range os.Args {
		if arg == "--" {
			args = append(args, os.Args[idx+1:]...)
			break
		}
	}
	payload := struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
	}{
		Args: args,
		Env: map[string]string{
			"LARKSUITE_CLI_CONFIG_DIR": os.Getenv("LARKSUITE_CLI_CONFIG_DIR"),
			"LARK_CHANNEL":             os.Getenv("LARK_CHANNEL"),
			"LARK_CHANNEL_HOME":        os.Getenv("LARK_CHANNEL_HOME"),
			"LARK_CHANNEL_PROFILE":     os.Getenv("LARK_CHANNEL_PROFILE"),
			"LARK_CHANNEL_CONFIG":      os.Getenv("LARK_CHANNEL_CONFIG"),
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode fake lark-cli record: %v", err)
	}
	if err := os.WriteFile(recordPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write fake lark-cli record: %v", err)
	}
	if os.Getenv("CSGCLAW_FAKE_LARK_EXIT_CODE") == "1" {
		os.Exit(1)
	}
}

func assertAPIErrorCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q; body=%s", payload.Error.Code, wantCode, rec.Body.String())
	}
}
