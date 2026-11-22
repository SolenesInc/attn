package daemon

import (
	"context"
	"fmt"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

var harnessSources []func(*Daemon) []harness.Harness

func (d *Daemon) harnesses() []harness.Harness {
	var all []harness.Harness
	for _, source := range harnessSources {
		all = append(all, source(d)...)
	}
	return all
}

func (d *Daemon) harness(id string) (harness.Harness, error) {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, h := range d.harnesses() {
		if h.Facts().ID == id {
			return h, nil
		}
	}
	return nil, fmt.Errorf("agent %q is not available", id)
}

var unconnectedHarness func(d *Daemon, id harness.ID) harness.Harness

func (d *Daemon) conversation(sessionID protocol.SessionID) harness.Conversation {
	return d.conversationFor(d.sessionFacts(sessionID))
}

func (d *Daemon) hasChannel(agent harness.ID) bool {
	_, ok := harness.As[harness.Channel](d.harnessOf(agent))
	return ok
}

func (d *Daemon) channelUp(sessionID protocol.SessionID) bool {
	facts := d.sessionFacts(sessionID)
	channel, ok := harness.As[harness.Channel](d.harnessOf(facts.Harness))
	return ok && channel.Reach(d.life.Context(), facts).Up
}

func (d *Daemon) sessionFacts(sessionID protocol.SessionID) harness.SessionFacts {
	facts := harness.SessionFacts{Session: sessionID, Lifetime: d.lifetime(sessionID), Terminal: d.primaryTerminal(sessionID)}
	if session := d.store.Get(sessionID); session != nil {
		facts.Harness, facts.Profile = session.Agent, session.ProfileID
	}
	conversation := d.store.GetSessionConversation(sessionID)
	facts.Conversation, facts.Path = conversation.NativeID, conversation.TranscriptPath
	run := d.store.GetAgentDriverRun(sessionID)
	facts.Run, facts.RunOwner, facts.Route = run.RunID, run.PluginName, run.Route
	return facts
}

func (d *Daemon) conversationFor(facts harness.SessionFacts) harness.Conversation {
	return d.harnessOf(facts.Harness).Conversation(facts)
}

func (d *Daemon) harnessOf(id harness.ID) harness.Harness {
	if h, err := d.harness(id); err == nil {
		return h
	}
	return unconnectedHarness(d, id)
}

func (d *Daemon) agentOf(sessionID protocol.SessionID) harness.ID {
	if session := d.store.Get(sessionID); session != nil {
		return session.Agent
	}
	return ""
}

func (d *Daemon) planLaunch(req *spawnRequest) (harness.Planned, error) {
	if req.isShell {
		return harness.Planned{Lifetime: harness.InTerminal}, nil
	}
	launch := harness.LaunchRequest{Session: req.msg.ID, Unattended: req.policy.unattendedLaunch}
	if req.existingSession != nil && !req.policy.fromLedger {
		launch.Lifetime = d.lifetime(req.msg.ID)
	}
	return d.harnessOf(req.agent).Plan(launch)
}

func (d *Daemon) startHarnesses(ctx context.Context) {
	for _, h := range d.harnesses() {
		if err := h.Start(ctx); err != nil {
			d.logf("%s: %v", h.Facts().ID, err)
		}
	}
}

func (d *Daemon) recoverHarnesses(ctx context.Context, point harness.RecoverPoint) {
	for _, h := range d.harnesses() {
		h.Recover(ctx, point)
	}
}

func (d *Daemon) stopHarnesses() {
	for _, h := range d.harnesses() {
		h.Stop()
	}
}

func (d *Daemon) terminalEvent(t harness.TerminalID, e harness.TerminalEvent) {
	for _, h := range d.harnesses() {
		h.Terminal(t, e)
	}
}

func (d *Daemon) callerResolvers() []harness.CallerResolver {
	var resolvers []harness.CallerResolver
	for _, h := range d.harnesses() {
		if r, ok := harness.As[harness.CallerResolver](h); ok {
			resolvers = append(resolvers, r)
		}
	}
	return resolvers
}

func (d *Daemon) resolveCaller(t protocol.TerminalID) (session protocol.SessionID, view harness.TerminalID, profile string, ours bool) {
	for _, r := range d.callerResolvers() {
		if session, view, profile, ours = r.ResolveCaller(string(t)); ours {
			return session, view, profile, true
		}
	}
	return "", "", "", false
}

func (d *Daemon) credentialOwner(subject string) harness.CallerResolver {
	for _, r := range d.callerResolvers() {
		if r.OwnsCredential(subject) {
			return r
		}
	}
	return nil
}

func (d *Daemon) runEnded(agent harness.ID, e harness.RunEnd) {
	if runs, ok := harness.As[harness.Runs](d.harnessOf(agent)); ok {
		runs.RunEnded(e)
	}
}

func (d *Daemon) runRoute(facts harness.SessionFacts) (string, bool) {
	if runs, ok := harness.As[harness.Runs](d.harnessOf(facts.Harness)); ok {
		return runs.Route(facts)
	}
	return "", false
}

func (d *Daemon) endRun(sessionID protocol.SessionID, reason harness.RunEndReason, exitCode *int, signal string) {
	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("run end skipped: session=%s reason=%s no longer in store", sessionID, reason)
		return
	}
	run := d.endAgentDriverRun(sessionID)
	if run.RunID == "" {
		d.logf("run end skipped: session=%s reason=%s has no active run", sessionID, reason)
		return
	}
	d.sessionMonitor(sessionID).ResolveSoon()
	d.runEnded(session.Agent, harness.RunEnd{Session: sessionID, Terminal: d.primaryTerminal(sessionID), Owner: run.PluginName, Run: run.RunID, Reason: reason, ExitCode: exitCode, Signal: signal})
}

