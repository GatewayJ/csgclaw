package apps

import (
	"context"
	"io"
	"time"

	"csgclaw/internal/larkcli"
	projection "csgclaw/internal/runtimeextension/larkcli"
	skill "csgclaw/internal/skill/state"
)

const KindMCP = "mcp"
const KindCLI = "cli"

type CLIRuntime interface {
	State(context.Context, string) (bool, error)
	Update(context.Context, string, bool, func(context.Context) error) error
	Configure(context.Context, string, *projection.Payload) error
	PlanSkills(string, string, string, []skill.ManagedSkill, bool) (skill.SkillPlan, error)
	ApplySkills(context.Context, string, skill.SkillPlan) error
}

type CLIOptions struct {
	Runtime      CLIRuntime
	LegacySecret func(context.Context, string, string) (string, error)
	Install      func(context.Context, string, func(string)) (larkcli.Package, error)
	Run          func(context.Context, larkcli.Client, io.Reader, ...string) ([]byte, error)
}

type CLIRequest struct {
	OperationID string `json:"operation_id"`
	Revision    uint64 `json:"resource_revision"`
	Action      string `json:"-"`
}

type CLIOperation struct {
	ID         string    `json:"operation_id"`
	SessionID  string    `json:"session_id"`
	ResourceID string    `json:"resource_id"`
	Revision   uint64    `json:"resource_revision"`
	Action     string    `json:"action"`
	Stage      string    `json:"stage"`
	Status     string    `json:"status"`
	URL        string    `json:"authorization_url,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type cliOperation struct {
	CLIOperation
	Process       larkcli.ProcessRef `json:"process,omitempty"`
	DeviceCode    string             `json:"device_code,omitempty"`
	Workers       map[string]bool    `json:"workers,omitempty"`
	BackupTaken   bool               `json:"backup_taken,omitempty"`
	ConfigExisted bool               `json:"config_existed,omitempty"`
	Committed     bool               `json:"committed,omitempty"`
	Settled       bool               `json:"settled,omitempty"`
	Cancelled     bool               `json:"cancelled,omitempty"`
}

type cliRecord struct {
	ConfigDir  string                  `json:"config_dir"`
	Executable string                  `json:"executable"`
	SkillsDir  string                  `json:"skills_dir"`
	Queued     *cliOperation           `json:"queued,omitempty"`
	Recovery   *cliOperation           `json:"recovery,omitempty"`
	Operation  *cliOperation           `json:"operation,omitempty"`
	Completed  map[string]CLIOperation `json:"completed,omitempty"`
}

type cliBinding struct {
	Remove          bool             `json:"remove,omitempty"`
	PlannedEnabled  bool             `json:"planned_enabled"`
	PlannedRevision uint64           `json:"planned_revision"`
	Pending         bool             `json:"pending"`
	Resume          bool             `json:"resume"`
	ResumeRecorded  bool             `json:"resume_recorded"`
	Plan            *skill.SkillPlan `json:"skill_plan,omitempty"`
}
