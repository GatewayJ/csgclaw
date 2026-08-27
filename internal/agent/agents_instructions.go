package agent

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

const (
	agentsInstructionsBlockStart = "<!-- BEGIN CSGCLAW-INSTRUCTIONS (auto-generated; do not edit) -->"
	agentsInstructionsBlockEnd   = "<!-- END CSGCLAW-INSTRUCTIONS -->"
)

//go:embed embed/agents_instructions.md.tmpl
var agentsInstructionsBlockTemplate string

var parsedAgentsInstructionsBlockTemplate = template.Must(
	template.New("agents_instructions_block").Parse(agentsInstructionsBlockTemplate),
)

func AgentsInstructionsBlockMarkers() (start string, end string) {
	return agentsInstructionsBlockStart, agentsInstructionsBlockEnd
}

func ExtractUserInstructionsFromAgentsDocument(document string) string {
	document = strings.ReplaceAll(document, "\r\n", "\n")
	start := strings.Index(document, agentsInstructionsBlockStart)
	if start < 0 {
		return ""
	}
	endOffset := strings.Index(document[start:], agentsInstructionsBlockEnd)
	if endOffset < 0 {
		return ""
	}
	block := document[start : start+endOffset]
	heading := "# Agent Instructions\n\n"
	bodyStart := strings.Index(block, heading)
	if bodyStart < 0 {
		return ""
	}
	body := block[bodyStart+len(heading):]
	for _, nextHeading := range []string{"\n# Managed Runtime Instructions", "\n# CSGClaw Rules"} {
		if idx := strings.Index(body, nextHeading); idx >= 0 {
			body = body[:idx]
			break
		}
	}
	return strings.TrimSpace(body)
}

func RenderAgentsInstructionsBlock(instructions string) string {
	return renderAgentsInstructionsBlock(instructions, "")
}

type RuntimeManagedInstructionsOptions struct {
	FeishuLarkCLI bool
}

func RenderRuntimeAgentsInstructionsBlock(agentID, instructions string) string {
	return RenderRuntimeAgentsInstructionsBlockWithOptions(agentID, instructions, RuntimeManagedInstructionsOptions{})
}

func RenderRuntimeAgentsInstructionsBlockWithOptions(agentID, instructions string, options RuntimeManagedInstructionsOptions) string {
	managedInstructions := strings.TrimSpace(runtimeFilePublishingInstructions)
	if strings.TrimSpace(agentID) == ManagerUserID {
		managedInstructions = joinManagedInstructions(managedInstructions, managerRuntimeConnectorInstructions)
	}
	if options.FeishuLarkCLI {
		managedInstructions = joinManagedInstructions(managedInstructions, feishuLarkCLIManagedInstructions)
	}
	return renderAgentsInstructionsBlock(instructions, managedInstructions)
}

const runtimeFilePublishingInstructions = `### Output File Delivery

- When ` + "`csgclaw_publish_file`" + ` is available and the user asks to receive a generated file, create the file in the Runtime workspace.
- Call ` + "`csgclaw_publish_file`" + ` with the file's workspace-relative path immediately after creating it.
- Do not search for or use ` + "`csgclaw-cli`" + `, ` + "`curl`" + `, HTTP APIs, channel-specific APIs, or other upload methods for output file delivery.
- Calling the tool publishes the file through the active channel. Mention the file in the final answer only after the tool succeeds.`

func joinManagedInstructions(values ...string) string {
	var parts []string
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}

