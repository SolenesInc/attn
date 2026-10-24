package daemon

import (
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/crashpoint"
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
	if binding := r.sessions[session]; binding != nil && binding.monitor == h.monitor {
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
		return d.admitMonitorConversation(h.monitor, view)
	}
	previous := binding.applied
	if previous.Revision.V >= view.Revision.V {
		r.applyMu.Unlock()
		return true
	}
	recorded, moved, err := h.recordConversation(session, previous, view)
	var linked []string
	if err == nil {
		linked, err = h.recordPullRequests(session, previous, view)
	}
	if err != nil {
		h.catchUp(session, binding, err)
		r.applyMu.Unlock()
		return false
	}
	binding.applied = view
	h.catchUp(session, binding, nil)
	r.applyMu.Unlock()
	if recorded != "" {
		h.monitor.SettleSwitch(recorded)
	}
	if moved {
		d.publishFact(FactSessionConversationChanged, string(session), agentConversationObservation{SessionID: session, NativeID: recorded})
	}
	for _, id := range linked {
		d.publishFact(FactSessionPullRequestChanged, string(session), sessionPullRequestFact{PRID: id})
	}
	if _, _, usageChanged := sessionmonitor.CostChange(previous, view); usageChanged {
		d.publishFact(FactSessionCostChanged, string(session), nil)
	}
	d.reactMonitorView(session, previous, view)
	return true
}

func (h *monitorHome) recordConversation(session protocol.SessionID, previous, view sessionmonitor.View) (string, bool, error) {
	conversation := view.Conversation.V.ConversationObservation
	if conversation == previous.Conversation.V.ConversationObservation || conversation.NativeID == "" {
		return "", false, nil
	}
	moved, err := h.daemon.store.TransitionSessionResumeID(session, conversation.NativeID)
	if err != nil {
		return "", false, fmt.Errorf("conversation: %w", err)
	}
	return conversation.NativeID, moved, nil
}

type storeSaver struct{ daemon *Daemon }

func (s storeSaver) SaveRuntime(session protocol.SessionID, saved, view sessionmonitor.View) error {
	d := s.daemon
	if runtime := runtimeStateOf(view); runtime != runtimeStateOf(saved) && !d.store.SaveSessionRuntime(session, runtime) {
		return fmt.Errorf("runtime state was not saved")
	}
	costChanged, costDurable, _ := sessionmonitor.CostChange(saved, view)
	if costChanged {
		if err := d.store.ApplySessionCostSnapshot(session, sessioncost.Checkpoint{Initialized: view.CostInitialized.V, Sources: view.Cost.Entries}, costDurable); err != nil {
			return fmt.Errorf("cost: %w", err)
		}
	}
	if exit := view.Exit.V; exit != nil && exit != saved.Exit.V {
		if !d.saveExitScreen(store.SessionExitScreen{SessionID: session, Text: exit.Text, Cols: exit.Cols, Rows: exit.Rows,
			ExitCode: exit.Code, ExitSignal: exit.Signal}, exit.At) {
			return fmt.Errorf("exit screen was not saved")
		}
	}
	return nil
}

func (h *monitorHome) recordPullRequests(session protocol.SessionID, previous, view sessionmonitor.View) ([]string, error) {
	var inserted []string
	for id, link := range view.PullRequests.Entries {
		if sameLink(previous.PullRequests.Entries[id], link) {
			continue
		}
		crashpoint.At(crashBeforeLinkRecorded)
		recorded, err := h.daemon.store.RecordSessionPullRequest(store.SessionPullRequestRecord{
			SessionID: session, PRID: id, Repository: link.Repository, Number: link.Number, URL: link.URL,
		}, time.Now())
		if err != nil {
			return inserted, fmt.Errorf("pull request %s: %w", id, err)
		}
		if recorded {
			inserted = append(inserted, id)
		}
	}
	return inserted, nil
}

func sameLink(a, b sessionmonitor.PullRequest) bool {
	return a.Repository == b.Repository && a.Number == b.Number && a.URL == b.URL
}

func encodePullRequests(links map[string]sessionmonitor.PullRequest) string {
	if len(links) == 0 {
		return ""
	}
	saved := make(map[string]sessionmonitor.PullRequest, len(links))
	for id, link := range links {
		saved[id] = sessionmonitor.PullRequest{Repository: link.Repository, Number: link.Number, URL: link.URL}
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func decodePullRequests(raw string) map[string]sessionmonitor.PullRequest {
	if raw == "" {
		return nil
	}
	var links map[string]sessionmonitor.PullRequest
	_ = json.Unmarshal([]byte(raw), &links)
	return links
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
		LastSeen:           view.LastSeen.V,
		PendingSwitch:      encodePendingSwitch(view.PendingSwitch.V),
		PullRequests:       encodePullRequests(view.PullRequests.Entries),
	}
}

func encodePendingSwitch(pending sessionmonitor.Conversation) string {
	if pending.NativeID == "" {
		return ""
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func decodePendingSwitch(raw string) sessionmonitor.Conversation {
	var pending sessionmonitor.Conversation
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &pending)
	}
	return pending
}
