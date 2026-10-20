package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"time"
)

func (r *monitorRegistry) sessionFor(entry *monitorEntry) (protocol.SessionID, *monitorBinding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for session, binding := range r.sessions {
		if binding.entry == entry {
			return session, binding
		}
	}
	return "", nil
}

type monitorHome struct {
	daemon *Daemon
	entry  *monitorEntry
}

func (h monitorHome) Applied(view monitorView) bool {
	d := h.daemon
	r := d.monitors()
	r.applyMu.Lock()
	session, binding := r.sessionFor(h.entry)
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

func (h monitorHome) saveView(session protocol.SessionID, previous, view monitorView) ([]string, bool, error) {
	d := h.daemon
	state, turn, driver := view.State.V, view.Turn.V, view.Driver.V
	if !d.store.ApplySessionRuntimeState(session, store.SessionRuntimeState{
		State: state.State, Since: state.Since, Updated: state.Updated,
		Turn:               store.TurnStamps{OpenedAt: turn.OpenedAt, SettledAt: turn.SettledAt, SnoozedUntil: turn.SnoozedUntil, AgentAt: turn.AgentAt, RequestedAt: turn.RequestedAt},
		LastModelRequestAt: view.LastModelRequestAt.V,
		Driver:             store.AgentDriverReportCursor{PluginName: driver.Plugin, RunID: driver.Run, Seq: driver.Sequence},
		DriverMetadata:     driver.Metadata,
		DriverTranscript:   driver.TranscriptPath,
		Branch:             view.Branch.V,
		HarnessTitle:       view.HarnessTitle.V,
	}) {
		return nil, false, fmt.Errorf("runtime state was not saved")
	}
	costChanged, costDurable, usageChanged := monitorCostChange(previous, view)
	if costChanged {
		if err := d.store.ApplySessionCostSnapshot(session, store.SessionMonitorCost{Initialized: view.CostInitialized.V, Sources: view.Cost.Entries}, costDurable); err != nil {
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
