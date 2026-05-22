package codex

import (
	"context"
	"strings"

	"csgclaw/internal/activity"
)

func NewPermissionActivityDecider(channel string, permission PermissionDecider) activity.ActivityDecider {
	channel = strings.TrimSpace(channel)
	if channel == "" || permission == nil {
		return nil
	}
	return permissionActivityDecider{
		channel:    channel,
		permission: permission,
	}
}

type permissionActivityDecider struct {
	channel    string
	permission PermissionDecider
}

func (d permissionActivityDecider) Decide(ctx context.Context, req activity.ActivityDecisionRequest) (activity.ActivitySnapshot, error) {
	channel := strings.TrimSpace(req.Channel)
	activityID := strings.TrimSpace(req.ActivityID)
	if channel == "" || activityID == "" {
		return activity.ActivitySnapshot{}, activity.ErrActionNotFound
	}
	if channel != d.channel {
		return activity.ActivitySnapshot{}, activity.ErrActionNotFound
	}
	return d.permission.Decide(ctx, activityID, req.OptionID)
}
