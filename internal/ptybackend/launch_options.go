package ptybackend

import "github.com/victorarias/attn/internal/pty"

func toPTYSpawnOptions(opts SpawnOptions) pty.SpawnOptions {
	return pty.SpawnOptions{
		ID:                      opts.ID,
		CWD:                     opts.CWD,
		Agent:                   opts.Agent,
		Label:                   opts.Label,
		Cols:                    opts.Cols,
		Rows:                    opts.Rows,
		ResumeSessionID:         opts.ResumeSessionID,
		ResumePicker:            opts.ResumePicker,
		YoloMode:                opts.YoloMode,
		InitialPromptFile:       opts.InitialPromptFile,
		Theme:                   opts.Theme,
		Executable:              opts.Executable,
		ClaudeExecutable:        opts.ClaudeExecutable,
		CodexExecutable:         opts.CodexExecutable,
		CopilotExecutable:       opts.CopilotExecutable,
		ExternalCommand:         opts.ExternalCommand,
		ExternalEnv:             opts.ExternalEnv,
		ExternalCWD:             opts.ExternalCWD,
		DaemonEnv:               opts.DaemonEnv,
		LifecycleID:             opts.LifecycleID,
		LoginShellEnv:           opts.LoginShellEnv,
		WorkflowGuidanceEnabled: opts.WorkflowGuidanceEnabled,
		AutoApprove:             opts.AutoApprove,
		TrustWorkingDirectory:   opts.TrustWorkingDirectory,
		Model:                   opts.Model,
		Effort:                  opts.Effort,
		ContextWindowCap:        opts.ContextWindowCap,
		UnattendedLaunch:        opts.UnattendedLaunch,
	}
}
