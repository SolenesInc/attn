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
	terminal := d.primaryTerminal(session.ID)
	d.terminalTitlesMu.RLock()
	title := d.terminalTitles[terminal]
	d.terminalTitlesMu.RUnlock()
	if title != "" {
		session.Title = protocol.Ptr(title)
	}
}
