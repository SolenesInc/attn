package daemon

import (
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/pausepoint"
	"time"

	"github.com/victorarias/attn/internal/crashpoint"
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

func (h *monitorHome) Applied(view sessionmonitor.View) (sessionmonitor.Answer, bool) {
	if !h.apply(view, false) {
		return sessionmonitor.Answer{}, false
	}
	if h.daemon.monitors().copied(h.sessionID()) {
		return sessionmonitor.Answer{}, true
	}
	pending := view.PendingSwitch.V
	if pending.NativeID != "" && pending.Terminal == "" {
		pausepoint.At(pausepoint.ConversationReportReady)
	}
	if pending.NativeID != "" && pending.Terminal != "" {
		shown, _ := h.daemon.shownIn(pending.Terminal)
		held := h.daemon.store.GetSessionConversation(shown).NativeID
		if held != "" && held != pending.NativeID {
			pausepoint.At(pausepoint.ConversationSwitchDecision)
		}
	}
	if current, _ := h.monitor.PendingSwitch(); current != pending {
		return sessionmonitor.Answer{}, true
	}
	answer := h.daemon.answerMonitorSwitch(h.sessionID(), pending, h.monitor)
	if answer.Outcome == sessionmonitor.SwitchOpened || answer.Outcome == sessionmonitor.SwitchShows {
		crashpoint.At("monitor-switch-applied")
		pausepoint.At(pausepoint.ConversationSwitchAnswer)
	}
	return answer, true
}

func (h *monitorHome) apply(view sessionmonitor.View, save bool) bool {
	d := h.daemon
	r := d.monitors()
	r.applyMu.Lock()
	if h.bridge != nil {
		if !d.isOutpostConn(h.host, h.bridge) {
			r.applyMu.Unlock()
			return false
		}
	}
	session, binding := r.bindingOf(h)
	if session == "" {
		r.applyMu.Unlock()
		return true
	}
	previous := binding.applied
	if binding.copied {
		if previous.Epoch.V != view.Epoch.V || previous.Revision.V < view.Revision.V {
			binding.applied = view
			r.notifyViewsChanged()
		}
		r.applyMu.Unlock()
		return true
	}
	if previous.Epoch.V == view.Epoch.V && previous.Revision.V >= view.Revision.V {
		r.applyMu.Unlock()
		return true
	}
	if save {
		var cost *sessionmonitor.CostSave
		if changed, durable, _ := sessionmonitor.CostChange(previous, view); changed {
			checkpoint := sessioncost.FromSourceTotals(view.CostInitialized.V, view.Cost.Entries)
			cost = &sessionmonitor.CostSave{Checkpoint: checkpoint, Durable: durable}
		}
		if err := (storeSaver{daemon: d}).SaveRuntime(session, previous, view, cost); err != nil {
			r.applyMu.Unlock()
			return false
		}
	}
	linked, err := h.recordPullRequests(session, previous, view)
	if err != nil {
		h.catchUp(session, binding, err)
		r.applyMu.Unlock()
		return false
	}
	binding.applied = view
	firstOnBridge := h.bridge != nil && binding.bridge != h.bridge
	binding.bridge = h.bridge
	h.catchUp(session, binding, nil)
	r.applyMu.Unlock()
	for _, id := range linked {
		d.publishFact(FactSessionPullRequestChanged, string(session), sessionPullRequestFact{PRID: id})
	}
	if _, _, usageChanged := sessionmonitor.CostChange(previous, view); usageChanged {
		d.publishFact(FactSessionCostChanged, string(session), nil)
	}
	d.reactMonitorView(session, previous, view, firstOnBridge)
	return true
}

type storeSaver struct{ daemon *Daemon }

func (s storeSaver) SaveRuntime(session protocol.SessionID, saved, view sessionmonitor.View, cost *sessionmonitor.CostSave) error {
	d := s.daemon
	runtime := runtimeStateOf(view)
	if d.store.Get(session) == nil {
		return nil
	}
	if runtime != runtimeStateOf(saved) && !d.store.SaveSessionRuntime(session, runtime) {
		return fmt.Errorf("runtime state was not saved")
	}
	if cost != nil {
		if err := d.store.ApplySessionCostSnapshot(session, cost.Checkpoint, cost.Durable); err != nil {
			return fmt.Errorf("cost: %w", err)
		}
	}
	if exit := view.Exit.V; exit != nil && !sameExitScreen(exit, saved.Exit.V) {
		if !d.saveExitScreen(store.SessionExitScreen{SessionID: session, Text: exit.Text, Cols: exit.Cols, Rows: exit.Rows,
			ExitCode: exit.Code, ExitSignal: exit.Signal}, exit.At) {
			return fmt.Errorf("exit screen was not saved")
		}
	}
	return nil
}

func sameExitScreen(a, b *sessionmonitor.Exit) bool {
	return b != nil && a.Text == b.Text && a.Cols == b.Cols && a.Rows == b.Rows && a.Code == b.Code && a.Signal == b.Signal && a.At.Equal(b.At)
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
		Live: view.Live.V, State: state.State, Since: state.Since, Updated: state.Updated,
		Turn:                       store.TurnStamps{OpenedAt: turn.OpenedAt, SettledAt: turn.SettledAt, SnoozedUntil: turn.SnoozedUntil, AgentAt: turn.AgentAt, RequestedAt: turn.RequestedAt},
		LastModelRequestAt:         view.LastModelRequestAt.V,
		Driver:                     store.AgentDriverReportCursor{PluginName: driver.Plugin, RunID: driver.Run, Seq: driver.Sequence},
		DriverMetadata:             driver.Metadata,
		DriverTranscript:           driver.TranscriptPath,
		Branch:                     view.Branch.V,
		HarnessTitle:               view.HarnessTitle.V,
		LastSeen:                   view.LastSeen.V,
		PullRequests:               encodePullRequests(view.PullRequests.Entries),
		ConversationNativeID:       view.Conversation.V.NativeID,
		ConversationTranscriptPath: view.Conversation.V.TranscriptPath,
	}
}

func encodePendingSwitch(pending []sessionmonitor.Conversation) string {
	if len(pending) == 0 || pending[0].NativeID == "" {
		return ""
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func decodePendingSwitch(raw string) []sessionmonitor.Conversation {
	if raw == "" {
		return nil
	}
	var pending []sessionmonitor.Conversation
	if json.Unmarshal([]byte(raw), &pending) == nil {
		return pending
	}
	var legacy sessionmonitor.Conversation
	if json.Unmarshal([]byte(raw), &legacy) == nil && legacy.NativeID != "" {
		return []sessionmonitor.Conversation{legacy}
	}
	return nil
}

func (s storeSaver) MovePending(from, to protocol.SessionID, remaining, moved []sessionmonitor.Conversation) error {
	pending := map[protocol.SessionID]string{from: encodePendingSwitch(remaining)}
	pending[to] = encodePendingSwitch(moved)
	return s.daemon.store.SaveSessionPendingSwitches(pending)
}
