package daemon

import (
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) resolveMonitorSwitch(session protocol.SessionID, proposed sessionmonitor.Conversation, source *sessionmonitor.Monitor) sessionmonitor.SwitchOutcome {
	if proposed.NativeID == "" {
		return sessionmonitor.SwitchOutcome{}
	}
	outcome := sessionmonitor.SwitchOutcome{Conversation: proposed.NativeID, Kind: sessionmonitor.SwitchDropped, Reason: "conversation switch was refused"}
	observation := agentConversationObservation{SessionID: session, NativeID: proposed.NativeID, TranscriptPath: proposed.TranscriptPath}
	before := session
	if proposed.Terminal != "" {
		before, _ = d.shownIn(proposed.Terminal)
	}
	held := d.store.GetSessionConversation(before).NativeID
	accepted := false
	if proposed.Terminal != "" {
		accepted = d.acceptMonitorConversation(source, proposed, observation)
	} else {
		defer d.lockSessionLifecycles(session)()
		if source != nil {
			if pending, _ := source.PendingSwitch(); pending != proposed {
				return outcome
			}
		}
		if d.store.Get(session) == nil {
			return outcome
		}
		transition := d.store.TransitionSessionConversation
		if proposed.Proposal == sessionmonitor.ConversationReported {
			transition = d.store.ClaimSessionConversation
		}
		if proposed.TranscriptPath == "" {
			transition = func(id protocol.SessionID, native, _ string) (bool, error) {
				return d.store.TransitionSessionResumeID(id, native)
			}
		}
		accepted = d.applyAgentConversation(observation, transition)
	}
	shown := session
	if proposed.Terminal != "" {
		shown, _ = d.terminals().Showing(proposed.Terminal)
	}
	outcome.Session = shown
	if !accepted && d.store.GetSessionConversation(shown).NativeID != proposed.NativeID {
		return outcome
	}
	outcome.Reason = ""
	outcome.Kind = sessionmonitor.SwitchInPlace
	if held == "" || held == proposed.NativeID {
		outcome.Kind = sessionmonitor.SwitchBound
	}
	if shown != before {
		outcome.Kind = sessionmonitor.SwitchShows
		if current := d.store.Get(shown); current != nil && protocol.Deref(current.Succeeds) == session {
			outcome.Kind = sessionmonitor.SwitchOpened
		}
	}
	if cost, err := d.store.SessionCostCheckpoint(shown); err == nil {
		outcome.Cost = &cost
	}
	return outcome
}

func (h monitorHost) SwitchApplied(source *sessionmonitor.Monitor, outcome sessionmonitor.SwitchOutcome) {
	d := h.daemon
	candidate := source.Candidate(outcome.Conversation)
	if candidate == nil {
		return
	}
	if outcome.Kind == sessionmonitor.SwitchDropped {
		source.AdoptDropped(candidate)
		source.SettleSwitch(outcome.Conversation, source, outcome.Reason)
		pausepoint.At(pausepoint.ConversationSwitchDropped)
		return
	}
	d.bindMonitorTerminals()
}

func (d *Daemon) bindMonitorCandidate(source, candidate *sessionmonitor.Monitor, outcome sessionmonitor.SwitchOutcome) {
	proposed, ok := source.PendingSwitch()
	if !ok {
		return
	}
	shown := outcome.Session
	if proposed.Terminal != "" {
		shown, _ = d.terminals().Showing(proposed.Terminal)
	}
	defer d.lockSessionLifecycles(source.Session(), shown)()
	defer d.lockTerminalEnds(shown)()
	if current, _ := source.PendingSwitch(); current != proposed || source.Candidate(outcome.Conversation) != candidate {
		return
	}
	if proposed.Terminal != "" {
		if current, _ := d.terminals().Showing(proposed.Terminal); current != shown {
			return
		}
	}
	if shown == "" || d.store.GetSessionConversation(shown).NativeID != outcome.Conversation {
		return
	}
	monitor := d.startSessionMonitorFrom(shown, source.Session(), candidate)
	if monitor == nil {
		return
	}
	d.bindMonitorTitle(monitor)
	monitor.ResumeUsage()
	if candidate != source {
		monitor.StartCost()
	}
	if driver := monitor.Driver(); driver.TranscriptPath != "" {
		monitor.WatchDriverUsage(driver.TranscriptPath)
	}
	monitor.EnsureTranscriptWatcher(proposed.TranscriptPath)
	if !source.SettleSwitch(outcome.Conversation, monitor, "") {
		return
	}
	if source.Session() != monitor.Session() && d.store.Get(source.Session()) == nil {
		d.forgetMonitor(source.Session())
	}
	if source.Session() != monitor.Session() {
		pausepoint.At(pausepoint.ConversationCandidateBound)
	}
}

func (d *Daemon) monitorInTerminal(terminal harness.TerminalID, session protocol.SessionID) *sessionmonitor.Monitor {
	r := d.monitors().registry
	if profile, native, shared := harness.ParseCodexThreadTerminal(terminal); shared {
		if monitor := r.Conversation(profile, native); monitor != nil {
			return monitor
		}
		if _, view, _ := d.linkCaller(terminal); view != "" {
			terminal = view
		}
	}
	return r.Input(terminal, d.sessionMonitor(session))
}

func (d *Daemon) bindMonitorTerminals() {
	for _, source := range d.monitors().each() {
		proposed, ok := source.PendingSwitch()
		if !ok {
			continue
		}
		candidate := source.Candidate(proposed.NativeID)
		if candidate == nil {
			continue
		}
		shown := source.Session()
		if proposed.Terminal != "" {
			shown, _ = d.shownIn(proposed.Terminal)
		}
		if shown == "" || d.store.GetSessionConversation(shown).NativeID != proposed.NativeID {
			continue
		}
		d.bindMonitorCandidate(source, candidate, sessionmonitor.SwitchOutcome{Conversation: proposed.NativeID, Session: shown})
	}
	for terminal := range d.localLiveTerminals(d.life.Context()) {
		shown, ok := d.shownIn(terminal)
		if !ok || !d.copiedSession(shown) || d.sessionMonitor(shown) != nil {
			continue
		}
		if input := d.monitors().registry.Input(terminal, nil); input != nil {
			if input.Session() == "" || d.store.Get(input.Session()) != nil {
				continue
			}
			d.forgetMonitor(input.Session())
		}
		live := liveTerminal{terminal: terminal}
		if provider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
			info, err := provider.SessionInfo(d.life.Context(), terminal)
			live.info, live.haveInfo = info, err == nil
			if err != nil {
				d.logf("copied terminal %s: read running state: %v", terminal, err)
			}
		}
		if session := d.store.Get(shown); session != nil {
			d.resumeMonitor(session, live)
		}
	}
}
