package ptyhost

import (
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyworker"
)

const (
	RuntimeKind = "rust_host"

	MethodSpawn    = "spawn"
	MethodHostInfo = "host_info"
	MethodWatchAll = "watch_all"
)

type SpawnParams struct {
	SessionID   string `json:"session_id"`
	Agent       string `json:"agent"`
	CWD         string `json:"cwd"`
	Label       string `json:"label,omitempty"`
	LifecycleID string `json:"lifecycle_id,omitempty"`
	Cols        uint16 `json:"cols"`
	Rows        uint16 `json:"rows"`

	Theme ptyworker.SetThemeParams `json:"theme"`

	Attempts []pty.PreparedLaunchAttempt `json:"attempts"`

	YoloMode          bool                                `json:"yolo_mode,omitempty"`
	ApprovalRoute     launchcontract.ApprovalRoute        `json:"approval_route,omitempty"`
	Executable        string                              `json:"executable,omitempty"`
	ClaudeExecutable  string                              `json:"claude_executable,omitempty"`
	CodexExecutable   string                              `json:"codex_executable,omitempty"`
	CopilotExecutable string                              `json:"copilot_executable,omitempty"`
	Model             string                              `json:"model,omitempty"`
	Effort            string                              `json:"effort,omitempty"`
	UnattendedLaunch  launchcontract.UnattendedLaunchSpec `json:"unattended_launch,omitzero"`
}

type SpawnResult struct {
	HostPID      int `json:"host_pid"`
	ChildPID     int `json:"child_pid"`
	AttemptIndex int `json:"attempt_index"`
}

type HostInfoResult struct {
	HostPID        int      `json:"host_pid"`
	SessionIDs     []string `json:"session_ids"`
	SnapshotFormat string   `json:"snapshot_format"`
}
