package participant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"csgclaw/cli/command"
	"csgclaw/internal/apitypes"
	participantpkg "csgclaw/internal/participant"
)

type appInfoAPIClient interface {
	GetAgentFeishuAppInfo(ctx context.Context, id string) (apitypes.FeishuBotAppInfo, error)
}

type execProviderRequest struct {
	ProtocolVersion int      `json:"protocolVersion"`
	Provider        string   `json:"provider"`
	IDs             []string `json:"ids"`
}

type execProviderResponse struct {
	ProtocolVersion int                          `json:"protocolVersion"`
	Values          map[string]string            `json:"values,omitempty"`
	Errors          map[string]execProviderError `json:"errors,omitempty"`
}

type execProviderError struct {
	Message string `json:"message"`
}

func (c cmd) runAppInfo(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	fs := run.NewFlagSet(
		c.Name()+" app-info",
		run.Program+" "+c.Name()+" app-info --channel feishu --agent-id <id> [--exec-provider]",
		"Read Feishu bot app info for a bound worker.",
	)
	channelName := fs.String("channel", "feishu", "channel name; only feishu is supported")
	agentID := fs.String("agent-id", "", "agent id for Feishu bot app info")
	execProvider := fs.Bool("exec-provider", false, "emit lark-cli exec secret provider protocol JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("%s app-info does not accept positional arguments", c.Name())
	}
	if normalizeChannel(*channelName) != participantpkg.ChannelFeishu {
		return fmt.Errorf("%s app-info currently supports only --channel feishu", c.Name())
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("%s app-info requires --agent-id", c.Name())
	}

	info, err := run.APIClient(globals).GetAgentFeishuAppInfo(ctx, *agentID)
	if err != nil {
		return err
	}
	if *execProvider {
		return renderExecProviderAppInfo(run.Stdin, run.Stdout, info)
	}
	return renderAppInfo(globals.Output, run.Stdout, info)
}

func renderExecProviderAppInfo(stdin io.Reader, stdout io.Writer, info apitypes.FeishuBotAppInfo) error {
	var req execProviderRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode exec provider request: %w", err)
	}
	if req.ProtocolVersion != 1 {
		return fmt.Errorf("exec provider protocolVersion must be 1, got %d", req.ProtocolVersion)
	}
	resp := execProviderResponse{
		ProtocolVersion: 1,
		Values:          map[string]string{},
		Errors:          map[string]execProviderError{},
	}
	for _, id := range req.IDs {
		switch strings.TrimSpace(id) {
		case "app_id":
			resp.Values[id] = strings.TrimSpace(info.AppID)
		case "app_secret":
			resp.Values[id] = strings.TrimSpace(info.AppSecret)
		case "":
			continue
		default:
			resp.Errors[id] = execProviderError{Message: "unsupported secret id"}
		}
	}
	if len(resp.Errors) == 0 {
		resp.Errors = nil
	}
	return json.NewEncoder(stdout).Encode(resp)
}

func renderAppInfo(output string, w io.Writer, info apitypes.FeishuBotAppInfo) error {
	if output == "json" {
		info.AppSecret = participantpkg.RedactedSecretValue
		return command.WriteJSON(w, info)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT_ID\tPARTICIPANT_ID\tAPP_ID\tAPP_SECRET")
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
		display(info.AgentID),
		display(info.ParticipantID),
		display(info.AppID),
		participantpkg.RedactedSecretValue,
	)
	return tw.Flush()
}