const managerRuntimeConnectorInstructions = `### GitHub Connector Access

- The Manager can request CSGClaw-managed connector credentials dynamically through the local CSGClaw API.
- For GitHub repository, pull request, issue, or review workflows, request a fresh lease with ` + "`POST $CSGCLAW_BASE_URL/api/v1/agents/agent-manager/connectors/github/credential`" + ` using ` + "`Authorization: Bearer $CSGCLAW_ACCESS_TOKEN`" + ` and ` + "`X-CSGClaw-Connector-Capability: $CSGCLAW_CONNECTOR_CAPABILITY`" + `.
- Use the returned ` + "`access_token`" + ` only in process memory for GitHub API or GitHub CLI calls.
- Never print, echo, log, write, persist, or include the token value in prompts, messages, UI text, state files, snapshots, or ` + "`AGENTS.md`" + ` edits.
- Do not rely on connector tokens from environment variables such as ` + "`GITHUB_TOKEN`" + `; connector credentials are intentionally fetched on demand so reconnects and refreshes work without restarting the Manager.
- Do not treat an empty result from an external Codex GitHub app connector as proof that the CSGClaw GitHub connector has no repository access.
- If the credential API returns ` + "`400`" + `, ` + "`401`" + `, or ` + "`403`" + `, tell the user to reconnect the CSGClaw GitHub OAuth connector or check connector access policy.

### GitLab Connector Access

- The Manager can request CSGClaw-managed GitLab credentials on demand with ` + "`POST $CSGCLAW_BASE_URL/api/v1/agents/agent-manager/connectors/gitlab/credential`" + ` using ` + "`Authorization: Bearer $CSGCLAW_ACCESS_TOKEN`" + ` and ` + "`X-CSGClaw-Connector-Capability: $CSGCLAW_CONNECTOR_CAPABILITY`" + `.
- Use the lease's ` + "`base_url`" + ` and ` + "`access_token`" + ` only in process memory. Never copy the GitLab token into environment setup, Git credential storage, prompts, messages, files, or logs.
- If the GitLab credential API returns ` + "`400`" + `, ` + "`401`" + `, or ` + "`403`" + `, tell the user to reconnect GitLab or check connector access policy.

### Historical Attachment Recovery

- Treat files under ` + "`.csgclaw/attachments/`" + ` as runtime-local cache copies, not as the durable attachment index.
- When the user refers to a previously uploaded file that is absent from the current workspace, query CSGClaw message history before claiming the file is unavailable or asking the user to upload it again.
- Use the current ` + "`channel`" + ` and ` + "`room_id`" + ` from the hidden channel context with ` + "`csgclaw-cli message list --channel <current_channel> --room-id <target_room_id>`" + `.
- Filter the JSON locally to attachment-bearing messages and retain ` + "`id`" + `, ` + "`name`" + `, ` + "`media_type`" + `, ` + "`size_bytes`" + `, ` + "`sha256`" + `, ` + "`created_at`" + `, the originating message ID, and the originating message text.
- Use a structured pipeline that excludes capability-bearing download URLs, such as ` + "`csgclaw-cli message list --channel <current_channel> --room-id <target_room_id> | jq '[.[] as $message | ($message.attachments // [])[] | {id, name, kind, media_type, size_bytes, sha256, created_at, message_id: $message.id, message_text: $message.content}]'`" + `.
- Match candidates using the filename, the originating message text, and recency.
- If exactly one candidate matches, download it by stable attachment ID into ` + "`.csgclaw/retrieved/<attachment-id>-<safe-name>`" + ` with ` + "`GET $CSGCLAW_BASE_URL/api/v1/attachments/<attachment-id>`" + ` and ` + "`Authorization: Bearer $CSGCLAW_ACCESS_TOKEN`" + `.
- A safe download command is ` + "`curl -fsS -H \"Authorization: Bearer ${CSGCLAW_ACCESS_TOKEN:?}\" \"$CSGCLAW_BASE_URL/api/v1/attachments/<attachment-id>\" --output \".csgclaw/retrieved/<attachment-id>-<safe-name>\"`" + `.
- Use the stable attachment ID for authenticated downloads instead of copying a capability-bearing ` + "`download_url`" + ` into commands, logs, or responses.
- Verify the downloaded file against its ` + "`sha256`" + ` before reading it.
- If multiple candidates plausibly match, show the user a concise candidate list instead of guessing.
- If the current room has no match and the user clearly refers to an upload from another conversation, list rooms and inspect only the relevant candidate rooms.
- Do not search the web for a referenced upload, rely only on ` + "`find`" + ` in the current workspace, or request a re-upload until durable CSGClaw history has been checked.
- Never print, echo, or include ` + "`CSGCLAW_ACCESS_TOKEN`" + ` or a capability token in tool output, logs, prompts, or responses.`

