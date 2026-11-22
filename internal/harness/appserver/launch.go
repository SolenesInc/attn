package appserver

import (
	"context"

	"github.com/victorarias/attn/internal/harness"
)

// Plan runs a launch on the server when it asks to, or when it leaves the choice open and the server takes new launches.
func (l *Link) Plan(plugin string, req harness.LaunchRequest) harness.Planned {
	if l == nil || plugin != "" && l.b.plugin != plugin || req.Unattended.Executable != "" {
		return harness.Planned{Lifetime: harness.InTerminal}
	}
	if req.Lifetime == harness.InServer || req.Lifetime == "" && l.enabledForNewLaunches() {
		return harness.Planned{Lifetime: harness.InServer}
	}
	return harness.Planned{Lifetime: harness.InTerminal}
}

// Launch readies the server for a launch that runs on it, admitting the conversation it resumes. A reload goes ahead before the server answers.
func (l *Link) Launch(req harness.LaunchRequest) error {
	if l == nil || req.Lifetime != harness.InServer {
		return nil
	}
	if err := l.admit(req.Profile, req.Conversation); err != nil {
		return err
	}
	if req.Reason == harness.Reload {
		return l.relaunch(&req)
	}
	return l.prepareLaunch(&req)
}

// Admit refuses a conversation that a session of another profile or harness holds on this server.
func (l *Link) Admit(ref harness.ConversationRef) error {
	if l == nil || ref.Lifetime != harness.InServer {
		return nil
	}
	return l.admit(ref.Profile, ref.ID)
}

func (l *Link) Recover(ctx context.Context, point harness.RecoverPoint) {
	if l == nil {
		return
	}
	switch point {
	case harness.RecoverProcesses:
		l.recoverProcesses(ctx)
	case harness.RecoverViews:
		l.recoverViews(ctx)
	}
}

// Release lets go of the server when no session uses it.
func (l *Link) Release() {
	if l != nil {
		l.releaseUnlessUsed()
	}
}

// Executable is what the launch runs: the one it names, else the configured one, else the declared one.
func (l *Link) Executable(req harness.LaunchRequest) string { return l.executable(&req) }

// HomeEnv points the harness's client at the server's home.
func (l *Link) HomeEnv() string { return l.homeEnv() }

// SharedTools are attn's tools as the server offers them to its sessions, once the server has registered them.
func (l *Link) SharedTools() *harness.AgentTools {
	p, err := l.connection()
	if err != nil || l.callerConversationHeader() == "" || !p.agentToolsRegistered() {
		return nil
	}
	return p.agentTools()
}

func (l *Link) Plugin() string { return l.b.plugin }

func (c *Client) Terminal(t harness.TerminalID, e harness.TerminalEvent) { c.terminal(t, e) }
