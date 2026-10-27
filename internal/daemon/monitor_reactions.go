package daemon

import (
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) reactMonitorView(session protocol.SessionID, previous, view sessionmonitor.View, catchUp bool) {
	if previous.HarnessTitle.V != view.HarnessTitle.V && d.store.SessionLabels(session).UserLabel == "" {
		d.publishFact(FactSessionRenamed, string(session), nil)
	}
	if previous.TerminalTitle.V != view.TerminalTitle.V {
		d.publishFact(FactSessionTitleChanged, string(session), nil)
	}
	for path, sequence := range view.SentFiles.Entries {
		if !catchUp && previous.SentFiles.Entries[path] != sequence {
			d.receiveSentFile(session, path)
		}
	}
	if view.AssistantWindow.V != previous.AssistantWindow.V {
		d.publishFact(FactSessionAssistantWindowChanged, string(session), nil)
	}
	if previous.Branch.V != view.Branch.V {
		d.logf("Branch changed: session=%s branch=%s isWorktree=%v", session, view.Branch.V.Branch, view.Branch.V.IsWorktree)
		d.publishFact(FactSessionBranchChanged, string(session), nil)
	}
	state, turn := view.State.V, view.Turn.V
	stateChanged := state.Sequence != previous.State.V.Sequence
	if view.LastModelRequestAt.V != previous.LastModelRequestAt.V && !stateChanged {
		d.publishFact(FactSessionModelRequestStarted, string(session), nil)
	}
	if previous.FirstReportAt.V.IsZero() && !view.FirstReportAt.V.IsZero() {
		d.noteLaunchStarted(session)
	}
	if turn.OpenedAt.After(previous.Turn.V.OpenedAt) || (stateChanged && attention.OpensTurn(state.State) && turn.SnoozedUntil.IsZero()) {
		d.enqueueSessionActivity(session)
	}
	if stateChanged && !state.Quiet {
		d.kickSessionInbox(session, string(state.State))
		if state.Broadcast {
			d.broadcastSessionStateChanged(session)
		}
	}
	if !stateChanged && (turn != previous.Turn.V || view.AutoSettle.V != previous.AutoSettle.V || state.Reason != previous.State.V.Reason) {
		d.broadcastSessionStateChanged(session)
	}
	if exit := view.Exit.V; exit != nil && !exit.Held && !sameAppliedExit(exit, previous.Exit.V) {
		d.reactSessionExit(session, *exit)
	}
}

func sameAppliedExit(a, b *sessionmonitor.Exit) bool {
	if b != nil && (b.Terminal != "" || b.LifecycleID != "") {
		return sessionmonitor.SameExit(a, b)
	}
	return sameExitScreen(a, b)
}
