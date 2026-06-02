package api

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"csgclaw/internal/agent"
	"csgclaw/internal/im"
	"csgclaw/internal/runtime/openclawsandbox"
	"csgclaw/internal/runtime/picoclawsandbox"
	"csgclaw/internal/skillinvocation"
)

func (h *Handler) attachSkillInvocation(req im.CreateMessageRequest) (im.CreateMessageRequest, error) {
	invocation, ok := skillinvocation.ParseSlash(req.Content)
	if !ok || h == nil || h.im == nil || h.svc == nil {
		return req, nil
	}

	agentItem, ok := h.directRoomTargetAgent(req.RoomID, req.SenderID)
	if !ok {
		return req, nil
	}
	root, err := h.agentWorkspaceRoot(agentItem.ID)
	if err != nil {
		return req, nil
	}

	agentContent, err := skillinvocation.BuildMessage(skillinvocation.BuildOptions{
		WorkspaceRoot:   root,
		Slug:            invocation.Slug,
		Instruction:     invocation.Instruction,
		RuntimeSkillDir: runtimeSkillDir(agentItem.RuntimeKind, invocation.Slug),
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return req, nil
		}
		return im.CreateMessageRequest{}, fmt.Errorf("build skill invocation: %w", err)
	}
	req.AgentContent = agentContent
	return req, nil
}

func (h *Handler) directRoomTargetAgent(roomID, senderID string) (agent.Agent, bool) {
	room, ok := h.im.Room(roomID)
	if !ok || !room.IsDirect || len(room.Members) != 2 {
		return agent.Agent{}, false
	}
	senderID = strings.TrimSpace(senderID)
	for _, memberID := range room.Members {
		memberID = strings.TrimSpace(memberID)
		if memberID == "" || memberID == senderID {
			continue
		}
		if item, ok := h.svc.Agent(memberID); ok {
			return item, true
		}
	}
	return agent.Agent{}, false
}

func runtimeSkillDir(runtimeKind, slug string) string {
	switch strings.TrimSpace(runtimeKind) {
	case agent.RuntimeKindPicoClawSandbox:
		return path.Join(picoclawsandbox.BoxWorkspaceDir, "skills", slug)
	case agent.RuntimeKindOpenClawSandbox:
		return path.Join(openclawsandbox.BoxWorkspaceDir, "skills", slug)
	default:
		return ""
	}
}
