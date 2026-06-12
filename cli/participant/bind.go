package participant

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"csgclaw/cli/command"
	"csgclaw/internal/agent"
	"csgclaw/internal/apiclient"
	"csgclaw/internal/apitypes"
	participantpkg "csgclaw/internal/participant"
)

type bindResult struct {
	Status          string   `json:"status"`
	Channel         string   `json:"channel"`
	ParticipantType string   `json:"participant_type"`
	ParticipantID   string   `json:"participant_id"`
	AgentID         string   `json:"agent_id,omitempty"`
	ConfigSaved     bool     `json:"config_saved"`
	RestartStatus   string   `json:"restart_status,omitempty"`
	RestartError    string   `json:"restart_error,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

func (c cmd) runBind(ctx context.Context, run *command.Context, args []string, globals command.GlobalOptions) error {
	fs := run.NewFlagSet(
		c.Name()+" bind",
		run.Program+" "+c.Name()+" bind --channel feishu --feishu-kind (human|bot) [flags]",
		"Bind a channel identity to a participant.",
	)
	channelName := fs.String("channel", "feishu", "channel name; only feishu is supported")
	feishuKind := fs.String("feishu-kind", "", "Feishu identity kind: human or bot")
	agentRef := fs.String("agent", "", "agent name or id for Feishu bot binding")
	name := fs.String("name", "", "participant display name for Feishu human binding")
	admin := fs.Bool("admin", false, "bind the Feishu admin human participant")
	openID := fs.String("open-id", "", "Feishu human open_id")
	appID := fs.String("app-id", "", "Feishu app id for bot binding")
	secretFile := fs.String("app-secret-file", "", "read Feishu app secret from file")
	secretEnv := fs.String("app-secret-env", "", "read Feishu app secret from environment variable")
	secretStdin := fs.Bool("app-secret-stdin", false, "read Feishu app secret from stdin")
	restart := fs.Bool("restart", false, "recreate worker after bot config is saved; manager returns restart_status=manager_restart_required")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("%s bind does not accept positional arguments", c.Name())
	}
	if normalizeChannel(*channelName) != participantpkg.ChannelFeishu {
		return fmt.Errorf("%s bind currently supports only --channel feishu", c.Name())
	}
	kind := strings.ToLower(strings.TrimSpace(*feishuKind))
	switch kind {
	case "human":
		return c.runBindFeishuHuman(ctx, run, globals, *admin, *openID, *name)
	case "bot":
		return c.runBindFeishuBot(ctx, run, globals, *agentRef, *appID, *secretFile, *secretEnv, *secretStdin, *restart)
	default:
		return fmt.Errorf("--feishu-kind must be one of %q or %q", "human", "bot")
	}
}

func (c cmd) runBindFeishuHuman(ctx context.Context, run *command.Context, globals command.GlobalOptions, admin bool, openID, name string) error {
	if !admin {
		return fmt.Errorf("%s bind --feishu-kind human currently requires --admin", c.Name())
	}
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return fmt.Errorf("%s bind --feishu-kind human requires --open-id", c.Name())
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "admin"
	}
	client := run.APIClient(globals)
	participantID := "admin"
	item, err := upsertFeishuAdminParticipant(ctx, client, participantID, name, openID)
	if err != nil {
		return fmt.Errorf("bind feishu admin human participant_id=%q: %w", participantID, err)
	}
	return renderBindResult(globals.Output, run.Stdout, bindResult{
		Status:          "configured",
		Channel:         participantpkg.ChannelFeishu,
		ParticipantType: participantpkg.TypeHuman,
		ParticipantID:   item.ID,
		ConfigSaved:     true,
	})
}

func (c cmd) runBindFeishuBot(ctx context.Context, run *command.Context, globals command.GlobalOptions, agentRef, appID, secretFile, secretEnv string, secretStdin bool, restart bool) error {
	agentRef = strings.TrimSpace(agentRef)
	if agentRef == "" {
		return fmt.Errorf("%s bind --feishu-kind bot requires --agent", c.Name())
	}
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return fmt.Errorf("%s bind --feishu-kind bot requires --app-id", c.Name())
	}
	appSecret, err := readSecret(run.Stdin, secretFile, secretEnv, secretStdin)
	if err != nil {
		return err
	}
	client := run.APIClient(globals)
	target, err := resolveBindAgent(ctx, client, agentRef)
	if err != nil {
		return fmt.Errorf("bind feishu bot resolve agent %q: %w", agentRef, err)
	}
	participantID := agent.ParticipantIDForAgent(target.Name, target.ID)
	item, warnings, err := upsertFeishuBotParticipant(ctx, client, participantID, target, appID, appSecret)
	if err != nil {
		return fmt.Errorf("bind feishu bot participant_id=%q agent_id=%q: %w", participantID, target.ID, err)
	}
	for _, warning := range warnings {
		fmt.Fprintln(run.Stderr, "warning:", warning)
	}

	result := bindResult{
		Status:          "configured",
		Channel:         participantpkg.ChannelFeishu,
		ParticipantType: participantpkg.TypeAgent,
		ParticipantID:   item.ID,
		AgentID:         target.ID,
		ConfigSaved:     true,
		Warnings:        warnings,
	}
	if restart {
		if strings.EqualFold(target.ID, agent.ManagerUserID) || strings.EqualFold(target.Role, agent.RoleManager) {
			result.RestartStatus = "manager_restart_required"
		} else {
			if _, err := client.RecreateAgent(ctx, target.ID); err != nil {
				fmt.Fprintf(run.Stderr, "pt bind failed at recreate: agent_id=%s participant_id=%s error=%v\n", target.ID, item.ID, err)
				result.Status = "partial"
				result.RestartStatus = "recreate_failed"
				result.RestartError = err.Error()
				return renderBindResult(globals.Output, run.Stdout, result)
			}
			result.RestartStatus = "worker_recreated"
		}
	} else {
		result.RestartStatus = "restart_skipped"
	}
	return renderBindResult(globals.Output, run.Stdout, result)
}

func resolveBindAgent(ctx context.Context, client *apiclient.Client, ref string) (apitypes.Agent, error) {
	ref = strings.TrimSpace(ref)
	for _, candidate := range bindAgentIDCandidates(ref) {
		if got, err := client.GetAgent(ctx, candidate); err == nil {
			return got, nil
		}
	}
	agents, err := client.ListAgents(ctx)
	if err != nil {
		return apitypes.Agent{}, err
	}
	var matches []apitypes.Agent
	for _, item := range agents {
		if strings.EqualFold(strings.TrimSpace(item.Name), ref) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return apitypes.Agent{}, fmt.Errorf("agent name %q matched multiple agents", ref)
	}
	return apitypes.Agent{}, fmt.Errorf("agent %q not found", ref)
}

func normalizeChannel(channelName string) string {
	return strings.ToLower(strings.TrimSpace(channelName))
}

func display(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
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

func bindAgentIDCandidates(ref string) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	candidates := []string{ref}
	if !strings.HasPrefix(ref, "u-") {
		candidates = append(candidates, "u-"+ref)
	}
	return candidates
}

func upsertFeishuAdminParticipant(ctx context.Context, client *apiclient.Client, participantID, name, openID string) (apitypes.Participant, error) {
	existing, ok, err := findParticipantByID(ctx, client, participantpkg.ChannelFeishu, participantID)
	if err != nil {
		return apitypes.Participant{}, err
	}
	if ok {
		if existing.Type != participantpkg.TypeHuman {
			return apitypes.Participant{}, fmt.Errorf("existing participant type is %q, want %q", existing.Type, participantpkg.TypeHuman)
		}
		kind := participantpkg.ChannelUserKindOpenID
		return client.UpdateParticipant(ctx, participantpkg.ChannelFeishu, participantID, participantpkg.UpdateRequest{
			Name:            &name,
			ChannelUserRef:  &openID,
			ChannelUserKind: &kind,
		})
	}
	return client.CreateParticipant(ctx, participantpkg.CreateRequest{
		ID:      participantID,
		Channel: participantpkg.ChannelFeishu,
		Type:    participantpkg.TypeHuman,
		Name:    name,
		ChannelUser: participantpkg.ChannelUserSpec{
			Ref:  openID,
			Kind: participantpkg.ChannelUserKindOpenID,
		},
	})
}

func upsertFeishuBotParticipant(ctx context.Context, client *apiclient.Client, participantID string, target apitypes.Agent, appID, appSecret string) (apitypes.Participant, []string, error) {
	all, err := client.ListParticipants(ctx, participantpkg.ChannelFeishu, "", "")
	if err != nil {
		return apitypes.Participant{}, nil, err
	}
	var existing apitypes.Participant
	hasExisting := false
	var warnings []string
	for i := range all {
		item := all[i]
		if item.ID == participantID {
			existing = item
			hasExisting = true
			continue
		}
		if item.Type == participantpkg.TypeAgent && strings.TrimSpace(item.AgentID) == strings.TrimSpace(target.ID) {
			warnings = append(warnings, fmt.Sprintf("found noncanonical feishu participant %q for agent %q; keeping it and writing canonical participant %q", item.ID, target.ID, participantID))
		}
	}
	cfg := map[string]any{
		"app_id":     appID,
		"app_secret": appSecret,
	}
	kind := participantpkg.ChannelUserKindAppID
	displayName := strings.TrimSpace(target.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(target.ID)
	}
	if hasExisting {
		if existing.Type != participantpkg.TypeAgent {
			return apitypes.Participant{}, warnings, fmt.Errorf("existing participant type is %q, want %q", existing.Type, participantpkg.TypeAgent)
		}
		if strings.TrimSpace(existing.AgentID) != "" && strings.TrimSpace(existing.AgentID) != strings.TrimSpace(target.ID) {
			return apitypes.Participant{}, warnings, fmt.Errorf("existing participant is bound to agent %q", existing.AgentID)
		}
		name := displayName
		agentID := target.ID
		channelUserRef := ""
		updated, err := client.UpdateParticipant(ctx, participantpkg.ChannelFeishu, participantID, participantpkg.UpdateRequest{
			Name:             &name,
			ChannelUserRef:   &channelUserRef,
			ChannelUserKind:  &kind,
			ChannelAppConfig: cfg,
			AgentID:          &agentID,
		})
		return updated, warnings, err
	}
	created, err := client.CreateParticipant(ctx, participantpkg.CreateRequest{
		ID:               participantID,
		Channel:          participantpkg.ChannelFeishu,
		Type:             participantpkg.TypeAgent,
		Name:             displayName,
		ChannelAppConfig: cfg,
		ChannelUser: participantpkg.ChannelUserSpec{
			Kind: participantpkg.ChannelUserKindAppID,
		},
		AgentBinding: participantpkg.AgentBindingSpec{
			Mode:    participantpkg.BindingModeReuse,
			AgentID: target.ID,
		},
	})
	return created, warnings, err
}

func findParticipantByID(ctx context.Context, client *apiclient.Client, channel, id string) (apitypes.Participant, bool, error) {
	items, err := client.ListParticipants(ctx, channel, "", "")
	if err != nil {
		return apitypes.Participant{}, false, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return apitypes.Participant{}, false, nil
}

func renderBindResult(output string, w io.Writer, result bindResult) error {
	if output == "json" {
		return command.WriteJSON(w, result)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tCHANNEL\tTYPE\tPARTICIPANT_ID\tAGENT_ID\tCONFIG_SAVED\tRESTART\tRESTART_ERROR")
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
		display(result.Status),
		display(result.Channel),
		display(result.ParticipantType),
		display(result.ParticipantID),
		display(result.AgentID),
		result.ConfigSaved,
		display(result.RestartStatus),
		display(result.RestartError),
	)
	return tw.Flush()
}
