package daemon

import (
	"errors"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
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
		d.logf("monitor apply: unknown entry %+v", view.Key.V)
		return false
	}
	previous := binding.applied
	if previous.Revision.V >= view.Revision.V {
		r.applyMu.Unlock()
		return true
	}
	state, turn, driver := view.State.V, view.Turn.V, view.Driver.V
	if !d.store.ApplySessionRuntimeState(session, store.SessionRuntimeState{
		State: state.State, Since: state.Since, Updated: state.Updated,
		Turn:               store.TurnStamps{OpenedAt: turn.OpenedAt, SettledAt: turn.SettledAt, SnoozedUntil: turn.SnoozedUntil},
		LastModelRequestAt: view.LastModelRequestAt.V,
		Driver:             store.AgentDriverReportCursor{PluginName: driver.Plugin, RunID: driver.Run, Seq: driver.Sequence},
	}) {
		h.catchUp(session, binding, errors.New("runtime state was not saved"))
		r.applyMu.Unlock()
		return false
	}
	binding.applied = view
	h.catchUp(session, binding, nil)
	r.applyMu.Unlock()
	d.reactMonitorView(session, previous, view)
	return true
}
