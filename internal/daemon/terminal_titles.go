package daemon

import (
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handlePTYTitle(terminal harness.TerminalID, title string) {
	d.terminalTitlesMu.Lock()
	if d.terminalTitles == nil {
		d.terminalTitles = make(map[harness.TerminalID]string)
	}
	changed := d.terminalTitles[terminal] != title
	d.terminalTitles[terminal] = title
	d.terminalTitlesMu.Unlock()
	if session, shown := d.shownIn(terminal); changed && shown {
		d.publishFact(FactSessionTitleChanged, string(session), nil)
	}
}

func (d *Daemon) forgetTerminalTitle(terminal harness.TerminalID) {
	d.terminalTitlesMu.Lock()
	delete(d.terminalTitles, terminal)
	d.terminalTitlesMu.Unlock()
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
	terminal := d.primaryTerminal(sessionID)
	d.terminalTitlesMu.RLock()
	defer d.terminalTitlesMu.RUnlock()
	return d.terminalTitles[terminal]
}

func (d *Daemon) decorateLedgerEntryWithTitle(entry *protocol.SessionLedgerEntry) {
	if entry.Agent != protocol.AgentShellValue || entry.Label != "" || protocol.Deref(entry.ClosedAt) != "" {
		return
	}
	entry.Label = d.sessionTerminalTitle(entry.ID)
	if entry.Label == "" {
		entry.Label = defaultSessionLabel(entry.Directory, entry.ID)
	}
}
