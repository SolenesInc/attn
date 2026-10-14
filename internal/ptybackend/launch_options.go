package ptybackend

import "github.com/victorarias/attn/internal/pty"

func toPTYSpawnOptions(opts SpawnOptions) pty.SpawnOptions {
	return pty.SpawnOptions{
		ID:              string(opts.ID),
		CWD:             opts.CWD,
		Agent:           opts.Agent,
		Cols:            opts.Cols,
		Rows:            opts.Rows,
		Theme:           opts.Theme,
		AgentLoginShell: opts.AgentLoginShell,
		AgentArgv:       opts.AgentArgv,
		AgentEnv:        opts.AgentEnv,
		ExternalCWD:     opts.ExternalCWD,
		DaemonEnv:       opts.DaemonEnv,
		LifecycleID:     opts.LifecycleID,
		LoginShellEnv:   opts.LoginShellEnv,
	}
}