func (d *Daemon) abortLaunch(sessionID protocol.SessionID) {
	owner, run, launching := d.sessionMonitor(sessionID).AbortLaunch()
	if launching {
		d.runEnded(d.agentOf(sessionID), harness.RunEnd{Session: sessionID, Terminal: d.primaryTerminal(sessionID), Owner: owner, Run: run, Reason: harness.RunLaunchFailed})
	}
}

type typedConversation struct{ refusal error }

func (typedConversation) Deliver(context.Context, harness.Input) harness.Custody {
	return harness.Custody{Outcome: harness.NotMine}
}

func (typedConversation) Compact(context.Context, harness.Input) harness.Custody {
	return harness.Custody{Outcome: harness.NotMine}
}

func (c typedConversation) CanCompact() error { return c.refusal }

func noCompaction(agent string) error {
	return fmt.Errorf("attn has no way to compact a %s session; close without --compact", agent)
}

func (d *Daemon) knowsHarness(id string) bool {
	_, err := d.harness(id)
	return err == nil
}

func (d *Daemon) factsOrNone(id string) harness.Facts {
	if h, err := d.harness(id); err == nil {
		return h.Facts()
	}
	return harness.Facts{ID: id}
}

func (d *Daemon) offered(id string) bool {
	h, err := d.harness(id)
	return err == nil && h.Health(d.life.Context()).Offered
}

func builtinFacts(driver agentdriver.Driver) harness.Facts {
	caps := agentdriver.EffectiveCapabilities(driver)
	_, declaresServer := agentdriver.DeclaredServer(driver)
	return harness.Facts{
		ID:          driver.Name(),
		DisplayName: driver.DisplayName(),
		Options: harness.Options{
			Resume:             caps.HasResume,
			Yolo:               caps.HasYolo,
			AutoApprove:        caps.HasAutoApprove,
			InitialPrompt:      caps.HasInitialPrompt,
			LaunchInstructions: caps.HasLaunchInstructions,
			ModelPin:           caps.HasModelPin,
			EffortPin:          caps.HasEffortPin,
			ContextCap:         caps.HasContextWindowCap,
			Picker:             true,
			Provider:           declaresServer,
		},
		Executable: harness.Executable{Default: driver.DefaultExecutable(), Env: driver.ExecutableEnvVar()},
		Signals:    hookSignals(caps.HasHooks),
	}
}

func hookSignals(hooks bool) harness.Signals {
	if !hooks {
		return harness.Signals{}
	}
	return harness.Signals{TurnStarts: harness.TurnStartsReported, ReportsTools: true}
}

func (d *Daemon) signalsOf(agent protocol.SessionAgent) harness.Signals {
	if agent == protocol.SessionAgentShell {
		return sessionstate.Shell
	}
	if h, err := d.harness(string(agent)); err == nil {
		return h.Facts().Signals
	}
	return sessionstate.Unregistered
}

func (d *Daemon) harnessOwnsState(sessionID protocol.SessionID) bool {
	if d.store.GetAgentDriverRun(sessionID).RunID == "" {
		return false
	}
	session := d.store.Get(sessionID)
	return session != nil && d.signalsOf(session.Agent).StateAuthority == harness.StateByHarness
}

func (d *Daemon) executableHealth(facts harness.Facts, offered bool) harness.Health {
	checking := harness.Health{Installation: harness.Unknown, Offered: offered, Executable: facts.Executable.Default, Reason: "checking the login environment for " + facts.DisplayName}
	if d.capturingLoginEnv() {
		return checking
	}
	executable, err := d.machineExecutableOf(facts, "")
	if err != nil {
		if d.loginEnvFailed() {
			return checking
		}
		return harness.Health{Installation: harness.Missing, Offered: offered, Executable: executable, Reason: err.Error()}
	}
	return harness.Health{Installation: harness.Installed, Offered: offered, Executable: executable}
}
