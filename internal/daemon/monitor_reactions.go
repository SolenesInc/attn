package daemon

import (
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) reactMonitorView(session protocol.SessionID, previous, view sessionmonitor.View, catchUp bool) {
	if previous.Live.V != view.Live.V {
		d.publishFact(FactSessionLiveChanged, string(session), nil)
	}
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
	if returnedInputMayPlace(previous, view) {
		d.claudeMod().placeReturned(session)
	}
	if stateChanged {
		d.kickCompactingClose(session)
	}
	broadcast := view.Driver.V.TranscriptPath != previous.Driver.V.TranscriptPath
	if stateChanged && !state.Quiet {
		d.kickSessionInbox(session, string(state.State))
		broadcast = broadcast || state.Broadcast
	}
	if !stateChanged && (turn != previous.Turn.V || view.AutoSettle.V != previous.AutoSettle.V || state.Reason != previous.State.V.Reason) {
		broadcast = true
	}
	if becamePromptReadyByReason(previous, view) || harnessJustStarted(previous, view) {
		d.kickSessionInboxAddresses(session)
	}
	if broadcast {
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

func returnedInputMayPlace(previous, view sessionmonitor.View) bool {
	return view.State.V.Sequence != previous.State.V.Sequence || becamePromptReadyByReason(previous, view) || harnessJustStarted(previous, view)
}

func harnessJustStarted(previous, view sessionmonitor.View) bool {
	return previous.AwaitingHarness.V && !view.AwaitingHarness.V
}

func becamePromptReadyByReason(previous, view sessionmonitor.View) bool {
	before, now := previous.State.V, view.State.V
	return now.Sequence == before.Sequence && !sessionmonitor.PromptReady(before.State, before.Reason) && sessionmonitor.PromptReady(now.State, now.Reason)
}
