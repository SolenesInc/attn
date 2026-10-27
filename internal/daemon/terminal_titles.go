package daemon

import (
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handlePTYTitle(terminal harness.TerminalID, title string) {
	r := d.terminals()
	r.mu.Lock()
	if r.titles == nil {
		r.titles = make(map[harness.TerminalID]string)
	}
	r.titles[terminal] = title
	r.mu.Unlock()
	if session, shown := d.shownIn(terminal); shown {
		d.reportTerminalTitle(session)
	}
}

func (d *Daemon) reportTerminalTitle(session protocol.SessionID) {
	monitor := d.sessionMonitor(session)
	if monitor == nil {
		return
	}
	terminal := d.primaryTerminal(session)
	r := d.terminals()
	r.mu.RLock()
	title := r.titles[terminal]
	r.mu.RUnlock()
	monitor.ReportTerminalTitle(title)
}

func (d *Daemon) forgetTerminalTitle(terminal harness.TerminalID) {
	r := d.terminals()
	r.mu.Lock()
	delete(r.titles, terminal)
	r.mu.Unlock()
}

func (d *Daemon) decorateSessionWithTitle(session *protocol.Session) {
	if session.Agent != protocol.AgentShellValue {
		return
	}
	if title := d.sessionTerminalTitle(session.ID); title != "" {
		session.Title = protocol.Ptr(title)
	}
}

func (d *Daemon) sessionTerminalTitle(sessionID protocol.SessionID) string {
	view, _ := d.monitorViewFor(sessionID)
	return view.TerminalTitle.V
}

func (d *Daemon) decorateLedgerEntryWithTitle(entry *protocol.SessionLedgerEntry) {
	if entry.Agent != protocol.AgentShellValue || entry.Label != "" || protocol.Deref(entry.ClosedAt) != "" {
		return
	}
	entry.Label = d.sessionTerminalTitle(entry.ID)
	if entry.Label == "" {
		entry.Label = store.DefaultSessionLabel(entry.Directory, entry.ID)
	}
}
