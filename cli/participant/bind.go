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
		run.Program+" "+c.Name()+" bind --channel feishu --subject (human|agent-app) [flags]",
		"Bind a channel identity to a participant.",
	)
	channelName := fs.String("channel", "feishu", "channel name; only feishu is supported")
	subject := fs.String("subject", "", "binding subject: human or agent-app")
	profile := fs.String("profile", "", "binding profile such as admin")
	identityKind := fs.String("identity-kind", "", "channel identity kind such as open_id")
	identityRef := fs.String("identity-ref", "", "channel identity reference such as Feishu open_id")
	agentID := fs.String("agent-id", "", "agent name or id for agent-app binding")
	appRef := fs.String("app-ref", "", "channel app/config reference such as Feishu app_id")
	name := fs.String("name", "", "participant display name for human binding")
	secretFile := fs.String("app-secret-file", "", "read Feishu app secret from file")
	secretEnv := fs.String("app-secret-env", "", "read Feishu app secret from environment variable")
	secretStdin := fs.Bool("app-secret-stdin", false, "read Feishu app secret from stdin")
	apply := fs.Bool("apply", false, "apply saved app config to the runtime; workers are recreated and manager returns restart_status=manager_restart_required")
	feishuKind := fs.String("feishu-kind", "", "deprecated alias for --subject: human or bot")
	agentRef := fs.String("agent", "", "deprecated alias for --agent-id")
	admin := fs.Bool("admin", false, "deprecated alias for --profile admin")
	openID := fs.String("open-id", "", "deprecated alias for --identity-ref with --identity-kind open_id")
	appID := fs.String("app-id", "", "deprecated alias for --app-ref")
	restart := fs.Bool("restart", false, "deprecated alias for --apply")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("%s bind does not accept positional arguments", c.Name())
	}
	if normalizeChannel(*channelName) != participantpkg.ChannelFeishu {
		return fmt.Errorf("%s bind currently supports only --channel feishu", c.Name())
	}
	opts, err := resolveBindOptions(bindOptionInputs{
		Subject:       *subject,
		LegacyKind:    *feishuKind,
		Profile:       *profile,
		LegacyAdmin:   *admin,
		IdentityKind:  *identityKind,
		IdentityRef:   *identityRef,
		LegacyOpenID:  *openID,
		AgentID:       *agentID,
		LegacyAgent:   *agentRef,
		AppRef:        *appRef,
		LegacyAppID:   *appID,
		Apply:         *apply,
		LegacyRestart: *restart,
	})
	if err != nil {
		return err
	}
	switch opts.Subject {
	case "human":
		return c.runBindFeishuHuman(ctx, run, globals, opts.Profile, opts.IdentityKind, opts.IdentityRef, *name)
	case "agent-app":
		return c.runBindFeishuBot(ctx, run, globals, opts.AgentID, opts.AppRef, *secretFile, *secretEnv, *secretStdin, opts.Apply)
	default:
		return fmt.Errorf("--subject must be one of %q or %q", "human", "agent-app")
	}
}

type bindOptionInputs struct {
	Subject       string
	LegacyKind    string
	Profile       string
	LegacyAdmin   bool
	IdentityKind  string
	IdentityRef   string
	LegacyOpenID  string
	AgentID       string
	LegacyAgent   string
	AppRef        string
	LegacyAppID   string
	Apply         bool
	LegacyRestart bool
}

type bindOptions struct {
	Subject      string
	Profile      string
	IdentityKind string
	IdentityRef  string
	AgentID      string
	AppRef       string
	Apply        bool
}