const feishuLarkCLIManagedInstructions = `### Feishu lark-cli Access

- This worker is bound to a Feishu app through lark-cli. Plain ` + "`lark-cli ...`" + ` commands inherit the current worker context from ` + "`LARK_CHANNEL=1`" + `, ` + "`LARK_CHANNEL_HOME`" + `, ` + "`LARK_CHANNEL_PROFILE`" + `, ` + "`LARK_CHANNEL_CONFIG`" + `, and ` + "`LARKSUITE_CLI_CONFIG_DIR`" + `.
- Do not unset those variables, do not use the host default lark-cli profile, and do not read or print lark-cli config files, app secrets, access tokens, refresh tokens, OAuth device codes, or CSGClaw API tokens.
- If lark-cli reports that the lark-channel context is not bound, stop and tell the user to initialize lark-cli for this worker from the Feishu channel profile page or restart the worker after initialization. Do not run bind manually from an ordinary prompt.
- For Feishu Doc/Docx file tokens, first try ` + "`lark-cli docs +fetch --api-version v2 --doc <file_token> --doc-format markdown`" + `. If this lark-cli version does not support that exact syntax, inspect ` + "`lark-cli docs --help`" + ` once and use the equivalent current read-only command for the same token.
- For Feishu Drive/Wiki file nodes, use the current lark-cli drive download/read-only command and write downloaded files under the current workspace, for example ` + "`./downloads/`" + `. Do not upload generated local files back to Feishu unless the user explicitly asks and the available command is clearly write-capable and authorized.

### Feishu Historical Attachment Recovery

- Apply these rules only when the hidden channel context identifies the current request as Feishu and provides the current ` + "`chat_id`" + `. When the user refers to a previously uploaded Feishu file that is absent from the workspace, search only that current chat before asking for a re-upload.
- List message metadata without downloading resources first: ` + "`lark-cli im +chat-messages-list --as bot --chat-id <current_chat_id> --order desc --page-size 50 --no-reactions --format json`" + `. Use the user's time description with ` + "`--start`" + ` or ` + "`--end`" + ` when available, and follow pagination only as needed.
- Do not search, list, or inspect other chats. Do not use ` + "`--download-resources`" + ` during discovery because it downloads every eligible resource in the result set.
- Match candidates using ` + "`message_id`" + `, ` + "`msg_type`" + `, filename or resource marker in ` + "`content`" + `, surrounding message text, sender, and creation time. The selected resource key and ` + "`message_id`" + ` must come from the same message; never guess or combine identifiers.
- If exactly one candidate matches, download only that resource with ` + "`lark-cli im +messages-resources-download --as bot --message-id <message_id> --file-key <resource_key> --type <file_or_image> --output downloads/feishu/<safe_name>`" + `. Use ` + "`image`" + ` for image keys and ` + "`file`" + ` for files, audio, or video. Keep the output path relative and free of ` + "`..`" + ` traversal.
- If multiple candidates match, show a concise list without raw resource keys and ask the user to choose. If none match, state that no matching attachment was found in the current Feishu conversation.
- Keep Bot identity explicit with ` + "`--as bot`" + `. If Bot access fails because of missing scopes or chat membership, report the lark-cli error and tell the user which Bot permission or membership is required; do not silently retry as a user.
- If either shortcut is unavailable, inspect its ` + "`--help`" + ` once. If it is still unavailable, tell the user to upgrade lark-cli; do not install or upgrade lark-cli from the worker prompt.

- Start ` + "`lark-cli auth login`" + ` only in a Feishu private chat with the user who should own the authorization. In group chats, ask the user to open a private chat instead.
- Prefer the two-step OAuth flow when user authorization is needed: run ` + "`lark-cli auth login --no-wait --json --recommend`" + `, show the verification URL plainly to the user, then wait in the foreground with ` + "`lark-cli auth login --device-code <code>`" + `. Do not background the device-code wait.
- After user OAuth succeeds, silently converge identity policy with ` + "`lark-cli config strict-mode off`" + ` and ` + "`lark-cli config default-as auto`" + ` before retrying a user-identity read. Do not ask the user to choose those internal settings.`

func renderAgentsInstructionsBlock(instructions, managedInstructions string) string {
	instructions = strings.TrimSpace(instructions)
	managedInstructions = strings.TrimSpace(managedInstructions)
	data := struct {
		StartMarker            string
		EndMarker              string
		Instructions           string
		HasInstructions        bool
		ManagedInstructions    string
		HasManagedInstructions bool
	}{
		StartMarker:            agentsInstructionsBlockStart,
		EndMarker:              agentsInstructionsBlockEnd,
		Instructions:           instructions,
		HasInstructions:        instructions != "",
		ManagedInstructions:    managedInstructions,
		HasManagedInstructions: managedInstructions != "",
	}
	var b bytes.Buffer
	if err := parsedAgentsInstructionsBlockTemplate.Execute(&b, data); err != nil {
		panic("render agents instructions block: " + err.Error())
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
