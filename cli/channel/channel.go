package channel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"csgclaw/cli/command"
)

type cmd struct{}

type feishuConfigRequest struct {
	AppID       string `json:"app_id"`
	AppSecret   string `json:"app_secret"`
	AdminOpenID string `json:"admin_open_id,omitempty"`
	Reload      *bool  `json:"reload,omitempty"`
}

type feishuConfigResponse struct {
	BotID       string `json:"bot_id"`
	Configured  bool   `json:"configured"`
	AppID       string `json:"app_id,omitempty"`
	AppSecret   string `json:"app_secret"`
	AdminOpenID string `json:"admin_open_id,omitempty"`
	Reloaded    bool   `json:"reloaded,omitempty"`
}

type reloadResponse struct {
	Status     string   `json:"status"`
	FeishuBots []string `json:"feishu_bots"`
}

type doctorResult struct {
	BotID       string `json:"bot_id"`
	Status      string `json:"status"`
	Configured  bool   `json:"configured"`
	AppID       string `json:"app_id,omitempty"`
	AppSecret   string `json:"app_secret"`
	AdminOpenID string `json:"admin_open_id,omitempty"`
	Message     string `json:"message,omitempty"`
}

func NewCmd() command.Command { return cmd{} }

func (cmd) Name() string { return "channel" }

func (cmd) Summary() string { return "Manage channel configuration." }

func (c cmd) Run(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	if len(args) == 0 || command.IsHelpArg(args[0]) {
		c.usage(run)
		return fmt.Errorf("channel requires a subcommand")
	}
	switch args[0] {
	case "reload":
		return c.runReload(ctx, run, args[1:], globals)
	case "feishu":
		return c.runFeishu(ctx, run, args[1:], globals)
	default:
		c.usage(run)
		return fmt.Errorf("unknown channel subcommand %q", args[0])
	}
}

func (c cmd) usage(run *command.Context) {
	run.UsageCommandGroup(c, run.Program+" channel <subcommand> [flags]", []string{
		"reload                  Reload channel configuration",
		"feishu <subcommand>     Manage Feishu channel configuration",
	})
}

func (c cmd) runReload(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	fs := run.NewFlagSet("channel reload", run.Program+" channel reload", "Reload channel configuration.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("channel reload does not accept positional arguments")
	}
	var resp reloadResponse
	if err := run.APIClient(globals).DoJSON(ctx, http.MethodPost, "/api/v1/channels/reload", nil, &resp); err != nil {
		return err
	}
	return renderReload(globals.Output, run.Stdout, resp)
}

func (c cmd) runFeishu(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	if len(args) == 0 || command.IsHelpArg(args[0]) {
		c.usageFeishu(run)
		return fmt.Errorf("channel feishu requires a subcommand")
	}
	switch args[0] {
	case "get":
		return c.runFeishuGet(ctx, run, args[1:], globals)
	case "set":
		return c.runFeishuSet(ctx, run, args[1:], globals)
	case "doctor":
		return c.runFeishuDoctor(ctx, run, args[1:], globals)
	default:
		c.usageFeishu(run)
		return fmt.Errorf("unknown channel feishu subcommand %q", args[0])
	}
}

func (c cmd) usageFeishu(run *command.Context) {
	run.UsageCommandGroup(c, run.Program+" channel feishu <subcommand> [flags]", []string{
		"get       Get masked Feishu config for a bot",
		"set       Set Feishu app_id/app_secret for a bot",
		"doctor    Check whether Feishu config is present for a bot",
	})
}

func (c cmd) runFeishuGet(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	fs := run.NewFlagSet("channel feishu get", run.Program+" channel feishu get --bot-id <id>", "Get masked Feishu channel config for a bot.")
	botID := fs.String("bot-id", "", "bot id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("channel feishu get does not accept positional arguments")
	}
	id, err := requireBotID(*botID)
	if err != nil {
		return err
	}
	cfg, err := getFeishuConfig(ctx, run, globals, id)
	if err != nil {
		return err
	}
	return renderFeishuConfig(globals.Output, run.Stdout, cfg)
}

func (c cmd) runFeishuSet(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	fs := run.NewFlagSet("channel feishu set", run.Program+" channel feishu set --bot-id <id> --app-id <id> (--app-secret-file <path>|--app-secret-stdin|--app-secret-env <name>)", "Set Feishu channel config for a bot. Secrets are not accepted as plain command-line values.")
	botID := fs.String("bot-id", "", "bot id")
	appID := fs.String("app-id", "", "Feishu app id")
	adminOpenID := fs.String("admin-open-id", "", "Feishu admin open_id")
	secretFile := fs.String("app-secret-file", "", "read Feishu app secret from file")
	secretEnv := fs.String("app-secret-env", "", "read Feishu app secret from environment variable")
	secretStdin := fs.Bool("app-secret-stdin", false, "read Feishu app secret from stdin")
	noReload := fs.Bool("no-reload", false, "write config without reloading running server")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("channel feishu set does not accept positional arguments")
	}
	id, err := requireBotID(*botID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*appID) == "" {
		return fmt.Errorf("channel feishu set requires --app-id")
	}
	secret, err := readSecret(run.Stdin, *secretFile, *secretEnv, *secretStdin)
	if err != nil {
		return err
	}
	reload := !*noReload
	var resp feishuConfigResponse
	path := "/api/v1/channels/feishu/config/" + url.PathEscape(id)
	req := feishuConfigRequest{AppID: strings.TrimSpace(*appID), AppSecret: secret, AdminOpenID: strings.TrimSpace(*adminOpenID), Reload: &reload}
	if err := run.APIClient(globals).DoJSON(ctx, http.MethodPut, path, req, &resp); err != nil {
		return err
	}
	return renderFeishuConfig(globals.Output, run.Stdout, resp)
}

