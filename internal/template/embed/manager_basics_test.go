package templateembed

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

func TestManagerBasicsRoomCreationKeepsRequesterAsCreator(t *testing.T) {
	tests := []struct {
		name string
		root string
	}{
		{name: "picoclaw", root: PicoClawManagerRoot},
		{name: "openclaw", root: OpenClawManagerRoot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := fs.ReadFile(FS(), path.Join(tt.root, WorkspaceDirName, "skills/basics/SKILL.md"))
			if err != nil {
				t.Fatalf("read basics skill: %v", err)
			}
			skill := string(data)

			for _, want := range []string{
				"csgclaw-cli room create --title test-room --creator-id admin --member-ids manager,<worker-participant-id> --channel csgclaw",
				"Resolve worker participant IDs with `participant list` before using them.",
				"preserve the requester as `--creator-id`",
				"include `manager` plus the requested participants in `--member-ids`",
				"Do not use `manager` as the creator just because the manager runs the CLI command.",
				"a display name such as `dev` or `qa` is not necessarily a valid participant ID.",
			} {
				if !strings.Contains(skill, want) {
					t.Fatalf("basics skill missing requester creator guidance %q", want)
				}
			}
			if strings.Contains(skill, "--creator-id manager") {
				t.Fatalf("basics skill still teaches manager as room creator:\n%s", skill)
			}
			if strings.Contains(skill, "--member-ids manager,dev") {
				t.Fatalf("basics skill still teaches sample dev as a literal participant ID:\n%s", skill)
			}
		})
	}
}

func TestManagerFeishuSkillRoomCreationKeepsRequesterAsCreator(t *testing.T) {
	tests := []struct {
		name string
		root string
	}{
		{name: "picoclaw", root: PicoClawManagerRoot},
		{name: "openclaw", root: OpenClawManagerRoot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := fs.ReadFile(FS(), path.Join(tt.root, WorkspaceDirName, "skills/feishu/SKILL.md"))
			if err != nil {
				t.Fatalf("read feishu skill: %v", err)
			}
			skill := string(data)

			for _, want := range []string{
				"csgclaw-cli room create --title worker-group --creator-id admin --member-ids manager,<worker-participant-id> --channel feishu",
				"keep the human requester as `--creator-id`",
				"Include `manager` plus the requested worker participant IDs in `--member-ids`",
				"replace `<worker-participant-id>` with IDs from `participant list`",
			} {
				if !strings.Contains(skill, want) {
					t.Fatalf("feishu skill missing requester creator guidance %q", want)
				}
			}
			if strings.Contains(skill, "--creator-id manager") {
				t.Fatalf("feishu skill still teaches manager as room creator:\n%s", skill)
			}
			if strings.Contains(skill, "--member-ids manager,dev") {
				t.Fatalf("feishu skill still teaches sample dev as a literal participant ID:\n%s", skill)
			}
		})
	}
}
