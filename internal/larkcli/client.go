package larkcli

import (
	"bytes"
	"context"
	"csgclaw/internal/processgroup"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	projection "csgclaw/internal/runtimeextension/larkcli"
)

type Identity struct {
	Status      string `json:"status"`
	Available   bool   `json:"available"`
	Verified    *bool  `json:"verified,omitempty"`
	OpenID      string `json:"openId,omitempty"`
	AppName     string `json:"appName,omitempty"`
	UserName    string `json:"userName,omitempty"`
	Scope       string `json:"scope,omitempty"`
	TokenStatus string `json:"tokenStatus,omitempty"`
}
type Status struct {
	AppID      string `json:"appId"`
	DefaultAs  string `json:"defaultAs"`
	Identities struct {
		Bot  Identity `json:"bot"`
		User Identity `json:"user"`
	} `json:"identities"`
}
type DeviceAuthorization struct {
	RequestedScope string `json:"requested_scope,omitempty"`
	URL            string `json:"verification_url"`
	DeviceCode     string `json:"device_code"`
	ExpiresIn      int64  `json:"expires_in"`
}
type OperationError struct{ Code string }

func (e *OperationError) Error() string { return e.Code }
func operationError(output []byte) error {
	value := strings.ToLower(string(output))
	for _, item := range []struct{ match, code string }{{"access_denied", "authorization_denied"}, {"authorization was denied", "authorization_denied"}, {"authorization denied", "authorization_denied"}, {"user denied", "authorization_denied"}, {"expired_token", "authorization_expired"}, {"device code has expired", "authorization_expired"}, {"device code expired", "authorization_expired"}, {"authorization expired", "authorization_expired"}, {"permission denied", "permission_denied"}, {"insufficient_scope", "permission_denied"}} {
		if strings.Contains(value, item.match) {
			return &OperationError{Code: item.code}
		}
	}
	return &OperationError{Code: "cli_operation_failed"}
}

type ProcessRef struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
}
type Client struct {
	Runner      func(context.Context, Client, io.Reader, ...string) ([]byte, error)
	Executable  string
	ConfigDir   string
	OperationID string
	Process     func(ProcessRef) error
	URL         func(string)
}

func (c Client) Environment() []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && !projection.IsConfigurationEnv(key) && key != "CSGCLAW_CLI_OPERATION" {
			result = append(result, item)
		}
	}
	return append(result, "LARKSUITE_CLI_CONFIG_DIR="+c.ConfigDir, "CSGCLAW_CLI_OPERATION="+c.OperationID)
}

var urlPattern = regexp.MustCompile(`https://[^\s<>"'\x1b]+`)

func AllowedAuthorizationURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "feishu.cn" || strings.HasSuffix(host, ".feishu.cn") || host == "larksuite.com" || strings.HasSuffix(host, ".larksuite.com")
}

type commandOutput struct {
	mu     sync.Mutex
	data   bytes.Buffer
	notify func(string)
	last   string
}