func resolveBindOptions(in bindOptionInputs) (bindOptions, error) {
	subject, err := resolveBindSubject(in.Subject, in.LegacyKind)
	if err != nil {
		return bindOptions{}, err
	}
	profile := strings.ToLower(strings.TrimSpace(in.Profile))
	if in.LegacyAdmin {
		if profile != "" && profile != "admin" {
			return bindOptions{}, fmt.Errorf("--admin conflicts with --profile %q", profile)
		}
		profile = "admin"
	}
	identityKind := strings.ToLower(strings.TrimSpace(in.IdentityKind))
	identityRef, err := resolveStringAlias("--identity-ref", in.IdentityRef, "--open-id", in.LegacyOpenID)
	if err != nil {
		return bindOptions{}, err
	}
	if strings.TrimSpace(in.LegacyOpenID) != "" {
		if identityKind != "" && identityKind != participantpkg.ChannelUserKindOpenID {
			return bindOptions{}, fmt.Errorf("--open-id conflicts with --identity-kind %q", identityKind)
		}
		identityKind = participantpkg.ChannelUserKindOpenID
	}
	agentID, err := resolveStringAlias("--agent-id", in.AgentID, "--agent", in.LegacyAgent)
	if err != nil {
		return bindOptions{}, err
	}
	appRef, err := resolveStringAlias("--app-ref", in.AppRef, "--app-id", in.LegacyAppID)
	if err != nil {
		return bindOptions{}, err
	}
	return bindOptions{
		Subject:      subject,
		Profile:      profile,
		IdentityKind: identityKind,
		IdentityRef:  identityRef,
		AgentID:      agentID,
		AppRef:       appRef,
		Apply:        in.Apply || in.LegacyRestart,
	}, nil
}

func resolveBindSubject(subject, legacyKind string) (string, error) {
	subject = normalizeBindSubject(subject)
	legacyKind = strings.ToLower(strings.TrimSpace(legacyKind))
	legacySubject := ""
	switch legacyKind {
	case "":
	case "human":
		legacySubject = "human"
	case "bot":
		legacySubject = "agent-app"
	default:
		return "", fmt.Errorf("--feishu-kind must be one of %q or %q", "human", "bot")
	}
	if subject != "" && legacySubject != "" && subject != legacySubject {
		return "", fmt.Errorf("--subject %q conflicts with --feishu-kind %q", subject, legacyKind)
	}
	if subject == "" {
		subject = legacySubject
	}
	if subject == "" {
		return "", fmt.Errorf("--subject must be one of %q or %q", "human", "agent-app")
	}
	return subject, nil
}

func normalizeBindSubject(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "human":
		return "human"
	case "agent-app", "agent_app", "agentapp", "bot":
		return "agent-app"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func resolveStringAlias(primaryName, primaryValue, aliasName, aliasValue string) (string, error) {
	primaryValue = strings.TrimSpace(primaryValue)
	aliasValue = strings.TrimSpace(aliasValue)
	if primaryValue != "" && aliasValue != "" && primaryValue != aliasValue {
		return "", fmt.Errorf("%s conflicts with %s", primaryName, aliasName)
	}
	if primaryValue != "" {
		return primaryValue, nil
	}
	return aliasValue, nil
}

func (c cmd) runBindFeishuHuman(ctx context.Context, run *command.Context, globals command.GlobalOptions, profile, identityKind, identityRef, name string) error {
	if strings.TrimSpace(profile) != "admin" {
		return fmt.Errorf("%s bind --subject human currently requires --profile admin", c.Name())
	}
	identityKind = strings.ToLower(strings.TrimSpace(identityKind))
	if identityKind == "" {
		identityKind = participantpkg.ChannelUserKindOpenID
	}
	if identityKind != participantpkg.ChannelUserKindOpenID {
		return fmt.Errorf("%s bind --subject human currently supports only --identity-kind %s", c.Name(), participantpkg.ChannelUserKindOpenID)
	}
	identityRef = strings.TrimSpace(identityRef)
	if identityRef == "" {
		return fmt.Errorf("%s bind --subject human requires --identity-ref", c.Name())
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "admin"
	}
	client := run.APIClient(globals)
	participantID := "admin"
	item, err := upsertFeishuAdminParticipant(ctx, client, participantID, name, identityRef)
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

func (c cmd) runBindFeishuBot(ctx context.Context, run *command.Context, globals command.GlobalOptions, agentRef, appID, secretFile, secretEnv string, secretStdin bool, apply bool) error {
	agentRef = strings.TrimSpace(agentRef)
	if agentRef == "" {
		return fmt.Errorf("%s bind --subject agent-app requires --agent-id", c.Name())
	}
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return fmt.Errorf("%s bind --subject agent-app requires --app-ref", c.Name())
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
	if apply {
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
