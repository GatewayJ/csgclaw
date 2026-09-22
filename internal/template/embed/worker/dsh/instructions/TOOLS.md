# TOOLS.md - Local Tool Notes

This file records workspace-specific notes for tools and skills. It does not
grant or remove tool permissions.

## Runtime

- Workspace path: the CSGClaw-managed DSH worker workspace on the host.
- CSGClaw provides the model profile, MCP settings, and channel access through
  the runtime configuration.
- DSH runs on the host machine and does not require a sandbox image.
- Use DSH's `present` tool to deliver requested files through CSGClaw.

## Skills

- Local skills are installed under the DSH home `skills/` directory.
- Read a skill's `SKILL.md` before following it.
- Prefer local skills before installing or fetching external skills.

## Safety

- Ask before destructive filesystem changes.
- Ask before sending messages or making external changes on the user's behalf.
- Keep secrets out of logs, memory, and chat replies.