func (w *commandOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.data.Len()+len(data) > 1<<20 {
		return 0, errors.New("CLI output exceeded limit")
	}
	_, _ = w.data.Write(data)
	if w.notify != nil {
		for _, raw := range urlPattern.FindAllString(w.data.String(), -1) {
			raw = strings.TrimRight(raw, ".,);]")
			if AllowedAuthorizationURL(raw) && raw != w.last {
				w.last = raw
				w.notify(raw)
			}
		}
	}
	return len(data), nil
}
func (c Client) Run(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	if c.Runner != nil {
		return c.Runner(ctx, c, input, args...)
	}
	if err := os.MkdirAll(c.ConfigDir, 0700); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, c.Executable, args...)
	cmd.Env = c.Environment()
	cmd.Dir = c.ConfigDir
	cmd.Stdin = input
	configureCommand(cmd)
	output := &commandOutput{notify: c.URL}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error { return stopCommand(cmd.Process.Pid) }
	if err := processgroup.Start(cmd); err != nil {
		return nil, errors.New("could not start lark-cli")
	}
	ref := ProcessRef{PID: cmd.Process.Pid, Started: processStarted(cmd.Process.Pid)}
	if c.Process != nil {
		if err := c.Process(ref); err != nil {
			_ = stopCommand(ref.PID)
			_ = cmd.Wait()
			return nil, err
		}
	}
	err := cmd.Wait()
	cleanupErr := stopCommand(ref.PID)
	if cleanupErr != nil {
		return nil, cleanupErr
	}
	if c.Process != nil {
		if saveErr := c.Process(ProcessRef{}); saveErr != nil {
			return nil, saveErr
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return output.data.Bytes(), operationError(output.data.Bytes())
	}
	return output.data.Bytes(), nil
}
func (c Client) Status(ctx context.Context, verify bool) (Status, error) {
	var result Status
	args := []string{"auth", "status", "--json"}
	if verify {
		args = append(args, "--verify")
	}
	data, err := c.Run(ctx, nil, args...)
	if err != nil {
		return result, err
	}
	if err := decodeObject(data, &result); err != nil {
		return result, err
	}
	if result.AppID == "" {
		return result, errors.New("CLI status has no application identity")
	}
	return result, nil
}
func decodeObject(data []byte, target any) error {
	// 官方命令可能先输出提示，再输出 JSON。
	for index, b := range data {
		if b == '{' && json.Unmarshal(bytes.TrimSpace(data[index:]), target) == nil {
			return nil
		}
	}
	return errors.New("invalid CLI JSON response")
}
func (c Client) Login(ctx context.Context) (DeviceAuthorization, error) {
	var result DeviceAuthorization
	data, err := c.Run(ctx, nil, "auth", "login", "--no-wait", "--json", "--scope", UserAuthorizationScopes())
	if err != nil {
		return result, err
	}
	if err := decodeObject(data, &result); err != nil {
		return result, err
	}
	if !AllowedAuthorizationURL(result.URL) || result.DeviceCode == "" || result.ExpiresIn <= 0 {
		return result, errors.New("invalid device authorization response")
	}
	result.RequestedScope = UserAuthorizationScopes()
	return result, nil
}
func (c Client) WaitLogin(ctx context.Context, code string) error {
	data, err := c.Run(ctx, nil, "auth", "login", "--device-code", code, "--json")
	if err != nil && ctx.Err() == nil {
		// The official CLI exits nonzero when login succeeds with fewer scopes
		// than requested. Retain that login; the caller verifies the identity.
		var completed struct {
			Event   string `json:"event"`
			OpenID  string `json:"user_open_id"`
			Warning struct {
				Type string `json:"type"`
			} `json:"warning"`
		}
		if decodeObject(data, &completed) == nil && completed.Event == "authorization_complete" && completed.OpenID != "" && completed.Warning.Type == "missing_scope" {
			return nil
		}
	}
	return err
}
func (c Client) Init(ctx context.Context) error {
	// Only the scoped URL reaches the connector UI; credential storage and
	// device polling remain owned by the official CLI.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var urlErr error
	notify := c.URL
	c.URL = func(raw string) {
		var scoped string
		scoped, urlErr = applicationAuthorizationURL(raw)
		if urlErr != nil {
			cancel()
			return
		}
		if notify != nil {
			notify(scoped)
		}
	}
	_, err := c.Run(ctx, nil, "config", "init", "--new", "--brand", "feishu", "--lang", "zh")
	if urlErr != nil {
		return urlErr
	}
	return err
}
func (c Client) Logout(ctx context.Context) error {
	_, err := c.Run(ctx, nil, "auth", "logout")
	return err
}
func (c Client) EnablePersonalIdentity(ctx context.Context) error {
	if _, err := c.Run(ctx, nil, "config", "strict-mode", "off", "--global"); err != nil {
		return err
	}
	if _, err := c.Run(ctx, nil, "config", "strict-mode", "off"); err != nil {
		return err
	}
	_, err := c.Run(ctx, nil, "config", "default-as", "auto")
	return err
}
func AtomicWrite(filename string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(filename), ".cli-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filename)
}
func (c Client) ConfigPath() string { return filepath.Join(c.ConfigDir, "config.json") }
func (c Client) Disconnect() error {
	// 凭证仍由官方凭证存储管理。
	if err := os.Remove(c.ConfigPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