func (c cmd) runFeishuDoctor(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	fs := run.NewFlagSet("channel feishu doctor", run.Program+" channel feishu doctor --bot-id <id>", "Check Feishu channel config for a bot.")
	botID := fs.String("bot-id", "", "bot id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("channel feishu doctor does not accept positional arguments")
	}
	id, err := requireBotID(*botID)
	if err != nil {
		return err
	}
	cfg, err := getFeishuConfig(ctx, run, globals, id)
	if err != nil {
		return err
	}
	result := doctorResult{BotID: id, Configured: cfg.Configured, AppID: cfg.AppID, AppSecret: cfg.AppSecret, AdminOpenID: cfg.AdminOpenID}
	if cfg.Configured && strings.TrimSpace(cfg.AppID) != "" && cfg.AppSecret == "present" {
		result.Status = "ok"
		result.Message = "Feishu channel config is present; recreate the agent to refresh PicoClaw runtime env."
	} else {
		result.Status = "missing"
		result.Message = "Feishu channel config is incomplete."
	}
	return renderDoctor(globals.Output, run.Stdout, result)
}

func getFeishuConfig(ctx context.Context, run *command.Context, globals command.GlobalOptions, botID string) (feishuConfigResponse, error) {
	var resp feishuConfigResponse
	path := "/api/v1/channels/feishu/config/" + url.PathEscape(botID)
	if err := run.APIClient(globals).DoJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return feishuConfigResponse{}, err
	}
	return resp, nil
}

func requireBotID(botID string) (string, error) {
	botID = strings.TrimSpace(botID)
	if botID == "" {
		return "", fmt.Errorf("--bot-id is required")
	}
	for _, r := range botID {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return "", fmt.Errorf("invalid --bot-id %q: only letters, digits, '-' and '_' are allowed", botID)
		}
	}
	return botID, nil
}

func readSecret(stdin io.Reader, filePath, envName string, fromStdin bool) (string, error) {
	count := 0
	if strings.TrimSpace(filePath) != "" {
		count++
	}
	if strings.TrimSpace(envName) != "" {
		count++
	}
	if fromStdin {
		count++
	}
	if count != 1 {
		return "", fmt.Errorf("provide exactly one of --app-secret-file, --app-secret-env, or --app-secret-stdin")
	}
	var secret string
	switch {
	case strings.TrimSpace(filePath) != "":
		data, err := os.ReadFile(strings.TrimSpace(filePath))
		if err != nil {
			return "", fmt.Errorf("read app secret file: %w", err)
		}
		secret = string(data)
	case strings.TrimSpace(envName) != "":
		value, ok := os.LookupEnv(strings.TrimSpace(envName))
		if !ok {
			return "", fmt.Errorf("environment variable %s is not set", strings.TrimSpace(envName))
		}
		secret = value
	case fromStdin:
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read app secret from stdin: %w", err)
		}
		secret = string(data)
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", fmt.Errorf("app secret is empty")
	}
	return secret, nil
}

func renderReload(output string, w io.Writer, resp reloadResponse) error {
	if output == "json" {
		return command.WriteJSON(w, resp)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tFEISHU_BOTS")
	fmt.Fprintf(tw, "%s\t%s\n", display(resp.Status), display(strings.Join(resp.FeishuBots, ",")))
	return tw.Flush()
}

func renderFeishuConfig(output string, w io.Writer, cfg feishuConfigResponse) error {
	if output == "json" {
		return command.WriteJSON(w, cfg)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BOT_ID\tCONFIGURED\tAPP_ID\tAPP_SECRET\tADMIN_OPEN_ID\tRELOADED")
	fmt.Fprintf(tw, "%s\t%t\t%s\t%s\t%s\t%t\n", cfg.BotID, cfg.Configured, display(cfg.AppID), display(cfg.AppSecret), display(cfg.AdminOpenID), cfg.Reloaded)
	return tw.Flush()
}

func renderDoctor(output string, w io.Writer, result doctorResult) error {
	if output == "json" {
		return command.WriteJSON(w, result)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BOT_ID\tSTATUS\tCONFIGURED\tAPP_ID\tAPP_SECRET\tMESSAGE")
	fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%s\n", result.BotID, result.Status, result.Configured, display(result.AppID), display(result.AppSecret), result.Message)
	return tw.Flush()
}

func display(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
