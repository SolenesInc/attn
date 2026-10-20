package daemon

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

func (r *monitorIndex) bindingOf(h *monitorHome) (protocol.SessionID, *monitorBinding) {
	session := h.sessionID()
	r.mu.Lock()
	defer r.mu.Unlock()
	if binding := r.sessions[session]; binding != nil && binding.entry == h.entry {
		return session, binding
	}
	return "", nil
}

func (h *monitorHome) Applied(view sessionmonitor.View) bool {
	d := h.daemon
	r := d.monitors()
	r.applyMu.Lock()
	session, binding := r.bindingOf(h)
	if session == "" {
		r.applyMu.Unlock()
		return d.admitMonitorConversation(h.entry, view)
	}
	previous := binding.applied
	if previous.Revision.V >= view.Revision.V {
		r.applyMu.Unlock()
		return true
	}
	recordedPullRequests, usageChanged, err := h.saveView(session, previous, view)
	if err != nil && d.store.Get(session) == nil {
		r.applyMu.Unlock()
		d.life.Go("forgetClosedMonitor", func() { d.forgetMonitor(session) })
		return false
	}
	if err != nil {
		h.catchUp(session, binding, err)
		r.applyMu.Unlock()
		return false
	}
	binding.applied = view
	h.catchUp(session, binding, nil)
	r.applyMu.Unlock()
	for _, id := range recordedPullRequests {
		d.publishFact(FactSessionPullRequestChanged, string(session), sessionPullRequestFact{PRID: id})
	}
	if usageChanged {
		d.publishFact(FactSessionCostChanged, string(session), nil)
	}
	d.reactMonitorView(session, previous, view)
	return true
}

func (h *monitorHome) saveView(session protocol.SessionID, previous, view sessionmonitor.View) ([]string, bool, error) {
	d := h.daemon
	if runtime := runtimeStateOf(view); runtime != runtimeStateOf(previous) && !d.store.ApplySessionRuntimeState(session, runtime) {
		return nil, false, fmt.Errorf("runtime state was not saved")
	}
	if conversation := view.Conversation.V.ConversationObservation; conversation != previous.Conversation.V.ConversationObservation && conversation.NativeID != "" {
		if _, err := d.store.TransitionSessionResumeID(session, conversation.NativeID); err != nil {
			return nil, false, fmt.Errorf("conversation: %w", err)
		}
	}
	costChanged, costDurable, usageChanged := sessionmonitor.CostChange(previous, view)
	if costChanged {
		if err := d.store.ApplySessionCostSnapshot(session, sessioncost.Checkpoint{Initialized: view.CostInitialized.V, Sources: view.Cost.Entries}, costDurable); err != nil {
			return nil, false, fmt.Errorf("cost: %w", err)
		}
	}
	var recordedPullRequests []string
	if exit := view.Exit.V; exit != nil && exit != previous.Exit.V {
		if !d.saveExitScreen(store.SessionExitScreen{SessionID: session, Text: exit.Text, Cols: exit.Cols, Rows: exit.Rows,
			ExitCode: exit.Code, ExitSignal: exit.Signal}, exit.At) {
			return nil, false, fmt.Errorf("exit screen was not saved")
		}
	}
	for id, link := range view.PullRequests.Entries {
		if previous.PullRequests.Entries[id] == link {
			continue
		}
		_, err := d.store.RecordSessionPullRequest(store.SessionPullRequestRecord{
			SessionID: session, PRID: id, Repository: link.Repository, Number: link.Number, URL: link.URL,
		}, time.Now())
		if err != nil {
			return nil, false, fmt.Errorf("pull request %s: %w", id, err)
		}
		recordedPullRequests = append(recordedPullRequests, id)
	}
	return recordedPullRequests, usageChanged, nil
}

func runtimeStateOf(view sessionmonitor.View) store.SessionRuntimeState {
	state, turn, driver := view.State.V, view.Turn.V, view.Driver.V
	return store.SessionRuntimeState{
		State: state.State, Since: state.Since, Updated: state.Updated,
		Turn:               store.TurnStamps{OpenedAt: turn.OpenedAt, SettledAt: turn.SettledAt, SnoozedUntil: turn.SnoozedUntil, AgentAt: turn.AgentAt, RequestedAt: turn.RequestedAt},
		LastModelRequestAt: view.LastModelRequestAt.V,
		Driver:             store.AgentDriverReportCursor{PluginName: driver.Plugin, RunID: driver.Run, Seq: driver.Sequence},
		DriverMetadata:     driver.Metadata,
		DriverTranscript:   driver.TranscriptPath,
		Branch:             view.Branch.V,
		HarnessTitle:       view.HarnessTitle.V,
	}
}
