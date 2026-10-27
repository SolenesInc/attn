package daemon

import (
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) answerMonitorSwitch(session protocol.SessionID, proposed sessionmonitor.Conversation, source *sessionmonitor.Monitor) sessionmonitor.Answer {
	if proposed.NativeID == "" {
		return sessionmonitor.Answer{}
	}
	answer := sessionmonitor.Answer{Conversation: proposed.NativeID, Outcome: sessionmonitor.SwitchDropped, Reason: "conversation switch was refused"}
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
				return answer
			}
		}
		if d.store.Get(session) == nil {
			return answer
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
	answer.Session = shown
	if !accepted && d.store.GetSessionConversation(shown).NativeID != proposed.NativeID {
		return answer
	}
	answer.Reason = ""
	answer.Outcome = sessionmonitor.SwitchInPlace
	if held == "" || held == proposed.NativeID {
		answer.Outcome = sessionmonitor.SwitchBound
	}
	if shown != before {
		answer.Outcome = sessionmonitor.SwitchShows
		if current := d.store.Get(shown); current != nil && protocol.Deref(current.Succeeds) == session {
			answer.Outcome = sessionmonitor.SwitchOpened
		}
	}
	if cost, err := d.store.SessionCostCheckpoint(shown); err == nil {
		answer.Cost = &cost
	}
	return answer
}

func (h monitorHost) Answered(source *sessionmonitor.Monitor, answer sessionmonitor.Answer) {
	d := h.daemon
	candidate := source.Candidate(answer.Conversation)
	if candidate == nil {
		return
	}
	if answer.Outcome == sessionmonitor.SwitchDropped {
		source.AdoptDropped(candidate)
		source.SettleSwitch(answer.Conversation, source, answer.Reason)
		pausepoint.At(pausepoint.ConversationSwitchDropped)
		return
	}
	d.bindMonitorTerminals()
}

func (d *Daemon) bindMonitorCandidate(source, candidate *sessionmonitor.Monitor, answer sessionmonitor.Answer) {
	proposed, ok := source.PendingSwitch()
	if !ok {
		return
	}
	shown := answer.Session
	if proposed.Terminal != "" {
		shown, _ = d.terminals().Showing(proposed.Terminal)
	}
	defer d.lockSessionLifecycles(source.Session(), shown)()
	defer d.lockTerminalEnds(shown)()
	if current, _ := source.PendingSwitch(); current != proposed || source.Candidate(answer.Conversation) != candidate {
		return
	}
	if proposed.Terminal != "" {
		if current, _ := d.terminals().Showing(proposed.Terminal); current != shown {
			return
		}
	}
	if shown == "" || d.store.GetSessionConversation(shown).NativeID != answer.Conversation {
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
	if !source.SettleSwitch(answer.Conversation, monitor, "") {
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
		d.bindMonitorCandidate(source, candidate, sessionmonitor.Answer{Conversation: proposed.NativeID, Session: shown})
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
