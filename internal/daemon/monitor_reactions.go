package daemon

import (
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) reactMonitorView(session protocol.SessionID, previous, view sessionmonitor.View) {
	driver := view.Driver.V
	if driver.Run != "" && driver.TranscriptPath != "" && (driver.Run != previous.Driver.V.Run || driver.TranscriptPath != previous.Driver.V.TranscriptPath) {
		pausepoint.At(pausepoint.DriverTranscriptApplied)
		d.sessionMonitor(session).WatchDriverUsage(driver.TranscriptPath)
	}
	if previous.HarnessTitle.V != view.HarnessTitle.V && d.store.SessionLabels(session).UserLabel == "" {
		d.publishFact(FactSessionRenamed, string(session), nil)
	}
	if previous.TerminalTitle.V != view.TerminalTitle.V {
		d.publishFact(FactSessionTitleChanged, string(session), nil)
	}
	for path, sequence := range view.SentFiles.Entries {
		if previous.SentFiles.Entries[path] != sequence {
			d.receiveSentFile(session, path)
		}
	}
	if native := view.Conversation.V.NativeID; native != previous.Conversation.V.NativeID {
		d.rememberDispatchResume(session, native)
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
		d.sessionMonitor(session).UpdateTranscriptState(state.State)
		source := state.Origin.Source
		if source == "" {
			source = state.Cause
		}
		d.recordStateObservation(session, statetrace.Observation{
			Source: source, Claim: string(state.State), Detail: state.Origin.Detail,
			Cause: state.Cause, Outcome: statetrace.OutcomeApplied, ObservedAt: state.Origin.ObservedAt,
		})
		d.kickSessionInbox(session, string(state.State))
		if state.Broadcast {
			d.broadcastSessionStateChanged(session)
		}
		if state.Resolve {
			d.sessionMonitor(session).ResolveSoon()
		}
	}
	if !stateChanged && (turn != previous.Turn.V || view.AutoSettle.V != previous.AutoSettle.V || state.Reason != previous.State.V.Reason) {
		d.broadcastSessionStateChanged(session)
	}
	if exit := view.Exit.V; exit != nil && !exit.Held && !sessionmonitor.SameExit(exit, previous.Exit.V) {
		d.reactSessionExit(session, *exit)
	}
}
